// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build unix

package data_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/globlit/k9s/internal/config/data"
	"github.com/globlit/k9s/internal/config/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd/api"
)

type atomicDoc struct {
	Writer string   `yaml:"writer"`
	Seq    int      `yaml:"seq"`
	Pad    []string `yaml:"pad"`
}

func newAtomicDoc(w string, seq int) atomicDoc {
	pad := make([]string, 64)
	for i := range pad {
		pad[i] = w
	}

	return atomicDoc{Writer: w, Seq: seq, Pad: pad}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ee, err := os.ReadDir(dir)
	require.NoError(t, err)
	nn := make([]string, 0, len(ee))
	for _, e := range ee {
		nn = append(nn, e.Name())
	}

	return nn
}

func readDoc(path string) (atomicDoc, error) {
	var d atomicDoc
	bb, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	err = yaml.Unmarshal(bb, &d)

	return d, err
}

func TestSaveYAMLConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, data.SaveYAML(path, newAtomicDoc("init", 0)))

	const runs = 200
	var (
		wg   sync.WaitGroup
		done = make(chan struct{})
	)
	for _, w := range []string{"a", "b"} {
		wg.Go(func() {
			for i := 1; i <= runs; i++ {
				assert.NoError(t, data.SaveYAML(path, newAtomicDoc(w, i)))
			}
		})
	}
	var reads int
	rg := sync.WaitGroup{}
	rg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			d, err := readDoc(path)
			if !assert.NoError(t, err) {
				return
			}
			assert.Len(t, d.Pad, 64)
			for _, p := range d.Pad {
				if !assert.Equal(t, d.Writer, p) {
					return
				}
			}
			reads++
		}
	})
	wg.Wait()
	close(done)
	rg.Wait()

	d, err := readDoc(path)
	require.NoError(t, err)
	assert.Contains(t, []string{"a", "b"}, d.Writer)
	assert.Equal(t, runs, d.Seq)
	assert.Positive(t, reads)
	assert.Equal(t, []string{"config.yaml"}, dirEntries(t, dir))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, data.DefaultFileMod, fi.Mode().Perm())
}

func TestWriteFileAtomicMode(t *testing.T) {
	uu := map[string]struct {
		pre  os.FileMode
		mod  os.FileMode
		want os.FileMode
	}{
		"new-0600":      {mod: 0o600, want: 0o600},
		"new-0640":      {mod: 0o640, want: 0o640},
		"new-0755":      {mod: 0o755, want: 0o755},
		"existing-0644": {pre: 0o644, mod: 0o600, want: 0o600},
		"existing-0400": {pre: 0o400, mod: 0o600, want: 0o600},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.yaml")
			if u.pre != 0 {
				require.NoError(t, os.WriteFile(path, []byte("old"), u.pre))
				require.NoError(t, os.Chmod(path, u.pre))
			}
			require.NoError(t, data.WriteFileAtomic(path, []byte("new"), u.mod))

			fi, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, u.want, fi.Mode().Perm())
			bb, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "new", string(bb))
			assert.Equal(t, []string{"f.yaml"}, dirEntries(t, dir))
		})
	}
}

func TestWriteFileAtomicSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))
	link := filepath.Join(dir, "link.yaml")
	require.NoError(t, os.Symlink(target, link))

	require.NoError(t, data.WriteFileAtomic(link, []byte("new"), 0o600))

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink)
	bb, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new", string(bb))
	assert.Equal(t, []string{"config.yaml"}, dirEntries(t, filepath.Dir(target)))
}

func TestWriteFileAtomicDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link.yaml")
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.yaml"), link))

	require.NoError(t, data.WriteFileAtomic(link, []byte("new"), 0o600))

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.Zero(t, fi.Mode()&os.ModeSymlink)
	_, err = os.Stat(filepath.Join(dir, "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWriteFileAtomicReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores dir permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, data.SaveYAML(path, newAtomicDoc("orig", 1)))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.Error(t, data.SaveYAML(path, newAtomicDoc("new", 2)))
	require.Error(t, data.WriteFileAtomic(path, []byte("x"), 0o600))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, []string{"config.yaml"}, dirEntries(t, dir))
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "config.yaml")

	require.Error(t, data.WriteFileAtomic(path, []byte("x"), 0o600))
	_, err := os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDirSaveConcurrent(t *testing.T) {
	dir := t.TempDir()
	d := data.NewDir(dir)
	path := filepath.Join(dir, "cl", "ct", data.MainConfigFile)

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 100 {
				cfg := data.NewConfig(&api.Context{Cluster: "cl", Namespace: "ns"})
				assert.NoError(t, d.Save(path, cfg))
			}
		})
	}
	wg.Wait()

	bb, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, data.JSONValidator.Validate(json.ContextSchema, bb))
	assert.Equal(t, []string{data.MainConfigFile}, dirEntries(t, filepath.Dir(path)))
}
