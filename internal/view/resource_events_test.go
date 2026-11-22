// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestResourceEvents(t *testing.T) {
	for _, ns := range []string{"team-a", ""} {
		t.Run(ns, func(t *testing.T) {
			v, target, err := resourceEvents("team-a/example", map[string]any{"metadata": map[string]any{"namespace": ns, "uid": "original-uid"}})
			require.NoError(t, err)
			expectedNS := ns
			if ns == "" {
				expectedNS = client.NamespaceAll
			}
			assert.Equal(t, expectedNS, target)
			ctx := v.GetContextFn()(context.Background())
			assert.Equal(t, "involvedObject.uid=original-uid", ctx.Value(internal.KeyFields))
			assert.Equal(t, "team-a/example", ctx.Value(internal.KeyPath))
		})
	}
	_, _, err := resourceEvents("team-a/example", map[string]any{})
	require.ErrorContains(t, err, "no UID")
}

func TestFetchResourceObjectReturnsRawAPIObjects(t *testing.T) {
	pod := simPod("ns1", "app")
	pod.SetUID("pod-uid")
	node := simNode("worker")
	node.SetUID("node-uid")
	cr := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.com/v1", "kind": "Widget", "metadata": map[string]any{"name": "widget", "namespace": "ns1", "uid": "cr-uid"}}}
	fc := fakecluster.New(t, fakecluster.WithObjects(pod, node, cr))
	cfg := config.NewConfig(fc.Conn().Config())
	cfg.SetConnection(fc.Conn())
	_, err := cfg.K9s.ActivateContext(fc.Conn().ActiveContext())
	require.NoError(t, err)
	app := NewApp(cfg)
	app.factory = watch.NewFactory(fc.Conn())
	t.Cleanup(app.factory.Terminate)
	for _, tc := range []struct {
		gvr           *client.GVR
		path, uid, ns string
	}{
		{client.PodGVR, "ns1/app", "pod-uid", "ns1"},
		{client.NodeGVR, "-/worker", "node-uid", client.NamespaceAll},
		{client.NewGVR("example.com/v1/widgets"), "ns1/widget", "cr-uid", "ns1"},
	} {
		obj, err := fetchResourceObject(context.Background(), app, tc.gvr, tc.path)
		require.NoError(t, err)
		assert.Equal(t, tc.uid, string((&unstructured.Unstructured{Object: obj}).GetUID()))
		_, ns, err := resourceEvents(tc.path, obj)
		require.NoError(t, err)
		assert.Equal(t, tc.ns, ns)
	}
}
