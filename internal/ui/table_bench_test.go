// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui_test

import (
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/ui"
)

func makePodLikeData(n int) *model1.TableData {
	h := render.NewPod().Header(client.NamespaceAll)
	st, _ := h.IndexOf("STATUS", true)
	va, _ := h.IndexOf("VALID", true)
	rr := make([]model1.RowEvent, 0, n)
	for i := range n {
		ff := make(model1.Fields, len(h))
		for c := range ff {
			ff[c] = fmt.Sprintf("v%d-%d", i, c)
		}
		ff[st], ff[va] = "Running", ""
		rr = append(rr, model1.RowEvent{Row: model1.Row{ID: fmt.Sprintf("ns/po-%d", i), Fields: ff}})
	}

	return model1.NewTableDataWithRows(client.PodGVR, h, model1.NewRowEventsWithEvts(rr...))
}

func BenchmarkTableUpdateUI(b *testing.B) {
	v := ui.NewTable(client.PodGVR)
	v.Init(makeContext())
	v.SetModel(new(mockModel))
	v.SetColorerFn(render.NewPod().ColorerFunc())
	data := makePodLikeData(5_000)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cdata := v.Update(data, true)
		v.UpdateUI(cdata, data)
	}
}
