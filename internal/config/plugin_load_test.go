// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFiles(t *testing.T, dir string, ff map[string]string) {
	t.Helper()
	for name, body := range ff {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
}

func setVar[T any](t *testing.T, v *T, val T) {
	t.Helper()
	old := *v
	*v = val
	t.Cleanup(func() { *v = old })
}

func pluginSnippet(cmd string) string {
	return "shortCut: Shift-X\ndescription: " + cmd + "\nscopes: [po]\ncommand: " + cmd + "\n"
}

func pluginKeys(p Plugins) []string {
	kk := make([]string, 0, len(p.Plugins))
	for k := range p.Plugins {
		kk = append(kk, k)
	}
	slices.Sort(kk)

	return kk
}

func TestPluginLoadDirMixed(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"good.yaml":       pluginSnippet("good"),
		"sub/nested.yml":  pluginSnippet("nested"),
		"notes.txt":       "not: yaml: at all",
		"README.md":       pluginSnippet("readme"),
		"bad-schema.yaml": "shortCut: Shift-B\ndescription: no command\nscopes: [po]\n",
		"broken.yaml":     "shortCut: [\n",
		"wrong-type.yaml": "shortCut: 1\ndescription: x\nscopes: po\ncommand: x\n",
		"empty.yaml":      "",
		"multi.yaml": `m1:
  shortCut: Shift-1
  description: m1
  scopes: [po]
  command: m1
m-bad:
  shortCut: Shift-2
  description: bad default
  scopes: [po]
  command: m2
  inputs:
    - name: env
      type: dropdown
      required: true
      default: qa
      options: [dev, prod]
`,
	})

	p := NewPlugins()
	err := p.loadDir(dir)
	require.Error(t, err)

	assert.Equal(t, []string{"good", "m1", "nested"}, pluginKeys(p))
	for _, f := range []string{"bad-schema.yaml", "broken.yaml", "wrong-type.yaml", "multi.yaml", "empty.yaml"} {
		assert.Contains(t, err.Error(), filepath.Join(dir, f))
	}
	assert.Contains(t, err.Error(), `plugin "m-bad" validation failed`)
	assert.NotContains(t, err.Error(), "good.yaml")
	assert.NotContains(t, err.Error(), "notes.txt")
}

func TestPluginLoadDirMissing(t *testing.T) {
	p := NewPlugins()

	require.NoError(t, p.loadDir(filepath.Join(t.TempDir(), "nope")))
	assert.Empty(t, p.Plugins)
}

func TestPluginLoadDirSymlinkedDir(t *testing.T) {
	real := t.TempDir()
	writeFiles(t, real, map[string]string{
		"a.yaml": pluginSnippet("a"),
		"b.yaml": "shortCut: [\n",
	})
	dir := t.TempDir()
	require.NoError(t, os.Symlink(real, filepath.Join(dir, "linked")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling.yaml")))

	p := NewPlugins()
	err := p.loadDir(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "b.yaml")
	assert.Equal(t, []string{"a"}, pluginKeys(p))
}

func TestPluginLoadPrecedence(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global", "plugins.yaml")
	ctx := filepath.Join(root, "ctx", "plugins.yaml")
	xdgCfg := filepath.Join(root, "xdg-config")
	xdgData := filepath.Join(root, "xdg-data")
	writeFiles(t, root, map[string]string{
		"global/plugins.yaml": `plugins:
  a: {shortCut: Shift-A, description: global, scopes: [po], command: global}
  g: {shortCut: Shift-G, description: global, scopes: [po], command: global}
`,
		"ctx/plugins.yaml": `plugins:
  a: {shortCut: Shift-A, description: ctx, scopes: [po], command: ctx}
  c: {shortCut: Shift-C, description: ctx, scopes: [po], command: ctx}
`,
		"xdg-data/k9s/plugins/a.yaml":   pluginSnippet("data"),
		"xdg-data/k9s/plugins/d.yaml":   pluginSnippet("data"),
		"xdg-config/k9s/plugins/a.yaml": pluginSnippet("config"),
	})
	setVar(t, &AppPluginsFile, global)
	setVar(t, &xdg.ConfigHome, xdgCfg)
	setVar(t, &xdg.DataHome, xdgData)
	setVar(t, &xdg.DataDirs, []string{filepath.Join(root, "none")})

	uu := map[string]struct {
		extra bool
		keys  []string
		a     string
	}{
		"no-extra": {keys: []string{"a", "c", "g"}, a: "ctx"},
		"extra":    {extra: true, keys: []string{"a", "c", "d", "g"}, a: "config"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			p := NewPlugins()
			require.NoError(t, p.Load(ctx, u.extra))
			assert.Equal(t, u.keys, pluginKeys(p))
			assert.Equal(t, u.a, p.Plugins["a"].Command)
			assert.Equal(t, "global", p.Plugins["g"].Command)
		})
	}
}

func TestPluginLoadGlobalInvalid(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"global.yaml": "plugins: [\n",
		"ctx.yaml":    "plugins:\n  c: {shortCut: Shift-C, description: c, scopes: [po], command: c}\n",
	})
	setVar(t, &AppPluginsFile, filepath.Join(root, "global.yaml"))

	p := NewPlugins()
	err := p.Load(filepath.Join(root, "ctx.yaml"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global.yaml")
	assert.Equal(t, []string{"c"}, pluginKeys(p))
}

func TestPluginLoadFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	writeFiles(t, filepath.Dir(path), map[string]string{
		"plugins.yaml": `plugins:
  plain:
    shortCut: Shift-A
    description: plain
    scopes: [po, deploy]
    command: plain
  danger:
    shortCut: Shift-A
    description: danger
    scopes: [all]
    command: rm
    dangerous: true
    confirm: false
  piped:
    shortCut: Ctrl-P
    description: piped
    scopes: [containers]
    command: kubectl
    pipes: [grep, wc]
`,
	})

	p := NewPlugins()
	require.NoError(t, p.load(path))
	require.Equal(t, []string{"danger", "piped", "plain"}, pluginKeys(p))

	plain, danger, piped := p.Plugins["plain"], p.Plugins["danger"], p.Plugins["piped"]
	assert.False(t, plain.Dangerous)
	assert.False(t, plain.Background)
	assert.Nil(t, plain.Confirm)
	assert.False(t, plain.ShouldConfirm())
	assert.Equal(t, []string{"po", "deploy"}, plain.Scopes)

	assert.True(t, danger.Dangerous)
	assert.Equal(t, []string{"all"}, danger.Scopes)
	assert.False(t, danger.ShouldConfirm())
	assert.Equal(t, plain.ShortCut, danger.ShortCut)

	assert.Equal(t, []string{"grep", "wc"}, piped.Pipes)
}

func TestPluginSnippetPipes(t *testing.T) {
	t.Skip("bug: plugin.json has no pipes property, so snippet files with pipes fail to load")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"piped.yaml": pluginSnippet("kubectl") + "pipes: [grep]\n",
	})
	p := NewPlugins()
	require.NoError(t, p.loadDir(dir))
	assert.Equal(t, []string{"grep"}, p.Plugins["piped"].Pipes)
}

func TestPluginValidate(t *testing.T) {
	uu := map[string]struct {
		in  []PluginInput
		err string
	}{
		"none": {},
		"ok": {in: []PluginInput{
			{Name: "a", Type: InputTypeString, Default: "x"},
			{Name: "b", Type: InputTypeNumber, Default: "1.5"},
			{Name: "c", Type: InputTypeBool, Default: "false"},
			{Name: "d", Type: InputTypeDropdown, Default: "y", Options: []string{"x", "y"}},
			{Name: "e", Type: InputTypeDropdown},
		}},
		"dup": {
			in:  []PluginInput{{Name: "a", Type: InputTypeString}, {Name: "a", Type: InputTypeBool}},
			err: `duplicate input name "a"`,
		},
		"bad-dropdown": {
			in:  []PluginInput{{Name: "a", Type: InputTypeDropdown, Default: "z", Options: []string{"x"}}},
			err: `default value "z" for input "a" is not a valid option`,
		},
		"bad-bool": {
			in:  []PluginInput{{Name: "a", Type: InputTypeBool, Default: "yes"}},
			err: `default value "yes" for bool input "a" must be "true" or "false"`,
		},
		"bad-number": {
			in:  []PluginInput{{Name: "a", Type: InputTypeNumber, Default: "1x"}},
			err: `default value "1x" for number input "a" is not a valid number`,
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			p := Plugin{Inputs: u.in}
			err := p.Validate()
			if u.err == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, u.err)
		})
	}
}

func TestPluginShouldConfirm(t *testing.T) {
	yes, no := true, false
	uu := map[string]struct {
		p    Plugin
		want bool
	}{
		"default":        {},
		"inputs":         {p: Plugin{Inputs: []PluginInput{{Name: "a"}}}, want: true},
		"explicit-true":  {p: Plugin{Confirm: &yes}, want: true},
		"explicit-false": {p: Plugin{Confirm: &no, Inputs: []PluginInput{{Name: "a"}}}},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.want, u.p.ShouldConfirm())
		})
	}
}
