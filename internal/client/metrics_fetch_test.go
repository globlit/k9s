// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package client

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	mv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"k8s.io/metrics/pkg/client/clientset/versioned"
	mxfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

type mxConn struct {
	Connection
	mx      *mxfake.Clientset
	hasMx   bool
	allowed bool
	canErr  error
}

func (c *mxConn) HasMetrics() bool { return c.hasMx }
func (c *mxConn) CanI(string, *GVR, string, []string) (bool, error) {
	return c.allowed, c.canErr
}
func (c *mxConn) MXDial() (versioned.Interface, error) { return c.mx, nil }

func usage(cpu, mem string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
}

func newMxConn(oo ...runtime.Object) *mxConn {
	cs := mxfake.NewSimpleClientset()
	for _, o := range oo {
		switch m := o.(type) {
		case *mv1beta1.PodMetrics:
			_ = cs.Tracker().Create(PmxGVR.GVR(), m, m.Namespace)
		case *mv1beta1.NodeMetrics:
			_ = cs.Tracker().Create(NmxGVR.GVR(), m, "")
		}
	}

	return &mxConn{mx: cs, hasMx: true, allowed: true}
}

func podMx(ns, n string, cc ...string) *mv1beta1.PodMetrics {
	m := mv1beta1.PodMetrics{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n}}
	for _, c := range cc {
		m.Containers = append(m.Containers, mv1beta1.ContainerMetrics{Name: c, Usage: usage("10m", "1Mi")})
	}

	return &m
}

func nodeMx(n, cpu, mem string) *mv1beta1.NodeMetrics {
	return &mv1beta1.NodeMetrics{ObjectMeta: metav1.ObjectMeta{Name: n}, Usage: usage(cpu, mem)}
}

func countLists(cs *mxfake.Clientset, res string) int {
	var n int
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == res {
			n++
		}
	}

	return n
}

func TestFetchPodsMetrics(t *testing.T) {
	c := newMxConn(podMx("ns1", "p1", "c1", "c2"), podMx("ns1", "p2", "c1"), podMx("ns2", "p3", "c1"))
	m := NewMetricsServer(c)
	ctx := context.Background()

	l, err := m.FetchPodsMetrics(ctx, "ns1")
	require.NoError(t, err)
	assert.Len(t, l.Items, 2)
	l, err = m.FetchPodsMetrics(ctx, NamespaceAll)
	require.NoError(t, err)
	assert.Len(t, l.Items, 3)

	_, err = m.FetchPodsMetrics(ctx, "ns1")
	require.NoError(t, err)
	assert.Equal(t, 2, countLists(c.mx, "pods"), "serve repeat calls from the cache")

	mm, err := m.FetchPodsMetricsMap(ctx, "ns1")
	require.NoError(t, err)
	assert.Len(t, mm, 2)
	again, err := m.FetchPodsMetricsMap(ctx, "ns1")
	require.NoError(t, err)
	assert.Equal(t, mm, again)

	pmx, err := m.FetchPodMetrics(ctx, "ns1/p1")
	require.NoError(t, err)
	assert.Equal(t, "p1", pmx.Name)
	_, err = m.FetchPodMetrics(ctx, "ns1/zorg")
	require.Error(t, err)

	cmx, err := m.FetchContainersMetrics(ctx, "ns1/p1")
	require.NoError(t, err)
	assert.Len(t, cmx, 2)
	assert.Equal(t, int64(10), cmx["c2"].Usage.Cpu().MilliValue())
	_, err = m.FetchContainersMetrics(ctx, "ns1/zorg")
	require.Error(t, err)
}

func TestFetchNodesMetrics(t *testing.T) {
	c := newMxConn(nodeMx("n1", "100m", "1Gi"), nodeMx("n2", "200m", "2Gi"))
	m := NewMetricsServer(c)
	ctx := context.Background()

	l, err := m.FetchNodesMetrics(ctx)
	require.NoError(t, err)
	assert.Len(t, l.Items, 2)
	mm, err := m.FetchNodesMetricsMap(ctx)
	require.NoError(t, err)
	assert.Len(t, mm, 2)
	assert.Equal(t, 1, countLists(c.mx, "nodes"))

	nmx, err := m.FetchNodeMetrics(ctx, "n2")
	require.NoError(t, err)
	assert.Equal(t, int64(200), nmx.Usage.Cpu().MilliValue())
	_, err = m.FetchNodeMetrics(ctx, "zorg")
	require.Error(t, err)
}

func TestFetchMetricsPaging(t *testing.T) {
	c := newMxConn()
	pages := 0
	c.mx.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		pages++
		l := mv1beta1.NodeMetricsList{Items: []mv1beta1.NodeMetrics{*nodeMx("n"+strconv.Itoa(pages), "1m", "1Mi")}}
		if pages < 3 {
			l.Continue = "more"
		}
		return true, &l, nil
	})
	podPages := 0
	c.mx.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		podPages++
		l := mv1beta1.PodMetricsList{Items: []mv1beta1.PodMetrics{*podMx("ns1", "p"+strconv.Itoa(podPages), "c1")}}
		if podPages == 1 {
			l.Continue = "more"
		}
		return true, &l, nil
	})
	m := NewMetricsServer(c)

	l, err := m.FetchNodesMetrics(context.Background())
	require.NoError(t, err)
	assert.Len(t, l.Items, 3)
	assert.Equal(t, 3, pages)

	pl, err := m.FetchPodsMetrics(context.Background(), "ns1")
	require.NoError(t, err)
	assert.Len(t, pl.Items, 2)
}

func TestFetchMetricsGraceful(t *testing.T) {
	boom := errors.New("boom")
	uu := map[string]struct {
		conn *mxConn
		err  string
	}{
		"no-server": {
			conn: &mxConn{mx: mxfake.NewSimpleClientset()},
			err:  "no metrics-server",
		},
		"denied": {
			conn: &mxConn{mx: mxfake.NewSimpleClientset(), hasMx: true},
			err:  "not authorized",
		},
		"can-error": {
			conn: &mxConn{mx: mxfake.NewSimpleClientset(), hasMx: true, canErr: boom},
			err:  "boom",
		},
	}
	ctx := context.Background()
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			m := NewMetricsServer(u.conn)

			nl, err := m.FetchNodesMetrics(ctx)
			require.ErrorContains(t, err, u.err)
			assert.NotNil(t, nl)
			_, err = m.FetchNodesMetricsMap(ctx)
			require.ErrorContains(t, err, u.err)
			_, err = m.FetchNodeMetrics(ctx, "n1")
			require.ErrorContains(t, err, u.err)

			pl, err := m.FetchPodsMetrics(ctx, "ns1")
			require.ErrorContains(t, err, u.err)
			assert.NotNil(t, pl)
			_, err = m.FetchPodsMetricsMap(ctx, "ns1")
			require.ErrorContains(t, err, u.err)
			_, err = m.FetchPodMetrics(ctx, "ns1/p1")
			require.ErrorContains(t, err, u.err)
			_, err = m.FetchContainersMetrics(ctx, "ns1/p1")
			require.ErrorContains(t, err, u.err)
			assert.Empty(t, u.conn.mx.Actions())
		})
	}
}

func TestFetchMetricsListError(t *testing.T) {
	c := newMxConn()
	c.mx.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
	m := NewMetricsServer(c)

	_, err := m.FetchNodesMetrics(context.Background())
	require.ErrorContains(t, err, "boom")
	_, err = m.FetchPodsMetrics(context.Background(), "ns1")
	require.ErrorContains(t, err, "boom")
}

func TestDialMetricsSingleton(t *testing.T) {
	ResetMetrics()
	t.Cleanup(ResetMetrics)

	c := newMxConn()
	m := DialMetrics(c)
	assert.Same(t, m, DialMetrics(newMxConn()))
	ResetMetrics()
	assert.NotSame(t, m, DialMetrics(c))
}

func TestClusterLoadNoNodes(t *testing.T) {
	var (
		m  MetricsServer
		mx ClusterMetrics
	)
	require.Error(t, m.ClusterLoad(nil, &mv1beta1.NodeMetricsList{}, &mx))
	require.NoError(t, m.ClusterLoad(&corev1.NodeList{}, &mv1beta1.NodeMetricsList{}, &mx))
	assert.Zero(t, mx.PercCPU)
	assert.Zero(t, mx.PercMEM)
}

func TestToPercentageStr(t *testing.T) {
	assert.Equal(t, NA, ToPercentageStr(1, 0))
	assert.Equal(t, "50", ToPercentageStr(1, 2))
}
