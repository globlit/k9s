// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

// Package expect compares test output with files in testdata/expected.
package expect

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "write expected files from test output")

// Dir holds the expected files, relative to the test package.
const Dir = "testdata/expected"

// Match compares got with testdata/expected/<name>.
// Run the tests with -update to write the file.
func Match(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join(Dir, name)
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing %s: run the test with -update", path)
	assert.Equal(t, string(want), got, "output differs from %s", path)
}
