// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	mxv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"k8s.io/utils/ptr"
)

func pod(ns, n string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n}}
}

func TestDynDeleteRecordsOptions(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(pod("ns1", "p1")))
	dyn, err := fc.Conn().DynDial()
	require.NoError(t, err)

	orphan := metav1.DeletePropagationOrphan
	err = dyn.Resource(client.PodGVR.GVR()).Namespace("ns1").Delete(context.Background(), "p1", metav1.DeleteOptions{
		PropagationPolicy:  &orphan,
		GracePeriodSeconds: ptr.To[int64](0),
	})
	require.NoError(t, err)

	assert.Equal(t, []fakecluster.DeleteCall{{
		GVR:         client.PodGVR.GVR(),
		NS:          "ns1",
		Name:        "p1",
		Propagation: &orphan,
		Grace:       ptr.To[int64](0),
		Source:      fakecluster.SourceDyn,
	}}, fc.Deletes())
	_, err = fc.DynGet(client.PodGVR, "ns1", "p1")
	assert.True(t, apierrors.IsNotFound(err))
}

func TestTypedDeleteRecordsNilOptions(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(pod("ns1", "p1")))
	cs, err := fc.Conn().Dial()
	require.NoError(t, err)

	require.NoError(t, cs.CoreV1().Pods("ns1").Delete(context.Background(), "p1", metav1.DeleteOptions{}))
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, fakecluster.SourceTyped, dd[0].Source)
	assert.Nil(t, dd[0].Propagation)
	assert.Nil(t, dd[0].Grace)

	fc.Reset()
	assert.Empty(t, fc.Deletes())
	assert.Empty(t, fc.Calls())
}

func TestSeedFillsBothStores(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithNamespaces("extra"),
		fakecluster.WithObjects(
			pod("ns1", "p1"),
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "d1"}},
			&unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"namespace": "ns1", "name": "cm1"},
			}},
		),
	)
	ctx := context.Background()
	cs, _ := fc.Conn().Dial()

	_, err := cs.CoreV1().Pods("ns1").Get(ctx, "p1", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.CoreV1().ConfigMaps("ns1").Get(ctx, "cm1", metav1.GetOptions{})
	require.NoError(t, err)

	dyn, _ := fc.Conn().DynDial()
	l, err := dyn.Resource(client.DpGVR.GVR()).Namespace("ns2").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, l.Items, 1)
	assert.Equal(t, "Deployment", l.Items[0].GetKind())

	nn, err := fc.Conn().ValidNamespaceNames()
	require.NoError(t, err)
	assert.Equal(t, client.NamespaceNames{"default": {}, "extra": {}, "ns1": {}, "ns2": {}}, nn)
	assert.True(t, fc.Conn().IsValidNamespace("ns1"))
	assert.True(t, fc.Conn().IsValidNamespace(client.NamespaceAll))
	assert.False(t, fc.Conn().IsValidNamespace("nope"))
}

func TestDynListsEveryBuiltinGVR(t *testing.T) {
	fc := fakecluster.New(t)
	dyn, _ := fc.Conn().DynDial()
	for _, gvr := range []*client.GVR{
		client.DpGVR, client.StsGVR, client.DsGVR, client.RsGVR, client.RcGVR,
		client.SaGVR, client.PvcGVR, client.PvGVR, client.CmGVR, client.SecGVR,
		client.EvGVR, client.EpGVR, client.PodGVR, client.NsGVR, client.NodeGVR,
		client.SvcGVR, client.EpsGVR, client.HpaGVR, client.CjGVR, client.JobGVR,
		client.CrdGVR, client.PcGVR, client.NpGVR, client.ScGVR, client.PdbGVR,
		client.PspGVR, client.IngGVR, client.NmxGVR, client.PmxGVR, client.CrGVR,
		client.CrbGVR, client.RoGVR, client.RobGVR,
	} {
		_, err := dyn.Resource(gvr.GVR()).List(context.Background(), metav1.ListOptions{})
		assert.NoError(t, err, gvr.String())
	}
}

func TestCRD(t *testing.T) {
	gvr := client.NewGVR("fred.com/v1/blees")
	fc := fakecluster.New(t,
		fakecluster.WithCRD(gvr, "Blee", true),
		fakecluster.WithObjects(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "fred.com/v1",
			"kind":       "Blee",
			"metadata":   map[string]any{"namespace": "ns1", "name": "b1"},
		}}),
	)
	dyn, _ := fc.Conn().DynDial()
	l, err := dyn.Resource(gvr.GVR()).Namespace("ns1").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, l.Items, 1)

	crds, err := dyn.Resource(client.CrdGVR.GVR()).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, crds.Items, 1)
	assert.Equal(t, "blees.fred.com", crds.Items[0].GetName())

	disc, _ := fc.Conn().CachedDiscovery()
	rl, err := disc.ServerResourcesForGroupVersion("fred.com/v1")
	require.NoError(t, err)
	assert.Equal(t, "blees", rl.APIResources[0].Name)
}

func TestCRDWithExtScheme(t *testing.T) {
	// Register CRD types in the client-go scheme, as helm does in dao.
	require.NoError(t, apiextv1.AddToScheme(scheme.Scheme))
	gvr := client.NewGVR("fred.com/v1/blees")
	fc := fakecluster.New(t, fakecluster.WithCRD(gvr, "Blee", true))

	dyn, _ := fc.Conn().DynDial()
	crds, err := dyn.Resource(client.CrdGVR.GVR()).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, crds.Items, 1)
	assert.Equal(t, "blees.fred.com", crds.Items[0].GetName())
}

func TestRBACOnClients(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.DenyNS("locked"),
		fakecluster.DenyVerb("delete"),
		fakecluster.WithObjects(pod("ns1", "p1"), pod("locked", "p2")),
	)
	dyn, _ := fc.Conn().DynDial()
	cs, _ := fc.Conn().Dial()
	ctx := context.Background()

	l, err := dyn.Resource(client.PodGVR.GVR()).Namespace("ns1").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, l.Items, 1)
	_, err = dyn.Resource(client.PodGVR.GVR()).Namespace("locked").List(ctx, metav1.ListOptions{})
	assert.True(t, apierrors.IsForbidden(err), "dyn list: %v", err)
	_, err = cs.CoreV1().Pods("locked").Get(ctx, "p2", metav1.GetOptions{})
	assert.True(t, apierrors.IsForbidden(err), "typed get: %v", err)
	err = cs.CoreV1().Pods("ns1").Delete(ctx, "p1", metav1.DeleteOptions{})
	assert.True(t, apierrors.IsForbidden(err), "typed delete: %v", err)
	_, err = fc.DynGet(client.PodGVR, "ns1", "p1")
	require.NoError(t, err)
}

func TestRBAC(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.DenyVerb("delete"),
		fakecluster.DenyNS("locked"),
		fakecluster.WithRBAC(func(_ string, gvr *client.GVR, _, _ string) bool {
			return gvr != client.SecGVR
		}),
	)
	conn := fc.Conn()
	for _, u := range []struct {
		ns    string
		gvr   *client.GVR
		verbs []string
		ok    bool
	}{
		{"ns1", client.PodGVR, client.ReadAllAccess, true},
		{"ns1", client.PodGVR, []string{client.GetVerb, client.DeleteVerb}, false},
		{"locked", client.PodGVR, client.GetAccess, false},
		{"ns1", client.SecGVR, client.GetAccess, false},
	} {
		ok, err := conn.CanI(u.ns, u.gvr, "", u.verbs)
		require.NoError(t, err)
		assert.Equal(t, u.ok, ok, "%s %s %v", u.ns, u.gvr, u.verbs)
	}
	assert.Equal(t, 4, fc.CanICalls())
	fc.Reset()
	assert.Zero(t, fc.CanICalls())
}

func TestFailOnNth(t *testing.T) {
	boom := errors.New("boom")
	fc := fakecluster.New(t,
		fakecluster.FailOn("delete", "pods", 2, boom),
		fakecluster.WithObjects(pod("ns1", "p1"), pod("ns1", "p2"), pod("ns1", "p3")),
	)
	dyn, _ := fc.Conn().DynDial()
	cs, _ := fc.Conn().Dial()
	ctx := context.Background()

	require.NoError(t, dyn.Resource(client.PodGVR.GVR()).Namespace("ns1").Delete(ctx, "p1", metav1.DeleteOptions{}))
	require.ErrorIs(t, cs.CoreV1().Pods("ns1").Delete(ctx, "p2", metav1.DeleteOptions{}), boom)
	require.NoError(t, dyn.Resource(client.PodGVR.GVR()).Namespace("ns1").Delete(ctx, "p3", metav1.DeleteOptions{}))

	assert.Len(t, fc.Deletes(), 3)
	_, err := fc.DynGet(client.PodGVR, "ns1", "p2")
	require.NoError(t, err)
}

func TestFailOnAll(t *testing.T) {
	boom := errors.New("boom")
	fc := fakecluster.New(t, fakecluster.FailOn("*", "*", 0, boom))
	dyn, _ := fc.Conn().DynDial()
	_, err := dyn.Resource(client.PodGVR.GVR()).List(context.Background(), metav1.ListOptions{})
	require.ErrorIs(t, err, boom)
}

func TestMirrorTypedToDyn(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.MirrorTypedToDyn())
	cs, _ := fc.Conn().Dial()
	ctx := context.Background()

	_, err := cs.CoreV1().Pods("default").Create(ctx, pod("default", "p1"), metav1.CreateOptions{})
	require.NoError(t, err)
	u, err := fc.DynGet(client.PodGVR, "default", "p1")
	require.NoError(t, err)
	assert.Equal(t, "Pod", u.GetKind())

	p := pod("default", "p1")
	p.Labels = map[string]string{"a": "b"}
	_, err = cs.CoreV1().Pods("default").Update(ctx, p, metav1.UpdateOptions{})
	require.NoError(t, err)
	u, err = fc.DynGet(client.PodGVR, "default", "p1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "b"}, u.GetLabels())

	require.NoError(t, cs.CoreV1().Pods("default").Delete(ctx, "p1", metav1.DeleteOptions{}))
	_, err = fc.DynGet(client.PodGVR, "default", "p1")
	assert.True(t, apierrors.IsNotFound(err))
}

func TestNoMirrorByDefault(t *testing.T) {
	fc := fakecluster.New(t)
	cs, _ := fc.Conn().Dial()
	_, err := cs.CoreV1().Pods("default").Create(context.Background(), pod("default", "p1"), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fc.DynGet(client.PodGVR, "default", "p1")
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscovery(t *testing.T) {
	fc := fakecluster.New(t)
	disc, err := fc.Conn().CachedDiscovery()
	require.NoError(t, err)
	assert.True(t, disc.Fresh())
	disc.Invalidate()
	assert.Equal(t, 1, fc.Invalidations())

	rr, err := disc.ServerPreferredResources()
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, l := range rr {
		for _, r := range l.APIResources {
			names[l.GroupVersion+"/"+r.Name] = true
		}
	}
	assert.True(t, names["v1/pods"])
	assert.True(t, names["apps/v1/deployments"])
	assert.False(t, names["apps/v1/deployments/scale"])

	apps, err := disc.ServerResourcesForGroupVersion("apps/v1")
	require.NoError(t, err)
	var scale bool
	for _, r := range apps.APIResources {
		scale = scale || r.Name == "deployments/scale"
	}
	assert.True(t, scale)

	v, err := fc.Conn().ServerVersion()
	require.NoError(t, err)
	assert.Equal(t, "v1.37.2", v.GitVersion)
}

func TestMetrics(t *testing.T) {
	fc := fakecluster.New(t)
	assert.False(t, fc.Conn().HasMetrics())

	fc = fakecluster.New(t, fakecluster.WithMetrics(
		&mxv1beta1.PodMetrics{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
		&mxv1beta1.NodeMetrics{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	))
	assert.True(t, fc.Conn().HasMetrics())
	mx, err := fc.Conn().MXDial()
	require.NoError(t, err)
	pl, err := mx.MetricsV1beta1().PodMetricses("ns1").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, pl.Items, 1)
	nl, err := mx.MetricsV1beta1().NodeMetricses().List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, nl.Items, 1)
}

func TestConnBasics(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithContext("other"))
	conn := fc.Conn()
	assert.True(t, conn.ConnectionOK())
	assert.True(t, conn.CheckConnectivity())
	assert.Equal(t, "other", conn.ActiveContext())
	assert.Equal(t, 5*time.Second, conn.Config().CallTimeout())

	require.NoError(t, conn.SwitchContext(fakecluster.DefaultContext))
	assert.Equal(t, fakecluster.DefaultContext, conn.ActiveContext())
	require.Error(t, conn.SwitchContext("missing"))

	cfg, err := conn.RestConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://127.0.0.1:1", cfg.Host)

	fc.SetConnected(false)
	assert.False(t, conn.ConnectionOK())
	assert.False(t, conn.CheckConnectivity())
}
