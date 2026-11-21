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
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func useHelmFake(t *testing.T, h *helmfake.Helm) {
	t.Helper()
	old := helmConfigFor
	helmConfigFor = func(*genericclioptions.ConfigFlags, string) (*action.Configuration, error) {
		return h.Cfg, nil
	}
	t.Cleanup(func() { helmConfigFor = old })
}

func TestHelmHistoryMetaHasNoDelete(t *testing.T) {
	m := make(ResourceMetas)
	loadHelm(m)

	assert.False(t, client.Can(m[client.HmhGVR].Verbs, verbDelete))
	assert.True(t, client.Can(m[client.HmGVR].Verbs, verbDelete))
}

func TestHelmHistoryDeleteKeepsRelease(t *testing.T) {
	uu := map[string]string{
		"revision": "ns1/r1:2",
		"release":  "ns1/r1",
	}

	for k, path := range uu {
		t.Run(k, func(t *testing.T) {
			h := helmfake.New(t, "ns1")
			h.Seed(t, "r1", "ns1", 3)
			useHelmFake(t, h)
			var hh HelmHistory
			hh.Init(newFakeFactory(t, fakecluster.New(t)), client.HmhGVR)

			err := hh.Delete(context.Background(), path, nil, DefaultGrace)
			require.ErrorContains(t, err, "not supported")
			rr, err := h.Cfg.Releases.History("r1")
			require.NoError(t, err)
			assert.Len(t, rr, 3)
		})
	}
}
