// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/data"
	"github.com/globlit/k9s/internal/config/json"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type loadFn func(path string) (any, error)

func seedFiles(f *testing.F, globs ...string) {
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

func schemaCheck(k string) func([]byte) error {
	return func(bb []byte) error { return data.JSONValidator.Validate(k, bb) }
}

// Load bb, save it without null keys, load it again and compare the values.
// Set strict when the loader rejects a document that fails the schema.
func checkLoader(t *testing.T, bb []byte, load loadFn, validate func([]byte) error, strict bool) any {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.yaml")
	require.NoError(t, os.WriteFile(in, bb, 0o600))
	v1, err := load(in)
	if err != nil {
		return nil
	}
	valid := validate(bb) == nil
	if strict {
		require.True(t, valid, "loaded a document that fails the schema:\n%s", bb)
	}
	saved := filepath.Join(dir, "saved.yaml")
	require.NoError(t, data.SaveYAML(saved, v1))
	out1, err := os.ReadFile(saved)
	require.NoError(t, err)
	out1 = dropNulls(t, out1)
	if valid {
		require.NoError(t, validate(out1), "saved:\n%s", out1)
	}

	require.NoError(t, os.WriteFile(saved, out1, 0o600))
	v2, err := load(saved)
	require.NoError(t, err, "reload failed:\n%s", out1)
	out2, err := data.WriteYAML(v2)
	require.NoError(t, err)
	require.Equal(t, string(out1), string(dropNulls(t, out2)))

	return v1
}

// Remove null keys. The loaders read a null key and a missing key the same way.
func dropNulls(t *testing.T, bb []byte) []byte {
	t.Helper()
	var doc any
	require.NoError(t, yaml.Unmarshal(bb, &doc))
	out, err := yaml.Marshal(prune(doc))
	require.NoError(t, err)

	return out
}

func prune(v any) any {
	switch o := v.(type) {
	case map[string]any:
		for k, e := range o {
			if e == nil {
				delete(o, k)
				continue
			}
			o[k] = prune(e)
		}
	case []any:
		for i, e := range o {
			o[i] = prune(e)
		}
	}

	return v
}

func loadK9s(path string) (any, error) {
	cfg := config.NewConfig(nil)
	return cfg, cfg.Load(path, true)
}

func FuzzK9sConfig(f *testing.F) {
	seedFiles(f,
		"testdata/configs/*.yaml",
		"json/testdata/k9s/*.yaml",
		"../../cmd/testdata/*.yaml",
	)
	f.Add([]byte("k9s: null\n"))
	f.Add([]byte("k9s:\n  shellPod: null\n  thresholds: null\n  ui: null\n"))
	f.Add([]byte("k9s:\n  refreshRate: -1\n  maxConnRetry: -5\n  apiServerTimeout: bad\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		checkLoader(t, bb, loadK9s, schemaCheck(json.K9sSchema), true)
	})
}

func FuzzAliases(f *testing.F) {
	seedFiles(f, "testdata/aliases/*.yaml", "json/testdata/aliases/*.yaml")
	f.Add([]byte("aliases:\n  pp: null\n  dp: apps/v1/deployments\n"))
	f.Add([]byte("aliases: []\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		checkLoader(t, bb, func(p string) (any, error) {
			a := config.NewAliases()
			return a, a.LoadFile(p)
		}, schemaCheck(json.AliasesSchema), false)
	})
}

func FuzzHotKeys(f *testing.F) {
	seedFiles(f, "testdata/hotkeys/*.yaml", "json/testdata/hotkeys/*.yaml")
	f.Add([]byte("hotKeys:\n  a: null\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		checkLoader(t, bb, func(p string) (any, error) {
			h := config.NewHotKeys()
			return h, h.LoadHotKeys(p)
		}, schemaCheck(json.HotkeysSchema), false)
	})
}

func FuzzPlugins(f *testing.F) {
	seedFiles(f,
		"testdata/plugins/*.yaml",
		"testdata/plugins/dir/*.yaml",
		"json/testdata/plugins/*.yaml",
	)
	f.Add([]byte("plugins:\n  a:\n    shortCut: x\n    command: ls\n    scopes: [all]\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		checkLoader(t, bb, func(p string) (any, error) {
			pp := config.NewPlugins()
			return pp, pp.Load(p, false)
		}, func(bb []byte) error {
			_, err := data.JSONValidator.ValidatePlugins(bb)
			return err
		}, true)
	})
}

func FuzzViews(f *testing.F) {
	seedFiles(f, "testdata/views/*.yaml", "json/testdata/views/*.yaml")
	f.Add([]byte("views:\n  v1/pods:\n    sortColumn: AGE:asc\n    columns: [NAME, 'AGE|R']\n"))
	f.Add([]byte("views:\n  v1/pods: null\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		v := checkLoader(t, bb, func(p string) (any, error) {
			cv := config.NewCustomView()
			return cv, cv.Load(p)
		}, schemaCheck(json.ViewsSchema), false)
		if cv, ok := v.(*config.CustomView); ok {
			for _, vs := range cv.Views {
				_, _, _ = vs.SortCol()
			}
		}
	})
}

func FuzzSkins(f *testing.F) {
	seedFiles(f,
		"testdata/skins/*.yaml",
		"json/testdata/skins/*.yaml",
		"../../skins/*.yaml",
	)

	f.Fuzz(func(t *testing.T, bb []byte) {
		v := checkLoader(t, bb, func(p string) (any, error) {
			s := config.NewStyles()
			return s, s.Load(p, false)
		}, schemaCheck(json.SkinSchema), true)
		if s, ok := v.(*config.Styles); ok {
			_, _ = s.FgColor(), s.BgColor()
		}
	})
}

func FuzzJumps(f *testing.F) {
	f.Add([]byte("jumps:\n  v1/pods:\n    targetGVR: v1/services\n    labelSelector: app={{.metadata.name}}\n"))
	f.Add([]byte("jumps:\n  apps/v1/deployments:\n    targetGVR: v1/pods\n    fieldSelector: a=b\n    targetNamespace: all\n"))
	f.Add([]byte("jumps: null\n"))
	f.Add([]byte("jumps:\n  v1/pods: null\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		v := checkLoader(t, bb, func(p string) (any, error) {
			j := config.NewCustomJumps()
			return j, j.Load(p)
		}, schemaCheck(json.JumpsSchema), false)
		if j, ok := v.(*config.CustomJumps); ok {
			for k := range j.Jumps {
				if r, ok := j.GetRule(client.NewGVR(k)); ok {
					_ = r.ValidateRule()
				}
			}
		}
	})
}

// Put the fuzz document under k9s.<key> and load it as a k9s.yaml file.
func checkK9sSection(t *testing.T, key string, bb []byte) *config.Config {
	t.Helper()
	var section any
	if err := yaml.Unmarshal(bb, &section); err != nil {
		return nil
	}
	doc, err := yaml.Marshal(map[string]any{"k9s": map[string]any{key: section}})
	require.NoError(t, err)
	v := checkLoader(t, doc, loadK9s, schemaCheck(json.K9sSchema), true)
	if v == nil {
		return nil
	}

	return v.(*config.Config)
}

func FuzzScans(f *testing.F) {
	f.Add([]byte("enable: true\nexclusions:\n  namespaces: [kube-system]\n  labels:\n    app: [a, b]\n"))
	f.Add([]byte("enable: false\n"))
	f.Add([]byte("enable: true\nexclusions:\n  labels: null\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		cfg := checkK9sSection(t, "imageScans", bb)
		if cfg == nil {
			return
		}
		_ = cfg.K9s.ImageScans.ShouldExclude("kube-system", map[string]string{"app": "a"})
	})
}

func FuzzThresholds(f *testing.F) {
	f.Add([]byte("cpu:\n  critical: 90\n  warn: 70\nmemory:\n  critical: 80\n  warn: 60\n"))
	f.Add([]byte("cpu:\n  critical: -1\n  warn: 500\n"))
	f.Add([]byte("memory: null\n"))

	f.Fuzz(func(t *testing.T, bb []byte) {
		cfg := checkK9sSection(t, "thresholds", bb)
		if cfg == nil {
			return
		}
		th := cfg.K9s.Thresholds.Validate()
		require.True(t, th.IsValid())
		for _, k := range []string{config.CPU, config.MEM} {
			_ = th.LevelFor(k, 75)
			_ = th.SeverityColor(k, 95)
		}
	})
}

func TestK9sConfigNoShellPod(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.yaml")
	require.NoError(t, os.WriteFile(in, []byte("k9s:\n  refreshRate: 2\n"), 0o600))
	cfg := config.NewConfig(nil)
	require.NoError(t, cfg.Load(in, true))
	out := filepath.Join(dir, "out.yaml")
	require.NoError(t, cfg.SaveFile(out))

	require.NotNil(t, cfg.K9s.ShellPod)
	require.NoError(t, config.NewConfig(nil).Load(out, true))
}
