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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func phasePod(n string, ph corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
		Status:     corev1.PodStatus{Phase: ph},
	}
}

func waitingPod(n, reason string) *corev1.Pod {
	po := phasePod(n, corev1.PodRunning)
	po.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  "c1",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
	}}
	return po
}

func terminatedPod(n, reason string) *corev1.Pod {
	po := phasePod(n, corev1.PodFailed)
	po.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  "c1",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason}},
	}}
	return po
}

func sanitizePod(t *testing.T, fc *fakecluster.Cluster) *Pod {
	t.Helper()
	f := newFakeFactory(t, fc)
	f.Start("ns1")
	_, err := f.ForResource("ns1", client.PodGVR)
	require.NoError(t, err)
	f.WaitForCacheSync()
	var p Pod
	p.Init(f, client.PodGVR)

	return &p
}

func TestPodSanitizeIsGraceful(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		phasePod("pending", corev1.PodPending),
		phasePod("running", corev1.PodRunning),
	))

	n, err := sanitizePod(t, fc).Sanitize(context.Background(), "ns1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "pending", dd[0].Name)
	assert.Nil(t, dd[0].Grace)
}

func TestPodSanitizePhases(t *testing.T) {
	evicted := phasePod("evicted", corev1.PodFailed)
	evicted.Status.Reason = "Evicted"
	fc := fakecluster.New(t, fakecluster.WithObjects(
		phasePod("pending", corev1.PodPending),
		phasePod("running", corev1.PodRunning),
		phasePod("succeeded", corev1.PodSucceeded),
		phasePod("failed", corev1.PodFailed),
		evicted,
		waitingPod("crashloop", "CrashLoopBackOff"),
		waitingPod("pullbackoff", "ImagePullBackOff"),
		waitingPod("creating", "ContainerCreating"),
		terminatedPod("completed", "Completed"),
		terminatedPod("error", "Error"),
		terminatedPod("oom", "OOMKilled"),
		terminatedPod("unknown", "ContainerStatusUnknown"),
	))

	n, err := sanitizePod(t, fc).Sanitize(context.Background(), "ns1")

	require.NoError(t, err)
	names := make([]string, 0, n)
	for _, d := range fc.Deletes() {
		names = append(names, d.Name)
		assert.Nil(t, d.Grace)
		assert.Nil(t, d.Propagation)
	}
	assert.Len(t, names, n)
	assert.ElementsMatch(t, []string{
		"pending", "evicted", "crashloop", "pullbackoff", "completed", "error", "oom", "unknown",
	}, names)
}

func TestPodSanitizeStopsOnError(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(
			phasePod("p1", corev1.PodPending),
			phasePod("p2", corev1.PodPending),
			phasePod("p3", corev1.PodPending),
		),
		fakecluster.FailOn("delete", "pods", 2, errMut),
	)

	n, err := sanitizePod(t, fc).Sanitize(context.Background(), "ns1")

	require.ErrorIs(t, err, errMut)
	assert.Equal(t, 1, n)
	assert.Len(t, fc.Deletes(), 2)
}

func TestPodSanitizeDenied(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(phasePod("p1", corev1.PodPending)),
		fakecluster.WithRBAC(func(_ string, _ *client.GVR, _, verb string) bool { return verb != "delete" }),
	)

	n, err := sanitizePod(t, fc).Sanitize(context.Background(), "ns1")

	require.ErrorContains(t, err, "not authorized")
	assert.Zero(t, n)
	assert.Empty(t, fc.Deletes())
}

func TestPodSanitizeListDenied(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(phasePod("p1", corev1.PodPending)),
		fakecluster.DenyVerb("list"),
	)
	var p Pod
	p.Init(newFakeFactory(t, fc), client.PodGVR)

	_, err := p.Sanitize(context.Background(), "ns1")

	require.Error(t, err)
	assert.Empty(t, fc.Deletes())
}
