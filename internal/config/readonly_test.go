// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roFlag int

const (
	roNone roFlag = iota
	roReadOnly
	roWrite
	roBoth
)

func (f roFlag) flags() *config.Flags {
	ff := config.NewFlags()
	*ff.ReadOnly = f == roReadOnly || f == roBoth
	*ff.Write = f == roWrite || f == roBoth

	return ff
}

func boolP(b bool) *bool { return &b }

func TestIsReadOnlyPrecedence(t *testing.T) {
	uu := []struct {
		cfg  bool
		ctx  *bool
		flag roFlag
		want bool
	}{
		{cfg: false, ctx: nil, flag: roNone, want: false},
		{cfg: false, ctx: nil, flag: roReadOnly, want: true},
		{cfg: false, ctx: nil, flag: roWrite, want: false},
		{cfg: false, ctx: nil, flag: roBoth, want: false},
		{cfg: false, ctx: boolP(true), flag: roNone, want: true},
		{cfg: false, ctx: boolP(true), flag: roReadOnly, want: true},
		{cfg: false, ctx: boolP(true), flag: roWrite, want: false},
		{cfg: false, ctx: boolP(true), flag: roBoth, want: false},
		{cfg: false, ctx: boolP(false), flag: roNone, want: false},
		{cfg: false, ctx: boolP(false), flag: roReadOnly, want: true},
		{cfg: false, ctx: boolP(false), flag: roWrite, want: false},
		{cfg: false, ctx: boolP(false), flag: roBoth, want: false},
		{cfg: true, ctx: nil, flag: roNone, want: true},
		{cfg: true, ctx: nil, flag: roReadOnly, want: true},
		{cfg: true, ctx: nil, flag: roWrite, want: false},
		{cfg: true, ctx: nil, flag: roBoth, want: false},
		{cfg: true, ctx: boolP(true), flag: roNone, want: true},
		{cfg: true, ctx: boolP(true), flag: roReadOnly, want: true},
		{cfg: true, ctx: boolP(true), flag: roWrite, want: false},
		{cfg: true, ctx: boolP(true), flag: roBoth, want: false},
		{cfg: true, ctx: boolP(false), flag: roNone, want: false},
		{cfg: true, ctx: boolP(false), flag: roReadOnly, want: true},
		{cfg: true, ctx: boolP(false), flag: roWrite, want: false},
		{cfg: true, ctx: boolP(false), flag: roBoth, want: false},
	}

	for _, u := range uu {
		ctx := "nil"
		if u.ctx != nil {
			ctx = fmt.Sprint(*u.ctx)
		}
		name := fmt.Sprintf("cfg=%t/ctx=%s/flag=%d", u.cfg, ctx, u.flag)
		t.Run(name, func(t *testing.T) {
			cfg := mock.NewMockConfig(t)
			_, err := cfg.K9s.ActivateContext("ct-1-1")
			require.NoError(t, err)
			ct, err := cfg.K9s.ActiveContext()
			require.NoError(t, err)

			cfg.K9s.ReadOnly = u.cfg
			ct.ReadOnly = u.ctx
			cfg.K9s.Override(u.flag.flags())

			assert.Equal(t, u.want, cfg.K9s.IsReadOnly())
			assert.Equal(t, u.want, cfg.IsReadOnly())
		})
	}
}

func TestIsReadOnlyNoActiveContext(t *testing.T) {
	uu := map[string]struct {
		cfg  bool
		flag roFlag
		want bool
	}{
		"cfg-off":      {},
		"cfg-on":       {cfg: true, want: true},
		"flag-ro":      {flag: roReadOnly, want: true},
		"flag-write":   {cfg: true, flag: roWrite},
		"flag-both":    {cfg: true, flag: roBoth},
		"nil-pointers": {cfg: true, flag: -1, want: true},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			cfg := mock.NewMockConfig(t)
			cfg.K9s.ReadOnly = u.cfg
			if u.flag < 0 {
				cfg.K9s.Override(&config.Flags{})
			} else {
				cfg.K9s.Override(u.flag.flags())
			}
			assert.Equal(t, u.want, cfg.K9s.IsReadOnly())
		})
	}
}

func writeContextConfig(t *testing.T, cl, ct, body string) {
	t.Helper()
	path := config.AppContextConfig(cl, ct)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func TestIsReadOnlyContextSwitch(t *testing.T) {
	uu := map[string]struct {
		cfg   bool
		flag  roFlag
		wants []bool
	}{
		"no-flag":     {wants: []bool{true, false, true, false}},
		"cfg-ro":      {cfg: true, wants: []bool{true, false, true, true}},
		"flag-ro":     {flag: roReadOnly, wants: []bool{true, true, true, true}},
		"flag-write":  {flag: roWrite, wants: []bool{false, false, false, false}},
		"cfg-ro-both": {cfg: true, flag: roBoth, wants: []bool{false, false, false, false}},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			cfg := mock.NewMockConfig(t)
			writeContextConfig(t, "cl-1", "ct-1-1", "k9s:\n  readOnly: true\n")
			writeContextConfig(t, "cl-1", "ct-1-2", "k9s:\n  readOnly: false\n")
			writeContextConfig(t, "cl-1", "ct-1-3", "k9s:\n  skin: fred\n")
			cfg.K9s.ReadOnly = u.cfg
			cfg.K9s.Override(u.flag.flags())

			for i, ct := range []string{"ct-1-1", "ct-1-2", "ct-1-1", "ct-1-3"} {
				_, err := cfg.ActivateContext(ct)
				require.NoError(t, err)
				assert.Equal(t, u.wants[i], cfg.IsReadOnly(), "step %d: %s", i, ct)
			}
		})
	}
}

func TestIsReadOnlyReload(t *testing.T) {
	cfg := mock.NewMockConfig(t)
	writeContextConfig(t, "cl-1", "ct-1-1", "k9s:\n  readOnly: false\n")
	_, err := cfg.ActivateContext("ct-1-1")
	require.NoError(t, err)
	assert.False(t, cfg.IsReadOnly())

	writeContextConfig(t, "cl-1", "ct-1-1", "k9s:\n  readOnly: true\n")
	require.NoError(t, cfg.K9s.Reload())
	assert.True(t, cfg.IsReadOnly())
}

func TestActivateContextNullContext(t *testing.T) {
	t.Skip("bug: a context config with a null k9s key panics in ActivateContext")

	cfg := mock.NewMockConfig(t)
	writeContextConfig(t, "cl-1", "ct-1-1", "k9s: null\n")
	assert.NotPanics(t, func() {
		_, _ = cfg.ActivateContext("ct-1-1")
	})
	assert.False(t, cfg.IsReadOnly())
}
