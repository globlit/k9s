// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build linux

package procstats

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProcessCounters(t *testing.T) {
	// Include spaces and parentheses in the process name.
	raw := "123 (worker (log) name) S 1 2 3 4 5 6 7 8 9 10 125 75 0 0 0 0 4 0 99 1000 3"
	cpu, rss, err := parse(raw, 100, 4096)
	require.NoError(t, err)
	require.Equal(t, 2.0, cpu)
	require.Equal(t, uint64(12288), rss)
	for _, bad := range []string{"", "1 (name) S 1", strings.Replace(raw, "125", "invalid", 1), strings.TrimSuffix(raw, "3") + "-1"} {
		_, _, err := parse(bad, 100, 4096)
		require.Error(t, err)
	}
}

func TestReadOwnProcessAndMissingProcess(t *testing.T) {
	s, err := Read(os.Getpid(), 100)
	require.NoError(t, err)
	require.Positive(t, s.RSSBytes)
	require.Positive(t, s.FDs)
	require.False(t, s.At.IsZero())
	_, err = readAt(t.TempDir(), 123, 100)
	require.Error(t, err)
	_, err = Read(0, 100)
	require.Error(t, err)
}
