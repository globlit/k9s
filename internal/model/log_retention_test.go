// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/stretchr/testify/require"
)

func TestLogSustainedStreamRetainsNewestBoundedWindow(t *testing.T) {
	const capacity = 128
	m := NewLog(client.NewGVR("pods"), makeLogOpts(capacity), time.Millisecond)
	for window := range 20 {
		var last *dao.LogItem
		for i := range 1000 {
			last = dao.NewLogItemFromString(fmt.Sprintf("window=%d line=%d", window, i))
			m.Append(last)
		}
		require.Equal(t, capacity, m.lines.Len())
		require.Same(t, last, m.lines.Items()[capacity-1], "rotation discarded newest line")
		require.LessOrEqual(t, cap(m.lines.Items()), 2*capacity, "backing storage grows with stream")
	}
	m.Clear()
	require.Zero(t, m.lines.Len())
	m.Append(dao.NewLogItemFromString("after-clear"))
	require.Equal(t, 1, m.lines.Len())
}

func BenchmarkLogRetainedBuffer(b *testing.B) {
	for _, size := range []int{128, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m := NewLog(client.NewGVR("pods"), makeLogOpts(size), time.Millisecond)
			line := dao.NewLogItemFromString("steady log stream")
			for range size {
				m.Append(line)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.Append(line)
			}
		})
	}
}
