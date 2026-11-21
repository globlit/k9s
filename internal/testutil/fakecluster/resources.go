// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster

import (
	"github.com/globlit/k9s/internal/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Resource describes one API resource the fake cluster serves.
type Resource struct {
	GVR        *client.GVR
	Kind       string
	Namespaced bool
	ShortNames []string
	Scalable   bool
}

func (r Resource) gvr() schema.GroupVersionResource {
	return r.GVR.GVR()
}

func (r Resource) gvk() schema.GroupVersionKind {
	return r.gvr().GroupVersion().WithKind(r.Kind)
}

var (
	allVerbs     = metav1.Verbs{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"}
	metricsVerbs = metav1.Verbs{"get", "list"}
)

var coreEventGVR = client.NewGVR("v1/events")

func builtins() []Resource {
	return []Resource{
		{GVR: client.PodGVR, Kind: "Pod", Namespaced: true, ShortNames: []string{"po"}},
		{GVR: client.NsGVR, Kind: "Namespace", ShortNames: []string{"ns"}},
		{GVR: client.NodeGVR, Kind: "Node", ShortNames: []string{"no"}},
		{GVR: client.SvcGVR, Kind: "Service", Namespaced: true, ShortNames: []string{"svc"}},
		{GVR: client.CmGVR, Kind: "ConfigMap", Namespaced: true, ShortNames: []string{"cm"}},
		{GVR: client.SecGVR, Kind: "Secret", Namespaced: true},
		{GVR: client.SaGVR, Kind: "ServiceAccount", Namespaced: true, ShortNames: []string{"sa"}},
		{GVR: client.PvcGVR, Kind: "PersistentVolumeClaim", Namespaced: true, ShortNames: []string{"pvc"}},
		{GVR: client.PvGVR, Kind: "PersistentVolume", ShortNames: []string{"pv"}},
		{GVR: client.EpGVR, Kind: "Endpoints", Namespaced: true, ShortNames: []string{"ep"}},
		{GVR: client.RcGVR, Kind: "ReplicationController", Namespaced: true, ShortNames: []string{"rc"}, Scalable: true},
		{GVR: coreEventGVR, Kind: "Event", Namespaced: true, ShortNames: []string{"ev"}},
		{GVR: client.EvGVR, Kind: "Event", Namespaced: true},
		{GVR: client.DpGVR, Kind: "Deployment", Namespaced: true, ShortNames: []string{"deploy"}, Scalable: true},
		{GVR: client.StsGVR, Kind: "StatefulSet", Namespaced: true, ShortNames: []string{"sts"}, Scalable: true},
		{GVR: client.DsGVR, Kind: "DaemonSet", Namespaced: true, ShortNames: []string{"ds"}},
		{GVR: client.RsGVR, Kind: "ReplicaSet", Namespaced: true, ShortNames: []string{"rs"}, Scalable: true},
		{GVR: client.EpsGVR, Kind: "EndpointSlice", Namespaced: true},
		{GVR: client.HpaGVR, Kind: "HorizontalPodAutoscaler", Namespaced: true, ShortNames: []string{"hpa"}},
		{GVR: client.CjGVR, Kind: "CronJob", Namespaced: true, ShortNames: []string{"cj"}},
		{GVR: client.JobGVR, Kind: "Job", Namespaced: true},
		{GVR: client.CrdGVR, Kind: "CustomResourceDefinition", ShortNames: []string{"crd", "crds"}},
		{GVR: client.PcGVR, Kind: "PriorityClass", ShortNames: []string{"pc"}},
		{GVR: client.NpGVR, Kind: "NetworkPolicy", Namespaced: true, ShortNames: []string{"netpol"}},
		{GVR: client.ScGVR, Kind: "StorageClass", ShortNames: []string{"sc"}},
		{GVR: client.PdbGVR, Kind: "PodDisruptionBudget", Namespaced: true, ShortNames: []string{"pdb"}},
		{GVR: client.PspGVR, Kind: "PodSecurityPolicy", ShortNames: []string{"psp"}},
		{GVR: client.IngGVR, Kind: "Ingress", Namespaced: true, ShortNames: []string{"ing"}},
		{GVR: client.NmxGVR, Kind: "NodeMetrics"},
		{GVR: client.PmxGVR, Kind: "PodMetrics", Namespaced: true},
		{GVR: client.CrGVR, Kind: "ClusterRole"},
		{GVR: client.CrbGVR, Kind: "ClusterRoleBinding"},
		{GVR: client.RoGVR, Kind: "Role", Namespaced: true},
		{GVR: client.RobGVR, Kind: "RoleBinding", Namespaced: true},
	}
}

func (r Resource) verbs() metav1.Verbs {
	if r.gvr().Group == "metrics.k8s.io" {
		return metricsVerbs
	}
	return allVerbs
}

func discoveryLists(rr []Resource) []*metav1.APIResourceList {
	var (
		order []string
		byGV  = make(map[string]*metav1.APIResourceList)
	)
	for _, r := range rr {
		gv := r.gvr().GroupVersion().String()
		l, ok := byGV[gv]
		if !ok {
			l = &metav1.APIResourceList{GroupVersion: gv}
			byGV[gv] = l
			order = append(order, gv)
		}
		l.APIResources = append(l.APIResources, metav1.APIResource{
			Name:       r.gvr().Resource,
			Kind:       r.Kind,
			Namespaced: r.Namespaced,
			Verbs:      r.verbs(),
			ShortNames: r.ShortNames,
		})
		if r.Scalable {
			l.APIResources = append(l.APIResources, metav1.APIResource{
				Name:       r.gvr().Resource + "/scale",
				Kind:       "Scale",
				Group:      "autoscaling",
				Version:    "v1",
				Namespaced: r.Namespaced,
				Verbs:      metav1.Verbs{"get", "patch", "update"},
			})
		}
	}
	out := make([]*metav1.APIResourceList, 0, len(order))
	for _, gv := range order {
		out = append(out, byGV[gv])
	}

	return out
}
