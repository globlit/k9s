// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func simPod(ns, n string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1", Image: "nginx"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestSimAppStartsOnPods(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a"), simPod("ns2", "pod-b")))
	s := NewSimApp(t, fc)

	assert.Equal(t, "Pod", s.TopView())
	s.Eventually(func() bool {
		scr := s.Screen()
		return strings.Contains(scr, "pod-a") && strings.Contains(scr, "pod-b")
	}, "seeded pods not shown")
}

func TestSimAppNavigatesToDeployments(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp-a"}},
	))
	s := NewSimApp(t, fc)

	s.Cmd(":deploy")
	s.Eventually(func() bool { return s.TopView() == "Deployment" }, "no deployments view")
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "dp-a") }, "deployment not shown")
}

func TestSimAppDeletePodOrphan(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	s := NewSimApp(t, fc)
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-a") }, "pod not shown")
	fc.Reset()

	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Orphan", false)
	s.Eventually(func() bool { return len(fc.Deletes()) > 0 }, "no delete call")

	orphan := metav1.DeletePropagationOrphan
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, fakecluster.DeleteCall{
		GVR:         client.PodGVR.GVR(),
		NS:          "ns1",
		Name:        "pod-a",
		Propagation: &orphan,
		Source:      dd[0].Source,
	}, dd[0])
	assert.False(t, s.DialogOpen())
	assert.Equal(t, -1, s.ExitCode())
}

func TestSimAppQuitUsesExitFunc(t *testing.T) {
	s := NewSimApp(t, fakecluster.New(t))

	s.Cmd(":q")
	s.Eventually(s.Stopped, "app still running")
	assert.Equal(t, 0, s.ExitCode())
}
