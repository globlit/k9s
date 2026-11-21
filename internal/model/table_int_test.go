// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestTableReconcile(t *testing.T) {
	ta := NewTable(client.PodGVR)
	ta.SetNamespace(client.NamespaceAll)

	ctx := fixtureCtx(t, client.NamespaceAll, load(t, "p1"))
	ctx = context.WithValue(ctx, internal.KeyFields, "")
	ctx = context.WithValue(ctx, internal.KeyWithMetrics, false)
	err := ta.reconcile(ctx)
	require.NoError(t, err)
	data := ta.Peek()
	assert.Equal(t, 26, data.HeaderCount())
	assert.Equal(t, 1, data.RowCount())
	assert.Equal(t, client.NamespaceAll, data.GetNamespace())
}

func TestTableList(t *testing.T) {
	ta := NewTable(client.PodGVR)
	ta.SetNamespace("blee")

	acc := accessor{}
	ctx := fixtureCtx(t, "blee")
	rows, err := ta.list(ctx, &acc)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func TestTableGet(t *testing.T) {
	ta := NewTable(client.PodGVR)
	ta.SetNamespace("blee")

	ctx := fixtureCtx(t, "default", load(t, "p1"))
	ctx = context.WithValue(ctx, internal.KeyWithMetrics, false)
	row, err := ta.Get(ctx, "default/nginx-7fb78fb6d8-2w75j")
	require.NoError(t, err)
	assert.NotNil(t, row)
	assert.Len(t, row.(*render.PodWithMetrics).Raw.Object, 5)
}

func TestTableMeta(t *testing.T) {
	uu := map[string]struct {
		gvr      *client.GVR
		accessor dao.Accessor
		renderer model1.Renderer
	}{
		"generic": {
			gvr:      client.CoGVR,
			accessor: &dao.Container{},
			renderer: &render.Container{},
		},
		"node": {
			gvr:      client.NodeGVR,
			accessor: &dao.Node{},
			renderer: &render.Node{},
		},
	}

	for k := range uu {
		u := uu[k]
		ta := NewTable(u.gvr)
		m := resourceMeta(ta.gvr)

		assert.Equal(t, u.accessor, m.DAO)
		assert.Equal(t, u.renderer, m.Renderer)
	}
}

// ----------------------------------------------------------------------------
// Helpers...

func mustLoad(n string) *unstructured.Unstructured {
	raw, err := os.ReadFile(fmt.Sprintf("testdata/%s.json", n))
	if err != nil {
		panic(err)
	}
	var o unstructured.Unstructured
	if err = json.Unmarshal(raw, &o); err != nil {
		panic(err)
	}
	return &o
}

func load(t *testing.T, n string) *unstructured.Unstructured {
	raw, err := os.ReadFile(fmt.Sprintf("testdata/%s.json", n))
	require.NoError(t, err)
	var o unstructured.Unstructured
	err = json.Unmarshal(raw, &o)
	require.NoError(t, err)
	return &o
}

// ----------------------------------------------------------------------------

func fakeFactory(tb testing.TB, ns string, oo ...runtime.Object) *watch.Factory {
	tb.Helper()
	fc := fakecluster.New(tb, fakecluster.WithObjects(oo...))
	f := watch.NewFactory(fc.Conn())
	f.Start(ns)
	tb.Cleanup(f.Terminate)

	return f
}

// fixtureCtx returns a context with a synced fake cluster factory.
func fixtureCtx(t *testing.T, ns string, oo ...runtime.Object) context.Context {
	t.Helper()
	f := fakeFactory(t, ns, oo...)
	_, err := f.List(client.PodGVR, ns, true, labels.Everything())
	require.NoError(t, err)

	return context.WithValue(context.Background(), internal.KeyFactory, f)
}

// ----------------------------------------------------------------------------

type accessor struct {
	gvr *client.GVR
}

var _ dao.Accessor = (*accessor)(nil)

func (*accessor) SetIncludeObject(bool) {}

func (*accessor) List(context.Context, string) ([]runtime.Object, error) {
	return []runtime.Object{&render.PodWithMetrics{Raw: mustLoad("p1")}}, nil
}

func (*accessor) Get(context.Context, string) (runtime.Object, error) {
	return &render.PodWithMetrics{Raw: mustLoad("p1")}, nil
}

func (a *accessor) Init(_ dao.Factory, gvr *client.GVR) {
	a.gvr = gvr
}

func (a *accessor) GVR() string {
	return a.gvr.String()
}
