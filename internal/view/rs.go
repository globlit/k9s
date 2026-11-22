// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/k9s/internal/ui/dialog"
	"github.com/globlit/tcell/v2"
)

// ReplicaSet presents a replicaset viewer.
type ReplicaSet struct {
	ResourceViewer
}

// NewReplicaSet returns a new viewer.
func NewReplicaSet(gvr *client.GVR) ResourceViewer {
	r := ReplicaSet{
		ResourceViewer: NewOwnerExtender(
			NewVulnerabilityExtender(
				NewBrowser(gvr),
			),
		),
	}
	r.AddBindKeysFn(r.bindKeys)
	r.GetTable().SetEnterFn(r.showPods)

	return &r
}

func (r *ReplicaSet) bindKeys(aa *ui.KeyActions) {
	aa.Add(ui.KeyShiftD, ui.NewKeyAction("Diff", r.diffCmd, true))
	if r.App().Config.IsReadOnly() {
		return
	}
	aa.Add(tcell.KeyCtrlL, ui.NewKeyActionWithOpts("Rollback", r.rollbackCmd,
		ui.ActionOpts{
			Visible:   true,
			Dangerous: true,
		}))
}

func (*ReplicaSet) showPods(app *App, _ ui.Tabular, _ *client.GVR, path string) {
	var drs dao.ReplicaSet
	rs, err := drs.Load(app.factory, path)
	if err != nil {
		app.Flash().Err(err)
		return
	}

	showPodsFromSelector(app, path, rs.Spec.Selector)
}

func (r *ReplicaSet) rollbackCmd(evt *tcell.EventKey) *tcell.EventKey {
	path := r.GetTable().GetSelectedItem()
	if path == "" {
		return evt
	}

	msg := fmt.Sprintf("Rollback %s %s?", r.GVR(), path)

	d := r.App().Styles.Dialog()
	dialog.ShowConfirm(&d, r.App().Content.Pages, "Rollback", msg, func() {
		r.App().Flash().Infof("Rolling back %s %s", r.GVR(), path)
		var drs dao.ReplicaSet
		drs.Init(r.App().factory, r.GVR())
		runAsync(context.Background(), r.App(), []string{path}, "rollback",
			func(context.Context, string) error { return drs.Rollback(path) },
			func(_ []string, errs []error) {
				if len(errs) > 0 {
					r.App().Flash().Err(errs[0])
				} else {
					r.App().Flash().Infof("%s successfully rolled back", path)
				}
				r.Refresh()
			})
	}, func() {})

	return nil
}

func (r *ReplicaSet) diffCmd(evt *tcell.EventKey) *tcell.EventKey {
	paths := r.GetTable().GetSelectedItems()
	if len(paths) == 0 {
		return evt
	}
	dial, err := r.App().Conn().Dial()
	if err != nil {
		r.App().Flash().Err(err)
		return nil
	}
	openDiff(r, func(ctx context.Context) (diffContent, error) {
		text, subject, err := dao.DiffReplicaSets(ctx, dial, paths)
		return diffContent{subject: subject, values: text}, err
	})
	return nil
}
