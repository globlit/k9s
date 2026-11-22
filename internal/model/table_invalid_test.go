// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"errors"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTableDeleteInvalid(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}}
	errBoom := errors.New("boom")
	uu := map[string]struct {
		gvr  *client.GVR
		opts []fakecluster.Option
		ctx  func(*watch.Factory) context.Context
		path string
		err  string
	}{
		"no-factory": {
			gvr: client.PodGVR, path: "ns1/p1", err: "expected Factory in context but got <nil>",
			ctx: func(*watch.Factory) context.Context { return context.Background() },
		},
		"wrong-factory": {
			gvr: client.PodGVR, path: "ns1/p1", err: "expected Factory in context but got string",
			ctx: func(*watch.Factory) context.Context {
				return context.WithValue(context.Background(), internal.KeyFactory, "x")
			},
		},
		"no-nuker":  {gvr: client.CoGVR, path: "ns1/p1", err: `no nuker for "containers"`},
		"missing":   {gvr: client.PodGVR, path: "ns1/nope", err: "not found"},
		"empty":     {gvr: client.PodGVR, path: "", err: "not found"},
		"forbidden": {gvr: client.PodGVR, path: "ns1/p1", opts: []fakecluster.Option{fakecluster.DenyVerb("delete")}, err: "not authorized"},
		"api-error": {gvr: client.PodGVR, path: "ns1/p1", opts: []fakecluster.Option{fakecluster.FailOn("delete", "pods", 0, errBoom)}, err: "boom"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, append([]fakecluster.Option{fakecluster.WithObjects(pod)}, u.opts...)...)
			f := watch.NewFactory(fc.Conn())
			t.Cleanup(f.Terminate)
			ctx := context.WithValue(context.Background(), internal.KeyFactory, f)
			if u.ctx != nil {
				ctx = u.ctx(f)
			}

			err := NewTable(u.gvr).Delete(ctx, u.path, nil, dao.DefaultGrace)

			require.ErrorContains(t, err, u.err)
			_, gerr := fc.DynGet(client.PodGVR, "ns1", "p1")
			assert.NoError(t, gerr, "p1 stays")
		})
	}
}

func TestTableDeleteValid(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}}
	fc := fakecluster.New(t, fakecluster.WithObjects(pod))
	f := watch.NewFactory(fc.Conn())
	t.Cleanup(f.Terminate)
	ctx := context.WithValue(context.Background(), internal.KeyFactory, f)

	require.NoError(t, NewTable(client.PodGVR).Delete(ctx, "ns1/p1", nil, dao.NowGrace))

	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "p1", dd[0].Name)
	require.NotNil(t, dd[0].Grace)
	assert.Equal(t, int64(1), *dd[0].Grace)
}
