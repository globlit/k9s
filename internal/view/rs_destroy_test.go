// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func rollbackObjects() (*appsv1.Deployment, *appsv1.ReplicaSet) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "a"}}
	tpl := func(img string) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "a"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1", Image: img}}},
		}
	}
	dp := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1", Name: "dp-a", UID: types.UID("dp-a-uid"),
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Replicas: new(int32(1)), Selector: sel, Template: tpl("nginx:2")},
	}
	rs := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1", Name: "rs-a", Labels: map[string]string{"app": "a"},
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "dp-a",
				UID: dp.UID, Controller: new(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: new(int32(1)), Selector: sel, Template: tpl("nginx:1")},
	}

	return &dp, &rs
}

func rsTop(t *testing.T, s *SimApp) ResourceViewer {
	t.Helper()
	var v ResourceViewer
	s.onLoop(func() { v, _ = s.app.Content.Top().(ResourceViewer) })
	require.NotNil(t, v)

	return v
}

func TestReplicaSetRollbackHiddenInReadOnly(t *testing.T) {
	dp, rs := rollbackObjects()
	s := NewSimApp(t, fakecluster.New(t, fakecluster.WithObjects(dp, rs)), SimReadOnly(), SimView("rs"))
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "rs-a") }, "rs not shown")

	_, ok := rsTop(t, s).Actions().Get(tcell.KeyCtrlL)
	assert.False(t, ok)
}

func TestReplicaSetRollback(t *testing.T) {
	dp, rs := rollbackObjects()
	fc := fakecluster.New(t, fakecluster.WithObjects(dp, rs))
	s := NewSimApp(t, fc, SimView("rs"))
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "rs-a") }, "rs not shown")

	a, ok := rsTop(t, s).Actions().Get(tcell.KeyCtrlL)
	require.True(t, ok)
	assert.True(t, a.Opts.Dangerous)

	fc.Reset()
	s.Keys(tcell.KeyCtrlL)
	require.True(t, s.DialogOpen())
	s.Keys(tcell.KeyTab, tcell.KeyEnter)
	s.Eventually(func() bool { return len(fc.Actions("patch", "deployments")) == 1 }, "no rollback patch")
	s.Eventually(func() bool { return strings.Contains(s.Flash(), "successfully rolled back") }, "no success flash")
}
