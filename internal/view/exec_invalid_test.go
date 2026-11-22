// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestShellPodFailed(t *testing.T) {
	waiting := func(reason string) *corev1.Pod {
		return &corev1.Pod{Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "m"},
			}}},
		}}
	}
	uu := map[string]struct {
		pod    *corev1.Pod
		reason string
		failed bool
	}{
		"failed":      {pod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}, reason: "Failed", failed: true},
		"succeeded":   {pod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}, reason: "Succeeded", failed: true},
		"running":     {pod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
		"no-phase":    {pod: &corev1.Pod{}},
		"pull":        {pod: waiting("ImagePullBackOff"), reason: "ImagePullBackOff: m", failed: true},
		"bad-image":   {pod: waiting("InvalidImageName"), reason: "InvalidImageName: m", failed: true},
		"bad-config":  {pod: waiting("CreateContainerConfigError"), reason: "CreateContainerConfigError: m", failed: true},
		"creating":    {pod: waiting("ContainerCreating")},
		"no-reason":   {pod: waiting("")},
		"not-waiting": {pod: &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{}}}}},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			reason, failed := shellPodFailed(u.pod)
			assert.Equal(t, u.reason, reason)
			assert.Equal(t, u.failed, failed)
		})
	}
}

func TestNodeShellLaunchInvalid(t *testing.T) {
	t.Run("pull-backoff", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1")), fakecluster.MirrorTypedToDyn())
		fc.Typed().PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if po, ok := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod); ok {
				po.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "no image"},
				}}}
			}
			return false, nil, nil
		})
		s := shellApp(t, fc)

		err := launchShellPod(context.Background(), s.app, "n1")

		require.ErrorContains(t, err, "failed to start: ImagePullBackOff: no image")
		assert.Equal(t, 1, shellDeletes(fc), "a failed shell pod is deleted")
	})
	t.Run("malformed-pod", func(t *testing.T) {
		bad := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"namespace": "ns1", "name": k9sShellPodName()},
			"status":   "oops",
		}}
		fc := fakecluster.New(t, fakecluster.WithObjects(simNode("n1"), bad))
		s := shellApp(t, fc)

		err := launchShellPod(context.Background(), s.app, "n1")

		require.ErrorContains(t, err, "cannot restore struct")
		assert.Equal(t, 1, shellDeletes(fc))
	})
	t.Run("cleanup-fails-before-launch", func(t *testing.T) {
		fc := fakecluster.New(t,
			fakecluster.WithObjects(simNode("n1")),
			fakecluster.FailOn("delete", "pods", 0, errInvalid),
		)
		s := shellApp(t, fc)
		selectPath(t, s, "n1")
		fc.Reset()

		s.Keys(ui.KeyS)

		s.Eventually(flashHas(s, "Cleaning node shell failed: boom"), "no cleanup flash")
		assert.False(t, s.DialogOpen())
		assert.Empty(t, fc.Actions("create", "pods"))
	})
	t.Run("create-fails", func(t *testing.T) {
		fc := fakecluster.New(t,
			fakecluster.WithObjects(simNode("n1")),
			fakecluster.FailOn("create", "pods", 0, errInvalid),
		)
		s := shellApp(t, fc)
		selectPath(t, s, "n1")

		s.Keys(ui.KeyS)

		s.Eventually(flashHas(s, "Launching node shell failed: boom"), "no launch flash")
		assert.Zero(t, shellDeletes(fc)-1, "only the pre-launch cleanup deletes")
	})
	t.Run("cancel-cleanup-fails", func(t *testing.T) {
		fc := fakecluster.New(t,
			fakecluster.WithObjects(simNode("n1")),
			fakecluster.MirrorTypedToDyn(),
		)
		var deletes atomic.Int32
		fc.Typed().PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			if deletes.Add(1) > 1 {
				return true, nil, errInvalid
			}
			return false, nil, nil
		})
		setShellPhase(fc, corev1.PodPending)
		s := shellApp(t, fc)
		selectPath(t, s, "n1")

		s.Keys(ui.KeyS)
		s.Eventually(func() bool { return len(fc.Actions("create", "pods")) == 1 }, "shell pod not created")
		s.Keys(tcell.KeyEnter)

		s.Eventually(flashHas(s, "Cleaning node shell failed: boom"), "no cleanup flash")
	})
}

const invalidPlugins = `plugins:
  missing:
    shortCut: Ctrl-T
    description: missing
    scopes: [pods]
    command: %[1]s/k9s-no-such-bin
    background: true
    args: [x]
  failing:
    shortCut: Ctrl-Y
    description: failing
    scopes: [pods]
    command: %[1]s/kfail
    background: true
    pipes: ["", "'unclosed"]
    args: [$NAME]
  badkey:
    shortCut: Ctrl-Nope
    description: badkey
    scopes: [pods]
    command: x
  conflict:
    shortCut: Ctrl-D
    description: conflict
    scopes: [pods]
    command: x
  override:
    shortCut: Ctrl-K
    override: true
    description: override
    scopes: [pods]
    command: %[1]s/kfail
    background: true
  other-scope:
    shortCut: Shift-Q
    description: other
    scopes: [deploy]
    command: x
`

func TestPluginsInvalid(t *testing.T) {
	dir := filepath.Dir(failBin(t, "kfail"))
	p := filepath.Join(t.TempDir(), "plugins.yaml")
	require.NoError(t, os.WriteFile(p, []byte(strings.ReplaceAll(invalidPlugins, "%[1]s", dir)), 0o600))
	old := config.AppPluginsFile
	config.AppPluginsFile = p
	t.Cleanup(func() { config.AppPluginsFile = old })
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	selectPath(t, s, "ns1/pod-a")
	s.Eventually(func() bool {
		_, ok := topViewer(t, s).Actions().Get(tcell.KeyCtrlT)
		return ok
	}, "plugins not loaded")
	po, ok := topViewer(t, s).(*Pod)
	require.True(t, ok)
	v := po.browser

	t.Run("load-errors", func(t *testing.T) {
		var err error
		s.OnLoop(func() { err = pluginActions(v, v.Actions()) })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Ctrl-Nope")
		assert.Contains(t, err.Error(), `plugin "conflict" key "Ctrl-D" conflicts with built-in shortcut`)
		a, ok := v.Actions().Get(tcell.KeyCtrlK)
		require.True(t, ok)
		assert.Equal(t, "override", a.Description)
		a, ok = v.Actions().Get(tcell.KeyCtrlD)
		require.True(t, ok)
		assert.NotEqual(t, "conflict", a.Description)
		_, ok = v.Actions().Get(ui.KeyShiftQ)
		assert.False(t, ok, "out of scope plugins stay unbound")
	})
	t.Run("missing-binary", func(t *testing.T) {
		s.Keys(tcell.KeyCtrlT)
		s.Eventually(flashHas(s, `Plugin "missing" failed: fork/exec`), "no failure flash")
	})
	t.Run("failing-command", func(t *testing.T) {
		selectPath(t, s, "ns1/pod-a")
		s.Keys(tcell.KeyCtrlY)
		s.Eventually(flashHas(s, `Plugin "failing" failed`), "no failure flash")
	})
	t.Run("override-runs-plugin", func(t *testing.T) {
		selectPath(t, s, "ns1/pod-a")
		fc.Reset()
		s.Keys(tcell.KeyCtrlK)
		s.Eventually(flashHas(s, `Plugin "override" failed`), "no plugin flash")
		assert.Empty(t, fc.Deletes(), "the override replaces kill")
	})
	t.Run("offline", func(t *testing.T) {
		fc.SetConnected(false)
		t.Cleanup(func() { fc.SetConnected(true) })
		var err error
		s.OnLoop(func() { err = pluginActions(v, v.Actions()) })
		require.NoError(t, err)
	})
	t.Run("empty-selection", func(t *testing.T) {
		evt := tcell.NewEventKey(tcell.KeyCtrlT, 0, tcell.ModNone)
		h := pluginAction(&emptyRunner{Runner: v}, &config.Plugin{Command: "x"})
		assert.Equal(t, evt, h(evt))
	})
}

// emptyRunner reports no selected item.
type emptyRunner struct {
	Runner
}

func (*emptyRunner) GetSelectedItem() string { return "" }
