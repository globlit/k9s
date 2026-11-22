// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func simNode(n string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n}}
}

func simNodePod(n string) *corev1.Pod {
	po := simPod("ns1", n)
	po.Spec.NodeName = "n1"
	po.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs1", Controller: new(true)}}
	return po
}

func nodePatchBodies(fc *fakecluster.Cluster) []string {
	var out []string
	for _, a := range fc.Actions("patch", "nodes") {
		out = append(out, string(a.(k8stesting.PatchAction).GetPatch()))
	}
	return out
}

// focusItem presses Tab until the focus is on the form item or button with label.
func focusItem(t *testing.T, s *SimApp, label string) {
	t.Helper()
	for range 20 {
		switch p := s.focus().(type) {
		case *tview.Button:
			if p.GetLabel() == label {
				return
			}
		case tview.FormItem:
			if p.GetLabel() == label {
				return
			}
		}
		s.Keys(tcell.KeyTab)
	}
	t.Fatalf("no focus on %q", label)
}

func clearField(s *SimApp, n int) {
	for range n {
		s.Keys(tcell.KeyBackspace2)
	}
}

func TestNodeCordonKeys(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1")), fakecluster.MirrorTypedToDyn())
	s := NewSimApp(t, fc, SimView("nodes"))
	selectPath(t, s, "n1")
	fc.Reset()

	s.Keys(ui.KeyC)
	require.True(t, s.DialogOpen())
	assert.Contains(t, s.Screen(), "Cordon n1?")
	s.Keys(tcell.KeyEnter)
	s.Sync()
	assert.Empty(t, nodePatchBodies(fc), "cancel sends no patch")

	s.Keys(ui.KeyC, tcell.KeyTab, tcell.KeyEnter)
	s.Eventually(func() bool { return len(nodePatchBodies(fc)) == 1 }, "no cordon patch")
	assert.Contains(t, nodePatchBodies(fc)[0], `"unschedulable":true`)
	s.Eventually(func() bool {
		n, err := fc.DynGet(client.NodeGVR, "", "n1")
		return err == nil && n.Object["spec"].(map[string]any)["unschedulable"] == true
	}, "node not cordoned")

	fc.Reset()
	s.Eventually(func() bool {
		selectPath(t, s, "n1")
		s.Keys(ui.KeyU)
		assert.Contains(t, s.Screen(), "Uncordon n1?")
		s.Keys(tcell.KeyTab, tcell.KeyEnter)
		return len(nodePatchBodies(fc)) == 1
	}, "no uncordon patch")
	assert.Contains(t, nodePatchBodies(fc)[0], `"unschedulable":null`)
}

func TestNodeDrainDialogOptions(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1")))
	s := NewSimApp(t, fc, SimView("nodes"))
	selectPath(t, s, "n1")
	v := topViewer(t, s)

	var got []dao.DrainOptions
	var sels []string
	s.onLoop(func() {
		ShowDrain(v, []string{"n1", "n2"}, dao.DrainOptions{GracePeriodSeconds: -1, Timeout: 5 * time.Second},
			func(_ ResourceViewer, ss []string, o dao.DrainOptions) {
				sels, got = ss, append(got, o)
			})
	})
	s.Sync()
	assert.Contains(t, s.Screen(), "Drain (2) nodes?")
	focusItem(t, s, "GracePeriod:")
	clearField(s, 2)
	s.Keys("30")
	focusItem(t, s, "Timeout:")
	clearField(s, 2)
	s.Keys("1m")
	for _, l := range []string{"Ignore DaemonSets:", "Delete EmptyDir Data:", "Force:", "Disable Eviction:"} {
		focusItem(t, s, l)
		s.Keys(' ')
	}
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	require.Len(t, got, 1)
	assert.Equal(t, []string{"n1", "n2"}, sels)
	assert.Equal(t, dao.DrainOptions{
		GracePeriodSeconds:  30,
		Timeout:             time.Minute,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Force:               true,
		DisableEviction:     true,
	}, got[0])

	t.Run("bad-values-keep-last-good", func(t *testing.T) {
		got = nil
		s.onLoop(func() {
			ShowDrain(v, []string{"n1"}, dao.DrainOptions{GracePeriodSeconds: -1, Timeout: 5 * time.Second},
				func(_ ResourceViewer, _ []string, o dao.DrainOptions) { got = append(got, o) })
		})
		s.Sync()
		assert.Contains(t, s.Screen(), "Drain n1?")
		focusItem(t, s, "GracePeriod:")
		s.Keys("x")
		focusItem(t, s, "Timeout:")
		s.Keys("x")
		focusItem(t, s, "OK")
		s.Keys(tcell.KeyEnter)

		require.Len(t, got, 1)
		assert.Equal(t, dao.DrainOptions{GracePeriodSeconds: -1, Timeout: 5 * time.Second}, got[0])
	})

	t.Run("cancel", func(t *testing.T) {
		got = nil
		s.onLoop(func() {
			ShowDrain(v, []string{"n1"}, dao.DrainOptions{}, func(ResourceViewer, []string, dao.DrainOptions) {
				got = append(got, dao.DrainOptions{})
			})
		})
		focusItem(t, s, "Cancel")
		s.Keys(tcell.KeyEnter)
		assert.Empty(t, got)
		assert.False(t, s.DialogOpen())
	})
}

func TestNodeDrainKey(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simNode("n1"), simNodePod("pod-a"), simNodePod("pod-b")),
		fakecluster.MirrorTypedToDyn(),
	)
	s := NewSimApp(t, fc, SimView("nodes"))
	selectPath(t, s, "n1")
	fc.Reset()

	s.Keys(ui.KeyR)
	assert.Contains(t, s.Screen(), "Drain n1?")
	focusItem(t, s, "GracePeriod:")
	clearField(s, 2)
	s.Keys("7")
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	s.Eventually(func() bool { return len(fc.Deletes()) == 2 }, "pods not drained")
	for _, d := range fc.Deletes() {
		require.NotNil(t, d.Grace)
		assert.Equal(t, int64(7), *d.Grace)
	}
	assert.Len(t, nodePatchBodies(fc), 1)
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "Node n1 drained!") }, "no drain output")
}

func TestNodeDrainDenied(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simNode("n1"), simNodePod("pod-a")),
		fakecluster.FailOn("delete", "pods", 0, errors.New("forbidden")),
	)
	s := NewSimApp(t, fc, SimView("nodes"))
	selectPath(t, s, "n1")

	s.Keys(ui.KeyR)
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	s.Eventually(func() bool { return strings.Contains(s.Flash(), "forbidden") }, "no failure flash")
}

const shellPodYAML = `
image: busybox:9
namespace: ns1
limits:
  cpu: 200m
  memory: 64Mi
labels:
  a: b
command: [sh]
args: [-c, x]
hostPathVolume:
- name: logs
  mountPath: /logs
  hostPath: /var/log
  readOnly: true
`

func shellPodConfig() *config.ShellPod {
	var cfg config.ShellPod
	if err := yaml.Unmarshal([]byte(shellPodYAML), &cfg); err != nil {
		panic(err)
	}
	return &cfg
}

func TestNodeShellPodSpec(t *testing.T) {
	po, err := k9sShellPod("n1", shellPodConfig())
	require.NoError(t, err)

	assert.Equal(t, k9sShellPodName(), po.Name)
	assert.Equal(t, "ns1", po.Namespace)
	assert.Equal(t, map[string]string{"a": "b"}, po.Labels)
	assert.Equal(t, "n1", po.Spec.NodeName)
	assert.True(t, po.Spec.HostPID)
	assert.True(t, po.Spec.HostNetwork)
	assert.Equal(t, corev1.RestartPolicyNever, po.Spec.RestartPolicy)
	assert.Equal(t, int64(0), *po.Spec.TerminationGracePeriodSeconds)
	assert.Equal(t, []corev1.Toleration{{Operator: "Exists"}}, po.Spec.Tolerations)
	require.Len(t, po.Spec.Containers, 1)
	c := po.Spec.Containers[0]
	assert.Equal(t, "busybox:9", c.Image)
	assert.True(t, *c.SecurityContext.Privileged)
	assert.Equal(t, []string{"sh"}, c.Command)
	assert.Equal(t, []string{"-c", "x"}, c.Args)
	assert.Equal(t, "200m", c.Resources.Limits.Cpu().String())
	assert.Equal(t, "64Mi", c.Resources.Limits.Memory().String())
	assert.Equal(t, []corev1.VolumeMount{
		{Name: "root-vol", MountPath: "/host", ReadOnly: true},
		{Name: "logs", MountPath: "/logs", ReadOnly: true},
	}, c.VolumeMounts)
	require.Len(t, po.Spec.Volumes, 2)
	assert.Equal(t, "/", po.Spec.Volumes[0].HostPath.Path)
	assert.Equal(t, "/var/log", po.Spec.Volumes[1].HostPath.Path)
}

func TestNodeShellPodPartialLimits(t *testing.T) {
	cfg := shellPodConfig()
	cfg.Limits = config.Limits{corev1.ResourceMemory: "64Mi"}

	po, err := k9sShellPod("n1", cfg)
	require.ErrorContains(t, err, "missing shell pod cpu limit")
	assert.Nil(t, po)
}

// shellApp starts an app with the node shell feature on.
func shellApp(t *testing.T, fc *fakecluster.Cluster) *SimApp {
	t.Helper()
	s := NewSimApp(t, fc, SimView("nodes"))
	s.onLoop(func() {
		ct, err := s.app.Config.K9s.ActiveContext()
		require.NoError(t, err)
		ct.FeatureGates.NodeShell = true
		s.app.Config.K9s.ShellPod = shellPodConfig()
	})

	return s
}

// setShellPhase sets the phase of created shell pods.
func setShellPhase(fc *fakecluster.Cluster, ph corev1.PodPhase) {
	fc.Typed().PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if po, ok := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod); ok {
			po.Status.Phase = ph
		}
		return false, nil, nil
	})
}

func shellDeletes(fc *fakecluster.Cluster) int {
	var n int
	for _, d := range fc.Deletes() {
		if d.Name == k9sShellPodName() && d.NS == "ns1" {
			n++
		}
	}
	return n
}

func TestNodeShellLaunch(t *testing.T) {
	uu := map[string]struct {
		phase   corev1.PodPhase
		failOn  string
		cancel  bool
		err     string
		deletes int
	}{
		"running": {phase: corev1.PodRunning},
		"failed":  {phase: corev1.PodFailed, err: "failed to start: Failed", deletes: 1},
		"create":  {phase: corev1.PodRunning, failOn: "create", err: "boom"},
		"cancel":  {phase: corev1.PodPending, cancel: true, err: "context canceled", deletes: 1},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			opts := []fakecluster.Option{fakecluster.WithObjects(simNode("n1")), fakecluster.MirrorTypedToDyn()}
			if u.failOn != "" {
				opts = append(opts, fakecluster.FailOn(u.failOn, "pods", 0, errors.New("boom")))
			}
			fc := fakecluster.New(t, opts...)
			setShellPhase(fc, u.phase)
			s := shellApp(t, fc)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if u.cancel {
				time.AfterFunc(100*time.Millisecond, cancel)
			}

			err := launchShellPod(ctx, s.app, "n1")

			if u.err == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, u.err)
			}
			cc := fc.Actions("create", "pods")
			require.Len(t, cc, 1)
			po := cc[0].(k8stesting.CreateAction).GetObject().(*corev1.Pod)
			assert.Equal(t, "n1", po.Spec.NodeName)
			assert.Equal(t, "busybox:9", po.Spec.Containers[0].Image)
			assert.Equal(t, u.deletes, shellDeletes(fc))
		})
	}
}

func TestNodeShellCleanup(t *testing.T) {
	shell := simPod("ns1", k9sShellPodName())

	t.Run("prompt-cancel", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1")), fakecluster.MirrorTypedToDyn())
		setShellPhase(fc, corev1.PodPending)
		s := shellApp(t, fc)
		selectPath(t, s, "n1")
		a, ok := topViewer(t, s).Actions().Get(ui.KeyS)
		require.True(t, ok && a.Description == "Shell", "shell key not bound")

		s.Keys(ui.KeyS)
		s.Eventually(func() bool { return len(fc.Actions("create", "pods")) == 1 }, "shell pod not created")
		require.True(t, s.DialogOpen())
		assert.Contains(t, s.Screen(), "Launching node shell on n1...")
		s.Keys(tcell.KeyEnter)

		s.Eventually(func() bool { return shellDeletes(fc) >= 1 }, "shell pod not deleted on cancel")
	})

	t.Run("app-exit", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1"), shell), fakecluster.MirrorTypedToDyn())
		s := shellApp(t, fc)
		fc.Reset()

		s.Cmd(":q")
		s.Eventually(s.Stopped, "app still running")

		assert.Equal(t, 1, shellDeletes(fc))
		_, err := fc.DynGet(client.PodGVR, "ns1", k9sShellPodName())
		require.Error(t, err)
	})

	t.Run("feature-off", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1"), shell))
		s := NewSimApp(t, fc, SimView("nodes"))
		// Earlier apps can save the gate on exit. Turn it off and rebuild the view.
		s.onLoop(func() {
			ct, err := s.app.Config.K9s.ActiveContext()
			require.NoError(t, err)
			ct.FeatureGates.NodeShell = false
		})
		s.Cmd(":pods")
		s.Cmd(":nodes")
		s.Eventually(func() bool { return s.TopView() == "Node" }, "no node view")
		fc.Reset()

		require.NoError(t, nukeK9sShell(s.app))
		assert.Zero(t, shellDeletes(fc))
		a, ok := topViewer(t, s).Actions().Get(ui.KeyS)
		assert.False(t, ok && a.Description == "Shell", "shell key bound")
	})

	t.Run("missing-pod", func(t *testing.T) {
		s := shellApp(t, fakecluster.New(t, fakecluster.WithObjects(simNode("n1"))))

		require.NoError(t, nukeK9sShell(s.app))
	})
}
