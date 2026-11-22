// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"sync"
	"testing"
	"time"

	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runSimApp(t testing.TB) (*tview.Application, *dispatcher) {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, scr.Init())
	app := tview.NewApplication().SetScreen(scr)
	app.SetRoot(tview.NewBox(), true)
	d := newDispatcher()
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	t.Cleanup(func() {
		d.shutdown(app, func() {})
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("application did not finish shutdown")
		}
	})

	return app, d
}

func TestDispatcherKeepsOrderPerProducer(t *testing.T) {
	app, d := runSimApp(t)

	const producers, perProducer = 8, 500
	var (
		mx   sync.Mutex
		seen = make(map[int][]int)
		done sync.WaitGroup
	)
	done.Add(producers * perProducer)
	for p := range producers {
		go func() {
			for i := range perProducer {
				d.enqueue(app, uiUpdate{f: func() {
					mx.Lock()
					seen[p] = append(seen[p], i)
					mx.Unlock()
					done.Done()
				}, draw: i%2 == 0})
			}
		}()
	}

	waitOrFail(t, &done)
	mx.Lock()
	defer mx.Unlock()
	for p := range producers {
		require.Len(t, seen[p], perProducer)
		for i, v := range seen[p] {
			assert.Equal(t, i, v, "producer %d out of order", p)
		}
	}
}

func TestDispatcherNestedEnqueue(t *testing.T) {
	app, d := runSimApp(t)

	var done sync.WaitGroup
	done.Add(2)
	var order []string
	d.enqueue(app, uiUpdate{f: func() {
		order = append(order, "outer")
		// Enqueue from the UI goroutine: no block, no deadlock.
		d.enqueue(app, uiUpdate{f: func() {
			order = append(order, "inner")
			done.Done()
		}})
		done.Done()
	}})

	waitOrFail(t, &done)
	assert.Equal(t, []string{"outer", "inner"}, order)
}

func waitOrFail(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	c := make(chan struct{})
	go func() { wg.Wait(); close(c) }()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("updates not applied in time")
	}
}

func TestDispatcherShutdownDrainsAndTerminates(t *testing.T) {
	app, d := runSimApp(t)
	var order []string
	d.enqueue(app, uiUpdate{f: func() { order = append(order, "accepted") }})
	d.shutdown(app, func() { order = append(order, "cleanup") })
	d.shutdown(app, func() { t.Error("duplicate cleanup") })
	d.enqueue(app, uiUpdate{f: func() { t.Error("update after shutdown") }})
	select {
	case <-d.done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not terminate")
	}
	assert.Equal(t, []string{"accepted", "cleanup"}, order)
}
