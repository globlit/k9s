// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"sync"
	"testing"

	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
)

func BenchmarkDispatcherBurst(b *testing.B) {
	const burst = 1_000
	app := tview.NewApplication().SetScreen(tcell.NewSimulationScreen("UTF-8"))
	app.SetRoot(tview.NewBox(), true)
	go func() { _ = app.Run() }()
	b.Cleanup(app.Stop)
	d := newDispatcher()

	b.ReportAllocs()
	for b.Loop() {
		var wg sync.WaitGroup
		wg.Add(burst)
		for i := range burst {
			d.enqueue(app, uiUpdate{f: wg.Done, draw: i%10 == 0})
		}
		wg.Wait()
	}
}
