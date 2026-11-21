// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

func mutNode(n string, cordoned bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: n},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
	}
}

func mutNodePod(n string, mod func(*corev1.Pod)) *corev1.Pod {
	po := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n},
		Spec:       corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "c1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if mod != nil {
		mod(&po)
	}

	return &po
}

func ownedBy(kind, name string) func(*corev1.Pod) {
	return func(po *corev1.Pod) {
		po.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1", Kind: kind, Name: name, Controller: new(true),
		}}
	}
}

// drainObjects returns a node with a managed, a daemonset, an emptydir and
// an unmanaged pod.
func drainObjects(cordoned bool) []runtime.Object {
	return []runtime.Object{
		mutNode("n1", cordoned),
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ds1"}},
		mutNodePod("managed", ownedBy("ReplicaSet", "rs1")),
		mutNodePod("daemon", ownedBy("DaemonSet", "ds1")),
		mutNodePod("scratch", func(po *corev1.Pod) {
			ownedBy("ReplicaSet", "rs1")(po)
			po.Spec.Volumes = []corev1.Volume{{Name: "v", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
		}),
		mutNodePod("bare", nil),
	}
}

type drainFake struct {
	*fakecluster.Cluster

	mx        sync.Mutex
	evictions []*policyv1.Eviction
	evictErr  error
}

// drainCluster serves and records the eviction subresource. An eviction
// deletes the pod unless keep is set.
func drainCluster(t *testing.T, cordoned, keep bool, opts ...fakecluster.Option) *drainFake {
	t.Helper()
	fc := &drainFake{Cluster: fakecluster.New(t, append([]fakecluster.Option{
		fakecluster.WithObjects(drainObjects(cordoned)...), fakecluster.MirrorTypedToDyn(),
	}, opts...)...)}
	disc := fc.Typed().Discovery().(*fakediscovery.FakeDiscovery)
	for _, l := range disc.Resources {
		if l.GroupVersion == "v1" {
			l.APIResources = append(l.APIResources, metav1.APIResource{
				Name: "pods/eviction", Kind: "Eviction", Group: "policy", Version: "v1", Namespaced: true,
			})
		}
	}
	fc.Typed().PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		ev := a.(k8stesting.CreateAction).GetObject().(*policyv1.Eviction)
		fc.mx.Lock()
		fc.evictions = append(fc.evictions, ev.DeepCopy())
		err := fc.evictErr
		fc.mx.Unlock()
		if err != nil {
			return true, nil, err
		}
		if !keep {
			if err := fc.Typed().Tracker().Delete(client.PodGVR.GVR(), ev.Namespace, ev.Name); err != nil {
				return true, nil, err
			}
		}
		return true, ev, nil
	})

	return fc
}

func evictions(fc *drainFake) map[string]*int64 {
	fc.mx.Lock()
	defer fc.mx.Unlock()
	out := make(map[string]*int64)
	for _, ev := range fc.evictions {
		out[ev.Name] = ev.DeleteOptions.GracePeriodSeconds
	}

	return out
}

func podDeletes(fc *drainFake) map[string]*int64 {
	out := make(map[string]*int64)
	for _, d := range fc.Deletes() {
		if d.GVR.Resource == "pods" && d.Source == fakecluster.SourceTyped {
			out[d.Name] = d.Grace
		}
	}

	return out
}

func nodePatches(fc interface {
	Actions(string, string) []k8stesting.Action
}) []string {
	var out []string
	for _, a := range fc.Actions("patch", "nodes") {
		out = append(out, string(a.(k8stesting.PatchAction).GetPatch()))
	}

	return out
}

func newNode(t *testing.T, conn client.Connection) *Node {
	t.Helper()
	var n Node
	n.Init(mutFactory(t, conn), client.NodeGVR)

	return &n
}

func TestNodeDrain(t *testing.T) {
	uu := map[string]struct {
		opts      DrainOptions
		cordoned  bool
		evicted   []string
		deleted   []string
		grace     *int64
		cordonsTo int
	}{
		"all-options": {
			opts:      DrainOptions{GracePeriodSeconds: 5, Timeout: time.Second, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true},
			evicted:   []string{"bare", "managed", "scratch"},
			grace:     new(int64(5)),
			cordonsTo: 1,
		},
		"default-grace": {
			opts:      DrainOptions{GracePeriodSeconds: -1, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true},
			evicted:   []string{"bare", "managed", "scratch"},
			cordonsTo: 1,
		},
		"disable-eviction": {
			opts:      DrainOptions{GracePeriodSeconds: 0, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true, DisableEviction: true},
			deleted:   []string{"bare", "managed", "scratch"},
			grace:     new(int64(0)),
			cordonsTo: 1,
		},
		"already-cordoned": {
			opts:     DrainOptions{GracePeriodSeconds: -1, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true},
			cordoned: true,
			evicted:  []string{"bare", "managed", "scratch"},
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := drainCluster(t, u.cordoned, false)
			var out bytes.Buffer

			require.NoError(t, newNode(t, fc.Conn()).Drain("n1", u.opts, &out))

			assert.Contains(t, out.String(), "Node n1 drained!")
			assert.Len(t, nodePatches(fc), u.cordonsTo)
			ev, del := evictions(fc), podDeletes(fc)
			assert.ElementsMatch(t, u.evicted, slices.Collect(mapKeys(ev)))
			assert.ElementsMatch(t, u.deleted, slices.Collect(mapKeys(del)))
			for _, g := range ev {
				assert.Equal(t, u.grace, g)
			}
			for _, g := range del {
				assert.Equal(t, u.grace, g)
			}
			n, err := fc.Typed().CoreV1().Nodes().Get(t.Context(), "n1", metav1.GetOptions{})
			require.NoError(t, err)
			assert.True(t, n.Spec.Unschedulable)
		})
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func TestNodeDrainRefused(t *testing.T) {
	uu := map[string]struct {
		opts DrainOptions
		errs []string
	}{
		"no-options": {
			opts: DrainOptions{GracePeriodSeconds: -1},
			errs: []string{"DaemonSet-managed Pods", "local storage", "declare no controller"},
		},
		"no-force": {
			opts: DrainOptions{GracePeriodSeconds: -1, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true},
			errs: []string{"ns1/bare"},
		},
		"no-emptydir": {
			opts: DrainOptions{GracePeriodSeconds: -1, IgnoreAllDaemonSets: true, Force: true},
			errs: []string{"ns1/scratch"},
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := drainCluster(t, false, false)
			var out bytes.Buffer

			err := newNode(t, fc.Conn()).Drain("n1", u.opts, &out)

			require.Error(t, err)
			for _, e := range u.errs {
				assert.Contains(t, err.Error(), e)
				assert.Contains(t, out.String(), e)
			}
			assert.Contains(t, out.String(), "[n1]")
			assert.Empty(t, evictions(fc))
			assert.Empty(t, podDeletes(fc))
			assert.Len(t, nodePatches(fc), 1, "node stays cordoned")
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errMut }

func TestNodeDrainOutputError(t *testing.T) {
	fc := drainCluster(t, false, false)

	err := newNode(t, fc.Conn()).Drain("n1", DrainOptions{GracePeriodSeconds: -1}, failWriter{})

	require.ErrorIs(t, err, errMut)
	assert.Empty(t, evictions(fc))
}

func TestNodeDrainErrors(t *testing.T) {
	all := DrainOptions{GracePeriodSeconds: -1, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true}
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods/eviction"}, "managed", errMut)
	uu := map[string]struct {
		opts     DrainOptions
		keep     bool
		cordoned bool
		path     string
		fc       []fakecluster.Option
		evictErr error
		conn     func(client.Connection) client.Connection
		err      string
	}{
		"evict-forbidden": {opts: all, evictErr: forbidden, err: "forbidden"},
		"timeout":         {opts: DrainOptions{GracePeriodSeconds: -1, Timeout: 50 * time.Millisecond, Force: true, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true}, keep: true, err: "deadline exceeded"},
		"deny-node-get":   {opts: all, fc: []fakecluster.Option{fakecluster.DenyVerb("get")}, err: "not authorized to list nodes"},
		"missing-node":    {opts: all, path: "n9", err: "not found"},
		"cordon-fails":    {opts: all, fc: []fakecluster.Option{fakecluster.FailOn("patch", "nodes", 0, errMut)}, err: "boom"},
		"dial": {
			opts: all, cordoned: true,
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := drainCluster(t, u.cordoned, u.keep, u.fc...)
			fc.evictErr = u.evictErr
			var conn client.Connection = fc.Conn()
			if u.conn != nil {
				conn = u.conn(conn)
			}
			path := u.path
			if path == "" {
				path = "n1"
			}

			require.ErrorContains(t, newNode(t, conn).Drain(path, u.opts, new(bytes.Buffer)), u.err)
		})
	}
}

func TestNodeToggleCordon(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutNode("n1", false)), fakecluster.MirrorTypedToDyn())
	steps := []struct {
		cordon bool
		err    string
		patch  bool
	}{
		{cordon: true, patch: true},
		{cordon: true, err: "already cordoned"},
		{cordon: false, patch: true},
		{cordon: false, err: "already uncordoned"},
	}

	for _, s := range steps {
		fc.Reset()
		err := newNode(t, fc.Conn()).ToggleCordon("n1", s.cordon)
		if s.err != "" {
			require.ErrorContains(t, err, s.err)
			assert.Empty(t, nodePatches(fc))
			continue
		}
		require.NoError(t, err)
		pp := nodePatches(fc)
		require.Len(t, pp, 1)
		var body struct {
			Spec map[string]any `json:"spec"`
		}
		require.NoError(t, json.Unmarshal([]byte(pp[0]), &body))
		if s.cordon {
			assert.Equal(t, map[string]any{"unschedulable": true}, body.Spec)
		} else {
			assert.Equal(t, map[string]any{"unschedulable": nil}, body.Spec)
		}
	}
}

func TestNodeToggleCordonErrors(t *testing.T) {
	uu := map[string]struct {
		fc   []fakecluster.Option
		conn func(client.Connection) client.Connection
		path string
		err  string
	}{
		"deny":    {fc: []fakecluster.Option{fakecluster.DenyVerb("get")}, err: "not authorized"},
		"missing": {path: "n9", err: "not found"},
		"patch":   {fc: []fakecluster.Option{fakecluster.FailOn("patch", "nodes", 0, errMut)}, err: "boom"},
		"dial": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
		"cani": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, canIErr: errMut} },
			err:  "boom",
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, append([]fakecluster.Option{fakecluster.WithObjects(mutNode("n1", false))}, u.fc...)...)
			var conn client.Connection = fc.Conn()
			if u.conn != nil {
				conn = u.conn(conn)
			}
			path := u.path
			if path == "" {
				path = "n1"
			}

			require.ErrorContains(t, newNode(t, conn).ToggleCordon(path, true), u.err)
		})
	}
}
