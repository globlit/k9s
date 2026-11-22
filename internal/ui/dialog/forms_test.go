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

func showRestart(t *testing.T, accept bool) (*ui.Pages, *keyDriver, *calls, *[]metav1.PatchOptions) {
	t.Helper()
	p, c := ui.NewPages(), new(calls)
	var got []metav1.PatchOptions
	ShowRestart(new(config.Dialog), p, &RestartDialogOpts{
		Title:        "Restart",
		Message:      "Restart dp?",
		FieldManager: "k9s",
		Ack: func(o *metav1.PatchOptions) bool {
			got = append(got, *o)
			return accept
		},
		Cancel: c.cancelFn,
	})

	return p, openDialog(t, p, confirmKey), c, &got
}

func TestRestartDialog(t *testing.T) {
	t.Run("focus-on-cancel", func(t *testing.T) {
		p, d, c, got := showRestart(t, true)
		assert.Equal(t, "Cancel", d.focusedLabel())

		d.press(tcell.KeyEnter)

		assert.Empty(t, *got)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	})

	t.Run("escape", func(t *testing.T) {
		p, d, c, got := showRestart(t, true)

		d.press(tcell.KeyEscape)

		assert.Empty(t, *got)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	})

	t.Run("ok-default", func(t *testing.T) {
		p, d, c, got := showRestart(t, true)

		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		require.Len(t, *got, 1)
		assert.Equal(t, "k9s", (*got)[0].FieldManager)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	})

	t.Run("ok-field-manager", func(t *testing.T) {
		_, d, _, got := showRestart(t, true)

		d.tabToItem(t, "FieldManager:")
		d.text("-x")
		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		require.Len(t, *got, 1)
		assert.Equal(t, "k9s-x", (*got)[0].FieldManager)
	})

	t.Run("ack-refused", func(t *testing.T) {
		p, d, c, got := showRestart(t, false)

		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		assert.Len(t, *got, 1)
		assert.Equal(t, 0, c.cancel)
		assert.NotNil(t, p.GetPrimitive(confirmKey))
	})
}

func showTransfer(t *testing.T, cc []string, accept bool) (*ui.Pages, *keyDriver, *calls, *[]TransferArgs) {
	t.Helper()
	p, c := ui.NewPages(), new(calls)
	var got []TransferArgs
	ShowUploads(new(config.Dialog), p, &TransferDialogOpts{
		Containers: cc,
		Pod:        "ns1/p1:",
		Title:      "Transfer",
		Message:    "Download Files",
		Retries:    3,
		Ack: func(a TransferArgs) bool {
			got = append(got, a)
			return accept
		},
		Cancel: c.cancelFn,
	})

	return p, openDialog(t, p, confirmKey), c, &got
}

func modalText(t *testing.T, p *ui.Pages) string {
	t.Helper()
	m, ok := p.GetPrimitive(confirmKey).(*tview.ModalForm)
	require.True(t, ok)
	scr := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, scr.Init())
	scr.SetSize(100, 30)
	m.SetRect(0, 0, 100, 30)
	m.Draw(scr)
	scr.Show()
	cells, w, _ := scr.GetContents()
	var out []rune
	for i, c := range cells {
		if i%w == 0 {
			out = append(out, '\n')
		}
		if len(c.Runes) > 0 {
			out = append(out, c.Runes[0])
		}
	}

	return string(out)
}

func TestTransferDialog(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p, d, c, got := showTransfer(t, []string{"c1", "c2"}, true)
		assert.Equal(t, "Download:", d.focusedLabel())
		assert.Contains(t, modalText(t, p), "Available Containers:c1,c2")

		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		require.Len(t, *got, 1)
		assert.Equal(t, TransferArgs{From: "ns1/p1:", CO: "c1", Download: true, Retries: 3}, (*got)[0])
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	})

	t.Run("upload", func(t *testing.T) {
		p, d, _, got := showTransfer(t, []string{"c1"}, true)

		d.rune(' ')
		assert.Contains(t, modalText(t, p), "Upload Files")
		assert.NotContains(t, modalText(t, p), "Available Containers")
		d.tabToItem(t, "From:")
		d.text("/tmp/a")
		d.tabToItem(t, "NoPreserve:")
		d.rune(' ')
		d.tabToItem(t, "Container:")
		d.text("x")
		d.tabToItem(t, "Retries:")
		d.press(tcell.KeyBackspace2)
		d.text("7")
		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		require.Len(t, *got, 1)
		assert.Equal(t, TransferArgs{
			From: "/tmp/a", To: "ns1/p1:", CO: "c1x", NoPreserve: true, Retries: 7,
		}, (*got)[0])
	})

	t.Run("toggle-twice", func(t *testing.T) {
		p, d, _, got := showTransfer(t, nil, true)

		d.rune(' ')
		d.rune(' ')
		assert.Contains(t, modalText(t, p), "Download Files")
		d.tabToItem(t, "Retries:")
		d.text("x")
		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		require.Len(t, *got, 1)
		assert.Equal(t, TransferArgs{From: "ns1/p1:", Download: true, Retries: 3}, (*got)[0])
	})

	t.Run("cancel", func(t *testing.T) {
		p, d, c, got := showTransfer(t, nil, true)

		d.tabTo(t, "Cancel")
		d.press(tcell.KeyEnter)

		assert.Empty(t, *got)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(confirmKey))
	})

	t.Run("escape", func(t *testing.T) {
		p, d, c, got := showTransfer(t, nil, true)

		d.press(tcell.KeyEscape)

		assert.Empty(t, *got)
		assert.Equal(t, 1, c.cancel)
		assert.Nil(t, p.GetPrimitive(dialogKey))
	})

	t.Run("ack-refused", func(t *testing.T) {
		p, d, c, got := showTransfer(t, nil, false)

		d.tabTo(t, "OK")
		d.press(tcell.KeyEnter)

		assert.Len(t, *got, 1)
		assert.Equal(t, 0, c.cancel)
		assert.NotNil(t, p.GetPrimitive(confirmKey))
	})
}

func TestHelmUpgradeDialog(t *testing.T) {
	uu := map[string]struct {
		force, reset bool
		button       string
		called       bool
	}{
		"none":    {button: "OK"},
		"force":   {force: true, button: "OK", called: true},
		"reset":   {reset: true, button: "OK", called: true},
		"both":    {force: true, reset: true, button: "OK", called: true},
		"cancel":  {force: true, button: "Cancel"},
		"escaped": {force: true},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			p := ui.NewPages()
			var (
				called       bool
				force, reset bool
			)
			ShowHelmUpgrade(new(config.Dialog), p, "Yo", func(f, r bool) {
				called, force, reset = true, f, r
			})
			d := openDialog(t, p, dialogKey)
			if u.force {
				d.tabToItem(t, "Force (upgrade even without changes):")
				d.rune(' ')
			}
			if u.reset {
				d.tabToItem(t, "Reset (clear retry failures):")
				d.rune(' ')
			}
			if u.button == "" {
				d.press(tcell.KeyEscape)
			} else {
				d.tabTo(t, u.button)
				d.press(tcell.KeyEnter)
			}

			assert.Equal(t, u.called, called)
			assert.Equal(t, u.called && u.force, force)
			assert.Equal(t, u.called && u.reset, reset)
			assert.Nil(t, p.GetPrimitive(dialogKey))
		})
	}
}

func TestSelectionDialog(t *testing.T) {
	uu := map[string]struct {
		keys []tcell.Key
		e    int
	}{
		"first":  {keys: []tcell.Key{tcell.KeyEnter}, e: 0},
		"second": {keys: []tcell.Key{tcell.KeyDown, tcell.KeyEnter}, e: 1},
		"escape": {keys: []tcell.Key{tcell.KeyEscape}, e: -1},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			p := ui.NewPages()
			got := -2
			ShowSelection(new(config.Dialog), p, "Pick", []string{"a", "b"}, func(i int) { got = i })
			d := openDialog(t, p, dialogKey)

			d.press(u.keys...)

			assert.Equal(t, u.e, got)
			assert.Nil(t, p.GetPrimitive(dialogKey))
		})
	}
}
