// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tview"
)

// ShowHelmUpgrade pops the HelmRelease force/reset dialog. Call ok only
// with at least one option checked.
func ShowHelmUpgrade(styles *config.Dialog, pages *ui.Pages, msg string, ok func(force, reset bool)) {
	force, reset := false, false
	f := tview.NewForm()
	f.SetItemPadding(0)
	f.SetButtonsAlign(tview.AlignCenter).
		SetButtonBackgroundColor(styles.ButtonBgColor.Color()).
		SetButtonTextColor(styles.ButtonFgColor.Color()).
		SetLabelColor(styles.LabelFgColor.Color()).
		SetFieldTextColor(styles.FieldFgColor.Color())
	f.AddCheckbox("Force (upgrade even without changes):", force, func(_ string, checked bool) {
		force = checked
	})
	f.AddCheckbox("Reset (clear retry failures):", reset, func(_ string, checked bool) {
		reset = checked
	})
	f.AddButton("Cancel", func() {
		dismiss(pages)
	})
	f.AddButton("OK", func() {
		dismiss(pages)
		if force || reset {
			ok(force, reset)
		}
	})
	for i := range 2 {
		b := f.GetButton(i)
		if b == nil {
			continue
		}
		b.SetBackgroundColorActivated(styles.ButtonFocusBgColor.Color())
		b.SetLabelColorActivated(styles.ButtonFocusFgColor.Color())
	}
	f.SetFocus(0)

	modal := tview.NewModalForm("<Reconcile HelmRelease>", f)
	modal.SetText(msg)
	modal.SetDoneFunc(func(int, string) {
		dismiss(pages)
	})
	pages.AddPage(dialogKey, modal, false, false)
	pages.ShowPage(dialogKey)
}
