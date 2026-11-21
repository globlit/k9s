// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { Main(m) }

func TestIsolateSetsEnv(t *testing.T) {
	dir := filepath.Dir(os.Getenv("KUBECONFIG"))
	require.True(t, strings.HasPrefix(filepath.Base(dir), "k9s-test-"))
	for _, k := range []string{
		config.K9sEnvConfigDir, "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"XDG_STATE_HOME", "XDG_CACHE_HOME", "KUBECACHEDIR",
	} {
		assert.True(t, strings.HasPrefix(os.Getenv(k), dir), k)
	}
	assert.True(t, strings.HasPrefix(config.AppConfigDir, dir))
	assert.True(t, strings.HasPrefix(config.AppContextsDir, dir))

	b, err := os.ReadFile(os.Getenv("KUBECONFIG"))
	require.NoError(t, err)
	assert.Empty(t, b)
}

func TestRefuseReal(t *testing.T) {
	known := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(known, nil, 0o600))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(known, link))

	t.Setenv("KUBECONFIG", "/tmp/other"+string(filepath.ListSeparator)+link)
	require.Error(t, refuseReal([]string{known}))

	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "scratch"))
	require.NoError(t, refuseReal([]string{known}))
}
