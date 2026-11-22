// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"testing"

	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestDeploymentRollout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment)
		want   string
		fault  bool
	}{
		{"complete", func(*appsv1.Deployment) {}, "Complete", false},
		{"default replicas", func(dp *appsv1.Deployment) { dp.Spec.Replicas = nil }, "Complete", false},
		{"pending", func(dp *appsv1.Deployment) { dp.Generation = 2 }, "Pending", false},
		{"paused", func(dp *appsv1.Deployment) { dp.Spec.Paused = true }, "Paused", false},
		{"progress", func(dp *appsv1.Deployment) { dp.Status.UpdatedReplicas = 0 }, "Progressing 0/1", false},
		{"old replicas", func(dp *appsv1.Deployment) { dp.Status.Replicas = 2 }, "Progressing 1/1", true},
		{"stalled", func(dp *appsv1.Deployment) {
			dp.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		}, "Stalled", true},
		{"quota", func(dp *appsv1.Deployment) {
			dp.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "pods exceeded quota: tiny"}}
		}, "QuotaExceeded", true},
		{"old failure", func(dp *appsv1.Deployment) {
			dp.Generation = 2
			dp.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "exceeded quota"}}
		}, "Pending", false},
		{"cleared failure", func(dp *appsv1.Deployment) {
			dp.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionFalse, Reason: "FailedCreate", Message: "exceeded quota"}}
		}, "Complete", false},
		{"scaled zero", func(dp *appsv1.Deployment) {
			dp.Spec.Replicas = ptr.To(int32(0))
			dp.Status.Replicas = 0
			dp.Status.AvailableReplicas = 0
			dp.Status.UpdatedReplicas = 0
		}, "Complete", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dp := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}}
			tc.change(dp)
			assert.Equal(t, tc.want, deploymentRollout(dp))
			assert.Equal(t, tc.fault, (Deployment{}).diagnose(dp) != nil)
			checkRolloutRow(t, &Deployment{}, dp, tc.want, tc.fault)
		})
	}
}

func TestStatefulSetRollout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.StatefulSet)
		want   string
	}{
		{"complete", func(*appsv1.StatefulSet) {}, "Complete"},
		{"pending", func(sts *appsv1.StatefulSet) { sts.Generation = 2 }, "Pending"},
		{"scaled zero", func(sts *appsv1.StatefulSet) {
			sts.Spec.Replicas = ptr.To(int32(0))
			sts.Status.Replicas = 0
			sts.Status.ReadyReplicas = 0
			sts.Status.UpdatedReplicas = 0
			sts.Status.CurrentRevision = "old"
		}, "Complete"},
		{"quota", func(sts *appsv1.StatefulSet) {
			sts.Status.Conditions = []appsv1.StatefulSetCondition{{Type: "ReplicaFailure", Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "exceeded quota"}}
		}, "QuotaExceeded"},
		{"old revision", func(sts *appsv1.StatefulSet) { sts.Status.CurrentRevision = "old" }, "Progressing 3/3"},
		{"partition", func(sts *appsv1.StatefulSet) {
			sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(2))}
			sts.Status.CurrentRevision = "old"
			sts.Status.UpdatedReplicas = 1
		}, "Complete"},
		{"partition waits for readiness", func(sts *appsv1.StatefulSet) {
			sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(2))}
			sts.Status.ReadyReplicas = 2
		}, "Progressing 1/1"},
		{"partition above replicas", func(sts *appsv1.StatefulSet) {
			sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(8))}
			sts.Status.CurrentRevision = "old"
			sts.Status.UpdatedReplicas = 0
		}, "Complete"},
		{"ordinal partition", func(sts *appsv1.StatefulSet) {
			sts.Spec.Ordinals = &appsv1.StatefulSetOrdinals{Start: 10}
			sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(11))}
			sts.Status.CurrentRevision = "old"
			sts.Status.UpdatedReplicas = 1
		}, "Progressing 1/2"},
		{"on delete", func(sts *appsv1.StatefulSet) {
			sts.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType
			sts.Status.CurrentRevision = "old"
			sts.Status.UpdatedReplicas = 1
		}, "OnDelete 1/3"},
		{"not ready", func(sts *appsv1.StatefulSet) { sts.Status.ReadyReplicas = 2 }, "Progressing 3/3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To(int32(3))}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 1, Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 3, CurrentRevision: "new", UpdateRevision: "new"}}
			tc.change(sts)
			assert.Equal(t, tc.want, statefulSetRollout(sts))
			checkRolloutRow(t, &StatefulSet{}, sts, tc.want, tc.want == "QuotaExceeded" || sts.Status.ReadyReplicas != desiredReplicas(sts.Spec.Replicas))
		})
	}
}

func TestDaemonSetRollout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.DaemonSet)
		want   string
	}{
		{"complete", func(*appsv1.DaemonSet) {}, "Complete"},
		{"pending", func(ds *appsv1.DaemonSet) { ds.Generation = 2 }, "Pending"},
		{"progress", func(ds *appsv1.DaemonSet) { ds.Status.UpdatedNumberScheduled = 1 }, "Progressing 1/2"},
		{"quota", func(ds *appsv1.DaemonSet) {
			ds.Status.Conditions = []appsv1.DaemonSetCondition{{Type: "ReplicaFailure", Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "exceeded quota"}}
		}, "QuotaExceeded"},
		{"unavailable", func(ds *appsv1.DaemonSet) { ds.Status.NumberUnavailable = 1 }, "Progressing 2/2"},
		{"not available", func(ds *appsv1.DaemonSet) { ds.Status.NumberAvailable = 1 }, "Progressing 2/2"},
		{"on delete", func(ds *appsv1.DaemonSet) {
			ds.Spec.UpdateStrategy.Type = appsv1.OnDeleteDaemonSetStrategyType
			ds.Status.UpdatedNumberScheduled = 1
		}, "OnDelete 1/2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Status: appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 2, CurrentNumberScheduled: 2, NumberReady: 2, UpdatedNumberScheduled: 2, NumberAvailable: 2}}
			tc.change(ds)
			assert.Equal(t, tc.want, daemonSetRollout(ds))
			checkRolloutRow(t, &DaemonSet{}, ds, tc.want, tc.want == "QuotaExceeded")
		})
	}
}

func checkRolloutRow(t *testing.T, renderer model1.Renderer, obj any, want string, fault bool) {
	t.Helper()
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	require.NoError(t, err)
	var row model1.Row
	require.NoError(t, renderer.Render(&unstructured.Unstructured{Object: data}, "", &row))
	header := renderer.Header("")
	require.Len(t, row.Fields, len(header))
	idx, ok := header.IndexOf(colRollout, true)
	require.True(t, ok)
	assert.Equal(t, want, row.Fields[idx])
	assert.Equal(t, !fault, model1.IsValid("", header, row))
}

func TestDeploymentFaultColorWinsOverZeroReplicas(t *testing.T) {
	oldError, oldPending := model1.ErrColor, model1.PendingColor
	model1.ErrColor, model1.PendingColor = tcell.ColorRed, tcell.ColorYellow
	t.Cleanup(func() { model1.ErrColor, model1.PendingColor = oldError, oldPending })
	header := model1.Header{{Name: "READY"}, {Name: "VALID"}}
	row := model1.RowEvent{Row: model1.Row{Fields: model1.Fields{"0/0", "rollout Stalled"}}}
	assert.Equal(t, tcell.ColorRed, (Deployment{}).ColorerFunc()("", header, &row))
}
