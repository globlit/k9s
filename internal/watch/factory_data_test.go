// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/port"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/portforward"
)

type fakeFwd struct {
	id    string
	age   time.Time
	stops atomic.Int32
}

func newFakeFwd(id string, age time.Time) *fakeFwd { return &fakeFwd{id: id, age: age} }

func (*fakeFwd) Start(string, port.PortTunnel) (*portforward.PortForwarder, error) {
	return nil, nil
}
func (f *fakeFwd) Stop()                    { f.stops.Add(1) }
func (f *fakeFwd) ID() string               { return f.id }
func (*fakeFwd) Container() string          { return "c1" }
func (*fakeFwd) Port() string               { return "8080:80" }
func (*fakeFwd) Address() string            { return "localhost" }
func (f *fakeFwd) FQN() string              { return f.id }
func (*fakeFwd) Active() bool               { return true }
func (*fakeFwd) SetActive(bool)             {}
func (f *fakeFwd) Age() time.Time           { return f.age }
func (*fakeFwd) HasPortMapping(string) bool { return false }

func newPod(ns, n string, created time.Time) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace:         ns,
		Name:              n,
		CreationTimestamp: metav1.NewTime(created),
	}}
}

func newFactory(t *testing.T, ns string, opts ...fakecluster.Option) (*fakecluster.Cluster, *watch.Factory) {
	t.Helper()
	fc := fakecluster.New(t, opts...)
	f := watch.NewFactory(fc.Conn())
	f.Start(ns)
	t.Cleanup(f.Terminate)

	return fc, f
}

func TestFactoryForwarderRegistry(t *testing.T) {
	_, f := newFactory(t, "ns1")

	f1 := newFakeFwd("ns1/p1|c1|8080:80", time.Now())
	f2 := newFakeFwd("ns1/p1|c2|9090:90", time.Now())
	f3 := newFakeFwd("ns1/p10|c1|8080:80", time.Now())
	for _, fw := range []*fakeFwd{f1, f2, f3} {
		f.AddForwarder(fw)
	}

	fw, ok := f.ForwarderFor(f1.ID())
	require.True(t, ok)
	assert.Same(t, f1, fw)
	_, ok = f.ForwarderFor("ns1/zorg|c1|1:1")
	assert.False(t, ok)

	snap := f.Forwarders()
	delete(snap, f1.ID())
	assert.Len(t, f.Forwarders(), 3, "snapshot must not alias the registry")

	f.DeleteForwarder("ns1/p1")
	assert.Equal(t, int32(1), f1.stops.Load())
	assert.Equal(t, int32(1), f2.stops.Load())
	assert.Zero(t, f3.stops.Load(), "keep forwards of a pod with the same prefix")
	assert.Len(t, f.Forwarders(), 1)

	f.Terminate()
	assert.Equal(t, int32(1), f3.stops.Load())
	assert.Empty(t, f.Forwarders())
}

func TestFactoryRemoveForwarderKeepsReplacement(t *testing.T) {
	_, f := newFactory(t, "ns1")

	old := newFakeFwd("ns1/p1|c1|8080:80", time.Now())
	cur := newFakeFwd(old.ID(), time.Now())
	f.AddForwarder(old)
	f.AddForwarder(cur)

	f.RemoveForwarder(old)
	assert.Equal(t, int32(1), old.stops.Load())
	fw, ok := f.ForwarderFor(cur.ID())
	require.True(t, ok)
	assert.Same(t, cur, fw)

	f.RemoveForwarder(cur)
	_, ok = f.ForwarderFor(cur.ID())
	assert.False(t, ok)
}

func TestFactoryValidatePortForwards(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	_, f := newFactory(t, "ns1", fakecluster.WithObjects(
		newPod("ns1", "old", now.Add(-time.Hour)),
		newPod("ns1", "new", now.Add(time.Hour)),
	))

	keep := newFakeFwd("ns1/old|c1|8080:80", now)
	recreated := newFakeFwd("ns1/new|c1|8080:80", now)
	gone := newFakeFwd("ns1/gone|c1|8080:80", now)
	bad := newFakeFwd("ns1/old|c1", now)
	for _, fw := range []*fakeFwd{keep, recreated, gone, bad} {
		f.AddForwarder(fw)
	}

	f.ValidatePortForwards()

	ff := f.Forwarders()
	assert.Contains(t, ff, keep.ID())
	assert.Contains(t, ff, bad.ID(), "skip keys with a bad format")
	assert.NotContains(t, ff, recreated.ID(), "reap forwards older than the pod")
	assert.NotContains(t, ff, gone.ID(), "reap forwards of a deleted pod")
	assert.Zero(t, keep.stops.Load())
	assert.Equal(t, int32(1), recreated.stops.Load())
	assert.Equal(t, int32(1), gone.stops.Load())
}

func TestFactoryValidatePortForwardsKeepsOnTransientError(t *testing.T) {
	_, f := newFactory(t, "ns1", fakecluster.DenyVerb(client.GetVerb))

	fw := newFakeFwd("ns1/p1|c1|8080:80", time.Now())
	f.AddForwarder(fw)
	f.ValidatePortForwards()

	assert.Contains(t, f.Forwarders(), fw.ID())
	assert.Zero(t, fw.stops.Load())
}

func TestFactorySetActiveNS(t *testing.T) {
	_, f := newFactory(t, "ns1")

	require.NoError(t, f.SetActiveNS("ns1"))
	assert.NotNil(t, f.FactoryFor("ns1"))

	require.NoError(t, f.SetActiveNS(client.NamespaceAll))
	assert.NotNil(t, f.FactoryFor(client.BlankNamespace))

	require.NoError(t, f.SetActiveNS("ns2"))
	assert.Nil(t, f.FactoryFor("ns2"), "skip namespace informers when a cluster-wide one exists")
}

func TestFactoryWaitForCacheSync(t *testing.T) {
	_, f := newFactory(t, "ns1", fakecluster.WithObjects(newPod("ns1", "p1", time.Now())))

	inf, err := f.ForResource("ns1", client.PodGVR)
	require.NoError(t, err)
	f.WaitForCacheSync()
	assert.True(t, inf.Informer().HasSynced())

	ok, err := f.HasSynced(client.PodGVR, "ns1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFactoryHasSyncedDenied(t *testing.T) {
	_, f := newFactory(t, "ns1", fakecluster.DenyVerb(client.ListVerb))

	ok, err := f.HasSynced(client.PodGVR, "ns1")
	require.Error(t, err)
	assert.False(t, ok)
}

func TestFactoryGetScopes(t *testing.T) {
	_, f := newFactory(t, client.NamespaceAll,
		fakecluster.WithNamespaces("ns1"),
		fakecluster.WithObjects(
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
			newPod("ns1", "p1", time.Now()),
		),
	)

	uu := map[string]struct {
		gvr  *client.GVR
		fqn  string
		name string
	}{
		"node":            {gvr: client.NodeGVR, fqn: "-/n1", name: "n1"},
		"namespace":       {gvr: client.NsGVR, fqn: "-/ns1", name: "ns1"},
		"pod":             {gvr: client.PodGVR, fqn: "ns1/p1", name: "p1"},
		"pod-wrong-scope": {gvr: client.PodGVR, fqn: "-/p1"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			o, err := f.Get(u.gvr, u.fqn, true, labels.Everything())
			if u.name == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, u.name, o.(*unstructured.Unstructured).GetName())
		})
	}
}

func TestFactoryGetNotFound(t *testing.T) {
	_, f := newFactory(t, "ns1")

	_, err := f.Get(client.PodGVR, "ns1/zorg", true, labels.Everything())
	assert.True(t, apierrors.IsNotFound(err))
	_, err = f.Get(client.PodGVR, "ns1/zorg", false, labels.Everything())
	assert.True(t, apierrors.IsNotFound(err))
}

func TestFactoryRBAC(t *testing.T) {
	_, f := newFactory(t, "ns1",
		fakecluster.WithObjects(newPod("ns1", "p1", time.Now())),
		fakecluster.WithRBAC(func(ns string, _ *client.GVR, n, verb string) bool {
			return !(verb == client.GetVerb && n == "p1") && ns != "locked"
		}),
	)

	_, err := f.Get(client.PodGVR, "ns1/p1", true, labels.Everything())
	require.ErrorContains(t, err, "access denied")
	_, err = f.List(client.PodGVR, "locked", true, labels.Everything())
	require.ErrorContains(t, err, "access denied")
	_, err = f.CanForResource("locked", client.NsGVR, client.ListAccess)
	require.ErrorContains(t, err, "access denied")
	_, err = f.CanForInstance("locked", client.NsGVR, client.GetAccess)
	require.ErrorContains(t, err, "access denied")

	oo, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	assert.Len(t, oo, 1)
}

func TestFactoryListFailure(t *testing.T) {
	boom := errors.New("boom")
	_, f := newFactory(t, "ns1",
		fakecluster.WithObjects(newPod("ns1", "p1", time.Now())),
		fakecluster.FailOn("list", "pods", 0, boom),
	)

	oo, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	assert.Empty(t, oo, "unsynced cache returns no rows")
}
