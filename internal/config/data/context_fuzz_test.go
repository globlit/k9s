// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package data_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/config/data"
	"github.com/globlit/k9s/internal/config/json"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd/api"
)

func addSeedFiles(f *testing.F, globs ...string) {
	f.Helper()
	for _, g := range globs {
		mm, err := filepath.Glob(g)
		require.NoError(f, err)
		for _, m := range mm {
			bb, err := os.ReadFile(m)
			require.NoError(f, err)
			f.Add(bb)
		}
	}
}

func loadContextBytes(t *testing.T, root, ct string, bb []byte) (*data.Config, error) {
	t.Helper()
	path := filepath.Join(root, "cl", ct, data.MainConfigFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, bb, 0o600))

	return data.NewDir(root).Load(ct, &api.Context{Cluster: "cl", Namespace: "ns"})
}

func FuzzContextConfig(f *testing.F) {
	addSeedFiles(f,
		"testdata/configs/*.yaml",
		"testdata/data/k9s/*/*/config.yaml",
		"../json/testdata/context/*.yaml",
	)
	f.Add([]byte("k9s: null\n"))
	f.Add([]byte("k9s:\n  namespace: null\n  view: null\n  proxy: null\n"))
	f.Add([]byte("k9s:\n  readOnly: true\n  namespace:\n    favorites: [a, a, b, c, d, e, f, g, h, i, j, k]\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		root := t.TempDir()
		cfg, err := loadContextBytes(t, root, "ct", bb)
		if err != nil {
			return
		}
		valid := data.JSONValidator.Validate(json.ContextSchema, bb) == nil

		out1, err := data.WriteYAML(cfg)
		require.NoError(t, err)

		d := data.NewDir(root)
		p2 := filepath.Join(root, "cl", "ct2", data.MainConfigFile)
		require.NoError(t, d.Save(p2, cfg))
		saved, err := os.ReadFile(p2)
		require.NoError(t, err)
		require.Equal(t, string(out1), string(saved))

		cfg2, err := loadContextBytes(t, root, "ct3", saved)
		require.NoError(t, err)
		out2, err := data.WriteYAML(cfg2)
		require.NoError(t, err)
		require.Equal(t, string(out1), string(out2))

		cfg.Validate(nil, "ct", "cl")
		require.NotNil(t, cfg.Context)
		require.NotNil(t, cfg.Context.Namespace)
		require.NotNil(t, cfg.Context.View)
		require.LessOrEqual(t, len(cfg.Context.Namespace.Favorites), data.MaxFavoritesNS)
		out3, err := data.WriteYAML(cfg)
		require.NoError(t, err)
		if valid {
			require.NoError(t, data.JSONValidator.Validate(json.ContextSchema, out3), "validated:\n%s", out3)
		}
		cfg3, err := loadContextBytes(t, root, "ct4", out3)
		require.NoError(t, err)
		cfg3.Validate(nil, "ct", "cl")
		out4, err := data.WriteYAML(cfg3)
		require.NoError(t, err)
		require.Equal(t, string(out3), string(out4))
	})
}
