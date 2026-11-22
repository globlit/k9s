// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/sahilm/fuzzy"
)

// Diff reuses the searchable Details viewer for unified diffs.
type Diff struct {
	*Details
	values, manifest string
	showManifest     bool
}

// NewDiff creates a diff viewer with an optional manifest comparison.
func NewDiff(app *App, subject, values, manifest string) *Diff {
	return &Diff{Details: NewDetails(app, "Diff", subject, contentDiff, true), values: values, manifest: manifest}
}

// Name stays stable while the visible title switches between values and manifest.
func (*Diff) Name() string { return "Diff" }

func (d *Diff) Init(ctx context.Context) error {
	if err := d.Details.Init(ctx); err != nil {
		return err
	}
	if d.manifest != "" {
		d.actions.Add(ui.KeyM, ui.NewKeyAction("Toggle Values/Manifest", d.toggleModeCmd, true))
	}
	d.showContent()
	return nil
}

func (d *Diff) toggleModeCmd(*tcell.EventKey) *tcell.EventKey {
	d.showManifest = !d.showManifest
	d.showContent()
	return nil
}

func (d *Diff) showContent() {
	text := d.values
	d.title = "Diff"
	if d.manifest != "" {
		d.title = "Diff Values"
		if d.showManifest {
			text = d.manifest
			d.title = "Diff Manifest"
		}
	}
	d.Update(text)
	d.model.Filter(d.cmdBuff.GetText())
	d.updateTitle()
}

type diffContent struct{ subject, values, manifest string }

// openDiff keeps API reads off the UI thread and ignores late results after navigation.
func openDiff(source ResourceViewer, load func(context.Context) (diffContent, error)) {
	app := source.App()
	contextName := app.Config.ActiveContextName()
	timeout := app.Conn().Config().CallTimeout()
	app.Flash().Info("Loading diff...")
	go func() {
		defer internal.LogPanic("load diff")
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		content, err := load(ctx)
		app.QueueUpdateDraw(func() {
			if app.Content.Top() != source || app.Config.ActiveContextName() != contextName {
				return
			}
			if err != nil {
				app.Flash().Err(err)
				return
			}
			if err := app.inject(NewDiff(app, content.subject, content.values, content.manifest), false); err != nil {
				app.Flash().Err(err)
			}
		})
	}()
}

// colorizeDiff escapes source text and adds search regions only from trusted match offsets.
// The YAML placeholder helper cannot be used here: manifest values may contain
// its <<<"search_N">>> sentinel, which must remain literal source text.
func colorizeDiff(style config.Diff, lines []string, matches fuzzy.Matches) string {
	type region struct {
		id      int
		offsets []int
	}
	regions := make(map[int][]region, len(matches))
	for i, m := range matches {
		regions[m.Index] = append(regions[m.Index], region{i, m.MatchedIndexes})
	}
	colored := make([]string, len(lines))
	for i, line := range lines {
		color := config.Color("-")
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			color = style.Header
		case strings.HasPrefix(line, "+"):
			color = style.Add
		case strings.HasPrefix(line, "-"):
			color = style.Remove
		}
		var out strings.Builder
		fmt.Fprintf(&out, "[%s::]", color)
		cursor := 0
		for _, match := range regions[i] {
			for _, offsets := range dao.ContinuousRanges(match.offsets) {
				start, end := offsets[0], offsets[1]
				if start < cursor || start >= len(line) || end <= start || end > len(line) {
					continue
				}
				_, width := utf8.DecodeRuneInString(line[end-1:])
				end += width - 1
				if end > len(line) {
					end = len(line)
				}
				out.WriteString(tview.Escape(line[cursor:start]))
				fmt.Fprintf(&out, "[\"search_%d\"]%s[\"\"]", match.id, tview.Escape(line[start:end]))
				cursor = end
			}
		}
		out.WriteString(tview.Escape(line[cursor:]))
		out.WriteString("[-::-]")
		colored[i] = out.String()
	}
	return strings.Join(colored, "\n")
}
