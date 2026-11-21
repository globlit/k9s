// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"os"
	"path/filepath"
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

	// An oversized log left by a previous run is rotated at startup.
	require.NoError(t, os.WriteFile(path, []byte("0123456789ABC"), 0o600))
	r, err = openRotatingFile(path, 10)
	require.NoError(t, err)
	assert.Zero(t, r.size)
	require.NoError(t, r.Close())
}
