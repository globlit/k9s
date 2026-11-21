// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/derailed/k9s/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

type dynConn struct {
	client.Connection
	dial dynamic.Interface
}

func (c dynConn) DynDial() (dynamic.Interface, error)                    { return c.dial, nil }
func (dynConn) CanI(string, *client.GVR, string, []string) (bool, error) { return true, nil }

func TestFactoryGetColdCache(t *testing.T) {
	rs := &unstructured.Unstructured{}
	rs.SetAPIVersion("apps/v1")
	rs.SetKind("ReplicaSet")
	rs.SetNamespace("ns1")
	rs.SetName("rs1")

	dial := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{client.RsGVR.GVR(): "ReplicaSetList"},
		rs,
	)
	// Informer LIST never succeeds, so the cache stays cold.
	dial.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("slow list")
	})

	f := NewFactory(dynConn{dial: dial})
	f.Start("ns1")
	defer f.Terminate()

	o, err := f.Get(client.RsGVR, "ns1/rs1", true, nil)
	require.NoError(t, err)
	assert.Equal(t, "rs1", o.(*unstructured.Unstructured).GetName())
}

func newRSDial(rr ...*unstructured.Unstructured) *fake.FakeDynamicClient {
	oo := make([]runtime.Object, 0, len(rr))
	for _, r := range rr {
		oo = append(oo, r)
	}
	return fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{client.RsGVR.GVR(): "ReplicaSetList"},
		oo...,
	)
}

func newRS(ns, n string) *unstructured.Unstructured {
	rs := &unstructured.Unstructured{}
	rs.SetAPIVersion("apps/v1")
	rs.SetKind("ReplicaSet")
	rs.SetNamespace(ns)
	rs.SetName(n)
	rs.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl"}})

	return rs
}

func TestFactoryEvictIdle(t *testing.T) {
	f := NewFactory(dynConn{dial: newRSDial()})
	f.Start("ns1")
	defer f.Terminate()
	require.NoError(t, f.SetActiveNS("ns1"))

	_, err := f.List(client.RsGVR, "ns1", false, labels.Everything())
	require.NoError(t, err)
	_, err = f.List(client.RsGVR, "ns2", false, labels.Everything())
	require.NoError(t, err)
	assert.NotNil(t, f.FactoryFor("ns1"))
	assert.NotNil(t, f.FactoryFor("ns2"))

	f.evictIdle(time.Now().Add(2*factoryIdleTTL), factoryIdleTTL)
	assert.NotNil(t, f.FactoryFor("ns1"), "active namespace must be kept")
	assert.Nil(t, f.FactoryFor("ns2"), "idle namespace must be stopped")

	// A later access transparently restarts the informers.
	_, err = f.List(client.RsGVR, "ns2", false, labels.Everything())
	require.NoError(t, err)
	assert.NotNil(t, f.FactoryFor("ns2"))
}

func TestFactoryNamespacedReadsUseClusterWideCache(t *testing.T) {
	f := NewFactory(dynConn{dial: newRSDial(newRS("ns1", "rs1"), newRS("ns2", "rs2"))})
	f.Start(client.NamespaceAll)
	defer f.Terminate()

	oo, err := f.List(client.RsGVR, client.NamespaceAll, true, labels.Everything())
	require.NoError(t, err)
	require.Len(t, oo, 2)
	assert.Empty(t, oo[0].(*unstructured.Unstructured).GetManagedFields(), "managed fields must be stripped")

	require.Eventually(t, func() bool {
		ok, _ := f.HasSynced(client.RsGVR, client.BlankNamespace)
		return ok
	}, time.Second, 10*time.Millisecond)

	oo, err = f.List(client.RsGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	require.Len(t, oo, 1)
	assert.Equal(t, "rs1", oo[0].(*unstructured.Unstructured).GetName())
	assert.Nil(t, f.FactoryFor("ns1"), "no per-namespace informer when a cluster-wide one exists")

	o, err := f.Get(client.RsGVR, "ns2/rs2", true, nil)
	require.NoError(t, err)
	assert.Equal(t, "rs2", o.(*unstructured.Unstructured).GetName())
}
