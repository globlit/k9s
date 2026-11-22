// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dialog

import (
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inputsRun struct {
	p       *ui.Pages
	d       *keyDriver
	c       *calls
	values  []PluginInputValues
	flashes []string
}

func showInputs(t *testing.T, ii []config.PluginInput) *inputsRun {
	t.Helper()
	r := inputsRun{p: ui.NewPages(), c: new(calls)}
	ShowPluginInputs(new(config.Dialog), r.p, "Plug", ii,
		func(m string) { r.flashes = append(r.flashes, m) },
		func(v PluginInputValues) { r.values = append(r.values, v) },
		r.c.cancelFn,
	)
	if r.p.GetPrimitive(pluginInputsKey) != nil {
		r.d = openDialog(t, r.p, pluginInputsKey)
	}

	return &r
}

func (r *inputsRun) ok(t *testing.T) {
	t.Helper()
	r.d.tabTo(t, "OK")
	r.d.press(tcell.KeyEnter)
}

func TestPluginInputsNone(t *testing.T) {
	r := showInputs(t, nil)

	require.Len(t, r.values, 1)
	assert.Empty(t, r.values[0])
	assert.Nil(t, r.p.GetPrimitive(pluginInputsKey))
	assert.Equal(t, 0, r.c.cancel)
}

func TestPluginInputsDefaults(t *testing.T) {
	r := showInputs(t, []config.PluginInput{
		{Name: "s", Type: config.InputTypeString, Default: "a"},
		{Name: "n", Label: "Count", Type: config.InputTypeNumber, Default: "3"},
		{Name: "b", Type: config.InputTypeBool, Default: "true"},
		{Name: "d", Type: config.InputTypeDropdown, Default: "y", Options: []string{"x", "y"}},
		{Name: "e", Type: config.InputTypeString},
		{Name: "nb", Type: config.InputTypeBool},
		{Name: "no-opts", Type: config.InputTypeDropdown},
	})
	assert.Equal(t, "s:", r.d.focusedLabel())

	r.ok(t)

	require.Len(t, r.values, 1)
	assert.Equal(t, PluginInputValues{"s": "a", "n": "3", "b": "true", "d": "y", "nb": ""}, r.values[0])
	assert.Empty(t, r.flashes)
	assert.Equal(t, 1, r.c.cancel)
	assert.Nil(t, r.p.GetPrimitive(pluginInputsKey))
}

func TestPluginInputsEdit(t *testing.T) {
	r := showInputs(t, []config.PluginInput{
		{Name: "s", Type: config.InputTypeString, Required: true},
		{Name: "n", Type: config.InputTypeNumber},
		{Name: "b", Type: config.InputTypeBool},
		{Name: "d", Type: config.InputTypeDropdown, Options: []string{"x", "y"}},
	})

	r.d.text("a b")
	r.d.tabToItem(t, "n:")
	r.d.text("-1x.5")
	r.d.tabToItem(t, "b:")
	r.d.rune(' ')
	r.d.tabToItem(t, "d:")
	r.d.press(tcell.KeyEnter, tcell.KeyDown, tcell.KeyDown, tcell.KeyEnter)
	r.ok(t)

	require.Len(t, r.values, 1)
	assert.Equal(t, PluginInputValues{"s": `"a b"`, "n": "-1.5", "b": "true", "d": "y"}, r.values[0])
}

func TestPluginInputsRequired(t *testing.T) {
	r := showInputs(t, []config.PluginInput{
		{Name: "s", Label: "Name", Type: config.InputTypeString, Required: true},
		{Name: "b", Type: config.InputTypeBool, Required: true},
	})
	assert.Equal(t, "Name *:", r.d.focusedLabel())

	r.ok(t)

	assert.Empty(t, r.values)
	assert.Equal(t, []string{"Required fields are missing"}, r.flashes)
	assert.NotNil(t, r.p.GetPrimitive(pluginInputsKey))
	assert.Equal(t, 0, r.c.cancel)
}

func TestPluginInputsRequiredNoFlash(t *testing.T) {
	p, c := ui.NewPages(), new(calls)
	var called bool
	ShowPluginInputs(new(config.Dialog), p, "Plug",
		[]config.PluginInput{{Name: "s", Type: config.InputTypeString, Required: true}},
		nil, func(PluginInputValues) { called = true }, c.cancelFn)
	d := openDialog(t, p, pluginInputsKey)

	d.tabTo(t, "OK")
	d.press(tcell.KeyEnter)

	assert.False(t, called)
	assert.NotNil(t, p.GetPrimitive(pluginInputsKey))
}

func TestPluginInputsCancel(t *testing.T) {
	ii := []config.PluginInput{{Name: "s", Type: config.InputTypeString, Default: "a"}}
	t.Run("button", func(t *testing.T) {
		r := showInputs(t, ii)

		r.d.tabTo(t, "Cancel")
		r.d.press(tcell.KeyEnter)

		assert.Empty(t, r.values)
		assert.Equal(t, 1, r.c.cancel)
		assert.Nil(t, r.p.GetPrimitive(pluginInputsKey))
	})

	t.Run("escape", func(t *testing.T) {
		r := showInputs(t, ii)

		r.d.press(tcell.KeyEscape)

		assert.Empty(t, r.values)
		assert.Equal(t, 1, r.c.cancel)
		assert.Nil(t, r.p.GetPrimitive(pluginInputsKey))
	})
}
