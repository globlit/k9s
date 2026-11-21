// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package helmfake_test

import (
	"testing"

	"github.com/globlit/k9s/internal/testutil/helmfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

func TestSeedAndActions(t *testing.T) {
	h := helmfake.New(t, "ns1")
	h.Seed(t, "r1", "ns1", 3)
	h.Seed(t, "r2", "ns2", 1)

	l := action.NewList(h.Cfg)
	l.All = true
	rr, err := l.Run()
	require.NoError(t, err)
	require.Len(t, rr, 1)
	assert.Equal(t, "r1", rr[0].Name)
	assert.Equal(t, 3, rr[0].Version)

	hh, err := action.NewHistory(h.Cfg).Run("r1")
	require.NoError(t, err)
	assert.Len(t, hh, 3)

	rb := action.NewRollback(h.Cfg)
	rb.Version = 1
	require.NoError(t, rb.Run("r1"))
	last, err := h.Cfg.Releases.Last("r1")
	require.NoError(t, err)
	assert.Equal(t, 4, last.Version)
	assert.Equal(t, release.StatusDeployed, last.Info.Status)

	u := action.NewUninstall(h.Cfg)
	u.KeepHistory = true
	_, err = u.Run("r1")
	require.NoError(t, err)
	last, err = h.Cfg.Releases.Last("r1")
	require.NoError(t, err)
	assert.Equal(t, release.StatusUninstalled, last.Info.Status)

	_, err = action.NewUninstall(helmfake.New(t, "ns2").Cfg).Run("r2")
	require.Error(t, err)
}
