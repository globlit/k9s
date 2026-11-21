// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"encoding/json"
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

func mutTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "a"}},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "i1", Image: "busybox:1"}},
			Containers:     []corev1.Container{{Name: "c1", Image: "nginx:1"}, {Name: "c2", Image: "redis:1"}},
		},
	}
}

func mutWorkloads() []runtime.Object {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "a"}}
	om := metav1.ObjectMeta{Namespace: "ns1", Name: "a"}
	return []runtime.Object{
		&appsv1.Deployment{ObjectMeta: om, Spec: appsv1.DeploymentSpec{Selector: sel, Template: mutTemplate()}},
		&appsv1.DaemonSet{ObjectMeta: om, Spec: appsv1.DaemonSetSpec{Selector: sel, Template: mutTemplate()}},
		&appsv1.StatefulSet{ObjectMeta: om, Spec: appsv1.StatefulSetSpec{Selector: sel, Template: mutTemplate()}},
	}
}

type restarter interface {
	Init(Factory, *client.GVR)
	Restart(context.Context, string, *metav1.PatchOptions) error
}

var restartFamilies = map[string]struct {
	gvr *client.GVR
	res string
	new func() restarter
}{
	"dp":  {gvr: client.DpGVR, res: "deployments", new: func() restarter { return new(Deployment) }},
	"ds":  {gvr: client.DsGVR, res: "daemonsets", new: func() restarter { return new(DaemonSet) }},
	"sts": {gvr: client.StsGVR, res: "statefulsets", new: func() restarter { return new(StatefulSet) }},
}

func TestRestart(t *testing.T) {
	for k, u := range restartFamilies {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(mutWorkloads()...))
			r := u.new()
			r.Init(mutFactory(t, fc.Conn()), u.gvr)

			require.NoError(t, r.Restart(context.Background(), "ns1/a", &metav1.PatchOptions{FieldManager: "k9s-x"}))

			pp := fc.Actions("patch", u.res)
			require.Len(t, pp, 1)
			p := pp[0].(k8stesting.PatchAction)
			assert.Equal(t, "a", p.GetName())
			assert.Equal(t, "ns1", p.GetNamespace())
			assert.Equal(t, types.StrategicMergePatchType, p.GetPatchType())
			var body struct {
				Spec struct {
					Template struct {
						Metadata struct {
							Annotations map[string]string `json:"annotations"`
						} `json:"metadata"`
					} `json:"template"`
				} `json:"spec"`
			}
			require.NoError(t, json.Unmarshal(p.GetPatch(), &body))
			assert.NotEmpty(t, body.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"])
		})
	}
}

func TestRestartErrors(t *testing.T) {
	uu := map[string]struct {
		path string
		opts []fakecluster.Option
		conn func(client.Connection) client.Connection
		err  string
	}{
		"deny-patch": {path: "ns1/a", opts: []fakecluster.Option{fakecluster.DenyVerb("patch")}, err: "not authorized to restart"},
		"deny-get":   {path: "ns1/a", opts: []fakecluster.Option{fakecluster.DenyVerb("get")}, err: "access denied"},
		"missing":    {path: "ns1/nope", err: "not found"},
		"patch":      {path: "ns1/a", opts: []fakecluster.Option{fakecluster.FailOn("patch", "*", 0, errMut)}, err: "boom"},
		"dial": {
			path: "ns1/a",
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
		"cani-patch": {
			path: "ns1/a",
			conn: func(c client.Connection) client.Connection {
				return &mutConn{Connection: c, canIErr: errMut, canIVerb: "patch"}
			},
			err: "boom",
		},
	}

	for fk, fam := range restartFamilies {
		for k, u := range uu {
			t.Run(fk+"/"+k, func(t *testing.T) {
				opts := append([]fakecluster.Option{fakecluster.WithObjects(mutWorkloads()...)}, u.opts...)
				fc := fakecluster.New(t, opts...)
				var conn client.Connection = fc.Conn()
				if u.conn != nil {
					conn = u.conn(conn)
				}
				r := fam.new()
				r.Init(mutFactory(t, conn), fam.gvr)

				require.ErrorContains(t, r.Restart(context.Background(), u.path, &metav1.PatchOptions{}), u.err)
				if k != "patch" {
					assert.Empty(t, fc.Actions("patch", "*"))
				}
			})
		}
	}
}

func TestRestartPausedDeployment(t *testing.T) {
	dp := mutWorkloads()[0].(*appsv1.Deployment)
	dp.Spec.Paused = true
	fc := fakecluster.New(t, fakecluster.WithObjects(dp))
	var d Deployment
	d.Init(mutFactory(t, fc.Conn()), client.DpGVR)

	require.ErrorContains(t, d.Restart(context.Background(), "ns1/a", &metav1.PatchOptions{}), "paused")
	assert.Empty(t, fc.Actions("patch", "*"))
}

// Pin: restartRes skips resources it does not know and reports no error.
func TestRestartUnsupported(t *testing.T) {
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "a"}}
	fc := fakecluster.New(t, fakecluster.WithObjects(append(mutWorkloads(), rs)...))
	f := mutFactory(t, fc.Conn())

	require.NoError(t, restartRes[*appsv1.Deployment](context.Background(), f, client.RsGVR, "ns1/a", &metav1.PatchOptions{}))
	assert.Empty(t, fc.Actions("patch", "*"))
}
