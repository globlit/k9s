// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/render/helm"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/testutil/helmfake"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func useHelmFake(t *testing.T) (*helmfake.Helm, Factory) {
	t.Helper()
	h := helmfake.New(t, "")
	var mx sync.Mutex
	old := helmConfigFor
	helmConfigFor = func(_ *genericclioptions.ConfigFlags, ns string) (*action.Configuration, error) {
		mx.Lock()
		defer mx.Unlock()
		h.Mem.SetNamespace(ns)
		return h.Cfg, nil
	}
	t.Cleanup(func() { helmConfigFor = old })

	fc := fakecluster.New(t)
	f := watch.NewFactory(fc.Conn())
	t.Cleanup(f.Terminate)

	return h, f
}

func releaseNames(t *testing.T, a Accessor, ctx context.Context, ns string) []string {
	t.Helper()
	oo, err := a.List(ctx, ns)
	require.NoError(t, err)
	nn := make([]string, 0, len(oo))
	for _, o := range oo {
		r, ok := o.(helm.ReleaseRes)
		require.True(t, ok)
		nn = append(nn, fmt.Sprintf("%s/%s:%d", r.Release.Namespace, r.Release.Name, r.Release.Version))
	}

	return nn
}

func TestHelmChartListGet(t *testing.T) {
	h, f := useHelmFake(t)
	h.Seed(t, "r1", "ns1", 2)
	h.Seed(t, "r2", "ns2", 1)

	var hc HelmChart
	hc.Init(f, client.HmGVR)
	ctx := context.Background()

	assert.ElementsMatch(t, []string{"ns1/r1:2", "ns2/r2:1"}, releaseNames(t, &hc, ctx, client.BlankNamespace))
	assert.Equal(t, []string{"ns1/r1:2"}, releaseNames(t, &hc, ctx, "ns1"))
	assert.Empty(t, releaseNames(t, &hc, ctx, "ns3"))

	o, err := hc.Get(ctx, "ns2/r2")
	require.NoError(t, err)
	assert.Equal(t, "r2", o.(helm.ReleaseRes).Release.Name)
	_, err = hc.Get(ctx, "ns2/zorg")
	require.Error(t, err)

	notes, err := hc.Describe("ns1/r1")
	require.NoError(t, err)
	assert.Equal(t, "Some mock release notes!", notes)
	_, err = hc.Describe("ns1/zorg")
	require.Error(t, err)

	man, err := hc.ToYAML("ns1/r1", false)
	require.NoError(t, err)
	assert.Contains(t, man, "kind: Secret")
	_, err = hc.ToYAML("ns1/zorg", false)
	require.Error(t, err)
}

func TestHelmChartValues(t *testing.T) {
	h, f := useHelmFake(t)
	h.Seed(t, "r1", "ns1", 1)

	var hc HelmChart
	hc.Init(f, client.HmGVR)

	vals, err := hc.GetValues("ns1/r1", false)
	require.NoError(t, err)
	assert.Equal(t, "name: value\n", string(vals))

	vals, err = hc.GetValues("ns1/r1", true)
	require.NoError(t, err)
	assert.Equal(t, "name: value\nreplicas: 1\n", string(vals))

	_, err = hc.GetValues("ns1/zorg", true)
	require.Error(t, err)
}

func TestHelmHistoryListGet(t *testing.T) {
	h, f := useHelmFake(t)
	h.Seed(t, "r1", "ns1", 3)

	var hh HelmHistory
	hh.Init(f, client.HmhGVR)

	_, err := hh.List(context.Background(), "ns1")
	require.Error(t, err)

	ctx := context.WithValue(context.Background(), internal.KeyFQN, "ns1/r1")
	assert.Equal(t, []string{"ns1/r1:1", "ns1/r1:2", "ns1/r1:3"}, releaseNames(t, &hh, ctx, "ns1"))

	o, err := hh.Get(ctx, "ns1/r1:2")
	require.NoError(t, err)
	assert.Equal(t, 2, o.(helm.ReleaseRes).Release.Version)

	for _, p := range []string{"ns1/r1", "ns1/r1:", "ns1/r1:x", "ns1/r1:9"} {
		_, err = hh.Get(ctx, p)
		require.Error(t, err, p)
	}

	vals, err := hh.GetValues("ns1/r1:1", true)
	require.NoError(t, err)
	assert.Equal(t, "replicas: 1\n", string(vals))
	vals, err = hh.GetValues("ns1/r1:1", false)
	require.NoError(t, err)
	assert.Equal(t, "name: value\n", string(vals))
	_, err = hh.GetValues("ns1/r1:9", false)
	require.Error(t, err)

	notes, err := hh.Describe("ns1/r1:3")
	require.NoError(t, err)
	assert.Equal(t, "Some mock release notes!", notes)
	man, err := hh.ToYAML("ns1/r1:3", false)
	require.NoError(t, err)
	assert.Contains(t, man, "kind: Secret")
}

func resetHelmCache(t *testing.T) {
	t.Helper()
	t.Setenv("HELM_DRIVER", "memory")
	helmCfgMx.Lock()
	saved := helmCfgCache
	helmCfgCache = make(map[string]*action.Configuration)
	helmCfgMx.Unlock()
	t.Cleanup(func() {
		helmCfgMx.Lock()
		helmCfgCache = saved
		helmCfgMx.Unlock()
	})
}

func helmCacheLen() int {
	helmCfgMx.Lock()
	defer helmCfgMx.Unlock()

	return len(helmCfgCache)
}

func testFlags(t *testing.T, ctx string) *genericclioptions.ConfigFlags {
	t.Helper()
	fc := fakecluster.New(t, fakecluster.WithContext(ctx))
	f := fc.Conn().Config().Flags()
	f.Context = &ctx

	return f
}

func TestHelmConfigCacheKeys(t *testing.T) {
	resetHelmCache(t)
	f1, f2 := testFlags(t, "ct1"), testFlags(t, "ct2")

	c1, err := ensureHelmConfig(f1, "ns1")
	require.NoError(t, err)
	again, err := ensureHelmConfig(f1, "ns1")
	require.NoError(t, err)
	assert.Same(t, c1, again)

	c2, err := ensureHelmConfig(f1, "ns2")
	require.NoError(t, err)
	assert.NotSame(t, c1, c2, "split the cache by namespace")

	c3, err := ensureHelmConfig(f2, "ns1")
	require.NoError(t, err)
	assert.NotSame(t, c1, c3, "split the cache by context")

	tok := "secret"
	f1.BearerToken = &tok
	c4, err := ensureHelmConfig(f1, "ns1")
	require.NoError(t, err)
	assert.NotSame(t, c1, c4, "split the cache by token")

	t.Setenv("HELM_DRIVER", "configmap")
	f1.BearerToken = nil
	c5, err := ensureHelmConfig(f1, "ns1")
	require.NoError(t, err)
	assert.NotSame(t, c1, c5, "split the cache by driver")
	assert.Equal(t, 5, helmCacheLen())
}

func TestHelmConfigCacheEviction(t *testing.T) {
	resetHelmCache(t)
	f := testFlags(t, "ct1")

	first, err := ensureHelmConfig(f, "ns0")
	require.NoError(t, err)
	for i := 1; i < maxHelmCfgCache; i++ {
		_, err := ensureHelmConfig(f, fmt.Sprintf("ns%d", i))
		require.NoError(t, err)
	}
	assert.Equal(t, maxHelmCfgCache, helmCacheLen())
	again, err := ensureHelmConfig(f, "ns0")
	require.NoError(t, err)
	assert.Same(t, first, again)

	_, err = ensureHelmConfig(f, "overflow")
	require.NoError(t, err)
	assert.Equal(t, 1, helmCacheLen(), "clear the cache when full")

	again, err = ensureHelmConfig(f, "ns0")
	require.NoError(t, err)
	assert.NotSame(t, first, again)
}

func TestHelmConfigCacheConcurrent(t *testing.T) {
	resetHelmCache(t)
	ff := []*genericclioptions.ConfigFlags{testFlags(t, "ct1"), testFlags(t, "ct2")}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 40 {
				cfg, err := ensureHelmConfig(ff[(g+i)%2], fmt.Sprintf("ns%d", i%20))
				assert.NoError(t, err)
				assert.NotNil(t, cfg)
			}
		})
	}
	wg.Wait()
	assert.LessOrEqual(t, helmCacheLen(), maxHelmCfgCache)
}
