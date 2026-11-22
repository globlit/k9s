// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/testutil/helmfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const keptManifest = `---
# Source: r1/templates/cm.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kept-cm
  annotations:
    helm.sh/resource-policy: keep
`

func helmChartOn(t *testing.T, h *helmfake.Helm) *HelmChart {
	t.Helper()
	useHelmCfg(t, h)
	var c HelmChart
	c.Init(mutFactory(t, fakecluster.New(t).Conn()), client.HmGVR)

	return &c
}

func useHelmError(t *testing.T) {
	t.Helper()
	old := helmConfigFor
	helmConfigFor = func(*genericclioptions.ConfigFlags, string) (*action.Configuration, error) {
		return nil, errMut
	}
	t.Cleanup(func() { helmConfigFor = old })
}

func TestHelmChartDelete(t *testing.T) {
	h := helmfake.New(t, "ns1")
	h.Seed(t, "r1", "ns1", 2)
	h.Seed(t, "r2", "ns1", 1)
	c := helmChartOn(t, h)

	require.NoError(t, c.Delete(context.Background(), "ns1/r1", new(metav1.DeletePropagationOrphan), ForceGrace))

	_, err := h.Cfg.Releases.History("r1")
	require.Error(t, err)
	rr, err := h.Cfg.Releases.History("r2")
	require.NoError(t, err)
	assert.Len(t, rr, 1)
}

func TestHelmChartUninstall(t *testing.T) {
	uu := map[string]struct {
		keep bool
		revs int
	}{
		"drop-history": {},
		"keep-history": {keep: true, revs: 3},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			h := helmfake.New(t, "ns1")
			h.Seed(t, "r1", "ns1", 3)
			c := helmChartOn(t, h)

			require.NoError(t, c.Uninstall("ns1/r1", u.keep))

			rr, err := h.Cfg.Releases.History("r1")
			if u.revs == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, rr, u.revs)
			last, err := h.Cfg.Releases.Last("r1")
			require.NoError(t, err)
			assert.Equal(t, release.StatusUninstalled, last.Info.Status)
		})
	}
}

func TestHelmChartUninstallErrors(t *testing.T) {
	t.Run("missing-release", func(t *testing.T) {
		c := helmChartOn(t, helmfake.New(t, "ns1"))

		require.ErrorContains(t, c.Uninstall("ns1/nope", false), "not found")
	})

	t.Run("already-uninstalled", func(t *testing.T) {
		h := helmfake.New(t, "ns1")
		h.Seed(t, "r1", "ns1", 1)
		c := helmChartOn(t, h)
		require.NoError(t, c.Uninstall("ns1/r1", true))

		require.ErrorContains(t, c.Uninstall("ns1/r1", true), "already deleted")
	})

	t.Run("config", func(t *testing.T) {
		var c HelmChart
		c.Init(mutFactory(t, fakecluster.New(t).Conn()), client.HmGVR)
		useHelmError(t)

		require.ErrorIs(t, c.Uninstall("ns1/r1", false), errMut)
		require.ErrorIs(t, c.Delete(context.Background(), "ns1/r1", nil, DefaultGrace), errMut)
	})

	// Pin: kept resources come back as an error, but the release is gone.
	t.Run("kept-resources", func(t *testing.T) {
		h := helmfake.New(t, "ns1")
		rr := h.Seed(t, "r1", "ns1", 1)
		rr[0].Manifest = keptManifest
		require.NoError(t, h.Cfg.Releases.Update(rr[0]))
		c := helmChartOn(t, h)

		err := c.Uninstall("ns1/r1", false)

		require.ErrorContains(t, err, "kept due to the resource policy")
		assert.ErrorContains(t, err, "[ConfigMap] kept-cm")
		_, err = h.Cfg.Releases.History("r1")
		require.Error(t, err)
	})
}

func TestHelmHistoryRollback(t *testing.T) {
	h := helmfake.New(t, "ns1")
	h.Seed(t, "r1", "ns1", 3)
	useHelmCfg(t, h)
	var hh HelmHistory
	hh.Init(mutFactory(t, fakecluster.New(t).Conn()), client.HmhGVR)

	require.NoError(t, hh.Rollback(context.Background(), "ns1/r1", "1"))

	last, err := h.Cfg.Releases.Last("r1")
	require.NoError(t, err)
	assert.Equal(t, 4, last.Version)
	assert.Equal(t, release.StatusDeployed, last.Info.Status)
	assert.Contains(t, last.Info.Description, "Rollback to 1")
	prev, err := h.Cfg.Releases.Get("r1", 3)
	require.NoError(t, err)
	assert.Equal(t, release.StatusSuperseded, prev.Info.Status)
}

func TestHelmHistoryRollbackErrors(t *testing.T) {
	uu := map[string]struct {
		path, rev string
		cfgErr    bool
		err       string
	}{
		"bad-revision":     {path: "ns1/r1", rev: "x", err: "could not convert revision"},
		"missing-revision": {path: "ns1/r1", rev: "9", err: "has no 9 version"},
		"missing-release":  {path: "ns1/nope", rev: "1", err: "not found"},
		"config":           {path: "ns1/r1", rev: "1", cfgErr: true, err: "boom"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			h := helmfake.New(t, "ns1")
			h.Seed(t, "r1", "ns1", 2)
			useHelmCfg(t, h)
			if u.cfgErr {
				useHelmError(t)
			}
			var hh HelmHistory
			hh.Init(mutFactory(t, fakecluster.New(t).Conn()), client.HmhGVR)

			require.ErrorContains(t, hh.Rollback(context.Background(), u.path, u.rev), u.err)
			rr, err := h.Cfg.Releases.History("r1")
			require.NoError(t, err)
			assert.Len(t, rr, 2)
		})
	}
}
