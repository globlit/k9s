// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config/data"
	"github.com/globlit/k9s/internal/render/helm"
	"github.com/pmezard/go-difflib/difflib"
	"helm.sh/helm/v3/pkg/action"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// UnifiedDiff compares plain text. Views escape and color the result at display time.
func UnifiedDiff(before, after, from, to string) (string, error) {
	if before == after {
		return "No differences.\n", nil
	}
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(before), B: difflib.SplitLines(after),
		FromFile: from, ToFile: to, Context: 3,
	})
}

// templateYAML removes controller bookkeeping from a copy of the pod template.
func templateYAML(template *corev1.PodTemplateSpec) ([]byte, error) {
	normalized := template.DeepCopy()
	delete(normalized.Labels, "pod-template-hash")
	for key := range normalized.Annotations {
		if strings.HasPrefix(key, "deployment.kubernetes.io/") {
			delete(normalized.Annotations, key)
		}
	}
	return yaml.Marshal(normalized)
}

// Diff compares two ReplicaSets, or one selected ReplicaSet with its Deployment's
// current template. A single selection is resolved within its namespace and owner UID.
func (r *ReplicaSet) Diff(ctx context.Context, paths []string) (string, string, error) {
	if len(paths) < 1 || len(paths) > 2 {
		return "", "", fmt.Errorf("select one or two ReplicaSets to diff")
	}
	paths = slices.Clone(paths)
	slices.Sort(paths)
	dial, err := r.Client().Dial()
	if err != nil {
		return "", "", err
	}
	namespace, name := client.Namespaced(paths[0])
	before, err := dial.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	var after *appsv1.ReplicaSet
	if len(paths) == 2 {
		namespace, name := client.Namespaced(paths[1])
		after, err = dial.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", "", err
		}
		// Marks are a set. Prefer revision order to keep additions on the right.
		a, aerr := strconv.ParseInt(before.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		b, berr := strconv.ParseInt(after.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		if aerr == nil && berr == nil && a > b {
			before, after = after, before
		}
	} else {
		owner := metav1.GetControllerOf(before)
		if owner == nil || owner.Kind != "Deployment" || !strings.HasPrefix(owner.APIVersion, "apps/") || owner.UID == "" {
			return "", "", fmt.Errorf("ReplicaSet %s has no Deployment controller; mark two ReplicaSets", before.Name)
		}
		deployment, e := dial.AppsV1().Deployments(before.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if e != nil {
			return "", "", e
		}
		if deployment.UID != owner.UID {
			return "", "", fmt.Errorf("Deployment owner changed; refresh the ReplicaSets")
		}
		selector, e := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		if e != nil {
			return "", "", e
		}
		objects, e := dial.AppsV1().ReplicaSets(before.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
		if e != nil {
			return "", "", e
		}
		candidates := make([]*appsv1.ReplicaSet, len(objects.Items))
		for i := range objects.Items {
			candidates[i] = &objects.Items[i]
		}
		after, err = currentReplicaSet(deployment, candidates)
		if err != nil {
			return "", "", err
		}
		if before.UID == after.UID {
			return "", "", fmt.Errorf("selected ReplicaSet is current; mark two revisions to compare")
		}
	}
	a, err := templateYAML(&before.Spec.Template)
	if err != nil {
		return "", "", err
	}
	b, err := templateYAML(&after.Spec.Template)
	if err != nil {
		return "", "", err
	}
	from, to := client.FQN(before.Namespace, before.Name), client.FQN(after.Namespace, after.Name)
	diff, err := UnifiedDiff(string(a), string(b), from, to)
	return diff, from + " → " + to, err
}

func currentReplicaSet(deployment *appsv1.Deployment, candidates []*appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	expected, err := templateYAML(&deployment.Spec.Template)
	if err != nil {
		return nil, err
	}
	var current *appsv1.ReplicaSet
	for _, rs := range candidates {
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.UID != deployment.UID || rs.Namespace != deployment.Namespace {
			continue
		}
		actual, e := templateYAML(&rs.Spec.Template)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(expected, actual) {
			continue
		}
		// Match Kubernetes' oldest matching template choice, with a stable name tie-break.
		if current == nil || rs.CreationTimestamp.Before(&current.CreationTimestamp) || (rs.CreationTimestamp.Equal(&current.CreationTimestamp) && rs.Name < current.Name) {
			current = rs
		}
	}
	if current == nil {
		return nil, fmt.Errorf("no ReplicaSet matches Deployment %s's current template", deployment.Name)
	}
	return current, nil
}

// HelmDiff holds both comparisons so the view can toggle without more API reads.
type HelmDiff struct{ Values, Manifest, Subject string }

// Diff compares selected Helm revisions; one selection uses the previous stored revision.
func (h *HelmHistory) Diff(ctx context.Context, paths []string) (HelmDiff, error) {
	var result HelmDiff
	if len(paths) < 1 || len(paths) > 2 {
		return result, fmt.Errorf("select one or two Helm revisions to diff")
	}
	from, err := h.Get(ctx, paths[0])
	if err != nil {
		return result, err
	}
	first, ok := from.(helm.ReleaseRes)
	if !ok {
		return result, fmt.Errorf("expected Helm release, got %T", from)
	}
	before, after := first.Release, first.Release
	if len(paths) == 2 {
		other, err := h.Get(ctx, paths[1])
		if err != nil {
			return result, err
		}
		second, ok := other.(helm.ReleaseRes)
		if !ok {
			return result, fmt.Errorf("expected Helm release, got %T", other)
		}
		after = second.Release
		if before.Name != after.Name || before.Namespace != after.Namespace {
			return result, fmt.Errorf("select revisions of the same Helm release")
		}
		if before.Version > after.Version {
			before, after = after, before
		}
	} else {
		// History may have gaps after pruning, so revision-1 is not necessarily stored.
		cfg, err := helmConfigFor(h.Client().Config().Flags(), after.Namespace)
		if err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		releases, err := action.NewHistory(cfg).Run(after.Name)
		if err != nil {
			return result, err
		}
		before = nil
		for _, release := range releases {
			if release.Version < after.Version && (before == nil || release.Version > before.Version) {
				before = release
			}
		}
		if before == nil {
			return result, fmt.Errorf("revision %d has no previous stored revision", after.Version)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	a, err := data.WriteYAML(before.Config)
	if err != nil {
		return result, err
	}
	b, err := data.WriteYAML(after.Config)
	if err != nil {
		return result, err
	}
	left, right := fmt.Sprintf("%s/%s:%d", before.Namespace, before.Name, before.Version), fmt.Sprintf("%s/%s:%d", after.Namespace, after.Name, after.Version)
	result.Subject = left + " → " + right
	result.Values, err = UnifiedDiff(string(a), string(b), left, right)
	if err != nil {
		return result, err
	}
	result.Manifest, err = UnifiedDiff(before.Manifest, after.Manifest, left, right)
	return result, err
}
