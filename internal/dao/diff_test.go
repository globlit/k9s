// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func diffTemplate(image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: image}}}}
}

func diffReplica(name, image, revision string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1", UID: types.UID(name), Labels: map[string]string{"app": "web"}, Annotations: map[string]string{"deployment.kubernetes.io/revision": revision}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "owner", Controller: new(true)}}}, Spec: appsv1.ReplicaSetSpec{Template: diffTemplate(image)}}
}

func TestTemplateDiff(t *testing.T) {
	before := diffTemplate("nginx:1")
	after := before.DeepCopy()
	after.Labels["pod-template-hash"] = "abc"
	after.Annotations = map[string]string{"deployment.kubernetes.io/revision": "7"}
	a, err := templateYAML(&before)
	require.NoError(t, err)
	b, err := templateYAML(after)
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
	assert.Equal(t, "abc", after.Labels["pod-template-hash"], "must not mutate cached objects")
	after.Spec.Containers[0].Image = "nginx:2"
	b, err = templateYAML(after)
	require.NoError(t, err)
	text, err := UnifiedDiff(string(a), string(b), "old", "new")
	require.NoError(t, err)
	assert.Contains(t, text, "--- old\n+++ new\n@@")
	assert.Contains(t, text, "-  - image: nginx:1\n+  - image: nginx:2")
	text, err = UnifiedDiff(string(a), string(a), "old", "new")
	require.NoError(t, err)
	assert.Equal(t, "No differences.\n", text)
}

func TestCurrentReplicaSet(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns1", UID: "owner"}, Spec: appsv1.DeploymentSpec{Template: diffTemplate("nginx:2")}}
	old := diffReplica("old", "nginx:1", "1")
	current := diffReplica("current", "nginx:2", "2")
	unrelated := diffReplica("unrelated", "nginx:2", "99")
	unrelated.OwnerReferences[0].UID = "other-owner"
	current.Spec.Template.Labels["pod-template-hash"] = "current-hash"
	got, err := currentReplicaSet(deployment, []*appsv1.ReplicaSet{unrelated, old, current})
	require.NoError(t, err)
	assert.Same(t, current, got)
	duplicate := current.DeepCopy()
	duplicate.Name = "duplicate"
	duplicate.CreationTimestamp = metav1.NewTime(time.Now())
	got, err = currentReplicaSet(deployment, []*appsv1.ReplicaSet{duplicate, current})
	require.NoError(t, err)
	assert.Same(t, current, got)
	_, err = currentReplicaSet(deployment, []*appsv1.ReplicaSet{old, unrelated})
	require.ErrorContains(t, err, "no ReplicaSet matches")
}

func TestReplicaSetDiff(t *testing.T) {
	old, current := diffReplica("old", "nginx:1", "1"), diffReplica("current", "nginx:2", "2")
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns1", UID: "owner"}, Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, Template: diffTemplate("nginx:2")}}
	fc := fakecluster.New(t, fakecluster.WithObjects(deployment, old, current))
	var resource ReplicaSet
	resource.Init(newFakeFactory(t, fc), client.RsGVR)
	text, subject, err := resource.Diff(context.Background(), []string{"ns1/old"})
	require.NoError(t, err)
	assert.Equal(t, "ns1/old → ns1/current", subject)
	assert.Contains(t, text, "-  - image: nginx:1")
	text, subject, err = resource.Diff(context.Background(), []string{"ns1/current", "ns1/old"})
	require.NoError(t, err)
	assert.Equal(t, "ns1/old → ns1/current", subject)
	assert.Contains(t, text, "+  - image: nginx:2")
	_, _, err = resource.Diff(context.Background(), []string{"ns1/current"})
	require.ErrorContains(t, err, "is current")
	_, _, err = resource.Diff(context.Background(), []string{"ns1/missing"})
	require.Error(t, err)
	_, _, err = resource.Diff(context.Background(), []string{"a", "b", "c"})
	require.Error(t, err)
}

func TestHelmRevisionDiff(t *testing.T) {
	fake, f := useHelmFake(t)
	revisions := fake.Seed(t, "demo", "ns1", 3)
	for i, r := range revisions {
		r.Config = map[string]any{"message": []string{"one", "two", "three"}[i]}
		r.Manifest = "message: " + r.Config["message"].(string) + "\n"
		require.NoError(t, fake.Cfg.Releases.Update(r))
	}
	var history HelmHistory
	history.Init(f, client.NewGVR("helm-history"))
	result, err := history.Diff(context.Background(), []string{"ns1/demo:3"})
	require.NoError(t, err)
	assert.Equal(t, "ns1/demo:2 → ns1/demo:3", result.Subject)
	assert.Contains(t, result.Values, "-message: two\n+message: three")
	assert.Contains(t, result.Manifest, "-message: two\n+message: three")
	result, err = history.Diff(context.Background(), []string{"ns1/demo:3", "ns1/demo:1"})
	require.NoError(t, err)
	assert.Equal(t, "ns1/demo:1 → ns1/demo:3", result.Subject)
	_, err = history.Diff(context.Background(), []string{"ns1/demo:1"})
	require.ErrorContains(t, err, "no previous stored revision")
	_, err = fake.Cfg.Releases.Delete("demo", 2)
	require.NoError(t, err)
	result, err = history.Diff(context.Background(), []string{"ns1/demo:3"})
	require.NoError(t, err)
	assert.Equal(t, "ns1/demo:1 → ns1/demo:3", result.Subject)
	fake.Seed(t, "other", "ns1", 1)
	_, err = history.Diff(context.Background(), []string{"ns1/demo:3", "ns1/other:1"})
	require.ErrorContains(t, err, "same Helm release")
}
