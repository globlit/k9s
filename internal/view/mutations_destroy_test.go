// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func mutDeploy(n string, replicas int32) *appsv1.Deployment {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": n}}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n},
		Spec: appsv1.DeploymentSpec{
			Replicas: new(replicas),
			Selector: sel,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": n}},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{Name: "i1", Image: "busybox:1"}},
					Containers:     []corev1.Container{{Name: "c1", Image: "nginx:1"}},
				},
			},
		},
	}
}

// serveScale serves the deployment scale subresource and records updates.
func serveScale(fc *fakecluster.Cluster) func() []int32 {
	var (
		mx      sync.Mutex
		updates []int32
	)
	fc.Typed().PrependReactor("get", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		return true, &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{Namespace: a.GetNamespace(), Name: a.(k8stesting.GetAction).GetName()},
			Spec:       autoscalingv1.ScaleSpec{Replicas: 3},
		}, nil
	})
	fc.Typed().PrependReactor("update", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		sc := a.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
		mx.Lock()
		updates = append(updates, sc.Spec.Replicas)
		mx.Unlock()
		return true, sc, nil
	})

	return func() []int32 {
		mx.Lock()
		defer mx.Unlock()
		return append([]int32(nil), updates...)
	}
}

func patches(fc *fakecluster.Cluster, res string) []string {
	var out []string
	for _, a := range fc.Actions("patch", res) {
		out = append(out, string(a.(k8stesting.PatchAction).GetPatch()))
	}
	return out
}

func TestDeploymentScaleKey(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 3), mutDeploy("dp-b", 3)))
	updates := serveScale(fc)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))

	t.Run("cancel", func(t *testing.T) {
		selectPath(t, s, "ns1/dp-a")
		s.Keys(ui.KeyS)
		require.True(t, s.DialogOpen())
		assert.Contains(t, s.Screen(), "Scale deployment ns1/dp-a?")
		focusItem(t, s, "Cancel")
		s.Keys(tcell.KeyEnter)
		assert.False(t, s.DialogOpen())
		assert.Empty(t, updates())
	})

	t.Run("zero", func(t *testing.T) {
		selectPath(t, s, "ns1/dp-a")
		s.Keys(ui.KeyS)
		focusItem(t, s, "Replicas:")
		clearField(s, 3)
		s.Keys("-x0")
		focusItem(t, s, "OK")
		s.Keys(tcell.KeyEnter)
		s.Eventually(func() bool { return len(updates()) == 1 }, "no scale update")
		assert.Equal(t, []int32{0}, updates())
		s.Eventually(func() bool { return strings.Contains(s.Flash(), "scaled successfully") }, "no flash")
	})

	t.Run("marked", func(t *testing.T) {
		markPaths(t, s, "ns1/dp-a", "ns1/dp-b")
		s.Keys(ui.KeyS)
		assert.Contains(t, s.Screen(), "Scale [2] deployments?")
		focusItem(t, s, "Replicas:")
		s.Keys("4")
		focusItem(t, s, "OK")
		s.Keys(tcell.KeyEnter)
		s.Eventually(func() bool { return len(updates()) == 3 }, "no scale updates")
		assert.Equal(t, []int32{0, 4, 4}, updates())
		clearMarks(t, s)
	})
}

func TestDeploymentScaleDenied(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(mutDeploy("dp-a", 3)),
		fakecluster.WithRBAC(func(_ string, gvr *client.GVR, _, verb string) bool {
			return !strings.HasSuffix(gvr.String(), ":scale") || verb != "update"
		}),
	)
	updates := serveScale(fc)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")

	s.Keys(ui.KeyS)
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	s.Eventually(func() bool { return strings.Contains(s.Flash(), "not authorized to scale") }, "no denial flash")
	assert.Empty(t, updates())
}

func TestWorkloadRestartKey(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "a"}}
	tpl := mutDeploy("x", 1).Spec.Template
	fc := fakecluster.New(t, fakecluster.WithObjects(
		mutDeploy("dp-a", 1),
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ds-a"}, Spec: appsv1.DaemonSetSpec{Selector: sel, Template: tpl}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "sts-a"}, Spec: appsv1.StatefulSetSpec{Selector: sel, Template: tpl}},
	))
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	uu := []struct{ cmd, path, res string }{
		{"deploy", "ns1/dp-a", "deployments"},
		{"ds", "ns1/ds-a", "daemonsets"},
		{"sts", "ns1/sts-a", "statefulsets"},
	}

	for _, u := range uu {
		t.Run(u.res, func(t *testing.T) {
			s.Cmd(":" + u.cmd)
			selectPath(t, s, u.path)
			fc.Reset()

			s.Keys(ui.KeyR)
			require.True(t, s.DialogOpen())
			s.Keys(tcell.KeyEnter)
			assert.Empty(t, patches(fc, u.res), "cancel sends no patch")

			s.Keys(ui.KeyR)
			focusItem(t, s, "OK")
			s.Keys(tcell.KeyEnter)
			s.Eventually(func() bool { return len(patches(fc, u.res)) == 1 }, "no restart patch")
			assert.Contains(t, patches(fc, u.res)[0], "kubectl.kubernetes.io/restartedAt")
		})
	}
}

func TestDeploymentSetImageKey(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDeploy("dp-a", 1)))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	fc.Reset()

	s.Keys(ui.KeyI)
	require.True(t, s.DialogOpen())
	assert.Contains(t, s.Screen(), "Set image apps/v1/deployments ns1/dp-a")
	focusItem(t, s, "i1")
	clearField(s, len("busybox:1"))
	s.Keys("busybox:2")
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	s.Eventually(func() bool { return len(patches(fc, "deployments")) == 1 }, "no image patch")
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(patches(fc, "deployments")[0]), &body))
	spec := body["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, []any{map[string]any{"name": "i1", "image": "busybox:2"}}, spec["initContainers"])
	assert.Nil(t, spec["containers"])
}

func TestPodSetImageOwnedPod(t *testing.T) {
	po := simPod("ns1", "pod-a")
	po.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs1"}}
	fc := fakecluster.New(t, fakecluster.WithObjects(po))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-a")
	fc.Reset()

	s.Keys(ui.KeyI)
	focusItem(t, s, "c1")
	s.Keys("x")
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)

	s.Eventually(func() bool { return strings.Contains(s.Flash(), "managed by ReplicaSet/rs1") }, "no refusal flash")
	assert.Empty(t, patches(fc, "pods"))
}

func TestCronJobKeys(t *testing.T) {
	cj := batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cj-a", UID: "cj-uid"},
		Spec:       batchv1.CronJobSpec{Schedule: "* * * * *"},
	}
	fc := fakecluster.New(t, fakecluster.WithObjects(&cj))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("cj"))
	selectPath(t, s, "ns1/cj-a")

	t.Run("trigger", func(t *testing.T) {
		fc.Reset()
		s.Keys(ui.KeyT)
		assert.Contains(t, s.Screen(), "Trigger CronJob: ns1/cj-a?")
		s.Keys(tcell.KeyEnter)
		assert.Empty(t, fc.Actions("create", "jobs"))

		s.Keys(ui.KeyT, tcell.KeyTab, tcell.KeyEnter)
		s.Eventually(func() bool { return len(fc.Actions("create", "jobs")) == 1 }, "no job created")
		job := fc.Actions("create", "jobs")[0].(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		assert.Regexp(t, regexp.MustCompile(`\Acj-a-manual-[a-z0-9]{3}\z`), job.Name)
		require.Len(t, job.OwnerReferences, 1)
		assert.Equal(t, "cj-a", job.OwnerReferences[0].Name)
		assert.Equal(t, "CronJob", job.OwnerReferences[0].Kind)
	})

	t.Run("suspend", func(t *testing.T) {
		fc.Reset()
		selectPath(t, s, "ns1/cj-a")
		s.Keys(ui.KeyS)
		s.Eventually(s.DialogOpen, "no suspend dialog")
		assert.Contains(t, s.Screen(), "Suspend")
		s.Keys(tcell.KeyTab, tcell.KeyEnter)
		s.Eventually(func() bool { return len(patches(fc, "cronjobs")) == 1 }, "no suspend patch")
		assert.JSONEq(t, `[{"op":"test","path":"/metadata/uid","value":"cj-uid"},{"op":"add","path":"/spec/suspend","value":true}]`, patches(fc, "cronjobs")[0])
	})
}

func TestPodSanitizeKey(t *testing.T) {
	failed := simPod("ns1", "pod-bad")
	failed.Status.Phase = corev1.PodPending
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-ok"), failed))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-ok")
	fc.Reset()

	s.Keys(ui.KeyZ)
	require.True(t, s.DialogOpen())
	s.Keys("Yes please!")
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)
	assert.True(t, s.DialogOpen(), "wrong word keeps the dialog open")
	assert.Empty(t, fc.Deletes())
	s.Keys(tcell.KeyEscape)

	s.Keys(ui.KeyZ, magicPrompt)
	focusItem(t, s, "OK")
	s.Keys(tcell.KeyEnter)
	s.Eventually(func() bool { return len(fc.Deletes()) == 1 }, "no sanitize delete")
	assert.Equal(t, "pod-bad", fc.Deletes()[0].Name)
	assert.Nil(t, fc.Deletes()[0].Grace)
	s.Eventually(func() bool { return strings.Contains(s.Flash(), "Sanitized 1") }, "no flash")
}

var (
	fluxKsGVR = client.NewGVR("kustomize.toolkit.fluxcd.io/v1/kustomizations")
	fluxHrGVR = client.NewGVR("helm.toolkit.fluxcd.io/v2/helmreleases")
)

func fluxObj(gvr *client.GVR, kind, n string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GV().String(), "kind": kind,
		"metadata": map[string]any{"namespace": "ns1", "name": n},
		"spec":     spec,
	}}
}

func fluxPatch(t *testing.T, fc *fakecluster.Cluster, res string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, p := range patches(fc, res) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(p), &m))
		out = append(out, m)
	}
	return out
}

func annotations(m map[string]any) map[string]any {
	md, _ := m["metadata"].(map[string]any)
	aa, _ := md["annotations"].(map[string]any)
	return aa
}

// fluxTop returns the flux extender on top. Generic CR views list through a
// server-side table client the fake cannot serve, so drive the extender.
func fluxTop(t *testing.T, s *SimApp) *FluxExtender {
	t.Helper()
	var f *FluxExtender
	s.Eventually(func() bool {
		s.onLoop(func() { f, _ = s.app.Content.Top().(*FluxExtender) })
		return f != nil
	}, "no flux view")

	return f
}

func TestFluxActions(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithCRD(fluxKsGVR, "Kustomization", true),
		fakecluster.WithCRD(fluxHrGVR, "HelmRelease", true),
		fakecluster.WithObjects(
			fluxObj(fluxKsGVR, "Kustomization", "k1", map[string]any{}),
			fluxObj(fluxHrGVR, "HelmRelease", "h1", map[string]any{}),
		),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Cmd(":kustomizations")
	f := fluxTop(t, s)
	sels := []string{"ns1/k1"}

	t.Run("reconcile", func(t *testing.T) {
		fc.Reset()
		s.onLoop(func() { f.reconcile(sels, "Reconcile") })
		s.Eventually(func() bool { return len(patches(fc, "kustomizations")) == 1 }, "no reconcile patch")
		aa := annotations(fluxPatch(t, fc, "kustomizations")[0])
		assert.NotEmpty(t, aa["reconcile.fluxcd.io/requestedAt"])
	})

	t.Run("suspend", func(t *testing.T) {
		fc.Reset()
		s.onLoop(func() { f.confirmSuspend(sels, 0) })
		s.Sync()
		assert.Contains(t, s.Screen(), "Suspend kustomization ns1/k1?")
		s.Keys(tcell.KeyEnter)
		assert.Empty(t, patches(fc, "kustomizations"), "cancel sends no patch")

		s.onLoop(func() { f.confirmSuspend(sels, 0) })
		s.Keys(tcell.KeyTab, tcell.KeyEnter)
		s.Eventually(func() bool { return len(patches(fc, "kustomizations")) == 1 }, "no suspend patch")
		assert.Equal(t, map[string]any{"suspend": true}, fluxPatch(t, fc, "kustomizations")[0]["spec"])
	})

	t.Run("reconcile-suspended", func(t *testing.T) {
		fc.Reset()
		s.onLoop(func() { f.reconcile(sels, "Reconcile") })
		s.Eventually(func() bool { return strings.Contains(s.Flash(), "is suspended: resume it first") }, "no suspended flash")
		assert.Empty(t, patches(fc, "kustomizations"))
	})

	t.Run("resume", func(t *testing.T) {
		fc.Reset()
		s.onLoop(func() { f.confirmSuspend(sels, 1) })
		s.Sync()
		assert.Contains(t, s.Screen(), "Resume kustomization ns1/k1?")
		s.Keys(tcell.KeyTab, tcell.KeyEnter)
		s.Eventually(func() bool { return len(patches(fc, "kustomizations")) == 1 }, "no resume patch")
		p := fluxPatch(t, fc, "kustomizations")[0]
		assert.Equal(t, map[string]any{"suspend": false}, p["spec"])
		assert.NotEmpty(t, annotations(p)["reconcile.fluxcd.io/requestedAt"])
	})

	t.Run("helm-force", func(t *testing.T) {
		s.Cmd(":helmreleases")
		hf := fluxTop(t, s)
		fc.Reset()
		s.onLoop(func() { hf.reconcile([]string{"ns1/h1"}, "Force reconcile", dao.FluxForceAtAnn) })
		s.Eventually(func() bool { return len(patches(fc, "helmreleases")) == 1 }, "no force patch")
		aa := annotations(fluxPatch(t, fc, "helmreleases")[0])
		assert.Equal(t, aa["reconcile.fluxcd.io/requestedAt"], aa[dao.FluxForceAtAnn])
		assert.Nil(t, aa[dao.FluxResetAtAnn])
	})
}

// stubBin writes an executable script named n on PATH that appends its args
// to a log file. It returns the log path.
func stubBin(t *testing.T, n string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, n+".log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\necho done\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return log
}

func readLog(p string) string {
	bb, _ := os.ReadFile(p)
	return string(bb)
}

func TestDirApplyDelete(t *testing.T) {
	log := stubBin(t, "kubectl")
	root := t.TempDir()
	manifest := filepath.Join(root, "a.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte("kind: ConfigMap\n"), 0o600))
	nested := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, "sub"), 0o700))
	kust := filepath.Join(root, "kust")
	require.NoError(t, os.MkdirAll(kust, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(kust, "kustomization.yaml"), []byte("resources: []\n"), 0o600))

	s := NewSimApp(t, fakecluster.New(t), SimNamespace("ns1"))
	uu := []struct {
		path, flags string
	}{
		{manifest, "-f " + manifest},
		{nested, "-f -R " + nested},
		{kust, "-k " + kust},
	}

	for _, u := range uu {
		t.Run(filepath.Base(u.path), func(t *testing.T) {
			s.Cmd(":dir " + root)
			selectPath(t, s, u.path)
			require.NoError(t, os.WriteFile(log, nil, 0o600))

			s.Keys(ui.KeyA)
			s.Eventually(func() bool { return strings.Contains(readLog(log), "apply") }, "no kubectl apply")
			assert.Equal(t, "--context fake --kubeconfig "+kubeconfigOf(s)+" apply "+u.flags+"\n", readLog(log))
			s.Eventually(func() bool { return s.TopView() != "Directory" }, "no apply result")
			s.Keys(tcell.KeyEscape)

			s.Cmd(":dir " + root)
			selectPath(t, s, u.path)
			require.NoError(t, os.WriteFile(log, nil, 0o600))
			s.Keys(ui.KeyD)
			require.True(t, s.DialogOpen())
			s.Keys(tcell.KeyEnter)
			assert.Empty(t, readLog(log), "cancel runs no kubectl")
			s.Keys(ui.KeyD, tcell.KeyTab, tcell.KeyEnter)
			s.Eventually(func() bool { return strings.Contains(readLog(log), "delete") }, "no kubectl delete")
			assert.Equal(t, "--context fake --kubeconfig "+kubeconfigOf(s)+" delete "+u.flags+"\n", readLog(log))
			s.Eventually(func() bool { return s.TopView() != "Directory" }, "no delete result")
			s.Keys(tcell.KeyEscape)
		})
	}
}

func kubeconfigOf(s *SimApp) string {
	if p := s.fc.Conn().Config().Flags().KubeConfig; p != nil {
		return *p
	}
	return ""
}

const execPlugins = `plugins:
  echo:
    shortCut: Ctrl-T
    description: echo
    scopes: [pods]
    command: %s
    background: true
    confirm: true
    args: [$NAMESPACE, $NAME, $CONTEXT]
  tagged:
    shortCut: Ctrl-Y
    description: tagged
    scopes: [pods]
    command: %s
    background: true
    args: [$NAME, $INPUT_TAG, $INPUT_DRY]
    inputs:
    - name: tag
      type: string
      required: true
    - name: dry
      type: bool
      default: "true"
  unknown:
    shortCut: Ctrl-O
    description: unknown
    scopes: [pods]
    command: %s
    background: true
    args: [$NOPE]
  shadowed:
    shortCut: Ctrl-G
    description: shadowed
    scopes: [pods]
    command: %s
    background: true
    args: [shadowed]
`

func TestPluginExecution(t *testing.T) {
	log := stubBin(t, "k9splug")
	bin := filepath.Join(filepath.Dir(log), "k9splug")
	p := filepath.Join(t.TempDir(), "plugins.yaml")
	require.NoError(t, os.WriteFile(p, []byte(strings.ReplaceAll(execPlugins, "%s", bin)), 0o600))
	old := config.AppPluginsFile
	config.AppPluginsFile = p
	t.Cleanup(func() { config.AppPluginsFile = old })

	s := NewSimApp(t, fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a"))), SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-a")
	s.Eventually(func() bool {
		_, ok := topViewer(t, s).Actions().Get(tcell.KeyCtrlT)
		return ok
	}, "plugins not loaded")

	t.Run("confirm", func(t *testing.T) {
		s.Keys(tcell.KeyCtrlT)
		require.True(t, s.DialogOpen())
		assert.Contains(t, s.Screen(), "Run?")
		s.Keys(tcell.KeyEnter)
		assert.Empty(t, readLog(log), "cancel runs nothing")

		s.Keys(tcell.KeyCtrlT, tcell.KeyTab, tcell.KeyEnter)
		s.Eventually(func() bool { return readLog(log) != "" }, "plugin not run")
		assert.Equal(t, "ns1 pod-a fake\n", readLog(log))
	})

	t.Run("inputs", func(t *testing.T) {
		require.NoError(t, os.WriteFile(log, nil, 0o600))
		require.Equal(t, "Pod", s.TopView())
		selectPath(t, s, "ns1/pod-a")
		s.Keys(tcell.KeyCtrlY)
		require.True(t, s.DialogOpen())
		focusItem(t, s, "OK")
		s.Keys(tcell.KeyEnter)
		assert.True(t, s.DialogOpen(), "required input keeps the dialog open")
		focusItem(t, s, "tag *:")
		s.Keys("v2")
		focusItem(t, s, "OK")
		s.Keys(tcell.KeyEnter)
		require.True(t, s.DialogOpen(), "inputs ask for confirmation")
		s.Keys(tcell.KeyTab, tcell.KeyEnter)

		s.Eventually(func() bool { return readLog(log) != "" }, "plugin not run")
		assert.Equal(t, "pod-a v2 true\n", readLog(log))
	})

	// Pin: the app binds Ctrl-G, so a plugin on Ctrl-G is listed but never runs.
	t.Run("shadowed-by-app-key", func(t *testing.T) {
		require.NoError(t, os.WriteFile(log, nil, 0o600))
		a, ok := topViewer(t, s).Actions().Get(tcell.KeyCtrlG)
		require.True(t, ok)
		assert.Equal(t, "shadowed", a.Description)

		s.Keys(tcell.KeyCtrlG)
		s.Keys(tcell.KeyCtrlG)

		assert.False(t, s.DialogOpen())
		assert.Empty(t, readLog(log))
	})

	// Pin: an unknown variable reaches the command as a literal.
	t.Run("unknown-variable", func(t *testing.T) {
		require.NoError(t, os.WriteFile(log, nil, 0o600))
		s.Keys(tcell.KeyCtrlO)

		s.Eventually(func() bool { return readLog(log) != "" }, "plugin not run")
		assert.Equal(t, "$NOPE\n", readLog(log))
	})
}
