// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao_test

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

// Use one nonmatching pod to make incorrect selection deterministic.
func TestWorkloadPodHonorsFullSelector(t *testing.T) {
	for _, kind := range []string{"deployment", "daemonset", "statefulset"} {
		for _, style := range []string{"labels", "expressions", "combined", "invalid"} {
			for _, matches := range []bool{true, false} {
				name := "match"
				if !matches {
					name = "nonmatch"
				}
				t.Run(kind+"/"+style+"/"+name, func(t *testing.T) {
					sel := &metav1.LabelSelector{}
					if style != "expressions" {
						sel.MatchLabels = map[string]string{"app": "wanted"}
					}
					if style != "labels" {
						sel.MatchExpressions = []metav1.LabelSelectorRequirement{{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}}}
					}
					podLabels := map[string]string{"app": "wanted", "tier": "frontend"}
					if !matches {
						if style == "labels" {
							podLabels["app"] = "other"
						} else {
							podLabels["tier"] = "backend"
						}
					}
					if style == "invalid" {
						sel.MatchExpressions[0].Operator = "Invalid"
					}
					compiled, err := metav1.LabelSelectorAsSelector(sel)
					if style == "invalid" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						require.Equal(t, matches, compiled.Matches(labels.Set(podLabels)))
					}
					template := corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "wanted", "tier": "frontend"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "busybox:1.36.1"}}},
					}
					var obj runtime.Object
					var accessor interface {
						dao.Accessor
						Pod(string) (string, error)
					}
					var gvr *client.GVR
					switch kind {
					case "deployment":
						obj = &appsv1.Deployment{ObjectMeta: om("workload"), Spec: appsv1.DeploymentSpec{Selector: sel, Template: template}}
						accessor, gvr = new(dao.Deployment), client.DpGVR
					case "daemonset":
						obj = &appsv1.DaemonSet{ObjectMeta: om("workload"), Spec: appsv1.DaemonSetSpec{Selector: sel, Template: template}}
						accessor, gvr = new(dao.DaemonSet), client.DsGVR
					case "statefulset":
						obj = &appsv1.StatefulSet{ObjectMeta: om("workload"), Spec: appsv1.StatefulSetSpec{Selector: sel, Template: template, ServiceName: "workload"}}
						accessor, gvr = new(dao.StatefulSet), client.StsGVR
					}
					pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "candidate", Labels: podLabels}, Spec: template.Spec}
					_, factory := newDataFactory(t, fakecluster.WithObjects(obj, pod))
					accessor.Init(factory, gvr)
					got, err := accessor.Pod("ns1/workload")
					if style == "invalid" {
						require.ErrorContains(t, err, "invalid workload selector")
						assert.Empty(t, got)
					} else if matches {
						require.NoError(t, err)
						assert.Equal(t, "ns1/candidate", got)
					} else {
						assert.Error(t, err, "selected a pod excluded by the workload selector: %s", got)
						assert.Empty(t, got)
					}
				})
			}
		}
	}
}
