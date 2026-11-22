// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/mock"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffColorAndSearch(t *testing.T) {
	lines := []string{"--- old", "+++ new", "@@ -1 +1 @@", "-[red]é", "+[blue]é", " <<<\"search_99\">>>literal<<<\"\">>>"}
	style := config.NewStyles().Views().Diff
	text := colorizeDiff(style, lines, nil)
	assert.Contains(t, text, "["+style.Remove.String()+"::]-[red[]é[-::-]")
	assert.Contains(t, text, "["+style.Add.String()+"::]+[blue[]é[-::-]")
	assert.NotContains(t, text, `["search_99"]`)
	matched := colorizeDiff(style, lines, fuzzy.Find("é", lines))
	view := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetText(matched)
	assert.Contains(t, view.GetText(true), "-[red[]é")
	assert.Contains(t, view.GetText(true), "+[blue[]é")
	assert.Contains(t, matched, `["search_0"]é[""]`)
	assert.Contains(t, matched, `["search_1"]é[""]`)
}

func TestDiffBindingsReadOnly(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		cfg := mock.NewMockConfig(t)
		cfg.K9s.ReadOnly = readOnly
		stub := NewBrowser(client.RsGVR).(*Browser)
		stub.app = NewApp(cfg)
		rs := ReplicaSet{ResourceViewer: stub}
		actions := ui.NewKeyActions()
		rs.bindKeys(actions)
		action, ok := actions.Get(ui.KeyShiftD)
		require.True(t, ok)
		assert.Equal(t, "Diff", action.Description)
		assert.False(t, action.Opts.Dangerous)
		assert.Contains(t, actions.Hints(), model.MenuHint{Mnemonic: "Shift-D", Description: "Diff", Visible: true})
		history := History{ResourceViewer: stub}
		actions = ui.NewKeyActions()
		actions.Add(ui.KeySpace, ui.NewKeyAction("Mark", nil, false))
		history.bindKeys(actions)
		action, ok = actions.Get(ui.KeyShiftD)
		require.True(t, ok)
		assert.Equal(t, "Diff", action.Description)
		assert.False(t, action.Opts.Dangerous)
		assert.Contains(t, actions.Hints(), model.MenuHint{Mnemonic: "Shift-D", Description: "Diff", Visible: true})
		_, ok = actions.Get(ui.KeySpace)
		assert.True(t, ok, "Helm revisions must remain markable")
		_, ok = actions.Get(tcell.KeyCtrlD)
		assert.False(t, ok)
	}
}

func TestDiffModeKeepsPageIdentity(t *testing.T) {
	app := NewApp(mock.NewMockConfig(t))
	diff := NewDiff(app, "release revisions", "values", "manifest")
	before := diff.Name()
	diff.toggleModeCmd(nil)
	assert.Equal(t, before, diff.Name(), "page IDs must remain stable for back navigation")
	assert.Equal(t, "Diff Manifest", diff.title)
	assert.Equal(t, []string{"manifest"}, diff.model.Peek())
	diff.toggleModeCmd(nil)
	assert.Equal(t, before, diff.Name())
	assert.Equal(t, "Diff Values", diff.title)
	assert.Equal(t, []string{"values"}, diff.model.Peek())
}

type diffMatches struct{ matches fuzzy.Matches }

func (*diffMatches) TextChanged([]string)                             {}
func (c *diffMatches) TextFiltered(_ []string, matches fuzzy.Matches) { c.matches = matches }

func TestDiffSearchKeepsEachRegexOccurrence(t *testing.T) {
	text := model.NewText()
	capture := new(diffMatches)
	text.AddListener(capture)
	text.SetText("+same same")
	text.Filter("same")
	require.Len(t, capture.matches, 2)
	colored := colorizeDiff(config.NewStyles().Views().Diff, text.Peek(), capture.matches)
	assert.Contains(t, colored, `["search_0"]same[""]`)
	assert.Contains(t, colored, `["search_1"]same[""]`)
	view := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetText(colored)
	assert.Equal(t, "+same same", view.GetText(true))
}
