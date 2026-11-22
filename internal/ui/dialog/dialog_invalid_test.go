// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorDialogEscape(t *testing.T) {
	for _, msg := range []string{"", "\n", "boom\n", "a\nb", "[red]x[-]"} {
		t.Run(msg, func(t *testing.T) {
			p := ui.NewPages()
			ShowError(new(config.Dialog), p, msg)
			d := openDialog(t, p, dialogKey)

			d.press(tcell.KeyEscape)

			assert.Nil(t, p.GetPrimitive(dialogKey))
		})
	}
}

func TestShowPromptNilQueue(t *testing.T) {
	p := ui.NewPages()
	done := make(chan struct{})
	ShowPrompt(new(config.Dialog), p, nil, "Run", "msg", func(context.Context) { close(done) }, func() {
		t.Error("unexpected cancel")
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "the action does not run")
	}
}

func TestShowPromptEscape(t *testing.T) {
	var mu sync.Mutex
	queue := func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
	p := ui.NewPages()
	canceled := make(chan struct{})
	var cancels int
	ShowPrompt(new(config.Dialog), p, queue, "Run", "msg", func(ctx context.Context) {
		<-ctx.Done()
		close(canceled)
	}, func() { cancels++ })

	queue(func() { openDialog(t, p, dialogKey).press(tcell.KeyEscape) })

	select {
	case <-canceled:
	case <-time.After(time.Second):
		require.Fail(t, "the action context is not canceled")
	}
	queue(func() {
		assert.Equal(t, 1, cancels)
		assert.Nil(t, p.GetPrimitive(dialogKey))
	})
}
