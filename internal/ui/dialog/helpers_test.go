// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"testing"

	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/require"
)

// openDialog returns a key driver on the page at key, with the form focus set.
func openDialog(t *testing.T, p *ui.Pages, key string) *keyDriver {
	t.Helper()
	prim := p.GetPrimitive(key)
	require.NotNil(t, prim, "no page %q", key)
	d := keyDriver{root: prim}
	d.setFocus(prim)

	return &d
}

func (d *keyDriver) text(s string) {
	for _, r := range s {
		d.rune(r)
	}
}

// tabTo presses Tab until the focus is on the button with label.
func (d *keyDriver) tabTo(t *testing.T, label string) {
	t.Helper()
	for range 20 {
		if b, ok := d.focused.(*tview.Button); ok && b.GetLabel() == label {
			return
		}
		d.press(tcell.KeyTab)
	}
	t.Fatalf("button %q not found", label)
}

// tabToItem presses Tab until the focus is on the form item with label.
func (d *keyDriver) tabToItem(t *testing.T, label string) {
	t.Helper()
	for range 20 {
		if i, ok := d.focused.(tview.FormItem); ok && i.GetLabel() == label {
			return
		}
		d.press(tcell.KeyTab)
	}
	t.Fatalf("form item %q not found", label)
}

func (d *keyDriver) focusedLabel() string {
	switch p := d.focused.(type) {
	case *tview.Button:
		return p.GetLabel()
	case tview.FormItem:
		return p.GetLabel()
	}

	return ""
}

type calls struct {
	ok, cancel int
}

func (c *calls) okFn()     { c.ok++ }
func (c *calls) cancelFn() { c.cancel++ }
