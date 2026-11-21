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
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type deleteResult struct {
	called, cancelled bool
	prop              *metav1.DeletionPropagation
	force             bool
}

func showDelete(t *testing.T) (*ui.Pages, *keyDriver, *deleteResult) {
	t.Helper()
	p, res := ui.NewPages(), new(deleteResult)
	ok := func(prop *metav1.DeletionPropagation, force bool) {
		res.called, res.prop, res.force = true, prop, force
	}
	ShowDelete(new(config.Dialog), p, "Yo", ok, func() { res.cancelled = true })
	m, isModal := p.GetPrimitive(dialogKey).(*tview.ModalForm)
	require.True(t, isModal)
	d := keyDriver{root: m}
	d.setFocus(m)

	return p, &d, res
}

// keyDriver moves the focus like tview.Application does.
type keyDriver struct {
	root, focused tview.Primitive
}

func (d *keyDriver) setFocus(p tview.Primitive) {
	if d.focused != nil {
		d.focused.Blur()
	}
	d.focused = p
	p.Focus(d.setFocus)
}

func (d *keyDriver) press(keys ...tcell.Key) {
	for _, k := range keys {
		d.root.InputHandler()(tcell.NewEventKey(k, 0, tcell.ModNone), d.setFocus)
	}
}

func (d *keyDriver) rune(r rune) {
	d.root.InputHandler()(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone), d.setFocus)
}

func TestDeleteDialogFocusStartsOnCancel(t *testing.T) {
	_, m, res := showDelete(t)

	m.press(tcell.KeyEnter)

	assert.False(t, res.called)
	assert.True(t, res.cancelled)
}

func TestDeleteDialogCancel(t *testing.T) {
	p, m, res := showDelete(t)

	m.press(tcell.KeyEnter)

	assert.False(t, res.called)
	assert.True(t, res.cancelled)
	assert.Nil(t, p.GetPrimitive(dialogKey))
}

func TestDeleteDialogEscape(t *testing.T) {
	p, m, res := showDelete(t)

	m.press(tcell.KeyEscape)

	assert.False(t, res.called)
	assert.True(t, res.cancelled)
	assert.Nil(t, p.GetPrimitive(dialogKey))
}

func TestDeleteDialogDefaults(t *testing.T) {
	p, m, res := showDelete(t)

	m.press(tcell.KeyTab, tcell.KeyEnter)

	require.True(t, res.called)
	require.NotNil(t, res.prop)
	assert.Equal(t, metav1.DeletePropagationBackground, *res.prop)
	assert.False(t, res.force)
	assert.Nil(t, p.GetPrimitive(dialogKey))
}

func TestDeleteDialogPropagation(t *testing.T) {
	uu := map[string]struct {
		downs int
		e     *metav1.DeletionPropagation
	}{
		"background": {downs: 0, e: ptr(metav1.DeletePropagationBackground)},
		"foreground": {downs: 1, e: ptr(metav1.DeletePropagationForeground)},
		"orphan":     {downs: 2, e: ptr(metav1.DeletePropagationOrphan)},
		"none":       {downs: 3},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			_, m, res := showDelete(t)

			m.press(tcell.KeyBacktab, tcell.KeyBacktab, tcell.KeyEnter)
			for range u.downs {
				m.press(tcell.KeyDown)
			}
			m.press(tcell.KeyEnter, tcell.KeyTab, tcell.KeyTab, tcell.KeyTab, tcell.KeyEnter)

			require.True(t, res.called)
			assert.Equal(t, u.e, res.prop)
			assert.False(t, res.force)
		})
	}
}

func TestDeleteDialogForce(t *testing.T) {
	_, m, res := showDelete(t)

	m.press(tcell.KeyBacktab)
	m.rune(' ')
	m.press(tcell.KeyTab, tcell.KeyTab, tcell.KeyEnter)

	require.True(t, res.called)
	assert.True(t, res.force)
}

func ptr[T any](v T) *T {
	return &v
}
