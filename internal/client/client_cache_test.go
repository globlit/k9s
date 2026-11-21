// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package client

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type sarRecorder struct {
	calls atomic.Int32
	attrs []authorizationv1.ResourceAttributes
	allow func(*authorizationv1.ResourceAttributes) (bool, error)
}

func (r *sarRecorder) react(a k8stesting.Action) (bool, runtime.Object, error) {
	r.calls.Add(1)
	sar := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
	attr := *sar.Spec.ResourceAttributes
	r.attrs = append(r.attrs, attr)
	ok, err := r.allow(&attr)
	if err != nil {
		return true, nil, err
	}
	sar.Status.Allowed = ok

	return true, sar, nil
}

func newCanIClient(t *testing.T, allow func(*authorizationv1.ResourceAttributes) (bool, error), oo ...runtime.Object) (*APIClient, *sarRecorder, *fake.Clientset) {
	t.Helper()
	rec := sarRecorder{allow: allow}
	cs := fake.NewClientset(oo...)
	cs.PrependReactor("create", "selfsubjectaccessreviews", rec.react)
	_, _, a := setupSwitchTest(t)
	a.client = cs

	return a, &rec, cs
}

func allowAll(*authorizationv1.ResourceAttributes) (bool, error) { return true, nil }

func TestCanICachesAllowed(t *testing.T) {
	a, rec, _ := newCanIClient(t, allowAll)

	for range 3 {
		ok, err := a.CanI("ns1", PodGVR, "", ReadAllAccess)
		require.NoError(t, err)
		assert.True(t, ok)
	}
	assert.Equal(t, int32(len(ReadAllAccess)), rec.calls.Load(), "ask once per verb, then use the cache")

	ok, err := a.CanI("ns2", PodGVR, "", ReadAllAccess)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int32(2*len(ReadAllAccess)), rec.calls.Load(), "split the cache by namespace")
}

func TestCanIDenied(t *testing.T) {
	a, rec, _ := newCanIClient(t, func(r *authorizationv1.ResourceAttributes) (bool, error) {
		return r.Verb != DeleteVerb, nil
	})

	ok, err := a.CanI("ns1", PodGVR, "p1", []string{GetVerb, DeleteVerb, ListVerb})
	require.ErrorContains(t, err, "access denied")
	assert.False(t, ok)
	assert.Equal(t, int32(2), rec.calls.Load(), "stop at the first denied verb")

	ok, err = a.CanI("ns1", PodGVR, "p1", []string{GetVerb, DeleteVerb, ListVerb})
	require.NoError(t, err, "cached denials return no error")
	assert.False(t, ok)
	assert.Equal(t, int32(2), rec.calls.Load())
}

func TestCanIAPIError(t *testing.T) {
	boom := errors.New("boom")
	a, rec, _ := newCanIClient(t, func(*authorizationv1.ResourceAttributes) (bool, error) {
		return false, boom
	})

	ok, err := a.CanI("ns1", PodGVR, "", GetAccess)
	require.ErrorIs(t, err, boom)
	assert.False(t, ok)
	ok, err = a.CanI("ns1", PodGVR, "", GetAccess)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, int32(1), rec.calls.Load())
}

func TestCanIAttributes(t *testing.T) {
	a, rec, _ := newCanIClient(t, allowAll)

	_, err := a.CanI(ClusterScope, NsGVR, "ns1", GetAccess)
	require.NoError(t, err)
	_, err = a.CanI("ns1", HmGVR, "", ListAccess)
	require.NoError(t, err)
	_, err = a.CanI(NamespaceAll, DpGVR, "", ListAccess)
	require.NoError(t, err)

	require.Len(t, rec.attrs, 3)
	assert.Equal(t, "ns1", rec.attrs[0].Namespace, "use the namespace name as the namespace")
	assert.Equal(t, "namespaces", rec.attrs[0].Resource)
	assert.Equal(t, "secrets", rec.attrs[1].Resource, "check helm access on secrets")
	assert.Empty(t, rec.attrs[2].Namespace)
	assert.Equal(t, "apps", rec.attrs[2].Group)
}

func TestCanINoConnection(t *testing.T) {
	a, rec, _ := newCanIClient(t, allowAll)
	a.setConnOK(false)

	ok, err := a.CanI("ns1", PodGVR, "", GetAccess)
	require.Error(t, err)
	assert.False(t, ok)
	assert.Zero(t, rec.calls.Load())
}

func TestValidNamespaceNamesCached(t *testing.T) {
	a, _, cs := newCanIClient(t, allowAll,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns2"}},
	)

	for range 2 {
		nn, err := a.ValidNamespaceNames()
		require.NoError(t, err)
		assert.Equal(t, NamespaceNames{"ns1": {}, "ns2": {}}, nn)
	}
	var lists int
	for _, act := range cs.Actions() {
		if act.GetVerb() == "list" && act.GetResource().Resource == "namespaces" {
			lists++
		}
	}
	assert.Equal(t, 1, lists)

	assert.True(t, a.IsValidNamespace("ns1"))
	assert.False(t, a.IsValidNamespace("zorg"))
}

func TestValidNamespaceNamesDenied(t *testing.T) {
	a, _, _ := newCanIClient(t, func(*authorizationv1.ResourceAttributes) (bool, error) {
		return false, nil
	}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}})

	_, err := a.ValidNamespaceNames()
	require.Error(t, err)
	assert.False(t, a.IsValidNamespace("ns1"))

	nn, err := a.ValidNamespaceNames()
	require.NoError(t, err, "cache the denial as an empty set")
	assert.Empty(t, nn)

	var nilClient *APIClient
	_, err = nilClient.ValidNamespaceNames()
	require.Error(t, err)
}

func TestValidNamespaceNamesListError(t *testing.T) {
	a, _, cs := newCanIClient(t, allowAll)
	cs.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})

	_, err := a.ValidNamespaceNames()
	require.ErrorContains(t, err, "boom")
	assert.False(t, a.IsValidNamespace("ns1"))
}

func TestServerVersionCached(t *testing.T) {
	_, calls, a := setupSwitchTest(t)

	for range 3 {
		v, err := a.ServerVersion()
		require.NoError(t, err)
		assert.Equal(t, "v1.28.0", v.GitVersion)
	}
	assert.Equal(t, int32(1), calls.Load())

	a.clearCache()
	_, err := a.ServerVersion()
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())

	a.setConnOK(false)
	a.cache = cache.NewLRUExpireCache(cacheSize)
	_, err = a.ServerVersion()
	require.Error(t, err)
}

func TestAPIClientActiveContext(t *testing.T) {
	_, _, a := setupSwitchTest(t)

	assert.Equal(t, testContext1, a.ActiveContext())
	assert.True(t, a.HasActiveContext())
	assert.Equal(t, "default", a.ActiveNamespace())
	assert.True(t, a.IsActiveNamespace("default"))
	assert.False(t, a.IsActiveNamespace("kube-system"))
	assert.Same(t, a.config, a.Config())

	require.NoError(t, a.SwitchContext(testContext2))
	assert.Equal(t, "kube-system", a.ActiveNamespace())

	empty := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &empty
	b := &APIClient{config: NewConfig(flags), cache: cache.NewLRUExpireCache(cacheSize), log: slog.Default()}
	assert.Empty(t, b.ActiveContext())
	assert.False(t, b.HasActiveContext())
}

func TestAPIClientDialers(t *testing.T) {
	_, _, a := setupSwitchTest(t)

	l1, err := a.DialLogs()
	require.NoError(t, err)
	l2, err := a.DialLogs()
	require.NoError(t, err)
	assert.Same(t, l1, l2)

	m1, err := a.MXDial()
	require.NoError(t, err)
	m2, err := a.MXDial()
	require.NoError(t, err)
	assert.Same(t, m1, m2)

	a.setConnOK(false)
	_, err = a.DialLogs()
	require.Error(t, err)
	_, err = a.Dial()
	require.Error(t, err)
	_, err = a.CachedDiscovery()
	require.Error(t, err)
}

func TestAPIClientHasMetrics(t *testing.T) {
	_, _, a := setupSwitchTest(t)
	assert.False(t, a.HasMetrics(), "no metrics group on the server")

	a.cache.Add(cacheMXAPIKey, true, cacheExpiry)
	assert.True(t, a.HasMetrics())
}

func TestCheckMetricsVersion(t *testing.T) {
	assert.True(t, checkMetricsVersion(&metav1.APIGroup{Versions: []metav1.GroupVersionForDiscovery{{Version: "v1beta1"}}}))
	assert.False(t, checkMetricsVersion(&metav1.APIGroup{Versions: []metav1.GroupVersionForDiscovery{{Version: "v2"}}}))
	assert.False(t, checkMetricsVersion(&metav1.APIGroup{}))
}

func TestPingTimeout(t *testing.T) {
	_, _, a := setupSwitchTest(t)
	assert.Equal(t, "10s", a.pingTimeout().String(), "cap the default timeout")

	to := "2s"
	a.config.flags.Timeout = &to
	assert.Equal(t, "2s", a.pingTimeout().String())

	to = "1m"
	assert.Equal(t, "10s", a.pingTimeout().String())
}
