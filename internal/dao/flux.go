// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/derailed/k9s/internal/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// Flux reconcile annotations, as set by the flux CLI.
const (
	FluxRequestedAtAnn = "reconcile.fluxcd.io/requestedAt"
	FluxForceAtAnn     = "reconcile.fluxcd.io/forceAt"
	FluxResetAtAnn     = "reconcile.fluxcd.io/resetAt"

	// fluxOperatorReconcileAnn suspends (disabled) or resumes (enabled)
	// Flux Operator resources, which have no spec.suspend.
	fluxOperatorReconcileAnn = "fluxcd.controlplane.io/reconcile"

	fluxSourceGroup   = "source.toolkit.fluxcd.io"
	fluxOperatorGroup = "fluxcd.controlplane.io"
	fluxFieldManager  = "k9s"
)

// FluxCaps describes what k9s can do with a Flux resource.
type FluxCaps struct {
	// Suspend supports suspend/resume.
	Suspend bool
	// Reconcile has a controller honouring reconcile.fluxcd.io/requestedAt.
	Reconcile bool
	// Source references a source that can be reconciled or jumped to.
	Source bool
	// Operator is a Flux Operator resource (annotation based suspend).
	Operator bool
	// HelmRelease supports force/reset upgrades.
	HelmRelease bool
}

var fluxResources = map[string]map[string]FluxCaps{
	"kustomize.toolkit.fluxcd.io": {
		"kustomizations": {Suspend: true, Reconcile: true, Source: true},
	},
	"helm.toolkit.fluxcd.io": {
		"helmreleases": {Suspend: true, Reconcile: true, Source: true, HelmRelease: true},
	},
	fluxSourceGroup: {
		"gitrepositories":  {Suspend: true, Reconcile: true},
		"ocirepositories":  {Suspend: true, Reconcile: true},
		"helmrepositories": {Suspend: true, Reconcile: true},
		"helmcharts":       {Suspend: true, Reconcile: true},
		"buckets":          {Suspend: true, Reconcile: true},
	},
	"image.toolkit.fluxcd.io": {
		"imagerepositories":      {Suspend: true, Reconcile: true},
		"imageupdateautomations": {Suspend: true, Reconcile: true},
	},
	"notification.toolkit.fluxcd.io": {
		// Alerts and providers have no reconciler: suspend only.
		"alerts":    {Suspend: true},
		"providers": {Suspend: true},
		"receivers": {Suspend: true, Reconcile: true},
	},
	fluxOperatorGroup: {
		"fluxinstances":             {Suspend: true, Reconcile: true, Operator: true},
		"resourcesets":              {Suspend: true, Reconcile: true, Operator: true},
		"resourcesetinputproviders": {Suspend: true, Reconcile: true, Operator: true},
	},
}

// FluxCapsFor returns the Flux capabilities of a resource, any API version.
func FluxCapsFor(gvr *client.GVR) (FluxCaps, bool) {
	if gvr == nil {
		return FluxCaps{}, false
	}
	rr, ok := fluxResources[gvr.G()]
	if !ok {
		return FluxCaps{}, false
	}
	caps, ok := rr[gvr.R()]

	return caps, ok
}

// IsFluxGVR checks if a resource is managed by Flux controllers.
func IsFluxGVR(gvr *client.GVR) bool {
	_, ok := FluxCapsFor(gvr)
	return ok
}

// FluxTarget identifies an object to patch.
type FluxTarget struct {
	GVR       *client.GVR
	Namespace string
	Name      string
}

// FQN returns the target's fully qualified name.
func (t FluxTarget) FQN() string {
	return client.FQN(t.Namespace, t.Name)
}

func (t FluxTarget) String() string {
	return t.GVR.R() + " " + t.FQN()
}

// FluxIsSuspended reports whether a Flux object is suspended. A missing
// spec.suspend means active.
func FluxIsSuspended(o *unstructured.Unstructured, caps FluxCaps) bool {
	if o == nil {
		return false
	}
	if caps.Operator {
		return o.GetAnnotations()[fluxOperatorReconcileAnn] == "disabled"
	}
	b, _, _ := unstructured.NestedBool(o.Object, "spec", "suspend")

	return b
}

// FluxTimestamp formats a reconcile request time like the flux CLI.
func FluxTimestamp(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// FluxSuspendPatch builds the merge patch that suspends or resumes an
// object. Resume also requests a reconcile, as `flux resume` does.
func FluxSuspendPatch(caps FluxCaps, suspend bool, now string) []byte {
	anns := map[string]any{}
	body := map[string]any{}
	switch {
	case caps.Operator && suspend:
		anns[fluxOperatorReconcileAnn] = "disabled"
	case caps.Operator:
		anns[fluxOperatorReconcileAnn] = "enabled"
		anns[FluxRequestedAtAnn] = now
	case suspend:
		body["spec"] = map[string]any{"suspend": true}
	default:
		body["spec"] = map[string]any{"suspend": false}
		anns[FluxRequestedAtAnn] = now
	}
	if len(anns) > 0 {
		body["metadata"] = map[string]any{"annotations": anns}
	}

	return mustJSON(body)
}

// FluxReconcilePatch builds the merge patch requesting a reconcile. Extra
// annotations (forceAt, resetAt) must carry the same value as requestedAt
// for helm-controller to honour them.
func FluxReconcilePatch(now string, extra ...string) []byte {
	anns := map[string]any{FluxRequestedAtAnn: now}
	for _, a := range extra {
		anns[a] = now
	}

	return mustJSON(map[string]any{"metadata": map[string]any{"annotations": anns}})
}

func mustJSON(v any) []byte {
	bb, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings/bools always marshal.
	}

	return bb
}

// FluxPatch applies a merge patch. Strict field validation turns a patch on
// a field the CRD doesn't have into an error instead of a silent no-op.
func FluxPatch(ctx context.Context, dial dynamic.Interface, t FluxTarget, body []byte) error {
	_, err := dial.Resource(t.GVR.GVR()).Namespace(t.Namespace).Patch(
		ctx, t.Name, types.MergePatchType, body,
		metav1.PatchOptions{FieldManager: fluxFieldManager, FieldValidation: "Strict"},
	)
	if err != nil {
		return fmt.Errorf("%s: %w", t, err)
	}

	return nil
}

// GKResolver maps a group and kind to a resource.
type GKResolver func(group, kind string) (*client.GVR, bool)

// fluxRef is a Flux object reference (sourceRef, chartRef).
type fluxRef struct {
	apiVersion, kind, name, namespace string
}

func readRef(o map[string]any, fields ...string) (fluxRef, bool) {
	m, ok, _ := unstructured.NestedMap(o, fields...)
	if !ok {
		return fluxRef{}, false
	}
	var r fluxRef
	r.apiVersion, _, _ = unstructured.NestedString(m, "apiVersion")
	r.kind, _, _ = unstructured.NestedString(m, "kind")
	r.name, _, _ = unstructured.NestedString(m, "name")
	r.namespace, _, _ = unstructured.NestedString(m, "namespace")

	return r, r.kind != "" && r.name != ""
}

func (r fluxRef) target(defaultGroup, defaultNS string, resolve GKResolver) (FluxTarget, error) {
	group := defaultGroup
	if r.apiVersion != "" {
		if gv, err := schema.ParseGroupVersion(r.apiVersion); err == nil {
			group = gv.Group
		}
	}
	gvr, ok := resolve(group, r.kind)
	if !ok {
		return FluxTarget{}, fmt.Errorf("unknown source kind %s.%s", r.kind, group)
	}
	ns := r.namespace
	if ns == "" {
		ns = defaultNS
	}

	return FluxTarget{GVR: gvr, Namespace: ns, Name: r.name}, nil
}

// FluxSource returns the object a Kustomization or HelmRelease pulls from:
// its source for a Kustomization or a HelmRelease chartRef, or the
// HelmChart generated for a HelmRelease chart template.
func FluxSource(gvr *client.GVR, o *unstructured.Unstructured, resolve GKResolver) (FluxTarget, error) {
	ns := o.GetNamespace()
	switch gvr.R() {
	case "kustomizations":
		ref, ok := readRef(o.Object, "spec", "sourceRef")
		if !ok {
			return FluxTarget{}, fmt.Errorf("kustomization %s has no sourceRef", client.FQN(ns, o.GetName()))
		}
		return ref.target(fluxSourceGroup, ns, resolve)
	case "helmreleases":
		if ref, ok := readRef(o.Object, "spec", "chartRef"); ok {
			return ref.target(fluxSourceGroup, ns, resolve)
		}
		ref, ok := readRef(o.Object, "spec", "chart", "spec", "sourceRef")
		if !ok {
			return FluxTarget{}, fmt.Errorf("helmrelease %s has no chart source", client.FQN(ns, o.GetName()))
		}
		gvr, ok := resolve(fluxSourceGroup, "HelmChart")
		if !ok {
			return FluxTarget{}, fmt.Errorf("unknown kind HelmChart.%s", fluxSourceGroup)
		}
		// helm-controller names the chart <hr-namespace>-<hr-name> and
		// creates it next to the chart's source.
		chartNS := ref.namespace
		if chartNS == "" {
			chartNS = ns
		}
		return FluxTarget{GVR: gvr, Namespace: chartNS, Name: ns + "-" + o.GetName()}, nil
	default:
		return FluxTarget{}, fmt.Errorf("%s have no Flux source", gvr.R())
	}
}

// ObjectGetter fetches an object (used to follow HelmChart sources).
type ObjectGetter func(ctx context.Context, t FluxTarget) (*unstructured.Unstructured, error)

// FluxSourceChain returns the objects to reconcile, source first, to pick
// up the latest upstream changes for a Kustomization or HelmRelease, like
// `flux reconcile --with-source`. Unlike the CLI it doesn't wait for each
// source to be ready: controllers react to the new artifact on their own.
func FluxSourceChain(ctx context.Context, gvr *client.GVR, o *unstructured.Unstructured, resolve GKResolver, get ObjectGetter) ([]FluxTarget, error) {
	src, err := FluxSource(gvr, o, resolve)
	if err != nil {
		return nil, err
	}
	chain := []FluxTarget{src}
	// A HelmChart has a source of its own (HelmRepository, GitRepository,
	// Bucket): reconcile it too, before the chart.
	if src.GVR.G() == fluxSourceGroup && src.GVR.R() == "helmcharts" {
		var ref fluxRef
		var ok bool
		if spec, has := readRef(o.Object, "spec", "chart", "spec", "sourceRef"); has && !isChartRef(o) {
			ref, ok = spec, true
		} else {
			hc, err := get(ctx, src)
			if err != nil {
				return nil, err
			}
			ref, ok = readRef(hc.Object, "spec", "sourceRef")
		}
		if ok {
			up, err := ref.target(fluxSourceGroup, src.Namespace, resolve)
			if err != nil {
				return nil, err
			}
			// The chart's source lives in the chart's namespace.
			up.Namespace = src.Namespace
			chain = []FluxTarget{up, src}
		}
	}
	return append(chain, FluxTarget{GVR: gvr, Namespace: o.GetNamespace(), Name: o.GetName()}), nil
}

func isChartRef(o *unstructured.Unstructured) bool {
	_, ok := readRef(o.Object, "spec", "chartRef")
	return ok
}
