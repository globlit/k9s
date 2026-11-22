// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"

	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/ui/dialog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// confirmWorkloadState chooses one state for all marks using the current objects.
func confirmWorkloadState(v ResourceViewer, selections []string, field, enable, disable string, resource *dao.Generic, apply func(context.Context, string, types.UID, bool) error) {
	app := v.App()
	if app.Config.IsReadOnly() || len(selections) == 0 {
		return
	}
	top := app.Content.Top()
	activeContext := app.Config.K9s.ActiveContextName()
	target := false
	uids := make(map[string]types.UID, len(selections))
	runAsync(context.Background(), app, selections, "Read workload state", func(ctx context.Context, path string) error {
		obj, err := resource.Get(ctx, path)
		if err != nil {
			return err
		}
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("unexpected workload %T", obj)
		}
		if u.GetUID() == "" {
			return fmt.Errorf("workload %s has no UID; refresh before changing its state", path)
		}
		uids[path] = u.GetUID()
		current, _, err := unstructured.NestedBool(u.Object, "spec", field)
		if err != nil {
			return err
		}
		target = target || !current
		return nil
	}, func(_ []string, errs []error) {
		if len(errs) > 0 {
			if len(errs) == 1 {
				app.Flash().Err(errs[0])
			}
			return
		}
		if app.Config.IsReadOnly() || app.Content.Top() != top || app.Config.K9s.ActiveContextName() != activeContext {
			return
		}
		verb := disable
		if target {
			verb = enable
		}
		msg := fmt.Sprintf("%s %s %s?", verb, v.GVR().R(), selections[0])
		if len(selections) > 1 {
			msg = fmt.Sprintf("%s %d %s?", verb, len(selections), v.GVR().R())
		}
		styles := app.Styles.Dialog()
		dialog.ShowConfirm(&styles, app.Content.Pages, "Confirm "+verb, msg, func() {
			if app.Config.IsReadOnly() || app.Config.K9s.ActiveContextName() != activeContext {
				return
			}
			runAsync(context.Background(), app, selections, verb, func(ctx context.Context, path string) error {
				if app.Config.K9s.ActiveContextName() != activeContext {
					return fmt.Errorf("active context changed")
				}
				return apply(ctx, path, uids[path], target)
			}, func(_ []string, errs []error) {
				if len(errs) == 1 && len(selections) == 1 {
					app.Flash().Err(errs[0])
				}
				if app.Content.Top() == top && app.Config.K9s.ActiveContextName() == activeContext {
					v.Refresh()
				}
			})
		}, func() {})
	})
}
