// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view_test

import (
	"context"
	"testing"

	"github.com/derailed/k9s/internal"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config/mock"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/view"
	"github.com/derailed/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var fluxKeys = map[tcell.Key]string{
	ui.KeyShiftR: "Reconcile",
	ui.KeyShiftW: "Reconcile w/ Source",
	ui.KeyShiftT: "Suspend/Resume",
	ui.KeyShiftF: "Force/Reset",
	ui.KeyShiftZ: "Jump Source",
}

func fluxView(t *testing.T, gvr string, readOnly bool) *ui.KeyActions {
	t.Helper()
	g := client.NewGVR(gvr)
	dao.MetaAccess.RegisterMeta(gvr, &metav1.APIResource{
		Name: g.R(), Group: g.G(), Version: g.V(), Namespaced: true, Kind: "Fred",
		// k9s category: Browser.Init then skips the (absent) API server checks.
		Categories: []string{"k9s"},
	})
	cfg := mock.NewMockConfig(t)
	cfg.K9s.ReadOnly = readOnly
	ctx := context.WithValue(context.Background(), internal.KeyApp, view.NewApp(cfg))

	v := view.NewFluxExtender(view.NewBrowser(g))
	require.NoError(t, v.Init(ctx))

	return v.GetTable().Actions()
}

func boundFluxKeys(aa *ui.KeyActions) map[tcell.Key]string {
	got := make(map[tcell.Key]string)
	for k := range fluxKeys {
		if a, ok := aa.Get(k); ok {
			got[k] = a.Description
		}
	}
	return got
}

func TestFluxExtenderBindings(t *testing.T) {
	uu := map[string]struct {
		gvr      string
		readOnly bool
		e        []tcell.Key
	}{
		"helmrelease": {
			gvr: "helm.toolkit.fluxcd.io/v2/helmreleases",
			e:   []tcell.Key{ui.KeyShiftR, ui.KeyShiftW, ui.KeyShiftT, ui.KeyShiftF, ui.KeyShiftZ},
		},
		"kustomization": {
			gvr: "kustomize.toolkit.fluxcd.io/v1/kustomizations",
			e:   []tcell.Key{ui.KeyShiftR, ui.KeyShiftW, ui.KeyShiftT, ui.KeyShiftZ},
		},
		"gitrepo": {
			gvr: "source.toolkit.fluxcd.io/v1/gitrepositories",
			e:   []tcell.Key{ui.KeyShiftR, ui.KeyShiftT},
		},
		"alert": {
			gvr: "notification.toolkit.fluxcd.io/v1beta3/alerts",
			e:   []tcell.Key{ui.KeyShiftT},
		},
		"fluxinstance": {
			gvr: "fluxcd.controlplane.io/v1/fluxinstances",
			e:   []tcell.Key{ui.KeyShiftR, ui.KeyShiftT},
		},
		"read-only": {
			gvr:      "helm.toolkit.fluxcd.io/v2/helmreleases",
			readOnly: true,
			e:        []tcell.Key{ui.KeyShiftZ},
		},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			got := boundFluxKeys(fluxView(t, u.gvr, u.readOnly))
			e := make(map[tcell.Key]string, len(u.e))
			for _, key := range u.e {
				e[key] = fluxKeys[key]
			}
			assert.Equal(t, e, got)
		})
	}
}

// Flux keys must not shadow existing table or browser actions (e.g. Shift-G
// is "Goto Bottom" in tview tables).
func TestFluxKeysDontOverlapBrowser(t *testing.T) {
	g := "fred.example.io/v1/blees"
	gvr := client.NewGVR(g)
	dao.MetaAccess.RegisterMeta(g, &metav1.APIResource{Name: gvr.R(), Group: gvr.G(), Version: gvr.V(), Namespaced: true, Categories: []string{"k9s"}})
	v := view.NewScaleExtender(view.NewOwnerExtender(view.NewBrowser(gvr)))
	require.NoError(t, v.Init(makeCtx(t)))

	aa := v.GetTable().Actions()
	for k, d := range fluxKeys {
		_, ok := aa.Get(k)
		assert.False(t, ok, "flux key for %q collides with an existing binding", d)
	}
	assert.NotContains(t, fluxKeys, ui.KeyShiftG, "Shift-G is tview's Goto Bottom")
}
