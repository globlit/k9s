// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"sync"

	"github.com/derailed/tview"
)

// uiUpdate is a queued UI mutation.
type uiUpdate struct {
	f    func()
	draw bool
}

// dispatcher funnels UI updates to tview from a single goroutine.
//
// tview's QueueUpdate blocks until the UI goroutine has run the update, so
// callers can't use it directly (they may be the UI goroutine). Spawning a
// goroutine per update, as k9s used to, loses ordering between updates and
// costs one full screen draw per update. The dispatcher keeps a FIFO, hands
// every pending update to tview as one batch and draws at most once per
// batch.
type dispatcher struct {
	mx      sync.Mutex
	pending []uiUpdate
	wake    chan struct{}
	once    sync.Once
}

func newDispatcher() *dispatcher {
	return &dispatcher{wake: make(chan struct{}, 1)}
}

// enqueue adds an update; it never blocks.
func (d *dispatcher) enqueue(app *tview.Application, u uiUpdate) {
	d.once.Do(func() { go d.run(app) })

	d.mx.Lock()
	d.pending = append(d.pending, u)
	d.mx.Unlock()

	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *dispatcher) take() []uiUpdate {
	d.mx.Lock()
	defer d.mx.Unlock()

	batch := d.pending
	d.pending = nil

	return batch
}

func (d *dispatcher) run(app *tview.Application) {
	for range d.wake {
		batch := d.take()
		if len(batch) == 0 {
			continue
		}
		draw := false
		for _, u := range batch {
			draw = draw || u.draw
		}
		run := func() {
			for _, u := range batch {
				u.f()
			}
		}
		if draw {
			app.QueueUpdateDraw(run)
		} else {
			app.QueueUpdate(run)
		}
	}
}
