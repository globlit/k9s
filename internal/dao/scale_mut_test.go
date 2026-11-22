// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/scale"
	fakescale "k8s.io/client-go/scale/fake"
	k8stesting "k8s.io/client-go/testing"
)

// scaleRecorder serves the scale subresource of the typed fake.
type scaleRecorder struct {
	replicas map[string]int32
	updates  []*autoscalingv1.Scale
}

func newScaleRecorder(fc *fakecluster.Cluster, resources ...string) *scaleRecorder {
	r := scaleRecorder{replicas: make(map[string]int32)}
	for _, res := range resources {
		fc.Typed().PrependReactor("get", res, func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() != "scale" {
				return false, nil, nil
			}
			n := a.(k8stesting.GetAction).GetName()
			return true, &autoscalingv1.Scale{
				ObjectMeta: metav1.ObjectMeta{Namespace: a.GetNamespace(), Name: n},
				Spec:       autoscalingv1.ScaleSpec{Replicas: r.replicas[n]},
			}, nil
		})
		fc.Typed().PrependReactor("update", res, func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() != "scale" {
				return false, nil, nil
			}
			s := a.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
			r.updates = append(r.updates, s.DeepCopy())
			r.replicas[s.Name] = s.Spec.Replicas
			return true, s, nil
		})
	}

	return &r
}

func mutDp(ns, n string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n}}
}

func mutSts(ns, n string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n}}
}

func TestScaleRes(t *testing.T) {
	type scaler interface {
		Init(Factory, *client.GVR)
		Scale(context.Context, string, int32) error
	}
	uu := map[string]struct {
		gvr *client.GVR
		res string
		s   scaler
	}{
		"dp":  {gvr: client.DpGVR, res: "deployments", s: new(Deployment)},
		"sts": {gvr: client.StsGVR, res: "statefulsets", s: new(StatefulSet)},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			var checked []string
			fc := fakecluster.New(t,
				fakecluster.WithObjects(mutDp("ns1", "a"), mutSts("ns1", "a")),
				fakecluster.WithRBAC(func(_ string, gvr *client.GVR, _, verb string) bool {
					checked = append(checked, gvr.String()+"|"+verb)
					return true
				}),
			)
			rec := newScaleRecorder(fc, u.res)
			rec.replicas["a"] = 3
			u.s.Init(mutFactory(t, fc.Conn()), u.gvr)

			for _, n := range []int32{0, 5} {
				require.NoError(t, u.s.Scale(context.Background(), "ns1/a", n))
				assert.Equal(t, n, rec.replicas["a"])
			}
			require.Len(t, rec.updates, 2)
			assert.Equal(t, "a", rec.updates[0].Name)
			assert.Equal(t, "ns1", rec.updates[0].Namespace)
			assert.Equal(t, []string{
				u.gvr.String() + ":scale|get", u.gvr.String() + ":scale|update",
				u.gvr.String() + ":scale|get", u.gvr.String() + ":scale|update",
			}, checked)
		})
	}
}

// Pin: scaleRes sends negative replicas to the API. The server rejects them.
func TestScaleResNegative(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutDp("ns1", "a")))
	rec := newScaleRecorder(fc, "deployments")
	var d Deployment
	d.Init(mutFactory(t, fc.Conn()), client.DpGVR)

	require.NoError(t, d.Scale(context.Background(), "ns1/a", -1))
	assert.Equal(t, int32(-1), rec.replicas["a"])
}

func TestScaleResErrors(t *testing.T) {
	uu := map[string]struct {
		gvr  *client.GVR
		opts []fakecluster.Option
		fail [2]string
		conn func(client.Connection) client.Connection
		err  string
	}{
		"deny-dp":    {gvr: client.DpGVR, opts: []fakecluster.Option{fakecluster.DenyVerb("update")}, err: "not authorized to scale"},
		"deny-sts":   {gvr: client.StsGVR, opts: []fakecluster.Option{fakecluster.DenyVerb("get")}, err: "not authorized to scale"},
		"get-dp":     {gvr: client.DpGVR, fail: [2]string{"get", "deployments"}, err: "boom"},
		"get-sts":    {gvr: client.StsGVR, fail: [2]string{"get", "statefulsets"}, err: "boom"},
		"update-dp":  {gvr: client.DpGVR, fail: [2]string{"update", "deployments"}, err: "boom"},
		"update-sts": {gvr: client.StsGVR, fail: [2]string{"update", "statefulsets"}, err: "boom"},
		"cani": {
			gvr:  client.DpGVR,
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, canIErr: errMut} },
			err:  "boom",
		},
		"dial": {
			gvr:  client.DpGVR,
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
		"unsupported": {gvr: client.RsGVR, err: "unsupported resource for scaling"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			opts := append([]fakecluster.Option{fakecluster.WithObjects(mutDp("ns1", "a"), mutSts("ns1", "a"))}, u.opts...)
			fc := fakecluster.New(t, opts...)
			newScaleRecorder(fc, "deployments", "statefulsets")
			if u.fail[0] != "" {
				fc.Typed().PrependReactor(u.fail[0], u.fail[1], func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errMut
				})
			}
			var conn client.Connection = fc.Conn()
			if u.conn != nil {
				conn = u.conn(conn)
			}
			f := mutFactory(t, conn)

			require.ErrorContains(t, scaleRes(context.Background(), f, u.gvr, "ns1/a", 2), u.err)
			for _, a := range fc.Actions("update", "*") {
				assert.NotEqual(t, "scale", a.GetSubresource())
			}
		})
	}
}

func useScales(t *testing.T, sg scale.ScalesGetter, err error) {
	t.Helper()
	old := newScalesGetter
	newScalesGetter = func(client.Connection) (scale.ScalesGetter, error) { return sg, err }
	t.Cleanup(func() { newScalesGetter = old })
}

func TestScalerScale(t *testing.T) {
	gvr := client.NewGVR("example.com/v1/widgets")
	uu := map[string]struct {
		replicas       int32
		getErr, updErr error
		sgErr          error
		err            string
		updated        bool
		expect         int32
	}{
		"up":         {replicas: 4, updated: true, expect: 4},
		"zero":       {replicas: 0, updated: true, expect: 0},
		"negative":   {replicas: -2, updated: true, expect: -2},
		"get-error":  {replicas: 1, getErr: errMut, err: "boom"},
		"upd-error":  {replicas: 1, updErr: errMut, err: "boom", updated: true, expect: 1},
		"client-err": {replicas: 1, sgErr: errMut, err: "boom"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			var (
				sc      fakescale.FakeScaleClient
				updates []*autoscalingv1.Scale
			)
			sc.AddReactor("get", "widgets", func(a k8stesting.Action) (bool, runtime.Object, error) {
				if u.getErr != nil {
					return true, nil, u.getErr
				}
				return true, &autoscalingv1.Scale{
					ObjectMeta: metav1.ObjectMeta{Namespace: a.GetNamespace(), Name: a.(k8stesting.GetAction).GetName()},
					Spec:       autoscalingv1.ScaleSpec{Replicas: 2},
				}, nil
			})
			sc.AddReactor("update", "widgets", func(a k8stesting.Action) (bool, runtime.Object, error) {
				s := a.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
				updates = append(updates, s)
				if u.updErr != nil {
					return true, nil, u.updErr
				}
				return true, s, nil
			})
			useScales(t, &sc, u.sgErr)
			var s Scaler
			s.Init(mutFactory(t, fakecluster.New(t).Conn()), gvr)

			err := s.Scale(context.Background(), "ns1/w1", u.replicas)
			if u.err != "" {
				require.ErrorContains(t, err, u.err)
			} else {
				require.NoError(t, err)
			}
			if !u.updated {
				assert.Empty(t, updates)
				return
			}
			require.Len(t, updates, 1)
			assert.Equal(t, u.expect, updates[0].Spec.Replicas)
			assert.Equal(t, "w1", updates[0].Name)
		})
	}
}

func TestScalerReplicas(t *testing.T) {
	var sc fakescale.FakeScaleClient
	sc.AddReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 7}}, nil
	})
	useScales(t, &sc, nil)
	var s Scaler
	s.Init(mutFactory(t, fakecluster.New(t).Conn()), client.NewGVR("example.com/v1/widgets"))

	n, err := s.Replicas(context.Background(), "ns1/w1")
	require.NoError(t, err)
	assert.Equal(t, int32(7), n)

	useScales(t, nil, errMut)
	_, err = s.Replicas(context.Background(), "ns1/w1")
	require.ErrorIs(t, err, errMut)
}
