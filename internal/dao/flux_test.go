// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var (
	ksGVR  = client.NewGVR("kustomize.toolkit.fluxcd.io/v1/kustomizations")
	hrGVR  = client.NewGVR("helm.toolkit.fluxcd.io/v2/helmreleases")
	gitGVR = client.NewGVR("source.toolkit.fluxcd.io/v1/gitrepositories")
	ociGVR = client.NewGVR("source.toolkit.fluxcd.io/v1/ocirepositories")
	hcGVR  = client.NewGVR("source.toolkit.fluxcd.io/v1/helmcharts")
	hrpGVR = client.NewGVR("source.toolkit.fluxcd.io/v1/helmrepositories")
	fiGVR  = client.NewGVR("fluxcd.controlplane.io/v1/fluxinstances")
)

func testResolver(group, kind string) (*client.GVR, bool) {
	m := map[string]*client.GVR{
		"source.toolkit.fluxcd.io/GitRepository":  gitGVR,
		"source.toolkit.fluxcd.io/OCIRepository":  ociGVR,
		"source.toolkit.fluxcd.io/HelmChart":      hcGVR,
		"source.toolkit.fluxcd.io/HelmRepository": hrpGVR,
	}
	gvr, ok := m[group+"/"+kind]
	return gvr, ok
}

func noGet(context.Context, FluxTarget) (*unstructured.Unstructured, error) {
	return nil, errors.New("unexpected get")
}

func obj(ns, n string, spec map[string]any) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	o.SetNamespace(ns)
	o.SetName(n)
	return o
}

func decode(t *testing.T, bb []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(bb, &m))
	return m
}

func TestFluxCaps(t *testing.T) {
	c, ok := FluxCapsFor(client.NewGVR("helm.toolkit.fluxcd.io/v2beta1/helmreleases"))
	require.True(t, ok, "any API version must match")
	assert.True(t, c.HelmRelease && c.Source && c.Suspend && c.Reconcile)

	c, ok = FluxCapsFor(client.NewGVR("notification.toolkit.fluxcd.io/v1beta3/alerts"))
	require.True(t, ok)
	assert.True(t, c.Suspend)
	assert.False(t, c.Reconcile, "alerts have no reconciler")

	c, ok = FluxCapsFor(fiGVR)
	require.True(t, ok)
	assert.True(t, c.Operator)

	assert.False(t, IsFluxGVR(client.PodGVR))
	assert.False(t, IsFluxGVR(client.NewGVR("fred.toolkit.fluxcd.io/v1/blees")))
}

func TestFluxIsSuspended(t *testing.T) {
	assert.False(t, FluxIsSuspended(obj("ns", "a", map[string]any{}), FluxCaps{}), "missing suspend is active")
	assert.True(t, FluxIsSuspended(obj("ns", "a", map[string]any{"suspend": true}), FluxCaps{}))

	o := obj("ns", "a", map[string]any{"suspend": true})
	assert.False(t, FluxIsSuspended(o, FluxCaps{Operator: true}), "operator ignores spec.suspend")
	o.SetAnnotations(map[string]string{"fluxcd.controlplane.io/reconcile": "disabled"})
	assert.True(t, FluxIsSuspended(o, FluxCaps{Operator: true}))
}

func TestFluxSuspendPatch(t *testing.T) {
	const now = "2026-11-21T10:00:00.123456789Z"
	uu := map[string]struct {
		caps    FluxCaps
		suspend bool
		e       string
	}{
		"suspend": {
			suspend: true,
			e:       `{"spec":{"suspend":true}}`,
		},
		"resume": {
			e: `{"metadata":{"annotations":{"reconcile.fluxcd.io/requestedAt":"` + now + `"}},"spec":{"suspend":false}}`,
		},
		"operator-suspend": {
			caps:    FluxCaps{Operator: true},
			suspend: true,
			e:       `{"metadata":{"annotations":{"fluxcd.controlplane.io/reconcile":"disabled"}}}`,
		},
		"operator-resume": {
			caps: FluxCaps{Operator: true},
			e:    `{"metadata":{"annotations":{"fluxcd.controlplane.io/reconcile":"enabled","reconcile.fluxcd.io/requestedAt":"` + now + `"}}}`,
		},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.JSONEq(t, u.e, string(FluxSuspendPatch(u.caps, u.suspend, now)))
		})
	}
}

func TestFluxReconcilePatch(t *testing.T) {
	const now = "2026-11-21T10:00:00Z"
	m := decode(t, FluxReconcilePatch(now, FluxForceAtAnn, FluxResetAtAnn))
	anns := m["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.Equal(t, map[string]any{
		FluxRequestedAtAnn: now,
		FluxForceAtAnn:     now,
		FluxResetAtAnn:     now,
	}, anns, "force/reset must equal requestedAt")
	assert.NotContains(t, m, "spec")
}

func TestFluxSourceChainKustomization(t *testing.T) {
	ks := obj("apps", "web", map[string]any{
		"sourceRef": map[string]any{"kind": "GitRepository", "name": "repo"},
	})
	chain, err := FluxSourceChain(context.Background(), ksGVR, ks, testResolver, noGet)
	require.NoError(t, err)
	assert.Equal(t, []FluxTarget{
		{GVR: gitGVR, Namespace: "apps", Name: "repo"},
		{GVR: ksGVR, Namespace: "apps", Name: "web"},
	}, chain)

	ks = obj("apps", "web", map[string]any{
		"sourceRef": map[string]any{"kind": "OCIRepository", "name": "repo", "namespace": "flux-system"},
	})
	src, err := FluxSource(ksGVR, ks, testResolver)
	require.NoError(t, err)
	assert.Equal(t, FluxTarget{GVR: ociGVR, Namespace: "flux-system", Name: "repo"}, src)
}

func TestFluxSourceChainHelmReleaseChartSpec(t *testing.T) {
	hr := obj("apps", "web", map[string]any{
		"chart": map[string]any{"spec": map[string]any{
			"chart":     "web",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts", "namespace": "flux-system"},
		}},
	})
	chain, err := FluxSourceChain(context.Background(), hrGVR, hr, testResolver, noGet)
	require.NoError(t, err)
	assert.Equal(t, []FluxTarget{
		{GVR: hrpGVR, Namespace: "flux-system", Name: "charts"},
		{GVR: hcGVR, Namespace: "flux-system", Name: "apps-web"},
		{GVR: hrGVR, Namespace: "apps", Name: "web"},
	}, chain)
}

func TestFluxSourceChainHelmReleaseChartRef(t *testing.T) {
	hr := obj("apps", "web", map[string]any{
		"chartRef": map[string]any{"kind": "OCIRepository", "name": "web-chart"},
	})
	chain, err := FluxSourceChain(context.Background(), hrGVR, hr, testResolver, noGet)
	require.NoError(t, err)
	assert.Equal(t, []FluxTarget{
		{GVR: ociGVR, Namespace: "apps", Name: "web-chart"},
		{GVR: hrGVR, Namespace: "apps", Name: "web"},
	}, chain)

	// chartRef to a HelmChart: follow its source.
	hr = obj("apps", "web", map[string]any{
		"chartRef": map[string]any{"kind": "HelmChart", "name": "shared", "namespace": "charts"},
	})
	get := func(_ context.Context, tg FluxTarget) (*unstructured.Unstructured, error) {
		assert.Equal(t, FluxTarget{GVR: hcGVR, Namespace: "charts", Name: "shared"}, tg)
		return obj("charts", "shared", map[string]any{
			"sourceRef": map[string]any{"kind": "GitRepository", "name": "mono"},
		}), nil
	}
	chain, err = FluxSourceChain(context.Background(), hrGVR, hr, testResolver, get)
	require.NoError(t, err)
	assert.Equal(t, []FluxTarget{
		{GVR: gitGVR, Namespace: "charts", Name: "mono"},
		{GVR: hcGVR, Namespace: "charts", Name: "shared"},
		{GVR: hrGVR, Namespace: "apps", Name: "web"},
	}, chain)
}

func TestFluxSourceErrors(t *testing.T) {
	_, err := FluxSource(ksGVR, obj("apps", "web", map[string]any{}), testResolver)
	require.Error(t, err)
	_, err = FluxSource(ksGVR, obj("apps", "web", map[string]any{
		"sourceRef": map[string]any{"kind": "Nope", "name": "x"},
	}), testResolver)
	require.Error(t, err)
	_, err = FluxSource(gitGVR, obj("apps", "web", map[string]any{}), testResolver)
	require.Error(t, err)
}

func TestFluxPatchUsesStrictMergePatch(t *testing.T) {
	ks := obj("apps", "web", map[string]any{})
	ks.SetAPIVersion("kustomize.toolkit.fluxcd.io/v1")
	ks.SetKind("Kustomization")
	dial := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{ksGVR.GVR(): "KustomizationList"},
		ks,
	)
	var got k8stesting.PatchActionImpl
	dial.PrependReactor("patch", "kustomizations", func(a k8stesting.Action) (bool, runtime.Object, error) {
		got = a.(k8stesting.PatchActionImpl)
		return false, nil, nil
	})

	body := FluxSuspendPatch(FluxCaps{}, true, "now")
	require.NoError(t, FluxPatch(context.Background(), dial, FluxTarget{GVR: ksGVR, Namespace: "apps", Name: "web"}, body))
	assert.Equal(t, "web", got.Name)
	assert.Equal(t, "apps", got.Namespace)
	assert.Equal(t, "application/merge-patch+json", string(got.PatchType))
	assert.JSONEq(t, string(body), string(got.Patch))

	o, err := dial.Resource(ksGVR.GVR()).Namespace("apps").Get(context.Background(), "web", metav1.GetOptions{})
	require.NoError(t, err)
	assert.True(t, FluxIsSuspended(o, FluxCaps{}))
}

func TestGK2GVR(t *testing.T) {
	m := NewMeta()
	m.resMetas[gitGVR] = &metav1.APIResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "GitRepository"}
	gvr, ok := m.GK2GVR("source.toolkit.fluxcd.io", "GitRepository")
	require.True(t, ok)
	assert.Equal(t, gitGVR, gvr)
	_, ok = m.GK2GVR("source.toolkit.fluxcd.io", "Bucket")
	assert.False(t, ok)
}
