// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/view"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/require"
)

// Draw retained logs to include TextView reindexing.
func BenchmarkLogStreamDraw(b *testing.B) {
	for _, retained := range []int{1000, 5000} {
		b.Run(fmt.Sprint(retained), func(b *testing.B) {
			v := view.NewLog(client.PodGVR, &dao.LogOptions{Path: "ns/pod", Container: "stream"})
			require.NoError(b, v.Init(makeContext(b)))
			v.Logs().SetMaxLines(retained).SetWrap(false)
			v.Logs().SetRect(0, 0, 200, 50)
			screen := tcell.NewSimulationScreen("UTF-8")
			require.NoError(b, screen.Init())
			screen.SetSize(200, 50)
			b.Cleanup(screen.Fini)
			batch := make([][]byte, 100)
			for i := range batch {
				batch[i] = []byte("\x1b[32m" + strings.Repeat("x", 1000) + "\x1b[0m\n")
			}
			for range retained/len(batch) + 1 {
				v.Flush(batch)
				v.Logs().Draw(screen)
			}
			require.Greater(b, len(v.Logs().GetText(false)), retained*500, "benchmark must retain actual log content")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				v.Flush(batch)
				v.Logs().Draw(screen)
			}
		})
	}
}
