// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

const revAnn = "deployment.kubernetes.io/revision"

func rollbackSet(mod func(*appsv1.Deployment, *appsv1.ReplicaSet)) []runtime.Object {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "a"}}
	tpl := func(img string) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "a"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1", Image: img}}},
		}
	}
	dp := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1", Name: "dp-a", UID: "dp-uid",
			Annotations: map[string]string{revAnn: "2"},
		},
		Spec: appsv1.DeploymentSpec{Replicas: new(int32(1)), Selector: sel, Template: tpl("nginx:2")},
	}
	rs := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1", Name: "rs-a", Labels: map[string]string{"app": "a"},
			Annotations: map[string]string{revAnn: "1"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "dp-a", UID: dp.UID, Controller: new(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: new(int32(1)), Selector: sel, Template: tpl("nginx:1")},
	}
	if mod != nil {
		mod(&dp, &rs)
	}

	return []runtime.Object{&dp, &rs}
}

func newRS(t *testing.T, conn client.Connection) *ReplicaSet {
	t.Helper()
	var r ReplicaSet
	r.Init(mutFactory(t, conn), client.RsGVR)

	return &r
}

func TestReplicaSetRollbackPatch(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(rollbackSet(nil)...))

	require.NoError(t, newRS(t, fc.Conn()).Rollback("ns1/rs-a"))

	pp := fc.Actions("patch", "deployments")
	require.Len(t, pp, 1)
	p := pp[0].(k8stesting.PatchAction)
	assert.Equal(t, "dp-a", p.GetName())
	assert.Equal(t, types.JSONPatchType, p.GetPatchType())
	assert.Contains(t, string(p.GetPatch()), "nginx:1")
}

func TestReplicaSetRollbackErrors(t *testing.T) {
	uu := map[string]struct {
		path string
		mod  func(*appsv1.Deployment, *appsv1.ReplicaSet)
		fc   []fakecluster.Option
		conn func(client.Connection) client.Connection
		err  string
	}{
		"missing-rs":   {path: "ns1/nope", err: "not found"},
		"no-revision":  {path: "ns1/rs-a", mod: func(_ *appsv1.Deployment, rs *appsv1.ReplicaSet) { rs.Annotations = nil }, err: "revision"},
		"no-owner":     {path: "ns1/rs-a", mod: func(_ *appsv1.Deployment, rs *appsv1.ReplicaSet) { rs.OwnerReferences = nil }, err: "unable to find controller"},
		"missing-dp":   {path: "ns1/rs-a", mod: func(dp *appsv1.Deployment, _ *appsv1.ReplicaSet) { dp.Name = "other" }, err: "not found"},
		"patch":        {path: "ns1/rs-a", fc: []fakecluster.Option{fakecluster.FailOn("patch", "deployments", 0, errMut)}, err: "boom"},
		"deny-get":     {path: "ns1/rs-a", fc: []fakecluster.Option{fakecluster.DenyVerb("get")}, err: "denied"},
		"bad-revision": {path: "ns1/rs-a", mod: func(_ *appsv1.Deployment, rs *appsv1.ReplicaSet) { rs.Annotations[revAnn] = "x" }, err: "revision conversion failed"},
		"other-kind": {
			path: "ns1/rs-a",
			mod: func(_ *appsv1.Deployment, rs *appsv1.ReplicaSet) {
				rs.OwnerReferences[0].APIVersion, rs.OwnerReferences[0].Kind = "example.com/v1", "Rollout"
			},
			err: "error retrieving rollbacker",
		},
		"dial": {
			path: "ns1/rs-a",
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, append([]fakecluster.Option{fakecluster.WithObjects(rollbackSet(u.mod)...)}, u.fc...)...)
			var conn client.Connection = fc.Conn()
			if u.conn != nil {
				conn = u.conn(conn)
			}

			require.ErrorContains(t, newRS(t, conn).Rollback(u.path), u.err)
		})
	}
}
