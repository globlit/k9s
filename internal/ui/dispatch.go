// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"sync"

	"github.com/derailed/tview"
)

// uiUpdate holds a queued UI mutation.
type uiUpdate struct {
	f    func()
	draw bool
}

// dispatcher funnels UI updates to tview from one goroutine.
//
// tview QueueUpdate blocks until the UI goroutine runs the update. Keep a
// FIFO, hand pending updates to tview as one batch, draw once per batch.
type dispatcher struct {
	mx      sync.Mutex
	pending []uiUpdate
	wake    chan struct{}
	once    sync.Once
}

func newDispatcher() *dispatcher {
	return &dispatcher{wake: make(chan struct{}, 1)}
}

// enqueue adds an update. Never blocks.
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
