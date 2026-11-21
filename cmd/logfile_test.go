// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRotatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k9s.log")
	r, err := openRotatingFile(path, 10)
	require.NoError(t, err)

	_, err = r.Write([]byte("12345678\n"))
	require.NoError(t, err)
	_, err = r.Write([]byte("abc\n"))
	require.NoError(t, err)
	require.NoError(t, r.Close())

	cur, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "abc\n", string(cur))
	prev, err := os.ReadFile(path + ".1")
	require.NoError(t, err)
	assert.Equal(t, "12345678\n", string(prev))

	// Rotate oversized logs from previous runs at startup.
	require.NoError(t, os.WriteFile(path, []byte("0123456789ABC"), 0o600))
	r, err = openRotatingFile(path, 10)
	require.NoError(t, err)
	assert.Zero(t, r.size)
	require.NoError(t, r.Close())
}

func TestRotatingFileEdges(t *testing.T) {
	t.Run("missing-dir", func(t *testing.T) {
		_, err := openRotatingFile(filepath.Join(t.TempDir(), "nope", "k9s.log"), 10)
		require.Error(t, err)
	})

	t.Run("exact-max", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "k9s.log")
		r, err := openRotatingFile(path, 10)
		require.NoError(t, err)
		_, err = r.Write([]byte("0123456789"))
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.NoFileExists(t, path+".1")
	})

	t.Run("startup-at-max", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "k9s.log")
		require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
		r, err := openRotatingFile(path, 10)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		prev, err := os.ReadFile(path + ".1")
		require.NoError(t, err)
		assert.Equal(t, "0123456789", string(prev))
	})

	t.Run("replace-backup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "k9s.log")
		require.NoError(t, os.WriteFile(path+".1", []byte("old"), 0o600))
		r, err := openRotatingFile(path, 4)
		require.NoError(t, err)
		for _, s := range []string{"aaa", "bbb", "ccc"} {
			_, err = r.Write([]byte(s))
			require.NoError(t, err)
		}
		require.NoError(t, r.Close())
		prev, err := os.ReadFile(path + ".1")
		require.NoError(t, err)
		assert.Equal(t, "bbb", string(prev))
		cur, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "ccc", string(cur))
	})

	t.Run("rename-fails", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "k9s.log")
		require.NoError(t, os.MkdirAll(filepath.Join(path+".1", "x"), 0o700))
		r, err := openRotatingFile(path, 4)
		require.NoError(t, err)
		for _, s := range []string{"aaa", "bbb"} {
			_, err = r.Write([]byte(s))
			require.NoError(t, err)
		}
		require.NoError(t, r.Close())
		cur, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "aaabbb", string(cur))
	})

	t.Run("write-after-close", func(t *testing.T) {
		r, err := openRotatingFile(filepath.Join(t.TempDir(), "k9s.log"), 10)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		_, err = r.Write([]byte("x"))
		require.Error(t, err)
	})
}

func TestRotatingFileConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k9s.log")
	r, err := openRotatingFile(path, 1<<20)
	require.NoError(t, err)

	const writers, lines = 4, 200
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range lines {
				_, _ = fmt.Fprintf(r, "w%d-%03d\n", w, i)
			}
		}()
	}
	wg.Wait()
	require.NoError(t, r.Close())

	bb, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, writers*lines, bytes.Count(bb, []byte("\n")))
	assert.Equal(t, int64(len(bb)), r.size)
}
