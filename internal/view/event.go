// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
)

const faultsFilter = "Warning|Error"

// Event represents a command alias view.
type Event struct {
	ResourceViewer
}

// NewEvent returns a new alias view.
func NewEvent(gvr *client.GVR) ResourceViewer {
	e := Event{
		ResourceViewer: NewBrowser(gvr),
	}
	e.AddBindKeysFn(e.bindKeys)
	e.GetTable().SetSortCol("LAST SEEN", false)

	return &e
}

func (e *Event) bindKeys(aa *ui.KeyActions) {
	aa.Delete(tcell.KeyCtrlD, ui.KeyE, ui.KeyA)
	aa.Bulk(ui.KeyMap{
		tcell.KeyCtrlZ: ui.NewKeyAction("Toggle Faults", e.toggleFaults, false),
	})
}

func (e *Event) toggleFaults(*tcell.EventKey) *tcell.EventKey {
	b, ok := e.ResourceViewer.(*Browser)
	if !ok {
		return nil
	}
	filter := b.CmdBuff().GetText()
	if filter == faultsFilter {
		e.SetFilter("", true)
		e.App().Flash().Info("Showing all events")
	} else {
		e.SetFilter(faultsFilter, true)
		e.App().Flash().Info("Showing Warning and Error events only")
	}
	return nil
}

// eventsCmd opens events for this exact object, including its UID across recreations.
func (b *Browser) eventsCmd(evt *tcell.EventKey) *tcell.EventKey {
	path := b.GetSelectedItem()
	if path == "" {
		return evt
	}

	top := b.app.Content.Top()
	activeContext := b.app.Config.K9s.ActiveContextName()
	var obj map[string]any
	runAsync(b.defaultContext(), b.app, []string{path}, "Read event source", func(ctx context.Context, path string) error {
		var err error
		obj, err = fetchResourceObject(ctx, b.app, b.GVR(), path)
		return err
	}, func(_ []string, errs []error) {
		if b.app.Content.Top() != top || b.app.Config.K9s.ActiveContextName() != activeContext {
			return
		}
		if len(errs) > 0 {
			b.app.Flash().Err(errs[0])
			return
		}
		v, ns, err := resourceEvents(path, obj)
		if err == nil {
			err = b.app.Config.SetActiveNamespace(ns)
		}
		if err == nil {
			err = b.app.inject(v, false)
		}
		if err != nil {
			b.app.Flash().Err(err)
		}
	})
	return nil
}

func resourceEvents(path string, obj map[string]any) (ResourceViewer, string, error) {
	u := &unstructured.Unstructured{Object: obj}
	if u.GetUID() == "" {
		return nil, "", fmt.Errorf("resource %s has no UID", path)
	}
	ns := u.GetNamespace()
	if ns == "" {
		ns = client.NamespaceAll
	}
	v := NewEvent(client.EvGVR)
	v.SetContextFn(chainContext(func(ctx context.Context) context.Context {
		return context.WithValue(ctx, internal.KeyPath, path)
	}, fields.OneTermEqualSelector("involvedObject.uid", string(u.GetUID())).String()))
	return v, ns, nil
}
