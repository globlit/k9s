// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao_test

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	mxv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func TestLoadPreferred(t *testing.T) {
	_, f := newDataFactory(t)
	m := loadMetas(t, f)

	dp, err := m.MetaFor(client.DpGVR)
	require.NoError(t, err)
	assert.True(t, dao.IsScalable(dp))
	assert.False(t, dao.IsCRD(dp))
	assert.Equal(t, "apps", dp.Group)
	assert.Equal(t, "deployment", dp.SingularName)

	pod, err := m.MetaFor(client.PodGVR)
	require.NoError(t, err)
	assert.False(t, dao.IsScalable(pod))
	ok, err := m.IsNamespaced(client.PodGVR)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = m.IsNamespaced(client.NodeGVR)
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = m.IsNamespaced(client.NewGVR("zorg"))
	require.Error(t, err)

	_, err = m.MetaFor(client.NewGVR("v1/events"))
	require.Error(t, err, "skip deprecated resources")
	_, err = m.MetaFor(client.NewGVR("v1/pods/scale"))
	require.Error(t, err)

	wk, err := m.MetaFor(client.WkGVR)
	require.NoError(t, err)
	assert.True(t, dao.IsK9sMeta(wk))
	assert.False(t, dao.IsK8sMeta(wk))
}

func TestLoadPreferredDisconnected(t *testing.T) {
	fc, f := newDataFactory(t)
	fc.SetConnected(false)
	m := loadMetas(t, f)

	_, err := m.MetaFor(client.PodGVR)
	require.Error(t, err)
	_, err = m.MetaFor(client.WkGVR)
	require.NoError(t, err, "keep k9s resources")
}

func TestMetaLookups(t *testing.T) {
	_, f := newDataFactory(t)
	m := loadMetas(t, f)

	assert.Equal(t, client.DpGVR, m.Lookup("deploy"))
	assert.Equal(t, client.DpGVR, m.Lookup("deployments"))
	assert.Equal(t, client.DpGVR, m.Lookup("Deployment"))
	assert.Equal(t, client.NoGVR, m.Lookup("zorg"))

	gvr, ns, ok := m.GVK2GVR(schema.GroupVersion{Group: "apps", Version: "v1"}, "StatefulSet")
	assert.True(t, ok)
	assert.True(t, ns)
	assert.Equal(t, client.StsGVR, gvr)
	_, _, ok = m.GVK2GVR(schema.GroupVersion{Group: "apps", Version: "v9"}, "StatefulSet")
	assert.False(t, ok)

	gvr, ok = m.GK2GVR("", "Node")
	assert.True(t, ok)
	assert.Equal(t, client.NodeGVR, gvr)

	m.RegisterMeta("fred.com/v1/blees", &metav1.APIResource{Name: "blees", Kind: "Blee", ShortNames: []string{"bl"}})
	assert.Equal(t, client.NewGVR("fred.com/v1/blees"), m.Lookup("bl"))
	assert.Contains(t, m.AllGVRs(), client.NewGVR("fred.com/v1/blees"))
}

func TestPodListWithMetrics(t *testing.T) {
	_, f := newDataFactory(t,
		fakecluster.WithObjects(
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p2"}},
		),
		fakecluster.WithMetrics(&mxv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"},
			Containers: []mxv1beta1.ContainerMetrics{{Name: "c1", Usage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("10m"),
			}}},
		}),
	)
	client.ResetMetrics()
	t.Cleanup(client.ResetMetrics)
	_, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	var p dao.Pod
	p.Init(f, client.PodGVR)
	ctx := context.WithValue(context.Background(), internal.KeyWithMetrics, true)
	oo, err := p.List(ctx, "ns1")
	require.NoError(t, err)
	require.Len(t, oo, 2)
	mx := make(map[string]bool)
	for _, o := range oo {
		pm := o.(*render.PodWithMetrics)
		mx[pm.Raw.GetName()] = pm.MX != nil
	}
	assert.Equal(t, map[string]bool{"p1": true, "p2": false}, mx)

	o, err := p.Get(ctx, "ns1/p1")
	require.NoError(t, err)
	assert.NotNil(t, o.(*render.PodWithMetrics).MX)
}

func TestPodListNoMetricsServer(t *testing.T) {
	_, f := newDataFactory(t, fakecluster.WithObjects(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
	))
	client.ResetMetrics()
	t.Cleanup(client.ResetMetrics)
	_, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	var p dao.Pod
	p.Init(f, client.PodGVR)
	ctx := context.WithValue(context.Background(), internal.KeyWithMetrics, true)
	oo, err := p.List(ctx, "ns1")
	require.NoError(t, err)
	require.Len(t, oo, 1)
	assert.Nil(t, oo[0].(*render.PodWithMetrics).MX)
}
