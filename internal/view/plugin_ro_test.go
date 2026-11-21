// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const roPlugins = `plugins:
  safe:
    shortCut: Ctrl-G
    description: safe
    scopes: [pods]
    command: "true"
  risky:
    shortCut: Ctrl-Y
    dangerous: true
    description: risky
    scopes: [pods]
    command: "true"
`

func pluginKeys(t *testing.T, opts ...SimOption) (safe, risky bool) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plugins.yaml")
	require.NoError(t, os.WriteFile(p, []byte(roPlugins), 0o600))
	old := config.AppPluginsFile
	config.AppPluginsFile = p
	t.Cleanup(func() { config.AppPluginsFile = old })

	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	s := NewSimApp(t, fc, append(opts, SimNamespace("ns1"), SimView("pods"))...)
	s.Eventually(func() bool { return s.TopView() == "Pod" }, "no pod view")
	s.Eventually(func() bool {
		s.onLoop(func() {
			aa := s.app.Content.Top().(ResourceViewer).Actions()
			_, safe = aa.Get(tcell.KeyCtrlG)
			_, risky = aa.Get(tcell.KeyCtrlY)
		})
		s.Sync()
		return safe
	}, "plugins not loaded")

	return safe, risky
}

func TestPluginsReadWrite(t *testing.T) {
	safe, risky := pluginKeys(t)
	assert.True(t, safe)
	assert.True(t, risky)
}

func TestPluginsReadOnlyKeepNonDangerous(t *testing.T) {
	safe, risky := pluginKeys(t, SimReadOnly())
	assert.True(t, safe)
	assert.False(t, risky)
}
