// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

func newDataFactory(t *testing.T, opts ...fakecluster.Option) (*fakecluster.Cluster, *watch.Factory) {
	t.Helper()
	fc := fakecluster.New(t, opts...)
	f := watch.NewFactory(fc.Conn())
	f.Start(client.NamespaceAll)
	t.Cleanup(f.Terminate)

	return fc, f
}

func loadMetas(t *testing.T, f dao.Factory) *dao.Meta {
	t.Helper()
	m := dao.NewMeta()
	require.NoError(t, m.LoadResources(f))

	return m
}

func k8sGVRs(m *dao.Meta) []*client.GVR {
	var out []*client.GVR
	for _, gvr := range m.AllGVRs() {
		res, err := m.MetaFor(gvr)
		if err != nil || !dao.IsK8sMeta(res) {
			continue
		}
		out = append(out, gvr)
	}
	slices.SortFunc(out, func(a, b *client.GVR) int { return strings.Compare(a.String(), b.String()) })

	return out
}

func seedObj(gvr *client.GVR, res *metav1.APIResource, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetAPIVersion(gvr.GV().String())
	u.SetKind(res.Kind)
	u.SetName(name)
	if res.Namespaced {
		u.SetNamespace(ns)
	}

	return u
}

func objName(t *testing.T, o runtime.Object) string {
	t.Helper()
	switch x := o.(type) {
	case *render.PodWithMetrics:
		return x.Raw.GetName()
	case *render.NodeWithMetrics:
		return x.Raw.GetName()
	}
	m, err := meta.Accessor(o)
	require.NoError(t, err)

	return m.GetName()
}

func objNames(t *testing.T, oo []runtime.Object) []string {
	t.Helper()
	nn := make([]string, 0, len(oo))
	for _, o := range oo {
		nn = append(nn, objName(t, o))
	}
	slices.Sort(nn)

	return nn
}

func TestRegistryListGetAll(t *testing.T) {
	fc, f := newDataFactory(t, fakecluster.WithNamespaces("ns1", "ns2"))
	m := loadMetas(t, f)
	gvrs := k8sGVRs(m)
	require.NotEmpty(t, gvrs)

	for _, gvr := range gvrs {
		res, _ := m.MetaFor(gvr)
		if gvr == client.NsGVR {
			continue
		}
		if res.Namespaced {
			fc.Add(seedObj(gvr, res, "ns1", "a1"))
			fc.Add(seedObj(gvr, res, "ns2", "a2"))
			continue
		}
		if gvr == client.CrdGVR {
			// Seed the dynamic store only: the typed fake has no CRD mapping.
			require.NoError(t, fc.Dyn().Tracker().Create(gvr.GVR(), seedObj(gvr, res, "", "c1"), ""))
			continue
		}
		fc.Add(seedObj(gvr, res, "", "c1"))
	}

	ctx := context.WithValue(context.Background(), internal.KeyFactory, f)
	ctx = context.WithValue(ctx, internal.KeyWithMetrics, false)
	for _, gvr := range gvrs {
		res, _ := m.MetaFor(gvr)
		t.Run(gvr.String(), func(t *testing.T) {
			ctx := context.WithValue(ctx, internal.KeyGVR, gvr)
			a, err := dao.AccessorFor(f, gvr)
			require.NoError(t, err)

			switch {
			case gvr == client.NsGVR:
				assertList(t, f, a, ctx, gvr, client.BlankNamespace, "default", "ns1", "ns2")
				assertGet(t, a, "-/ns1", "ns1")
			case res.Namespaced:
				assertList(t, f, a, ctx, gvr, "ns1", "a1")
				assertList(t, f, a, ctx, gvr, client.NamespaceAll, "a1", "a2")
				assertGet(t, a, "ns2/a2", "a2")
			default:
				assertList(t, f, a, ctx, gvr, client.BlankNamespace, "c1")
				if gvr != client.NodeGVR {
					assertGet(t, a, "-/c1", "c1")
				}
			}
		})
	}
}

func assertList(t *testing.T, f *watch.Factory, a dao.Accessor, ctx context.Context, gvr *client.GVR, ns string, ee ...string) {
	t.Helper()
	_, err := f.List(gvr, ns, true, labels.Everything())
	require.NoError(t, err)
	var nn []string
	require.Eventually(t, func() bool {
		oo, err := a.List(ctx, ns)
		if err != nil {
			return false
		}
		nn = objNames(t, oo)
		return slices.Equal(ee, nn)
	}, 5*time.Second, 10*time.Millisecond, "%s %q: got %v", gvr, ns, nn)
}

func assertGet(t *testing.T, a dao.Accessor, path, e string) {
	t.Helper()
	o, err := a.Get(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, e, objName(t, o))
}

func TestRegistryNodeGet(t *testing.T) {
	_, f := newDataFactory(t, fakecluster.WithObjects(
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "n1"},
		}},
	))
	a, err := dao.AccessorFor(f, client.NodeGVR)
	require.NoError(t, err)
	_, err = f.List(client.NodeGVR, client.BlankNamespace, true, labels.Everything())
	require.NoError(t, err)

	o, err := a.Get(context.Background(), "n1")
	require.NoError(t, err)
	nmx, ok := o.(*render.NodeWithMetrics)
	require.True(t, ok)
	assert.Equal(t, "n1", nmx.Raw.GetName())
	assert.Equal(t, -1, nmx.PodCount)

	_, err = a.Get(context.Background(), "zorg")
	require.Error(t, err)
}

func TestRegistryListDenied(t *testing.T) {
	_, f := newDataFactory(t, fakecluster.DenyVerb(client.ListVerb, client.GetVerb))
	m := loadMetas(t, f)

	ctx := context.WithValue(context.Background(), internal.KeyFactory, f)
	for _, gvr := range k8sGVRs(m) {
		t.Run(gvr.String(), func(t *testing.T) {
			ctx := context.WithValue(ctx, internal.KeyGVR, gvr)
			a, err := dao.AccessorFor(f, gvr)
			require.NoError(t, err)
			assert.NotPanics(t, func() {
				_, _ = a.List(ctx, "ns1")
				_, _ = a.Get(ctx, "ns1/a1")
			})
			if _, ok := a.(*dao.Scaler); ok {
				return
			}
			if _, ok := a.(*dao.CronJob); ok {
				return
			}
			_, err = a.List(ctx, "ns1")
			require.ErrorContains(t, err, "access denied")
		})
	}
}

func TestRegistryLabelAndFieldSelectors(t *testing.T) {
	mk := func(n, node string, ll map[string]string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"namespace": "ns1", "name": n},
			"spec":     map[string]any{"nodeName": node},
		}}
		u.SetLabels(ll)
		return u
	}
	_, f := newDataFactory(t, fakecluster.WithObjects(
		mk("p1", "n1", map[string]string{"app": "a"}),
		mk("p2", "n2", map[string]string{"app": "b"}),
		mk("p3", "n1", map[string]string{"app": "b"}),
	))
	_, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	a, err := dao.AccessorFor(f, client.PodGVR)
	require.NoError(t, err)

	sel, err := labels.Parse("app=b")
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), internal.KeyLabels, sel)
	oo, err := a.List(ctx, "ns1")
	require.NoError(t, err)
	assert.Equal(t, []string{"p2", "p3"}, objNames(t, oo))

	ctx = context.WithValue(ctx, internal.KeyFields, "spec.nodeName=n1")
	oo, err = a.List(ctx, "ns1")
	require.NoError(t, err)
	assert.Equal(t, []string{"p3"}, objNames(t, oo))

	ctx = context.WithValue(context.Background(), internal.KeyFields, "spec.nodeName==")
	_, err = a.List(ctx, "ns1")
	require.Error(t, err)
}
