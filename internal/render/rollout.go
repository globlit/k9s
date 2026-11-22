// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const colRollout = "ROLLOUT"

func desiredReplicas(replicas *int32) int32 {
	if replicas == nil {
		return 1
	}
	return *replicas
}

func deploymentRollout(dp *appsv1.Deployment) string {
	if dp.Spec.Paused {
		return "Paused"
	}
	if dp.Status.ObservedGeneration < dp.Generation {
		return "Pending"
	}
	for _, c := range dp.Status.Conditions {
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue && quotaFailure(c.Reason, c.Message) {
			return "QuotaExceeded"
		}
	}
	for _, c := range dp.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse && c.Reason == "ProgressDeadlineExceeded" {
			return "Stalled"
		}
	}
	desired := desiredReplicas(dp.Spec.Replicas)
	if dp.Status.UpdatedReplicas == desired && dp.Status.Replicas == desired && dp.Status.AvailableReplicas == desired {
		return "Complete"
	}
	return rolloutProgress(dp.Status.UpdatedReplicas, desired)
}

func statefulSetRollout(sts *appsv1.StatefulSet) string {
	if sts.Status.ObservedGeneration < sts.Generation {
		return "Pending"
	}
	for _, c := range sts.Status.Conditions {
		if c.Status == corev1.ConditionTrue && quotaFailure(c.Reason, c.Message) {
			return "QuotaExceeded"
		}
	}
	desired := desiredReplicas(sts.Spec.Replicas)
	target := desired
	if sts.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		if rolling := sts.Spec.UpdateStrategy.RollingUpdate; rolling != nil && rolling.Partition != nil {
			// Partitions are pod ordinals, which may start above zero.
			start := int32(0)
			if sts.Spec.Ordinals != nil {
				start = sts.Spec.Ordinals.Start
			}
			target = max(0, min(desired, desired+start-*rolling.Partition))
		}
	}
	ready := sts.Status.ReadyReplicas == desired && sts.Status.Replicas == desired
	revisionsMatch := sts.Status.CurrentRevision == sts.Status.UpdateRevision
	if ready && sts.Status.UpdatedReplicas >= target && (desired == 0 || target < desired || revisionsMatch) {
		return "Complete"
	}
	// OnDelete revisions require manual pod replacement; do not imply automatic progress.
	if sts.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
		return fmt.Sprintf("OnDelete %d/%d", sts.Status.UpdatedReplicas, desired)
	}
	return rolloutProgress(min(sts.Status.UpdatedReplicas, target), target)
}

func daemonSetRollout(ds *appsv1.DaemonSet) string {
	if ds.Status.ObservedGeneration < ds.Generation {
		return "Pending"
	}
	for _, c := range ds.Status.Conditions {
		if c.Status == corev1.ConditionTrue && quotaFailure(c.Reason, c.Message) {
			return "QuotaExceeded"
		}
	}
	desired := ds.Status.DesiredNumberScheduled
	if ds.Status.UpdatedNumberScheduled == desired && ds.Status.CurrentNumberScheduled == desired && ds.Status.NumberAvailable == desired && ds.Status.NumberUnavailable == 0 {
		return "Complete"
	}
	if ds.Spec.UpdateStrategy.Type == appsv1.OnDeleteDaemonSetStrategyType {
		return fmt.Sprintf("OnDelete %d/%d", ds.Status.UpdatedNumberScheduled, desired)
	}
	return rolloutProgress(ds.Status.UpdatedNumberScheduled, desired)
}

func rolloutProgress(updated, desired int32) string {
	return fmt.Sprintf("Progressing %d/%d", updated, desired)
}

func quotaFailure(reason, message string) bool {
	return reason == "FailedCreate" && strings.Contains(strings.ToLower(message), "exceeded quota")
}

func rolloutError(state string) error {
	switch state {
	case "Stalled", "QuotaExceeded":
		return fmt.Errorf("rollout %s", state)
	}
	return nil
}
