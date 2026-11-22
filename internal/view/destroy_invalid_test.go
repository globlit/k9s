// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failBin writes a kubectl stub on PATH that logs its args and exits with 3.
func failBin(t *testing.T, n string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, n+".log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\necho 'error: bad manifest' >&2\nexit 3\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return log
}

func TestDirApplyDeleteFailure(t *testing.T) {
	log := failBin(t, "kubectl")
	root := t.TempDir()
	manifest := filepath.Join(root, "a.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte("kind: [\n"), 0o600))
	s := NewSimApp(t, fakecluster.New(t), SimNamespace("ns1"))

	t.Run("apply", func(t *testing.T) {
		s.Cmd(":dir " + root)
		selectPath(t, s, manifest)
		s.Keys(ui.KeyA)
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "status:") }, "no failure status")
		assert.Contains(t, readLog(log), "apply -f "+manifest)
		s.Keys(tcell.KeyEscape)
	})
	t.Run("delete", func(t *testing.T) {
		require.NoError(t, os.WriteFile(log, nil, 0o600))
		s.Cmd(":dir " + root)
		selectPath(t, s, manifest)
		s.Keys(ui.KeyD)
		confirmOK(t, s)
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "status:") }, "no failure status")
		assert.Contains(t, readLog(log), "delete -f "+manifest)
		s.Keys(tcell.KeyEscape)
	})
}

func TestDirEmptySelection(t *testing.T) {
	log := stubBin(t, "kubectl")
	s := NewSimApp(t, fakecluster.New(t), SimNamespace("ns1"))
	s.Cmd(":dir " + t.TempDir())
	evt := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	var d *Dir
	s.Eventually(func() bool {
		s.OnLoop(func() { d, _ = s.app.Content.Top().(*Dir) })
		return d != nil
	}, "no dir view")
	s.OnLoop(func() {
		assert.Equal(t, evt, d.applyCmd(evt))
		assert.Equal(t, evt, d.delCmd(evt))
	})
	assert.False(t, s.DialogOpen())
	assert.Empty(t, readLog(log))
}

// workloadDeleteSels opens the workload delete dialog on sels.
func workloadDeleteSels(t *testing.T, s *SimApp, sels ...string) {
	t.Helper()
	s.onLoop(func() {
		w, ok := s.app.Content.Top().(*Workload)
		require.True(t, ok)
		w.resourceDelete(sels, "Delete?")
	})
	s.Sync()
}

func TestWorkloadDeleteAPIErrors(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simPod("ns1", "pod-a"), simPod("ns1", "pod-b")),
		fakecluster.FailOn("delete", "pods", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("wk"))
	s.Eventually(func() bool { return s.TopView() == "Workload" }, "no workload view")

	t.Run("single", func(t *testing.T) {
		fc.Reset()
		workloadDeleteSels(t, s, "v1/pods|ns1|pod-a")
		s.ConfirmDelete("Background", false)
		s.Eventually(flashHas(s, "Delete failed with `boom"), "no failure flash")
		assert.Len(t, fc.Deletes(), 1)
	})
	t.Run("marked", func(t *testing.T) {
		fc.Reset()
		workloadDeleteSels(t, s, "v1/pods|ns1|pod-a", "bad-path", "v1/pods|ns1|pod-b")
		s.ConfirmDelete("Foreground", true)
		s.Eventually(flashHas(s, "delete failed for 3/3"), "no batch flash")
		assert.Len(t, fc.Deletes(), 2, "a bad path sends no call")
	})
	t.Run("single-bad-path", func(t *testing.T) {
		fc.Reset()
		workloadDeleteSels(t, s, "v1/pods/ns1/pod-a")
		s.ConfirmDelete("Background", false)
		s.Eventually(flashHas(s, "unable to parse path"), "no parse flash")
		assert.Empty(t, fc.Deletes())
	})
}

func TestWorkloadDeleteEmpty(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithNamespaces("empty"))
	s := NewSimApp(t, fc, SimNamespace("empty"), SimView("wk"))
	s.Eventually(func() bool { return s.TopView() == "Workload" }, "no workload view")
	fc.Reset()

	s.Keys(tcell.KeyCtrlD)
	assert.False(t, s.DialogOpen())
	assert.Empty(t, mutations(fc))
}

func TestXrayDeleteInvalid(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simPod("ns1", "pod-a")),
		fakecluster.FailOn("delete", "pods", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Cmd(":xray po")
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-a") }, "xray pod not shown")

	t.Run("api-error", func(t *testing.T) {
		xraySelect(t, s, "ns1/pod-a")
		fc.Reset()
		s.Keys(tcell.KeyCtrlD)
		s.ConfirmDelete("Background", false)
		s.Eventually(flashHas(s, "Delete failed with `boom"), "no failure flash")
		assert.Len(t, fc.Deletes(), 1)
	})
	var x *Xray
	s.OnLoop(func() { x, _ = s.app.Content.Top().(*Xray) })
	require.NotNil(t, x)
	evt := tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone)
	t.Run("container", func(t *testing.T) {
		xraySelect(t, s, "ns1/c1")
		fc.Reset()
		s.OnLoop(func() { x.deleteCmd(evt) })
		if s.DialogOpen() {
			s.ConfirmDelete("Background", false)
			s.Eventually(flashHas(s, "Invalid nuker"), "no nuker flash")
		}
		assert.Empty(t, fc.Deletes())
	})
	t.Run("root", func(t *testing.T) {
		var got *tcell.EventKey
		s.OnLoop(func() {
			x.SetCurrentNode(nil)
			got = x.deleteCmd(evt)
		})
		assert.Equal(t, evt, got)
		assert.False(t, s.DialogOpen())
	})
}

func TestPortForwardDeleteInvalid(t *testing.T) {
	fc := fakecluster.New(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	bad := &simFwd{id: "ns1/pod-x|c1|abc:80"}
	fa, fb := newSimFwd("ns1/pod-a"), newSimFwd("ns1/pod-b")
	s.Cmd(":pf")
	s.Eventually(func() bool { return s.TopView() != "" }, "no pf view")
	var p *PortForward
	s.Eventually(func() bool {
		s.OnLoop(func() { p, _ = s.app.Content.Top().(*PortForward) })
		return p != nil
	}, "no pf view")
	evt := tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone)

	t.Run("empty", func(t *testing.T) {
		var got *tcell.EventKey
		s.OnLoop(func() { got = p.deleteCmd(evt) })
		assert.Equal(t, evt, got)
		assert.False(t, s.DialogOpen())
	})
	t.Run("cmd-buffer", func(t *testing.T) {
		var got *tcell.EventKey
		s.OnLoop(func() {
			p.GetTable().CmdBuff().SetText("x", "", false)
			got = p.deleteCmd(evt)
		})
		assert.Nil(t, got)
		s.OnLoop(func() { assert.True(t, p.GetTable().CmdBuff().Empty()) })
		assert.False(t, s.DialogOpen())
	})

	s.app.factory.AddForwarder(bad)
	s.app.factory.AddForwarder(fa)
	s.app.factory.AddForwarder(fb)
	t.Run("unparsable", func(t *testing.T) {
		selectPath(t, s, bad.id)
		s.OnLoop(func() { p.deleteCmd(evt) })
		s.Eventually(flashHas(s, "unable to parse selection ns1/pod-x|c1|abc:80"), "no parse flash")
		assert.False(t, s.DialogOpen())
		assert.False(t, bad.stopped.Load())
	})
	t.Run("marked", func(t *testing.T) {
		markPaths(t, s, fa.id, fb.id)
		s.OnLoop(func() { p.deleteCmd(evt) })
		require.True(t, s.DialogOpen(), s.Screen())
		confirmOK(t, s)
		s.Eventually(flashHas(s, "Successfully deleted 2 PortForward"), "no delete flash")
		assert.True(t, fa.stopped.Load())
		assert.True(t, fb.stopped.Load())
		assert.False(t, bad.stopped.Load())
	})
}
