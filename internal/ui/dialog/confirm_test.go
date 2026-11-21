// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/assert"
)

func TestConfirmDialog(t *testing.T) {
	a := tview.NewApplication()
	p := ui.NewPages()
	a.SetRoot(p, false)
	ShowConfirm(new(config.Dialog), p, "Blee", "Yo", func() {}, func() {})

	d := p.GetPrimitive(dialogKey).(*tview.ModalForm)
	assert.NotNil(t, d)

	dismiss(p)
	assert.Nil(t, p.GetPrimitive(dialogKey))
}

func TestConfirmDialogKeys(t *testing.T) {
	uu := map[string]struct {
		keys       []tcell.Key
		ok, cancel int
	}{
		"enter-on-cancel": {keys: []tcell.Key{tcell.KeyEnter}, cancel: 1},
		"escape":          {keys: []tcell.Key{tcell.KeyEscape}, cancel: 1},
		"ok":              {keys: []tcell.Key{tcell.KeyTab, tcell.KeyEnter}, ok: 1, cancel: 1},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			p, c := ui.NewPages(), new(calls)
			ShowConfirm(new(config.Dialog), p, "Blee", "Yo", c.okFn, c.cancelFn)
			d := openDialog(t, p, dialogKey)
			assert.Equal(t, "Cancel", d.focusedLabel())

			d.press(u.keys...)

			assert.Equal(t, u.ok, c.ok)
			assert.Equal(t, u.cancel, c.cancel)
			assert.Nil(t, p.GetPrimitive(dialogKey))
		})
	}
}

func TestConfirmAckDialog(t *testing.T) {
	uu := map[string]struct {
		override   bool
		word       string
		ok, cancel int
		open       bool
	}{
		"no-override":  {ok: 1, cancel: 1},
		"right-word":   {override: true, word: "fred", ok: 1, cancel: 1},
		"wrong-word":   {override: true, word: "fre", open: true},
		"no-word":      {override: true, open: true},
		"case-matters": {override: true, word: "Fred", open: true},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			app := &ui.App{Configurator: ui.Configurator{Styles: config.NewStyles()}}
			p, c := ui.NewPages(), new(calls)
			ShowConfirmAck(app, p, "fred", u.override, "Blee", "Yo", c.okFn, c.cancelFn)
			d := openDialog(t, p, confirmKey)
			if u.override {
				assert.Equal(t, "Confirm:", d.focusedLabel())
			}

			d.text(u.word)
			d.tabTo(t, "OK")
			d.press(tcell.KeyEnter)

			assert.Equal(t, u.ok, c.ok)
			assert.Equal(t, u.cancel, c.cancel)
			assert.Equal(t, u.open, p.GetPrimitive(confirmKey) != nil)
		})
	}
}

func TestConfirmAckDialogCancel(t *testing.T) {
	for _, k := range []tcell.Key{tcell.KeyEscape, tcell.KeyEnter} {
		app := &ui.App{Configurator: ui.Configurator{Styles: config.NewStyles()}}
		p, c := ui.NewPages(), new(calls)
		ShowConfirmAck(app, p, "fred", true, "Blee", "Yo", c.okFn, c.cancelFn)
		d := openDialog(t, p, confirmKey)
		d.text("fred")
		if k == tcell.KeyEnter {
			d.tabTo(t, "Cancel")
		}

		d.press(k)

		assert.Equal(t, 0, c.ok)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	}
}
