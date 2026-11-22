// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
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
			assert.Equal(t, "regarding.uid=original-uid", ctx.Value(internal.KeyFields))
			assert.Equal(t, "team-a/example", ctx.Value(internal.KeyPath))
		})
	}
	_, _, err := resourceEvents("team-a/example", map[string]any{})
	require.ErrorContains(t, err, "no UID")
}

func TestResourceEventsUsesServedAPI(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithAPIServer())
	requests := make(chan string, 1)
	fc.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			return false
		}
		selector := r.URL.Query().Get("fieldSelector")
		requests <- r.URL.Path + "?" + selector
		w.Header().Set("Content-Type", "application/json")
		// Match the served events.k8s.io/v1 contract, which rejects core Event labels.
		if r.URL.Path != "/apis/events.k8s.io/v1/namespaces/team-a/events" || selector != "regarding.uid=original-uid" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(apierrors.NewBadRequest("unsupported Event field selector").Status())
			return true
		}
		_ = json.NewEncoder(w).Encode(metav1.Table{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "Table"}})
		return true
	})
	factory := watch.NewFactory(fc.Conn())
	t.Cleanup(factory.Terminate)
	v, ns, err := resourceEvents("team-a/example", map[string]any{"metadata": map[string]any{"namespace": "team-a", "uid": "original-uid"}})
	require.NoError(t, err)
	var resource dao.Table
	resource.Init(factory, v.GVR())
	_, err = resource.List(v.GetContextFn()(context.Background()), ns)
	require.NoError(t, err)
	select {
	case request := <-requests:
		assert.Equal(t, "/apis/events.k8s.io/v1/namespaces/team-a/events?regarding.uid=original-uid", request)
	default:
		t.Fatal("events were not requested from the API")
	}
}

func TestResourceEventsKeyOnLoadedCRD(t *testing.T) {
	for _, mode := range []string{"rw", "ro"} {
		t.Run(mode, func(t *testing.T) {
			gvr := client.NewGVR("example.com/v1/widgets")
			object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.com/v1", "kind": "Widget", "metadata": map[string]any{"namespace": "ns1", "name": "widget"}}}
			fc := fakecluster.New(t, fakecluster.WithAPIServer(), fakecluster.WithCRD(gvr, "Widget", true), fakecluster.WithObjects(object))
			opts := []SimOption{SimNamespace("ns1"), SimView("widgets")}
			if mode == "ro" {
				opts = append(opts, SimReadOnly())
			}
			s := NewSimApp(t, fc, opts...)
			v := topViewer(t, s)
			s.Eventually(func() bool {
				_, ok := v.Actions().Get(ui.KeyShiftE)
				return ok
			}, "loaded CRD did not bind resource events")
			action, _ := v.Actions().Get(ui.KeyShiftE)
			assert.Equal(t, "Events", action.Description)
			assert.False(t, action.Opts.Dangerous)
		})
	}
}

func TestFetchResourceObjectReturnsRawAPIObjects(t *testing.T) {
	pod := simPod("ns1", "app")
	pod.SetUID("pod-uid")
	node := simNode("worker")
	node.SetUID("node-uid")
	cr := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.com/v1", "kind": "Widget", "metadata": map[string]any{"name": "widget", "namespace": "ns1", "uid": "cr-uid"}}}
	fc := fakecluster.New(t, fakecluster.WithCRD(client.NewGVR("example.com/v1/widgets"), "Widget", true), fakecluster.WithObjects(pod, node, cr))
	factory := watch.NewFactory(fc.Conn())
	t.Cleanup(factory.Terminate)
	for _, tc := range []struct {
		gvr           *client.GVR
		path, uid, ns string
	}{
		{client.PodGVR, "ns1/app", "pod-uid", "ns1"},
		{client.NodeGVR, "-/worker", "node-uid", client.NamespaceAll},
		{client.NewGVR("example.com/v1/widgets"), "ns1/widget", "cr-uid", "ns1"},
	} {
		var accessor dao.Generic
		accessor.Init(factory, tc.gvr)
		require.NoError(t, accessor.PinClient())
		obj, err := fetchResourceObject(context.Background(), &accessor, tc.path)
		require.NoError(t, err)
		assert.Equal(t, tc.uid, string((&unstructured.Unstructured{Object: obj}).GetUID()))
		_, ns, err := resourceEvents(tc.path, obj)
		require.NoError(t, err)
		assert.Equal(t, tc.ns, ns)
	}
}

type switchingReadConn struct {
	client.Connection
	dynamic dynamic.Interface
}

func (c *switchingReadConn) DynDial() (dynamic.Interface, error) { return c.dynamic, nil }

func TestFetchResourceObjectKeepsPinnedClient(t *testing.T) {
	pod := simPod("ns1", "app")
	pod.UID = "original"
	original := fakecluster.New(t, fakecluster.WithObjects(pod))
	pod = pod.DeepCopy()
	pod.UID = "other-cluster"
	other := fakecluster.New(t, fakecluster.WithObjects(pod))
	conn := &switchingReadConn{Connection: original.Conn(), dynamic: original.Dyn()}
	factory := watch.NewFactory(conn)
	t.Cleanup(factory.Terminate)
	var accessor dao.Generic
	accessor.Init(factory, client.PodGVR)
	require.NoError(t, accessor.PinClient())
	conn.dynamic = other.Dyn()
	obj, err := fetchResourceObject(context.Background(), &accessor, "ns1/app")
	require.NoError(t, err)
	assert.Equal(t, "original", string((&unstructured.Unstructured{Object: obj}).GetUID()))
	assert.Empty(t, other.Actions("get", "pods"))
}
