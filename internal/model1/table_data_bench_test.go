// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model1

import (
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/client"
)

func benchTable(n int) *TableData {
	rr := make([]RowEvent, 0, n)
	for i := range n {
		ns, name := fmt.Sprintf("ns-%d", i%20), fmt.Sprintf("app-%d-7d9f8c6b5-x%04d", i%50, i)
		rr = append(rr, RowEvent{Row: Row{
			ID:     ns + "/" + name,
			Fields: Fields{ns, name, "Running", fmt.Sprintf("10.0.%d.%d", i/250, i%250), "", "3d"},
		}})
	}

	return NewTableDataWithRows(
		client.PodGVR,
		Header{
			HeaderColumn{Name: "NAMESPACE"},
			HeaderColumn{Name: "NAME"},
			HeaderColumn{Name: "STATUS"},
			HeaderColumn{Name: "IP", Attrs: Attrs{Wide: true}},
			HeaderColumn{Name: "VALID", Attrs: Attrs{Wide: true}},
			HeaderColumn{Name: "AGE", Attrs: Attrs{Time: true}},
		},
		NewRowEventsWithEvts(rr...),
	)
}

func BenchmarkTableDataFilter(b *testing.B) {
	td := benchTable(10_000)
	for _, c := range []struct{ name, q string }{
		{"rx", "app-4[0-9]"},
		{"inverse", "!app-4"},
		{"fuzzy", "-f app4x00"},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				td.Filter(FilterOpts{Filter: c.q})
			}
		})
	}
}
