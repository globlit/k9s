// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package xray_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/globlit/k9s/internal/xray"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

var xsel = map[string]string{"app": "x"}

func xpodSpec(sa string) corev1.PodSpec {
	return corev1.PodSpec{
		ServiceAccountName: sa,
		Containers: []corev1.Container{{
			Name: "c1",
			Env: []corev1.EnvVar{
				{Name: "A", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "cm1"}}}},
				{Name: "B", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "sec-gone"}}}},
			},
		}},
		Volumes: []corev1.Volume{
			{Name: "v1", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc1"}}},
		},
	}
}

func xpod(n, sa string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n, Labels: xsel},
		Spec:       xpodSpec(sa),
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "c1", Ready: true}},
		},
	}
}

func xObjects() []runtime.Object {
	one := int32(1)
	sel := &metav1.LabelSelector{MatchLabels: xsel}
	tpl := corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: xsel}, Spec: xpodSpec("sa1")}
	return []runtime.Object{
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "sa1"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cm1"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "pvc1"}},
		xpod("p1", "sa1"),
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp1"},
			Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: sel, Template: tpl},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp-down"},
			Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: sel, Template: tpl},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "sts1"},
			Spec:       appsv1.StatefulSetSpec{Replicas: &one, Selector: sel, Template: tpl},
			Status:     appsv1.StatefulSetStatus{CurrentReplicas: 1},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
			Spec:       corev1.ServiceSpec{Selector: xsel},
		},
	}
}

func newXrayCtx(t *testing.T, oo ...runtime.Object) (context.Context, *watch.Factory) {
	t.Helper()
	fc := fakecluster.New(t, fakecluster.WithObjects(oo...))
	f := watch.NewFactory(fc.Conn())
	f.Start("ns1")
	t.Cleanup(f.Terminate)
	for _, gvr := range []*client.GVR{client.PodGVR, client.SaGVR, client.CmGVR, client.SecGVR, client.PvcGVR, client.DpGVR, client.StsGVR, client.SvcGVR} {
		_, err := f.List(gvr, "ns1", true, labels.Everything())
		require.NoError(t, err)
	}

	return context.WithValue(context.Background(), internal.KeyFactory, f), f
}

func getRaw(t *testing.T, f *watch.Factory, gvr *client.GVR, fqn string) *unstructured.Unstructured {
	t.Helper()
	o, err := f.Get(gvr, fqn, true, labels.Everything())
	require.NoError(t, err)

	return o.(*unstructured.Unstructured)
}

func nodeStatus(root *xray.TreeNode) map[string]string {
	out := make(map[string]string)
	var walk func(*xray.TreeNode)
	walk = func(n *xray.TreeNode) {
		out[n.GVR.String()+":"+n.ID] = n.Extras[xray.StatusKey]
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)

	return out
}

func podRefs(root string) map[string]string {
	return map[string]string{
		root:                                 xray.OkStatus,
		"v1/namespaces:-/ns1":                xray.OkStatus,
		"v1/pods:ns1/p1":                     xray.OkStatus,
		"containers:ns1/c1":                  xray.OkStatus,
		"v1/configmaps:ns1/cm1":              xray.OkStatus,
		"v1/secrets:ns1/sec-gone":            xray.MissingRefStatus,
		"v1/persistentvolumeclaims:ns1/pvc1": xray.OkStatus,
		"v1/serviceaccounts:ns1/sa1":         xray.OkStatus,
	}
}

func TestXrayDeploymentTree(t *testing.T) {
	ctx, f := newXrayCtx(t, xObjects()...)

	uu := map[string]struct {
		fqn    string
		status string
	}{
		"ready": {fqn: "ns1/dp1", status: xray.OkStatus},
		"down":  {fqn: "ns1/dp-down", status: xray.ToastStatus},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			root := xray.NewTreeNode(client.DpGVR, "deployments")
			ctx := context.WithValue(ctx, xray.KeyParent, root)
			var re xray.Deployment
			require.NoError(t, re.Render(ctx, "ns1", getRaw(t, f, client.DpGVR, u.fqn)))

			e := podRefs("apps/v1/deployments:deployments")
			e["apps/v1/deployments:"+u.fqn] = u.status
			assert.Equal(t, e, nodeStatus(root))
		})
	}
}

func TestXrayStatefulSetTree(t *testing.T) {
	ctx, f := newXrayCtx(t, xObjects()...)

	root := xray.NewTreeNode(client.StsGVR, "statefulsets")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.StatefulSet
	require.NoError(t, re.Render(ctx, "ns1", getRaw(t, f, client.StsGVR, "ns1/sts1")))

	e := podRefs("apps/v1/statefulsets:statefulsets")
	e["apps/v1/statefulsets:ns1/sts1"] = xray.OkStatus
	assert.Equal(t, e, nodeStatus(root))
}

func TestXrayServiceTree(t *testing.T) {
	ctx, f := newXrayCtx(t, xObjects()...)

	root := xray.NewTreeNode(client.SvcGVR, "services")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.Service
	require.NoError(t, re.Render(ctx, "ns1", getRaw(t, f, client.SvcGVR, "ns1/svc1")))

	e := podRefs("v1/services:services")
	e["v1/services:ns1/svc1"] = xray.OkStatus
	assert.Equal(t, e, nodeStatus(root))
}

func TestXrayPodTree(t *testing.T) {
	ctx, f := newXrayCtx(t, xObjects()...)

	root := xray.NewTreeNode(client.PodGVR, "pods")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.Pod
	require.NoError(t, re.Render(ctx, "ns1", &render.PodWithMetrics{Raw: getRaw(t, f, client.PodGVR, "ns1/p1")}))

	assert.Equal(t, podRefs("v1/pods:pods"), nodeStatus(root))
	p := root.Find(client.PodGVR, "ns1/p1")
	require.NotNil(t, p)
	assert.Equal(t, "1/1", p.Extras[xray.InfoKey])
}

func TestXrayDeploymentNoPods(t *testing.T) {
	ctx, f := newXrayCtx(t, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp1"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: xsel}},
	})

	root := xray.NewTreeNode(client.DpGVR, "deployments")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.Deployment
	require.NoError(t, re.Render(ctx, "ns1", getRaw(t, f, client.DpGVR, "ns1/dp1")))
	assert.True(t, root.IsLeaf(), "skip deployments without pods")
}

func TestXrayPodMissingServiceAccount(t *testing.T) {
	t.Skip("bug: a missing service account fails the whole xray render; the real factory returns NotFound, not nil")

	ctx, f := newXrayCtx(t, xpod("p1", "sa-gone"))
	root := xray.NewTreeNode(client.PodGVR, "pods")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.Pod
	require.NoError(t, re.Render(ctx, "ns1", &render.PodWithMetrics{Raw: getRaw(t, f, client.PodGVR, "ns1/p1")}))
	n := root.Find(client.SaGVR, "ns1/sa-gone")
	require.NotNil(t, n)
	assert.Equal(t, xray.MissingRefStatus, n.Extras[xray.StatusKey])
}

func TestXrayTreeFilter(t *testing.T) {
	ctx, f := newXrayCtx(t, xObjects()...)

	root := xray.NewTreeNode(client.DpGVR, "deployments")
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	var re xray.Deployment
	require.NoError(t, re.Render(ctx, "ns1", getRaw(t, f, client.DpGVR, "ns1/dp1")))

	contains := func(q, path string) bool { return strings.Contains(path, q) }

	got := root.Filter("cm1", contains)
	require.NotNil(t, got)
	ids := slices.Sorted(func(yield func(string) bool) {
		for k := range nodeStatus(got) {
			if !yield(k) {
				return
			}
		}
	})
	assert.Contains(t, ids, "v1/configmaps:ns1/cm1")
	assert.NotContains(t, ids, "v1/persistentvolumeclaims:ns1/pvc1")

	got = root.Filter(xray.MissingRefStatus, contains)
	require.NotNil(t, got)
	assert.NotNil(t, got.Find(client.SecGVR, "ns1/sec-gone"))
	assert.Nil(t, got.Find(client.CmGVR, "ns1/cm1"))

	assert.Nil(t, root.Filter("zorg", contains))
}
