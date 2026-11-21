// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/derailed/k9s/internal"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/ui/dialog"
	"github.com/derailed/tcell/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// Flux key bindings. Shift-G means "Goto Bottom": use Shift-W for reconcile
// with source.
const (
	fluxKeyReconcile   = ui.KeyShiftR
	fluxKeyWithSource  = ui.KeyShiftW
	fluxKeySuspend     = ui.KeyShiftT
	fluxKeyHelmUpgrade = ui.KeyShiftF
	fluxKeyJumpSource  = ui.KeyShiftZ
)

// FluxExtender adds native FluxCD actions to Flux views: suspend/resume,
// reconcile, reconcile with source, force/reset, jump to source.
// No flux CLI required.
type FluxExtender struct {
	ResourceViewer

	caps dao.FluxCaps
}

// NewFluxExtender returns a new extender.
func NewFluxExtender(v ResourceViewer) ResourceViewer {
	caps, _ := dao.FluxCapsFor(v.GVR())
	f := FluxExtender{ResourceViewer: v, caps: caps}
	v.AddBindKeysFn(f.bindKeys)

	return &f
}

func (f *FluxExtender) bindKeys(aa *ui.KeyActions) {
	if f.caps.Source {
		aa.Add(fluxKeyJumpSource, ui.NewKeyAction("Jump Source", f.jumpSourceCmd, true))
	}
	if f.App().Config.IsReadOnly() {
		return
	}
	if f.caps.Reconcile {
		aa.Add(fluxKeyReconcile, ui.NewKeyAction("Reconcile", f.reconcileCmd, true))
	}
	if f.caps.Source {
		aa.Add(fluxKeyWithSource, ui.NewKeyAction("Reconcile w/ Source", f.reconcileWithSourceCmd, true))
	}
	if f.caps.Suspend {
		aa.Add(fluxKeySuspend, ui.NewKeyActionWithOpts("Suspend/Resume", f.suspendCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
	}
	if f.caps.HelmRelease {
		aa.Add(fluxKeyHelmUpgrade, ui.NewKeyActionWithOpts("Force/Reset", f.helmUpgradeCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
	}
}

func (f *FluxExtender) selections() []string {
	ss := f.GetTable().GetSelectedItems()
	if len(ss) == 0 || ss[0] == "" {
		return nil
	}

	return ss
}

func (f *FluxExtender) dial() (dynamic.Interface, error) {
	return f.App().Conn().DynDial()
}

func (f *FluxExtender) target(path string) dao.FluxTarget {
	ns, n := client.Namespaced(path)
	return dao.FluxTarget{GVR: f.GVR(), Namespace: ns, Name: n}
}

// get reads an object live: caches go stale right after a toggle.
func (f *FluxExtender) get(ctx context.Context, t dao.FluxTarget) (*unstructured.Unstructured, error) {
	d, err := f.dial()
	if err != nil {
		return nil, err
	}

	return d.Resource(t.GVR.GVR()).Namespace(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
}

func (f *FluxExtender) kind(n int) string {
	r := f.GVR().R()
	if n == 1 {
		return singularize(r)
	}

	return r
}

// reconcile requests a reconcile per selection, skipping suspended ones
// like the flux CLI. Stamp extra annotations with requestedAt.
func (f *FluxExtender) reconcile(sels []string, verb string, extra ...string) {
	f.App().Flash().Infof("%s requested for %d %s...", verb, len(sels), f.kind(len(sels)))
	f.run(sels, verb, func(ctx context.Context, d dynamic.Interface, path string) error {
		t := f.target(path)
		o, err := f.get(ctx, t)
		if err != nil {
			return err
		}
		if dao.FluxIsSuspended(o, f.caps) {
			return fmt.Errorf("%s is suspended: resume it first", t)
		}
		return dao.FluxPatch(ctx, d, t, dao.FluxReconcilePatch(dao.FluxTimestamp(time.Now()), extra...))
	})
}

func (f *FluxExtender) reconcileCmd(*tcell.EventKey) *tcell.EventKey {
	if sels := f.selections(); len(sels) > 0 {
		f.reconcile(sels, "Reconcile")
	}

	return nil
}

func (f *FluxExtender) reconcileWithSourceCmd(*tcell.EventKey) *tcell.EventKey {
	sels := f.selections()
	if len(sels) == 0 {
		return nil
	}
	verb := "Reconcile with source"
	f.App().Flash().Infof("%s requested for %d %s...", verb, len(sels), f.kind(len(sels)))
	f.run(sels, verb, func(ctx context.Context, d dynamic.Interface, path string) error {
		t := f.target(path)
		o, err := f.get(ctx, t)
		if err != nil {
			return err
		}
		if dao.FluxIsSuspended(o, f.caps) {
			return fmt.Errorf("%s is suspended: resume it first", t)
		}
		chain, err := dao.FluxSourceChain(ctx, t.GVR, o, dao.MetaAccess.GK2GVR, f.get)
		if err != nil {
			return err
		}
		// Sources first. Don't wait for readiness: controllers pick up artifacts.
		now := dao.FluxTimestamp(time.Now())
		for _, c := range chain {
			if err := dao.FluxPatch(ctx, d, c, dao.FluxReconcilePatch(now)); err != nil {
				return err
			}
		}
		return nil
	})

	return nil
}

func (f *FluxExtender) helmUpgradeCmd(*tcell.EventKey) *tcell.EventKey {
	sels := f.selections()
	if len(sels) == 0 {
		return nil
	}
	msg := fmt.Sprintf("Reconcile %s %s with:", f.kind(1), sels[0])
	if len(sels) > 1 {
		msg = fmt.Sprintf("Reconcile %d %s with:", len(sels), f.kind(len(sels)))
	}
	d := f.App().Styles.Dialog()
	dialog.ShowHelmUpgrade(&d, f.App().Content.Pages, msg, func(force, reset bool) {
		var (
			extra []string
			verb  []string
		)
		if force {
			extra, verb = append(extra, dao.FluxForceAtAnn), append(verb, "Force")
		}
		if reset {
			extra, verb = append(extra, dao.FluxResetAtAnn), append(verb, "Reset")
		}
		f.reconcile(sels, strings.Join(verb, "+")+" reconcile", extra...)
	})

	return nil
}

// suspendCmd resumes when all selections are suspended, else suspends all.
// Patch explicit values, never invert.
func (f *FluxExtender) suspendCmd(*tcell.EventKey) *tcell.EventKey {
	sels := f.selections()
	if len(sels) == 0 {
		return nil
	}
	go func() {
		defer internal.LogPanic("flux suspend state")
		ctx, cancel := context.WithTimeout(context.Background(), f.App().Conn().Config().CallTimeout())
		defer cancel()
		suspended := 0
		for _, s := range sels {
			o, err := f.get(ctx, f.target(s))
			if err != nil {
				f.App().QueueUpdateDraw(func() { f.App().Flash().Err(err) })
				return
			}
			if dao.FluxIsSuspended(o, f.caps) {
				suspended++
			}
		}
		f.App().QueueUpdateDraw(func() { f.confirmSuspend(sels, suspended) })
	}()

	return nil
}

func (f *FluxExtender) confirmSuspend(sels []string, suspended int) {
	suspend := suspended < len(sels)
	verb := "Resume"
	if suspend {
		verb = "Suspend"
	}
	msg := fmt.Sprintf("%s %s %s?", verb, f.kind(1), sels[0])
	if len(sels) > 1 {
		msg = fmt.Sprintf("%s %d %s?", verb, len(sels), f.kind(len(sels)))
		if suspend && suspended > 0 {
			msg = fmt.Sprintf("Suspend %d %s (%d already suspended)?", len(sels), f.kind(len(sels)), suspended)
		}
	}
	d := f.App().Styles.Dialog()
	dialog.ShowConfirm(&d, f.App().Content.Pages, "Confirm "+verb, msg, func() {
		f.run(sels, verb, func(ctx context.Context, d dynamic.Interface, path string) error {
			body := dao.FluxSuspendPatch(f.caps, suspend, dao.FluxTimestamp(time.Now()))
			return dao.FluxPatch(ctx, d, f.target(path), body)
		})
	}, func() {})
}

func (f *FluxExtender) jumpSourceCmd(*tcell.EventKey) *tcell.EventKey {
	sels := f.selections()
	if len(sels) == 0 {
		return nil
	}
	t := f.target(sels[0])
	go func() {
		defer internal.LogPanic("flux jump source")
		ctx, cancel := context.WithTimeout(context.Background(), f.App().Conn().Config().CallTimeout())
		defer cancel()
		o, err := f.get(ctx, t)
		var src dao.FluxTarget
		if err == nil {
			src, err = dao.FluxSource(t.GVR, o, dao.MetaAccess.GK2GVR)
		}
		f.App().QueueUpdateDraw(func() {
			if err != nil {
				f.App().Flash().Err(err)
				return
			}
			f.App().gotoResource(src.GVR.String(), src.FQN(), false, true)
		})
	}()

	return nil
}

// run applies fn to selections off the UI goroutine, then reports and refreshes.
func (f *FluxExtender) run(sels []string, verb string, fn func(context.Context, dynamic.Interface, string) error) {
	d, err := f.dial()
	if err != nil {
		f.App().Flash().Err(err)
		return
	}
	runAsync(context.Background(), f.App(), sels, verb,
		func(ctx context.Context, path string) error {
			return fn(ctx, d, path)
		},
		func(done []string, errs []error) {
			if len(errs) == 1 && len(sels) == 1 {
				f.App().Flash().Errf("%s failed: %s", verb, errs[0])
			} else if len(errs) == 0 {
				f.App().Flash().Infof("%s requested for %d/%d %s", verb, len(done), len(sels), f.kind(len(sels)))
			}
			f.Refresh()
		},
	)
}
