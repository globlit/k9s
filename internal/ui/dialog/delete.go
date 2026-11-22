// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	noDeletePropagation   = "None"
	defaultPropagationIdx = 0
)

type (
	okFunc     func(propagation *metav1.DeletionPropagation, force bool)
	cancelFunc func()
)

var propagationOptions []string = []string{
	string(metav1.DeletePropagationBackground),
	string(metav1.DeletePropagationForeground),
	string(metav1.DeletePropagationOrphan),
	noDeletePropagation,
}

// ShowDelete pops a resource deletion dialog.
func ShowDelete(styles *config.Dialog, pages *ui.Pages, msg string, ok okFunc, cancel cancelFunc) {
	showDeleteDialog(styles, pages, msg, false, false, func(p *metav1.DeletionPropagation, force, _ bool) { ok(p, force) }, cancel)
}

// ShowDeleteWithFinalizers adds an opt-in recovery action for terminating resources.
func ShowDeleteWithFinalizers(styles *config.Dialog, pages *ui.Pages, msg string, allTerminating bool, ok func(*metav1.DeletionPropagation, bool, bool), cancel cancelFunc) {
	showDeleteDialog(styles, pages, msg, true, allTerminating, ok, cancel)
}

func showDeleteDialog(styles *config.Dialog, pages *ui.Pages, msg string, allowFinalizers, allTerminating bool, ok func(*metav1.DeletionPropagation, bool, bool), cancel cancelFunc) {
	propagation, force, removeFinalizers := "", false, false
	f := tview.NewForm()
	f.SetItemPadding(0)
	f.SetButtonsAlign(tview.AlignCenter).
		SetButtonBackgroundColor(styles.ButtonBgColor.Color()).
		SetButtonTextColor(styles.ButtonFgColor.Color()).
		SetLabelColor(styles.LabelFgColor.Color()).
		SetFieldTextColor(styles.FieldFgColor.Color())
	f.AddDropDown("Propagation:", propagationOptions, defaultPropagationIdx, func(_ string, optionIndex int) {
		propagation = propagationOptions[optionIndex]
	})
	propField := f.GetFormItemByLabel("Propagation:").(*tview.DropDown)
	propField.SetListStyles(
		styles.FgColor.Color(), styles.BgColor.Color(),
		styles.ButtonFocusFgColor.Color(), styles.ButtonFocusBgColor.Color(),
	)
	syncFinalizers := func() {
		index := f.GetFormItemIndex("Remove finalizers:")
		visible := allowFinalizers && (allTerminating || force)
		if visible && index < 0 {
			f.AddCheckbox("Remove finalizers:", false, func(_ string, checked bool) { removeFinalizers = checked })
		}
		if !visible && index >= 0 {
			f.RemoveFormItem(index)
			removeFinalizers = false
		}
	}
	f.AddCheckbox("Force:", force, func(_ string, checked bool) {
		force = checked
		syncFinalizers()
	})
	syncFinalizers()
	f.AddButton("Cancel", func() {
		dismiss(pages)
		cancel()
	})
	f.AddButton("OK", func() {
		switch propagation {
		case noDeletePropagation:
			ok(nil, force, removeFinalizers)
		default:
			p := metav1.DeletionPropagation(propagation)
			ok(&p, force, removeFinalizers)
		}
		dismiss(pages)
		cancel()
	})
	for i := range 2 {
		b := f.GetButton(i)
		if b == nil {
			continue
		}
		b.SetBackgroundColorActivated(styles.ButtonFocusBgColor.Color())
		b.SetLabelColorActivated(styles.ButtonFocusFgColor.Color())
	}
	f.SetFocus(f.GetFormItemCount())

	confirm := tview.NewModalForm("<Delete>", f)
	confirm.SetText(msg)
	confirm.SetDoneFunc(func(int, string) {
		dismiss(pages)
		cancel()
	})
	pages.AddPage(dialogKey, confirm, false, false)
	pages.ShowPage(dialogKey)
}
