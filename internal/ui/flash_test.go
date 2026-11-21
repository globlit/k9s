// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui_test

import (
	"testing"
	"time"

	"github.com/derailed/k9s/internal/config/mock"
	"github.com/derailed/k9s/internal/model"
	"github.com/derailed/k9s/internal/ui"
	"github.com/stretchr/testify/assert"
)

func TestFlash(t *testing.T) {
	const delay = 10 * time.Millisecond
	uu := map[string]struct {
		l    model.FlashLevel
		i, e string
	}{
		"info": {l: model.FlashInfo, i: "hello", e: "😎 hello\n"},
		"warn": {l: model.FlashWarn, i: "hello", e: "😗 hello\n"},
		"err":  {l: model.FlashErr, i: "hello", e: "😡 hello\n"},
	}

	a := ui.NewApp(mock.NewMockConfig(t), "test")
	f := ui.NewFlash(a)
	f.SetTestMode(true)
	c := a.Flash().Channel()

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			a.Flash().SetMessage(u.l, u.i)
			// Apply on the test goroutine: tview widgets are not thread safe.
			select {
			case msg := <-c:
				f.SetMessage(msg)
			case <-time.After(delay * 10):
				t.Fatal("no flash message received")
			}
			assert.Equal(t, u.e, f.GetText(false))
		})
	}
}
