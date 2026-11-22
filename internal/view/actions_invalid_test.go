// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"os"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
)

func TestFluxCmdsEmptySelection(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithCRD(fluxKsGVR, "Kustomization", true),
		fakecluster.WithObjects(fluxObj(fluxKsGVR, "Kustomization", "k1", map[string]any{})),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Cmd(":kustomizations")
	f := fluxTop(t, s)
	fc.Reset()
	uu := map[string]func(*tcell.EventKey) *tcell.EventKey{
		"reconcile":   f.reconcileCmd,
		"with-source": f.reconcileWithSourceCmd,
		"helm":        f.helmUpgradeCmd,
		"suspend":     f.suspendCmd,
		"jump":        f.jumpSourceCmd,
	}
	for k, cmd := range uu {
		t.Run(k, func(t *testing.T) {
			var got *tcell.EventKey
			s.OnLoop(func() { got = cmd(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)) })
			assert.Nil(t, got)
			assert.False(t, s.DialogOpen())
		})
	}
	s.Sync()
	assert.Empty(t, mutations(fc))
}

func TestFluxActionsInvalid(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithCRD(fluxKsGVR, "Kustomization", true),
		fakecluster.WithObjects(
			fluxObj(fluxKsGVR, "Kustomization", "k1", map[string]any{}),
			fluxObj(fluxKsGVR, "Kustomization", "k2", map[string]any{"suspend": true}),
		),
		fakecluster.FailOn("patch", "kustomizations", 0, errInvalid),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Cmd(":kustomizations")
	f := fluxTop(t, s)

	t.Run("missing", func(t *testing.T) {
		fc.Reset()
		s.OnLoop(func() { f.reconcile([]string{"ns1/nope"}, "Reconcile") })
		s.Eventually(flashHas(s, "Reconcile failed: "), "no failure flash")
		assert.Contains(t, s.Flash(), "not found")
		assert.Empty(t, patches(fc, "kustomizations"))
	})
	t.Run("patch-error", func(t *testing.T) {
		fc.Reset()
		s.OnLoop(func() { f.reconcile([]string{"ns1/k1"}, "Reconcile") })
		s.Eventually(flashHas(s, "Reconcile failed: kustomizations ns1/k1: boom"), "no patch flash")
	})
	t.Run("mixed-batch", func(t *testing.T) {
		fc.Reset()
		s.OnLoop(func() { f.reconcile([]string{"ns1/k1", "ns1/k2", "ns1/nope"}, "Reconcile") })
		s.Eventually(flashHas(s, "failed for 3/3"), "no batch flash")
		assert.Len(t, patches(fc, "kustomizations"), 1, "only the active object gets a patch")
	})
	t.Run("suspend-patch-error", func(t *testing.T) {
		fc.Reset()
		s.OnLoop(func() { f.confirmSuspend([]string{"ns1/k1", "ns1/k2"}, 1) })
		s.Sync()
		assert.Contains(t, s.Screen(), "(1 already suspended)")
		confirmOK(t, s)
		s.Eventually(flashHas(s, "failed for 2/2"), "no suspend flash")
		assert.Len(t, patches(fc, "kustomizations"), 2)
	})
}

func TestContextDeleteInvalid(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithContext("other"))
	s := NewSimApp(t, fc)
	s.Cmd(":ctx")
	selectPath(t, s, "fake")
	kc := kubeconfigOf(s)
	before, err := os.ReadFile(kc)
	require.NoError(t, err)

	t.Run("read-only-kubeconfig", func(t *testing.T) {
		require.NoError(t, os.Chmod(kc, 0o400))
		t.Cleanup(func() { _ = os.Chmod(kc, 0o600) })
		s.Keys(tcell.KeyCtrlD)
		assert.Contains(t, s.Screen(), `Delete context "fake"?`)
		confirmOK(t, s)
		s.Eventually(flashHas(s, "permission denied"), "no write flash")
		after, err := os.ReadFile(kc)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})
	t.Run("writable-kubeconfig", func(t *testing.T) {
		selectPath(t, s, "fake")
		s.Keys(tcell.KeyCtrlD)
		confirmOK(t, s)
		s.Eventually(func() bool {
			cfg, err := clientcmd.LoadFromFile(kc)
			return err == nil && cfg.Contexts["fake"] == nil && cfg.Contexts["other"] != nil
		}, "context not deleted")
	})
	t.Run("empty", func(t *testing.T) {
		var c *Context
		s.OnLoop(func() { c, _ = s.app.Content.Top().(*Context) })
		require.NotNil(t, c)
		evt := tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone)
		var got *tcell.EventKey
		s.OnLoop(func() {
			c.GetTable().Select(0, 0)
			got = c.deleteCmd(evt)
		})
		assert.Equal(t, evt, got)
		assert.False(t, s.DialogOpen())
	})
}

func TestHelmHistoryInvalid(t *testing.T) {
	fc := fakecluster.New(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	h := NewHistory(client.HmhGVR).(*History)
	s.OnLoop(func() { _ = s.app.inject(h, false) })
	evt := tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone)

	t.Run("empty", func(t *testing.T) {
		var got *tcell.EventKey
		s.OnLoop(func() { got = h.rollbackCmd(evt) })
		assert.Equal(t, evt, got)
		assert.False(t, s.DialogOpen())
	})
	t.Run("values-without-revision", func(t *testing.T) {
		s.OnLoop(func() { h.getValsCmd(s.app, nil, client.HmhGVR, "ns1/r1") })
		s.Eventually(flashHas(s, `unable to parse version in "ns1/r1"`), "no parse flash")
	})
	t.Run("rollback-unreachable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), simTimeout)
		defer cancel()
		for _, rev := range []string{"", "x", "-1", "1"} {
			require.Error(t, h.rollback(ctx, "ns1/r1", rev), rev)
		}
	})
	assert.Empty(t, mutations(fc))
}
