// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"fmt"
	"testing"

	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/require"
)

func TestActionRangeSnapshotDuringCallback(t *testing.T) {
	for _, size := range []int{2, 128} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := NewKeyActions()
			for i := range size {
				a.Add(tcell.Key(i), KeyAction{Description: "action"})
			}
			visited := 0
			a.Range(func(tcell.Key, KeyAction) { visited++; a.Clear() })
			require.Equal(t, size, visited, "callbacks must not invalidate the iteration snapshot")
			require.Zero(t, a.Len())
		})
	}
}

func BenchmarkKeyActionsRange(b *testing.B) {
	for _, size := range []int{16, 64, 256} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			actions := NewKeyActions()
			for i := range size {
				actions.Add(tcell.Key(i), NewKeyAction("action", nil, false))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				actions.Range(func(tcell.Key, KeyAction) {})
			}
		})
	}
}
