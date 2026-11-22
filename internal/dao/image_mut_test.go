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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

type imageSetter interface {
	Init(Factory, *client.GVR)
	SetImages(context.Context, string, ImageSpecs) error
}

var imageFamilies = map[string]struct {
	gvr      *client.GVR
	res      string
	template bool
	kind     string
	new      func() imageSetter
}{
	"dp":  {gvr: client.DpGVR, res: "deployments", template: true, kind: "deployment", new: func() imageSetter { return new(Deployment) }},
	"ds":  {gvr: client.DsGVR, res: "daemonsets", template: true, kind: "daemonset", new: func() imageSetter { return new(DaemonSet) }},
	"sts": {gvr: client.StsGVR, res: "statefulsets", template: true, kind: "statefulset", new: func() imageSetter { return new(StatefulSet) }},
	"pod": {gvr: client.PodGVR, res: "pods", kind: "pod", new: func() imageSetter { return new(Pod) }},
}

func mutImageObjects(owned bool) []runtime.Object {
	po := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "a"},
		Spec:       mutTemplate().Spec,
	}
	if owned {
		po.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs1"}}
	}

	return append(mutWorkloads(), &po)
}

func TestSetImages(t *testing.T) {
	specs := ImageSpecs{
		{Name: "i1", DockerImage: "busybox:2", Init: true},
		{Name: "c2", DockerImage: "redis:2"},
	}
	for k, u := range imageFamilies {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(mutImageObjects(false)...))
			s := u.new()
			s.Init(mutFactory(t, fc.Conn()), u.gvr)

			require.NoError(t, s.SetImages(context.Background(), "ns1/a", specs))

			pp := fc.Actions("patch", u.res)
			require.Len(t, pp, 1)
			p := pp[0].(k8stesting.PatchAction)
			assert.Equal(t, "a", p.GetName())
			assert.Equal(t, types.StrategicMergePatchType, p.GetPatchType())
			var body map[string]any
			require.NoError(t, json.Unmarshal(p.GetPatch(), &body))
			spec := body["spec"].(map[string]any)
			if u.template {
				spec = spec["template"].(map[string]any)["spec"].(map[string]any)
			}
			assert.Equal(t, []any{map[string]any{"name": "i1", "image": "busybox:2"}}, spec["initContainers"])
			assert.Equal(t, []any{map[string]any{"name": "c2", "image": "redis:2"}}, spec["containers"])
			assert.Equal(t, []any{map[string]any{"name": "c2"}}, spec["$setElementOrder/containers"])
		})
	}
}

func TestSetImagesErrors(t *testing.T) {
	uu := map[string]struct {
		opts []fakecluster.Option
		conn func(client.Connection) client.Connection
		path string
		err  string
	}{
		"deny":  {opts: []fakecluster.Option{fakecluster.DenyVerb("patch")}, path: "ns1/a", err: "not authorized to patch"},
		"patch": {opts: []fakecluster.Option{fakecluster.FailOn("patch", "*", 0, errMut)}, path: "ns1/a", err: "boom"},
		"cani": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, canIErr: errMut} },
			path: "ns1/a", err: "boom",
		},
		"dial": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			path: "ns1/a", err: "boom",
		},
	}

	for fk, fam := range imageFamilies {
		for k, u := range uu {
			t.Run(fk+"/"+k, func(t *testing.T) {
				opts := append([]fakecluster.Option{fakecluster.WithObjects(mutImageObjects(false)...)}, u.opts...)
				fc := fakecluster.New(t, opts...)
				var conn client.Connection = fc.Conn()
				if u.conn != nil {
					conn = u.conn(conn)
				}
				s := fam.new()
				s.Init(mutFactory(t, conn), fam.gvr)

				err := s.SetImages(context.Background(), u.path, ImageSpecs{{Name: "c1", DockerImage: "x"}})
				require.ErrorContains(t, err, u.err)
				if k == "deny" {
					require.EqualError(t, err, "user is not authorized to patch a "+fam.kind)
				}
			})
		}
	}
}

func TestPodSetImagesRefusesOwnedPod(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutImageObjects(true)...))
	var p Pod
	p.Init(mutFactory(t, fc.Conn()), client.PodGVR)

	err := p.SetImages(context.Background(), "ns1/a", ImageSpecs{{Name: "c1", DockerImage: "x"}})

	require.ErrorContains(t, err, "managed by ReplicaSet/rs1")
	assert.Empty(t, fc.Actions("patch", "*"))

	err = p.SetImages(context.Background(), "ns1/nope", ImageSpecs{{Name: "c1", DockerImage: "x"}})
	require.ErrorContains(t, err, "not found")
	assert.Empty(t, fc.Actions("patch", "*"))
}
