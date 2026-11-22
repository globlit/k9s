// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"sync"
	"testing"
)

func BenchmarkDispatcherBurst(b *testing.B) {
	const burst = 1_000
	app, d := runSimApp(b)

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
