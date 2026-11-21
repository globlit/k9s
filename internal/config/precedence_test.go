// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hkGlobal = `hotKeys:
  a: {shortCut: Shift-0, description: global a, command: pods}
  b: {shortCut: Shift-1, description: global b, command: svc}
`
	hkContext = `hotKeys:
  a: {shortCut: Shift-0, description: ctx a, command: dp, keepHistory: true}
  c: {shortCut: Shift-2, description: ctx c, command: ns}
`
)

func TestHotKeysPrecedence(t *testing.T) {
	uu := map[string]struct {
		global, ctx string
		err         bool
		want        map[string]string
	}{
		"both": {
			global: hkGlobal, ctx: hkContext,
			want: map[string]string{"a": "dp", "b": "svc", "c": "ns"},
		},
		"global-only": {
			global: hkGlobal,
			want:   map[string]string{"a": "pods", "b": "svc"},
		},
		"context-only": {
			ctx:  hkContext,
			want: map[string]string{"a": "dp", "c": "ns"},
		},
		"none": {want: map[string]string{}},
		"global-broken": {
			global: "hotKeys: [\n", ctx: hkContext, err: true,
			want: map[string]string{},
		},
		"context-broken": {
			global: hkGlobal, ctx: "hotKeys: [\n", err: true,
			want: map[string]string{"a": "pods", "b": "svc"},
		},
		"schema-invalid-loads": {
			global: "hotKeys:\n  a: {shortCut: Shift-0, command: pods, bogus: 1}\n",
			want:   map[string]string{"a": "pods"},
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			dir := t.TempDir()
			global, ctx := filepath.Join(dir, "hotkeys.yaml"), filepath.Join(dir, "ctx", "hotkeys.yaml")
			ff := map[string]string{}
			if u.global != "" {
				ff["hotkeys.yaml"] = u.global
			}
			if u.ctx != "" {
				ff["ctx/hotkeys.yaml"] = u.ctx
			}
			writeFiles(t, dir, ff)
			setVar(t, &AppHotKeysFile, global)

			h := NewHotKeys()
			err := h.Load(ctx)
			if u.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			got := make(map[string]string, len(h.HotKey))
			for k, v := range h.HotKey {
				got[k] = v.Command
			}
			assert.Equal(t, u.want, got)
		})
	}
}

func TestHotKeysContextKeepsFields(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"hotkeys.yaml": hkGlobal, "ctx.yaml": hkContext})
	setVar(t, &AppHotKeysFile, filepath.Join(dir, "hotkeys.yaml"))

	h := NewHotKeys()
	require.NoError(t, h.Load(filepath.Join(dir, "ctx.yaml")))
	assert.Equal(t, HotKey{
		ShortCut:    "Shift-0",
		Description: "ctx a",
		Command:     "dp",
		KeepHistory: true,
	}, h.HotKey["a"])
}

func TestAliasesPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"aliases.yaml": "aliases:\n  dp: apps/v1/deployments\n  pp: v1/pods\n",
		"ctx/aliases.yaml": "aliases:\n  pp: v1/services\n  x: v1/configmaps\n  cc: v1/configmaps\n" +
			"  gone: null\n",
	})
	setVar(t, &AppConfigDir, dir)

	a := NewAliases()
	require.NoError(t, a.Load(filepath.Join(dir, "ctx", "aliases.yaml")))

	uu := map[string]string{
		"dp":   "apps/v1/deployments",
		"pp":   "v1/services",
		"x":    "v1/configmaps",
		"cc":   "v1/configmaps",
		"ctx":  client.CtGVR.String(),
		"nope": "",
	}
	for alias, want := range uu {
		gvr, ok := a.Get(alias)
		if want == "" {
			assert.False(t, ok, alias)
			continue
		}
		require.True(t, ok, alias)
		assert.Equal(t, want, gvr.String(), alias)
	}
	_, ok := a.Get("gone")
	assert.False(t, ok)
}

func TestAliasesLoadGeneratesGlobal(t *testing.T) {
	dir := t.TempDir()
	setVar(t, &AppConfigDir, dir)

	a := NewAliases()
	require.NoError(t, a.Load(filepath.Join(dir, "missing.yaml")))

	bb, err := os.ReadFile(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, err)
	assert.Equal(t, aliasesTpl, bb)
	gvr, ok := a.Get("q")
	require.True(t, ok)
	assert.Equal(t, client.QGVR, gvr)
}

func TestAliasesContextBroken(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"aliases.yaml":     "aliases:\n  dp: apps/v1/deployments\n",
		"ctx/aliases.yaml": "aliases: [\n",
	})
	setVar(t, &AppConfigDir, dir)

	a := NewAliases()
	require.Error(t, a.Load(filepath.Join(dir, "ctx", "aliases.yaml")))
	gvr, ok := a.Get("dp")
	require.True(t, ok)
	assert.Equal(t, "apps/v1/deployments", gvr.String())
}

func TestCustomViewReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "views.yaml")
	writeFiles(t, dir, map[string]string{
		"views.yaml": "views:\n  v1/pods:\n    columns: [NAME, AGE]\n  v1/services:\n    sortColumn: AGE:asc\n",
	})

	v := NewCustomView()
	require.NoError(t, v.Load(path))
	assert.Len(t, v.Views, 2)

	writeFiles(t, dir, map[string]string{
		"views.yaml": "views:\n  v1/pods:\n    columns: [NAME]\n",
	})
	require.NoError(t, v.Load(path))
	assert.Equal(t, map[string]ViewSetting{"v1/pods": {Columns: []string{"NAME"}}}, v.Views)

	require.NoError(t, v.Load(filepath.Join(dir, "missing.yaml")))
	assert.Len(t, v.Views, 1)

	writeFiles(t, dir, map[string]string{"views.yaml": "# none\n"})
	require.NoError(t, v.Load(path))
	assert.Nil(t, v.Views)

	writeFiles(t, dir, map[string]string{"views.yaml": "views: [\n"})
	require.Error(t, v.Load(path))
}

func TestCustomJumpsReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jumps.yaml")
	writeFiles(t, dir, map[string]string{
		"jumps.yaml": "jumps:\n  apps/v1/deployments:\n    targetGVR: v1/pods\n    labelSelector: app={{.metadata.name}}\n",
	})

	j := NewCustomJumps()
	require.NoError(t, j.Load(path))
	r, ok := j.GetRule(client.NewGVR("apps/v1/deployments"))
	require.True(t, ok)
	assert.Equal(t, "v1/pods", r.TargetGVR)
	require.NoError(t, r.ValidateRule())

	writeFiles(t, dir, map[string]string{"jumps.yaml": "# none\n"})
	require.NoError(t, j.Load(path))
	assert.Len(t, j.Jumps, 1)

	writeFiles(t, dir, map[string]string{"jumps.yaml": "jumps:\n  v1/pods:\n    targetGVR: v1/nodes\n"})
	require.NoError(t, j.Load(path))
	_, ok = j.GetRule(client.NewGVR("apps/v1/deployments"))
	assert.False(t, ok)
	r, ok = j.GetRule(client.NewGVR("v1/pods"))
	require.True(t, ok)
	require.Error(t, r.ValidateRule())

	j.Reset()
	assert.Empty(t, j.Jumps)
	require.Error(t, (&JumpRule{}).ValidateRule())
}
