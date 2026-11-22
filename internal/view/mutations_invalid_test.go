// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

var errInvalid = errors.New("boom")

// gvrViewer reports a different GVR than the wrapped viewer.
type gvrViewer struct {
	ResourceViewer

	gvr *client.GVR
}

func (g *gvrViewer) GVR() *client.GVR { return g.gvr }

// mutations returns every recorded call that changes the cluster.
func mutations(fc *fakecluster.Cluster) []string {
	var out []string
	for _, verb := range []string{"create", "update", "patch", "delete", "delete-collection"} {
		for _, a := range fc.Actions(verb, "*") {
			out = append(out, verb+" "+a.GetResource().Resource)
		}
	}

	return out
}

func flashHas(s *SimApp, txt string) func() bool {
	return func() bool { return strings.Contains(s.Flash(), txt) }
}

func confirmOK(t *testing.T, s *SimApp) {
	t.Helper()
	require.True(t, s.DialogOpen(), "no dialog")
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)
}

func simCronJob(n string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n, UID: types.UID("uid-" + n)},
		Spec:       batchv1.CronJobSpec{Schedule: "* * * * *"},
	}
}

func TestMutationsEmptySelection(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithNamespaces("ns1", "empty"))
	s := NewSimApp(t, fc, SimNamespace("empty"), SimView("deploy"))
	evt := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	uu := map[string]struct {
		view string
		cmd  func(ResourceViewer) func(*tcell.EventKey) *tcell.EventKey
		want *tcell.EventKey
	}{
		"kill": {view: "pods", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey { return (&Pod{ResourceViewer: v}).killCmd }, want: evt},
		"cordon": {view: "nodes", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&Node{ResourceViewer: v}).toggleCordonCmd(true)
		}, want: evt},
		"drain": {view: "nodes", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&Node{ResourceViewer: v}).drainCmd
		}, want: evt},
		"ssh": {view: "nodes", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey { return (&Node{ResourceViewer: v}).sshCmd }, want: evt},
		"trigger": {view: "cj", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&CronJob{ResourceViewer: v}).triggerCmd
		}, want: evt},
		"suspend": {view: "cj", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&CronJob{ResourceViewer: v}).toggleSuspendCmd
		}, want: evt},
		"rollback": {view: "rs", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&ReplicaSet{ResourceViewer: v}).rollbackCmd
		}, want: evt},
		"scale": {view: "deploy", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&ScaleExtender{ResourceViewer: v}).scaleCmd
		}},
		"restart": {view: "deploy", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&RestartExtender{ResourceViewer: v}).restartCmd
		}},
		"set-image": {view: "deploy", cmd: func(v ResourceViewer) func(*tcell.EventKey) *tcell.EventKey {
			return (&ImageExtender{ResourceViewer: v}).setImageCmd
		}},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			s.Cmd(":" + u.view)
			v := topViewer(t, s)
			fc.Reset()

			var got *tcell.EventKey
			s.OnLoop(func() { got = u.cmd(v)(evt) })

			assert.Equal(t, u.want, got)
			assert.False(t, s.DialogOpen())
			assert.Empty(t, mutations(fc))
		})
	}
}

func TestMutationsWrongAccessor(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 1), simPod("ns1", "pod-a"), simCronJob("cj-a")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	top := topViewer(t, s)
	cm := &gvrViewer{ResourceViewer: top, gvr: client.CmGVR}
	ctx := context.Background()

	t.Run("restart", func(t *testing.T) {
		err := (&RestartExtender{ResourceViewer: cm}).restartRollout(ctx, "ns1/dp-a", &metav1.PatchOptions{})
		require.EqualError(t, err, "resource is not restartable")
	})
	t.Run("scale", func(t *testing.T) {
		x := &ScaleExtender{ResourceViewer: cm}
		require.ErrorContains(t, x.scale(ctx, "ns1/dp-a", 2), "expecting a scalable resource")
		_, err := x.replicasFromScaleSubresource("ns1/dp-a")
		require.ErrorContains(t, err, "expecting a replicasGetter")
	})
	t.Run("image", func(t *testing.T) {
		x := &ImageExtender{ResourceViewer: cm}
		_, err := x.getPodSpec("ns1/dp-a")
		require.ErrorContains(t, err, "expecting a ContainsPodSpec")
		require.ErrorContains(t, x.setImages(ctx, "ns1/dp-a", dao.ImageSpecs{{Name: "c1", DockerImage: "x"}}), "expecting a scalable")
		s.OnLoop(func() { x.setImageCmd(nil) })
		s.Eventually(flashHas(s, "expecting a ContainsPodSpec"), "no image flash")
		assert.False(t, s.DialogOpen())
	})
	t.Run("drain", func(t *testing.T) {
		s.OnLoop(func() {
			drainNode(&gvrViewer{ResourceViewer: top, gvr: client.PodGVR}, []string{"n1"}, dao.DrainOptions{})
		})
		s.Eventually(flashHas(s, "expecting a maintainer"), "no drain flash")
	})
	t.Run("cordon", func(t *testing.T) {
		n := &Node{ResourceViewer: &gvrViewer{ResourceViewer: top, gvr: client.PodGVR}}
		s.OnLoop(func() { n.toggleCordonCmd(true)(nil) })
		confirmOK(t, s)
		s.Eventually(flashHas(s, "expecting a maintainer"), "no cordon flash")
	})
	t.Run("kill", func(t *testing.T) {
		p := &Pod{ResourceViewer: &gvrViewer{ResourceViewer: top, gvr: client.CoGVR}}
		s.OnLoop(func() { p.killCmd(nil) })
		s.Eventually(flashHas(s, "expecting a nuker"), "no kill flash")
		assert.False(t, s.DialogOpen())
	})
	t.Run("sanitize", func(t *testing.T) {
		p := &Pod{ResourceViewer: top}
		s.OnLoop(func() { p.sanitizeCmd(nil) })
		s.Eventually(flashHas(s, "expecting a sanitizer"), "no sanitize flash")
		assert.False(t, s.DialogOpen())
	})
	t.Run("trigger", func(t *testing.T) {
		c := &CronJob{ResourceViewer: cm}
		s.OnLoop(func() { c.triggerCmd(nil) })
		confirmOK(t, s)
		s.Eventually(flashHas(s, "expecting a job runner"), "no trigger flash")
	})
	t.Run("suspend", func(t *testing.T) {
		c := &CronJob{ResourceViewer: cm}
		s.OnLoop(func() { c.toggleSuspendCmd(nil) })
		s.Eventually(flashHas(s, "expecting a cron job"), "no suspend flash")
	})
	assert.Empty(t, mutations(fc))
}

func TestPodKillAPIErrors(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simPod("ns1", "pod-a"), simPod("ns1", "pod-b"), simPod("ns1", "pod-c")),
		fakecluster.FailOn("delete", "pods", 2, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	markPaths(t, s, "ns1/pod-a", "ns1/pod-b", "ns1/pod-c")
	fc.Reset()

	s.Keys(tcell.KeyCtrlK)
	confirmOK(t, s)

	s.Eventually(flashHas(s, "delete failed for 1/3: boom"), "no failure flash")
	assert.Len(t, fc.Deletes(), 3, "the batch goes on after an error")
	_, marks := selected(t, s)
	assert.Len(t, marks, 1, "the failed pod stays marked")
}

func TestPodKillSingleFailure(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simPod("ns1", "pod-a")),
		fakecluster.FailOn("delete", "pods", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-a")

	s.Keys(tcell.KeyCtrlK)
	confirmOK(t, s)

	s.Eventually(flashHas(s, "Delete failed with `boom"), "no failure flash")
	assert.Equal(t, []string{"delete pods"}, mutations(fc))
}

func TestPodSanitizeDeleteError(t *testing.T) {
	bad := simPod("ns1", "pod-bad")
	bad.Status.Phase = corev1.PodPending
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simPod("ns1", "pod-a"), bad),
		fakecluster.FailOn("delete", "pods", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-a")
	fc.Reset()

	s.Keys(ui.KeyZ, magicPrompt)
	confirmOK(t, s)

	s.Eventually(flashHas(s, "boom"), "no sanitize flash")
	assert.Len(t, fc.Deletes(), 1)
}

func TestNodeCordonAPIErrors(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simNode("n1"), simNode("n2")),
		fakecluster.FailOn("patch", "nodes", 1, errInvalid),
	)
	s := NewSimApp(t, fc, SimView("nodes"))
	markPaths(t, s, "n1", "n2")
	fc.Reset()

	s.Keys(ui.KeyC)
	assert.Contains(t, s.Screen(), "Cordon (2) marked nodes?")
	confirmOK(t, s)

	s.Eventually(func() bool { return len(nodePatchBodies(fc)) == 2 }, "the batch stops on the first error")
	s.Eventually(flashHas(s, "boom"), "no failure flash")
}

func TestNodeCordonMissingNode(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1")))
	s := NewSimApp(t, fc, SimView("nodes"))
	selectPath(t, s, "n1")
	n := &Node{ResourceViewer: topViewer(t, s)}
	require.NoError(t, fc.Typed().Tracker().Delete(corev1.SchemeGroupVersion.WithResource("nodes"), "", "n1"))
	require.NoError(t, fc.Dyn().Tracker().Delete(corev1.SchemeGroupVersion.WithResource("nodes"), "", "n1"))
	s.Eventually(func() bool { return !rowShown(s, "n1") }, "node still shown")
	fc.Reset()

	s.OnLoop(func() { n.toggleCordonCmd(false)(nil) })
	assert.False(t, s.DialogOpen())
	assert.Empty(t, mutations(fc))
}

func TestCronJobAPIErrors(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simCronJob("cj-a"), simCronJob("cj-b")),
		fakecluster.FailOn("create", "jobs", 1, errInvalid),
		fakecluster.FailOn("patch", "cronjobs", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("cj"))

	t.Run("trigger", func(t *testing.T) {
		markPaths(t, s, "ns1/cj-a", "ns1/cj-b")
		fc.Reset()
		s.Keys(ui.KeyT)
		assert.Contains(t, s.Screen(), "Trigger 2 CronJobs?")
		confirmOK(t, s)
		s.Eventually(func() bool { return len(fc.Actions("create", "jobs")) == 2 }, "the batch goes on after an error")
		clearMarks(t, s)
	})
	t.Run("suspend", func(t *testing.T) {
		selectPath(t, s, "ns1/cj-a")
		fc.Reset()
		s.Keys(ui.KeyS)
		s.Eventually(s.DialogOpen, "no suspend dialog")
		confirmOK(t, s)
		s.Eventually(flashHas(s, "boom"), "no suspend flash")
		assert.Len(t, fc.Actions("patch", "cronjobs"), 1)
	})
	t.Run("trigger-single", func(t *testing.T) {
		selectPath(t, s, "ns1/cj-a")
		fc.Reset()
		s.Keys(ui.KeyT)
		confirmOK(t, s)
		s.Eventually(func() bool { return len(fc.Actions("create", "jobs")) == 1 }, "no create")
	})
}

func TestReplicaSetRollbackAPIError(t *testing.T) {
	dp, rs := rollbackObjects()
	fc := fakecluster.New(t, fakecluster.WithObjects(dp, rs), fakecluster.FailOn("patch", "deployments", 0, errInvalid))
	s := NewSimApp(t, fc, SimView("rs"))
	selectPath(t, s, "ns1/rs-a")
	fc.Reset()

	s.Keys(tcell.KeyCtrlL)
	confirmOK(t, s)

	s.Eventually(flashHas(s, "boom"), "no rollback flash")
	assert.Equal(t, []string{"patch deployments"}, mutations(fc))
}

func TestReplicaSetRollbackMissingOwner(t *testing.T) {
	_, rs := rollbackObjects()
	fc := fakecluster.New(t, fakecluster.WithObjects(rs))
	s := NewSimApp(t, fc, SimView("rs"))
	selectPath(t, s, "ns1/rs-a")
	fc.Reset()

	s.Keys(tcell.KeyCtrlL)
	confirmOK(t, s)

	s.Eventually(flashHas(s, "not found"), "no rollback flash")
	assert.Empty(t, mutations(fc))
}

func TestDeploymentScaleInvalidInput(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 3), mutDeploy("dp-b", 3)))
	updates := serveScale(fc)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))

	t.Run("empty", func(t *testing.T) {
		selectPath(t, s, "ns1/dp-a")
		s.Keys(ui.KeyS)
		focusItem(t, s, "Replicas:")
		clearField(s, 3)
		confirmOK(t, s)
		s.Eventually(flashHas(s, "invalid syntax"), "no parse flash")
		assert.Empty(t, updates())
		assert.False(t, s.DialogOpen())
	})
	t.Run("non-numeric", func(t *testing.T) {
		selectPath(t, s, "ns1/dp-a")
		s.Keys(ui.KeyS)
		focusItem(t, s, "Replicas:")
		clearField(s, 3)
		s.Keys("-abc")
		confirmOK(t, s)
		s.Eventually(flashHas(s, "invalid syntax"), "no parse flash")
		assert.Empty(t, updates())
	})
	t.Run("no-ready-column", func(t *testing.T) {
		x := &ScaleExtender{ResourceViewer: &gvrViewer{ResourceViewer: topViewer(t, s), gvr: client.CmGVR}}
		s.OnLoop(func() { x.showScaleDialog([]string{"ns1/dp-a"}) })
		assert.True(t, s.DialogOpen())
		s.Keys(tcell.KeyEscape)
	})
}

func TestDeploymentScaleMidBatchError(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 3), mutDeploy("dp-b", 3)))
	serveScale(fc)
	var calls atomic.Int32
	fc.Typed().PrependReactor("update", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		calls.Add(1)
		return true, nil, errInvalid
	})
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	markPaths(t, s, "ns1/dp-a", "ns1/dp-b")

	s.Keys(ui.KeyS)
	focusItem(t, s, "Replicas:")
	s.Keys("2")
	confirmOK(t, s)

	s.Eventually(flashHas(s, "boom"), "no scale flash")
	assert.Equal(t, int32(1), calls.Load(), "the batch stops on the first error")
}

func TestWorkloadRestartAPIErrors(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(mutDeploy("dp-a", 1), mutDeploy("dp-b", 1)),
		fakecluster.FailOn("patch", "deployments", 1, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	markPaths(t, s, "ns1/dp-a", "ns1/dp-b")
	fc.Reset()

	s.Keys(ui.KeyR)
	assert.Contains(t, s.Screen(), "Restart 2 deployments?")
	confirmOK(t, s)

	s.Eventually(func() bool { return len(patches(fc, "deployments")) == 2 }, "the batch goes on after an error")
}

func TestDeploymentSetImageAPIError(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 1)), fakecluster.FailOn("patch", "deployments", 0, errInvalid))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	fc.Reset()

	s.Keys(ui.KeyI)
	focusItem(t, s, "c1")
	clearField(s, len("nginx:1"))
	confirmOK(t, s)

	s.Eventually(flashHas(s, "boom"), "no image flash")
	assert.False(t, s.DialogOpen())
	assert.Len(t, patches(fc, "deployments"), 1)
}

func TestDeploymentSetImageMissing(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 1)))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	x := &ImageExtender{ResourceViewer: topViewer(t, s)}
	fc.Reset()

	s.OnLoop(func() { require.Error(t, x.showImageDialog("ns1/nope")) })
	assert.False(t, s.DialogOpen())
	assert.Empty(t, mutations(fc))
}

func TestScaleDialogInvalidTables(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(
			mutDeploy("dp-a", 1),
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cm-a"}},
		),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("cm"))

	t.Run("no-ready-column", func(t *testing.T) {
		selectPath(t, s, "ns1/cm-a")
		x := &ScaleExtender{ResourceViewer: topViewer(t, s)}
		s.OnLoop(func() { x.showScaleDialog([]string{"ns1/cm-a"}) })
		s.Eventually(flashHas(s, "no column index for READY"), "no column flash")
		assert.False(t, s.DialogOpen())
	})
	t.Run("ready-without-total", func(t *testing.T) {
		s.Cmd(":deploy")
		selectPath(t, s, "ns1/dp-a")
		v := topViewer(t, s)
		x := &ScaleExtender{ResourceViewer: v}
		s.OnLoop(func() {
			tb := v.GetTable()
			idx, ok := tb.HeaderIndex("READY")
			require.True(t, ok)
			tb.GetCell(tb.GetSelectedRowIndex(), idx).SetText("True")
			x.showScaleDialog([]string{"ns1/dp-a"})
		})
		s.Eventually(flashHas(s, "unable to locate replicas from True"), "no replicas flash")
		assert.False(t, s.DialogOpen())
	})
	t.Run("scale-subresource-unreachable", func(t *testing.T) {
		gvr := client.NewGVR("example.com/v1/widgets")
		dao.MetaAccess.RegisterMeta(gvr.String(), &metav1.APIResource{
			Name: "widgets", Kind: "Widget", Group: "example.com", Version: "v1",
			Namespaced: true, Categories: []string{"scale"},
		})
		s.Cmd(":deploy")
		selectPath(t, s, "ns1/dp-a")
		x := &ScaleExtender{ResourceViewer: &gvrViewer{ResourceViewer: topViewer(t, s), gvr: gvr}}
		fc.Reset()
		s.OnLoop(func() { x.showScaleDialog([]string{"ns1/dp-a"}) })
		require.True(t, s.DialogOpen(), "the dialog falls back to the READY column")
		focusItem(t, s, "Cancel")
		s.Keys(tcell.KeyEnter)
		assert.False(t, s.DialogOpen())
		assert.Empty(t, mutations(fc))
	})
}

func TestSetImageDialogDismiss(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 1)))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	fc.Reset()

	s.Keys(ui.KeyI)
	focusItem(t, s, "c1")
	s.Keys("x")
	focusItem(t, s, "Cancel")
	s.Keys(tcell.KeyEnter)
	assert.False(t, s.DialogOpen())

	s.Keys(ui.KeyI)
	require.True(t, s.DialogOpen())
	s.Keys(tcell.KeyEscape)
	assert.False(t, s.DialogOpen())
	assert.Empty(t, mutations(fc))
}

func TestCronJobResumeAPIError(t *testing.T) {
	cj := simCronJob("cj-a")
	cj.Spec.Suspend = new(true)
	fc := fakecluster.New(t, fakecluster.WithObjects(cj), fakecluster.FailOn("patch", "cronjobs", 0, errInvalid))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("cj"))
	selectPath(t, s, "ns1/cj-a")
	fc.Reset()

	s.Keys(ui.KeyS)
	s.Eventually(s.DialogOpen, "no resume dialog")
	assert.Contains(t, s.Screen(), "Resume")
	confirmOK(t, s)

	s.Eventually(flashHas(s, "boom"), "no resume flash")
	assert.Len(t, fc.Actions("patch", "cronjobs"), 1)
}

func TestSimpleDeleteInvalid(t *testing.T) {
	fc := fakecluster.New(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	dir := s.app.Config.K9s.ContextScreenDumpDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	ee, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range ee {
		require.NoError(t, os.Remove(filepath.Join(dir, e.Name())))
	}

	t.Run("empty", func(t *testing.T) {
		s.Cmd(":sd")
		s.Keys(tcell.KeyCtrlD)
		assert.False(t, s.DialogOpen())
	})
	t.Run("marked-with-missing-file", func(t *testing.T) {
		pp := []string{filepath.Join(dir, "dump-a.csv"), filepath.Join(dir, "dump-b.csv"), filepath.Join(dir, "dump-c.csv")}
		for _, p := range pp {
			require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		}
		s.Cmd(":sd")
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "dump-c.csv") }, "dumps not shown")
		markPaths(t, s, pp...)
		require.NoError(t, os.Remove(pp[1]))

		s.Keys(tcell.KeyCtrlD)
		assert.Contains(t, s.Screen(), "Delete 3 marked")
		confirmOK(t, s)

		s.Eventually(flashHas(s, "delete failed for 1/3"), "no failure flash")
		assert.NoFileExists(t, pp[0])
		assert.NoFileExists(t, pp[2])
	})
	t.Run("single-missing-file", func(t *testing.T) {
		p := filepath.Join(dir, "dump-d.csv")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		s.Cmd(":sd")
		selectPath(t, s, p)
		require.NoError(t, os.Remove(p))

		s.Keys(tcell.KeyCtrlD)
		confirmOK(t, s)

		s.Eventually(flashHas(s, "Delete failed with"), "no failure flash")
	})
}
