// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package helm_test

import (
	"context"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/k9s/internal/render/helm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	helmtime "helm.sh/helm/v3/pkg/time"
)

func newRelease(status release.Status, rev int) helm.ReleaseRes {
	return helm.ReleaseRes{Release: &release.Release{
		Name:      "r1",
		Namespace: "ns1",
		Version:   rev,
		Info: &release.Info{
			Status:       status,
			Description:  "Upgrade complete",
			LastDeployed: helmtime.Time{Time: time.Now().Add(-2 * time.Minute)},
		},
		Chart: &chart.Chart{Metadata: &chart.Metadata{Name: "fred", Version: "1.2.3", AppVersion: "4.5"}},
	}}
}

func TestChartHeader(t *testing.T) {
	var c helm.Chart
	h := c.Header("")
	assert.Equal(t, []string{"NAMESPACE", "NAME", "REVISION", "STATUS", "CHART", "APP VERSION", "VALID", "AGE"}, h.ColumnNames(true))
	assert.False(t, c.IsGeneric())
	assert.NotNil(t, c.ColorerFunc())
	c.SetViewSetting(nil)
}

func TestChartRender(t *testing.T) {
	uu := map[string]struct {
		status release.Status
		valid  string
	}{
		"deployed": {status: release.StatusDeployed},
		"failed":   {status: release.StatusFailed, valid: "chart is in an invalid state"},
		"pending":  {status: release.StatusPendingUpgrade, valid: "chart is in an invalid state"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			var (
				c helm.Chart
				r model1.Row
			)
			rel := newRelease(u.status, 3)
			require.NoError(t, c.Render(rel, "", &r))
			assert.Equal(t, "ns1/r1", r.ID)
			assert.Equal(t, model1.Fields{"ns1", "r1", "3", u.status.String(), "fred-1.2.3", "4.5", u.valid}, r.Fields[:7])
			assert.Equal(t, "2m", r.Fields[7])
			assert.Equal(t, rel.Release.Info.LastDeployed.Time, r.Age)
		})
	}
}

func TestChartRenderBadType(t *testing.T) {
	var (
		c helm.Chart
		r model1.Row
	)
	require.Error(t, c.Render("zorg", "", &r))
	rel := newRelease(release.StatusDeployed, 1)
	require.Error(t, c.Render(&rel, "", &r))
}

func TestChartHealthy(t *testing.T) {
	var c helm.Chart
	ok := newRelease(release.StatusDeployed, 1)
	bad := newRelease(release.StatusFailed, 1)
	require.NoError(t, c.Healthy(context.Background(), &ok))
	require.Error(t, c.Healthy(context.Background(), &bad))
}

func TestChartHealthyValue(t *testing.T) {
	var c helm.Chart
	assert.NotPanics(t, func() {
		require.NoError(t, c.Healthy(context.Background(), newRelease(release.StatusDeployed, 1)))
		require.Error(t, c.Healthy(context.Background(), newRelease(release.StatusFailed, 1)))
		require.Error(t, c.Healthy(context.Background(), helm.ReleaseRes{}))
		require.Error(t, c.Healthy(context.Background(), "zorg"))
	})
}

func TestHistoryRender(t *testing.T) {
	var (
		h helm.History
		r model1.Row
	)
	assert.Equal(t, []string{"REVISION", "STATUS", "CHART", "APP VERSION", "DESCRIPTION", "VALID"}, h.Header("").ColumnNames(true))
	assert.False(t, h.IsGeneric())
	assert.NotNil(t, h.ColorerFunc())
	h.SetViewSetting(nil)

	require.NoError(t, h.Render(newRelease(release.StatusSuperseded, 2), "", &r))
	assert.Equal(t, "ns1/r1:2", r.ID)
	assert.Equal(t, model1.Fields{"2", "superseded", "fred-1.2.3", "4.5", "Upgrade complete", ""}, r.Fields)
	require.NoError(t, h.Healthy(context.Background(), nil))
	require.Error(t, h.Render(42, "", &r))
}

func TestReleaseResObject(t *testing.T) {
	rel := newRelease(release.StatusDeployed, 1)
	assert.Nil(t, rel.GetObjectKind())
	assert.Equal(t, rel, rel.DeepCopyObject())
}
