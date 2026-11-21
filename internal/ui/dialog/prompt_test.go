// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/stretchr/testify/assert"
)

func TestShowPrompt(t *testing.T) {
	t.Run("waiting done", func(t *testing.T) {
		a := tview.NewApplication()
		p := ui.NewPages()
		a.SetRoot(p, false)

		var mu sync.Mutex
		queue := func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
		ShowPrompt(new(config.Dialog), p, queue, "Running", "Pod", func(context.Context) {
			time.Sleep(time.Millisecond)
		}, func() {
			t.Errorf("unexpected cancellations")
		})
	})

	t.Run("canceled", func(t *testing.T) {
		a := tview.NewApplication()
		p := ui.NewPages()
		a.SetRoot(p, false)

		var mu sync.Mutex
		queue := func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
		done := make(chan struct{})
		ShowPrompt(new(config.Dialog), p, queue, "Running", "Pod", func(ctx context.Context) {
			defer close(done)
			select {
			case <-time.After(time.Second):
				t.Errorf("expected cancellations")
			case <-ctx.Done():
			}
		}, func() {})

		time.Sleep(time.Second / 2)
		queue(func() {
			d := p.GetPrimitive(dialogKey).(*tview.ModalForm)
			if assert.NotNil(t, d) {
				// Set focus like tview.Application to reach the Cancel button.
				var setFocus func(tview.Primitive)
				setFocus = func(p tview.Primitive) { p.Focus(setFocus) }
				d.Focus(setFocus)
				d.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, '\n', 0), setFocus)
			}
		})
		<-done
	})
}
