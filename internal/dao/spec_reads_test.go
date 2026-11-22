// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao_test

import (
	"context"
	"slices"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var appSel = map[string]string{"app": "a"}

func refSpec() corev1.PodSpec {
	return corev1.PodSpec{
		ServiceAccountName: "sa1",
		PriorityClassName:  "pc1",
		ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "sec-pull"}},
		InitContainers:     []corev1.Container{{Name: "i1", Image: "busybox:1"}},
		Containers: []corev1.Container{{
			Name:    "c1",
			Image:   "nginx:1",
			EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "cm1"}}}},
			Env: []corev1.EnvVar{{Name: "E", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "sec-env"}, Key: "k"},
			}}},
		}},
		Volumes: []corev1.Volume{
			{Name: "v1", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc1"}}},
			{Name: "v2", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "sec-vol"}}},
			{Name: "v3", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "cm-vol"}}}},
		},
	}
}

func om(n string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: "ns1", Name: n, Labels: appSel}
}

func refObjects() []runtime.Object {
	tpl := corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: appSel}, Spec: refSpec()}
	sel := &metav1.LabelSelector{MatchLabels: appSel}
	return []runtime.Object{
		&corev1.ServiceAccount{ObjectMeta: om("sa1"), Secrets: []corev1.ObjectReference{{Namespace: "ns1", Name: "sec-sa"}}},
		&appsv1.Deployment{ObjectMeta: om("dp1"), Spec: appsv1.DeploymentSpec{Selector: sel, Template: tpl}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp2"}},
		&appsv1.DaemonSet{ObjectMeta: om("ds1"), Spec: appsv1.DaemonSetSpec{Selector: sel, Template: tpl}},
		&appsv1.StatefulSet{ObjectMeta: om("sts1"), Spec: appsv1.StatefulSetSpec{Selector: sel, Template: tpl}},
		&appsv1.ReplicaSet{ObjectMeta: om("rs1"), Spec: appsv1.ReplicaSetSpec{Selector: sel, Template: tpl}},
		&batchv1.Job{ObjectMeta: om("job1"), Spec: batchv1.JobSpec{Template: tpl}},
		&batchv1.CronJob{ObjectMeta: om("cj1"), Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: tpl}}}},
		&corev1.Pod{ObjectMeta: om("p1"), Spec: refSpec()},
		&corev1.Service{ObjectMeta: om("svc1"), Spec: corev1.ServiceSpec{Selector: appSel}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc2"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "zorg"}}},
	}
}

func newRefFactory(t *testing.T) *watch.Factory {
	t.Helper()
	_, f := newDataFactory(t, fakecluster.WithObjects(refObjects()...))

	return f
}

func refNames(t *testing.T, oo []runtime.Object) []string {
	t.Helper()
	nn := make([]string, 0, len(oo))
	for _, o := range oo {
		r, ok := o.(render.ReferenceRes)
		require.True(t, ok)
		nn = append(nn, r.GVR+":"+r.Name)
	}
	slices.Sort(nn)

	return nn
}

func TestReferenceScan(t *testing.T) {
	f := newRefFactory(t)

	all := []string{
		"apps/v1/daemonsets:ds1", "apps/v1/deployments:dp1", "apps/v1/statefulsets:sts1",
		"batch/v1/cronjobs:cj1", "batch/v1/jobs:job1",
	}
	uu := map[string]struct {
		gvr  *client.GVR
		path string
		e    []string
	}{
		"cm-env":     {gvr: client.CmGVR, path: "ns1/cm1", e: all},
		"cm-volume":  {gvr: client.CmGVR, path: "ns1/cm-vol", e: all},
		"sec-env":    {gvr: client.SecGVR, path: "ns1/sec-env", e: all},
		"sec-pull":   {gvr: client.SecGVR, path: "ns1/sec-pull", e: all},
		"sec-sa":     {gvr: client.SecGVR, path: "ns1/sec-sa", e: all},
		"sec-volume": {gvr: client.SecGVR, path: "ns1/sec-vol", e: all},
		"pc":         {gvr: client.PcGVR, path: "ns1/pc1", e: all},
		"pvc": {gvr: client.PvcGVR, path: "ns1/pvc1", e: []string{
			"apps/v1/daemonsets:ds1", "apps/v1/deployments:dp1", "apps/v1/statefulsets:sts1",
		}},
		"none":  {gvr: client.CmGVR, path: "ns1/zorg", e: []string{}},
		"other": {gvr: client.CmGVR, path: "ns2/cm1", e: []string{}},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			var r dao.Reference
			r.Init(f, client.RefGVR)
			ctx := context.WithValue(context.Background(), internal.KeyGVR, u.gvr)
			ctx = context.WithValue(ctx, internal.KeyPath, u.path)
			ctx = context.WithValue(ctx, internal.KeyWait, true)
			oo, err := r.List(ctx, "")
			require.NoError(t, err)
			assert.Equal(t, u.e, refNames(t, oo))
		})
	}
}

func TestReferenceScanSA(t *testing.T) {
	f := newRefFactory(t)

	var r dao.Reference
	r.Init(f, client.RefGVR)
	ctx := context.WithValue(context.Background(), internal.KeyGVR, client.SaGVR)
	ctx = context.WithValue(ctx, internal.KeyPath, "ns1/sa1")
	ctx = context.WithValue(ctx, internal.KeyWait, true)
	oo, err := r.List(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"apps/v1/daemonsets:ds1", "apps/v1/deployments:dp1", "apps/v1/statefulsets:sts1",
		"batch/v1/cronjobs:cj1", "batch/v1/jobs:job1",
	}, refNames(t, oo))

	ctx = context.WithValue(ctx, internal.KeyPath, "ns1/default")
	oo, err = r.List(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"apps/v1/deployments:dp2"}, refNames(t, oo))
}

func TestReferenceScanMissingContext(t *testing.T) {
	f := newRefFactory(t)
	var r dao.Reference
	r.Init(f, client.RefGVR)

	_, err := r.List(context.Background(), "")
	require.Error(t, err)
	ctx := context.WithValue(context.Background(), internal.KeyGVR, client.CmGVR)
	_, err = r.List(ctx, "")
	require.Error(t, err)
	ctx = context.WithValue(context.Background(), internal.KeyGVR, client.SaGVR)
	_, err = r.List(ctx, "")
	require.Error(t, err)
	ctx = context.WithValue(ctx, internal.KeyPath, "ns1/sa1")
	_, err = r.List(ctx, "")
	require.Error(t, err, "require the wait key")
}

func TestListImages(t *testing.T) {
	f := newRefFactory(t)
	e := []string{"nginx:1"}

	uu := map[string]struct {
		gvr  *client.GVR
		a    dao.ImageLister
		path string
	}{
		"dp":  {gvr: client.DpGVR, a: new(dao.Deployment), path: "ns1/dp1"},
		"ds":  {gvr: client.DsGVR, a: new(dao.DaemonSet), path: "ns1/ds1"},
		"sts": {gvr: client.StsGVR, a: new(dao.StatefulSet), path: "ns1/sts1"},
		"rs":  {gvr: client.RsGVR, a: new(dao.ReplicaSet), path: "ns1/rs1"},
		"job": {gvr: client.JobGVR, a: new(dao.Job), path: "ns1/job1"},
		"cj":  {gvr: client.CjGVR, a: new(dao.CronJob), path: "ns1/cj1"},
		"pod": {gvr: client.PodGVR, a: new(dao.Pod), path: "ns1/p1"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			u.a.(dao.Accessor).Init(f, u.gvr)
			ii, err := u.a.ListImages(context.Background(), u.path)
			require.NoError(t, err)
			slices.Sort(ii)
			assert.Equal(t, e, ii)

			_, err = u.a.ListImages(context.Background(), "ns1/zorg")
			require.Error(t, err)
		})
	}
}

func TestGetPodSpecAndPod(t *testing.T) {
	f := newRefFactory(t)

	type specPod interface {
		dao.Accessor
		GetPodSpec(string) (*corev1.PodSpec, error)
		Pod(string) (string, error)
	}
	uu := map[string]struct {
		gvr  *client.GVR
		a    specPod
		path string
	}{
		"dp":  {gvr: client.DpGVR, a: new(dao.Deployment), path: "ns1/dp1"},
		"ds":  {gvr: client.DsGVR, a: new(dao.DaemonSet), path: "ns1/ds1"},
		"sts": {gvr: client.StsGVR, a: new(dao.StatefulSet), path: "ns1/sts1"},
		"pod": {gvr: client.PodGVR, a: new(dao.Pod), path: "ns1/p1"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			u.a.Init(f, u.gvr)
			spec, err := u.a.GetPodSpec(u.path)
			require.NoError(t, err)
			assert.Equal(t, "sa1", spec.ServiceAccountName)
			_, err = u.a.GetPodSpec("ns1/zorg")
			require.Error(t, err)

			p, err := u.a.Pod(u.path)
			require.NoError(t, err)
			assert.Equal(t, "ns1/p1", p)
		})
	}
}

func TestServicePod(t *testing.T) {
	f := newRefFactory(t)
	var s dao.Service
	s.Init(f, client.SvcGVR)

	p, err := s.Pod("ns1/svc1")
	require.NoError(t, err)
	assert.Equal(t, "ns1/p1", p)

	_, err = s.Pod("ns1/svc2")
	require.ErrorContains(t, err, "no matching pods")
	_, err = s.Pod("ns1/zorg")
	require.Error(t, err)
}

func TestPodContainers(t *testing.T) {
	f := newRefFactory(t)
	var p dao.Pod
	p.Init(f, client.PodGVR)

	cc, err := p.Containers("ns1/p1", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"c1"}, cc)
	cc, err = p.Containers("ns1/p1", true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"i1", "c1"}, cc)
	_, err = p.Containers("ns1/zorg", true)
	require.Error(t, err)
}

func TestGetInstances(t *testing.T) {
	f := newRefFactory(t)

	var cj dao.CronJob
	cj.Init(f, client.CjGVR)
	o, err := cj.GetInstance("ns1/cj1")
	require.NoError(t, err)
	assert.Equal(t, "cj1", o.Name)

	var j dao.Job
	j.Init(f, client.JobGVR)
	jo, err := j.GetInstance("ns1/job1")
	require.NoError(t, err)
	assert.Equal(t, "job1", jo.Name)

	var rs dao.ReplicaSet
	ro, err := rs.Load(f, "ns1/rs1")
	require.NoError(t, err)
	assert.Equal(t, "rs1", ro.Name)
	_, err = rs.Load(f, "ns1/zorg")
	require.Error(t, err)

	var sts dao.StatefulSet
	so, err := sts.GetInstance(f, "ns1/sts1")
	require.NoError(t, err)
	assert.Equal(t, "sts1", so.Name)
}

func TestPodNoSelector(t *testing.T) {
	_, f := newDataFactory(t, fakecluster.WithObjects(
		&appsv1.Deployment{ObjectMeta: om("dp1")},
		&appsv1.DaemonSet{ObjectMeta: om("ds1"), Spec: appsv1.DaemonSetSpec{Selector: &metav1.LabelSelector{}}},
		&appsv1.StatefulSet{ObjectMeta: om("sts1")},
		&corev1.Pod{ObjectMeta: om("p1")},
	))
	uu := map[string]struct {
		gvr *client.GVR
		a   interface {
			dao.Accessor
			Pod(string) (string, error)
		}
		path string
	}{
		"dp":  {gvr: client.DpGVR, a: new(dao.Deployment), path: "ns1/dp1"},
		"ds":  {gvr: client.DsGVR, a: new(dao.DaemonSet), path: "ns1/ds1"},
		"sts": {gvr: client.StsGVR, a: new(dao.StatefulSet), path: "ns1/sts1"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			u.a.Init(f, u.gvr)
			var err error
			require.NotPanics(t, func() { _, err = u.a.Pod(u.path) })
			require.ErrorContains(t, err, "no valid selector found")
		})
	}
}
