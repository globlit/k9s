// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"strings"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tview"
)

// ShowDebug collects argv values without shell quoting or interpolation.
func ShowDebug(styles *config.Dialog, pages *ui.Pages, target string, defaults config.DebugContainer, ok func(config.DebugContainer)) {
	values := defaults.Validate()
	f := tview.NewForm()
	f.SetItemPadding(0)
	f.SetButtonsAlign(tview.AlignCenter).
		SetButtonBackgroundColor(styles.ButtonBgColor.Color()).
		SetButtonTextColor(styles.ButtonFgColor.Color()).
		SetLabelColor(styles.LabelFgColor.Color()).
		SetFieldTextColor(styles.FieldFgColor.Color())
	f.AddInputField("Image:", values.Image, 40, nil, func(v string) { values.Image = strings.TrimSpace(v) })
	f.AddInputField("Profile:", values.Profile, 24, nil, func(v string) { values.Profile = strings.TrimSpace(v) })
	f.AddButton("Cancel", func() { dismiss(pages) })
	f.AddButton("Debug", func() {
		if values.Image == "" || values.Profile == "" {
			return
		}
		dismiss(pages)
		ok(values)
	})
	for i := range f.GetButtonCount() {
		f.GetButton(i).SetBackgroundColorActivated(styles.ButtonFocusBgColor.Color()).SetLabelColorActivated(styles.ButtonFocusFgColor.Color())
	}
	f.SetFocus(0)
	modal := tview.NewModalForm("<Debug container>", f)
	modal.SetText(target)
	modal.SetTextColor(styles.FgColor.Color())
	modal.SetDoneFunc(func(int, string) { dismiss(pages) })
	pages.AddPage(dialogKey, modal, false, false)
	pages.ShowPage(dialogKey)
}
