// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

// Package helmfake provides a helm action config with in-memory storage.
package helmfake

import (
	"io"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
)

// Helm is a helm action config backed by memory.
type Helm struct {
	Cfg *action.Configuration
	Mem *driver.Memory
	ns  string
}

// New returns a helm config scoped to ns. Use "" for all namespaces.
func New(t testing.TB, ns string) *Helm {
	t.Helper()
	mem := driver.NewMemory()
	mem.SetNamespace(ns)

	return &Helm{
		Cfg: &action.Configuration{
			Releases:     storage.Init(mem),
			KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard},
			Capabilities: chartutil.DefaultCapabilities,
			Log:          func(string, ...any) {},
		},
		Mem: mem,
		ns:  ns,
	}
}

// Seed stores revisions 1 to n of a release. The last one is deployed, the
// others are superseded. It returns the stored releases.
func (h *Helm) Seed(t testing.TB, name, ns string, revisions int) []*release.Release {
	t.Helper()
	ch := &chart.Chart{
		Metadata: &chart.Metadata{Name: name, Version: "0.1.0", AppVersion: "1.0.0", APIVersion: chart.APIVersionV2},
		Values:   map[string]any{"replicas": 1},
	}
	rr := make([]*release.Release, 0, revisions)
	for v := 1; v <= revisions; v++ {
		st := release.StatusSuperseded
		if v == revisions {
			st = release.StatusDeployed
		}
		r := release.Mock(&release.MockReleaseOptions{
			Name: name, Namespace: ns, Version: v, Status: st, Chart: ch,
		})
		if err := h.Cfg.Releases.Create(r); err != nil {
			t.Fatalf("seed release %s.v%d: %v", name, v, err)
		}
		rr = append(rr, r)
	}
	// Create moves the driver to the release namespace.
	h.Mem.SetNamespace(h.ns)

	return rr
}
