// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
)

func TestDebugDialog(t *testing.T) {
	for _, accept := range []bool{true, false} {
		t.Run(map[bool]string{true: "accept", false: "cancel"}[accept], func(t *testing.T) {
			pages := ui.NewPages()
			var got *config.DebugContainer
			ShowDebug(new(config.Dialog), pages, "ns/pod", config.NewDebugContainer(), func(v config.DebugContainer) { got = &v })
			d := openDialog(t, pages, dialogKey)
			if accept {
				d.tabTo(t, "Debug")
				d.press(tcell.KeyEnter)
				assert.Equal(t, config.NewDebugContainer(), *got)
			} else {
				d.press(tcell.KeyEscape)
				assert.Nil(t, got)
			}
			assert.Nil(t, pages.GetPrimitive(dialogKey))
		})
	}
}
