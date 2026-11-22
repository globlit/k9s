// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster

import (
	"context"

	"github.com/globlit/k9s/internal/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	k8stesting "k8s.io/client-go/testing"
)

// pseudoGVRs are k9s views that the API server does not serve.
var pseudoGVRs = []*client.GVR{
	client.WkGVR, client.CoGVR, client.CtGVR, client.RefGVR, client.PuGVR,
	client.ScnGVR, client.DirGVR, client.PfGVR, client.SdGVR, client.BeGVR,
	client.AliGVR, client.XGVR, client.HmGVR, client.RbacGVR, client.PolGVR,
	client.UsrGVR, client.GrpGVR,
}

func isPseudo(gvr schema.GroupVersionResource) bool {
	for _, g := range pseudoGVRs {
		if g.GVR() == gvr {
			return true
		}
	}

	return false
}

// PseudoCalls returns the calls made on k9s pseudo resources.
func (c *Cluster) PseudoCalls() []Call {
	var out []Call
	for _, call := range c.Calls() {
		if isPseudo(call.Action.GetResource()) {
			out = append(out, call)
		}
	}

	return out
}

// pseudoDyn wraps the dynamic fake. The fake panics on a list of an unknown
// resource. Record such calls and fail them with not found instead.
type pseudoDyn struct {
	dynamic.Interface

	c *Cluster
}

// IsWatchListSemanticsUnSupported keeps informers on list and watch, like on
// the dynamic fake.
func (d *pseudoDyn) IsWatchListSemanticsUnSupported() bool {
	return d.c.dyn.IsWatchListSemanticsUnSupported()
}

func (d *pseudoDyn) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	if isPseudo(gvr) {
		return &pseudoRes{c: d.c, gvr: gvr}
	}

	return d.Interface.Resource(gvr)
}

type pseudoRes struct {
	dynamic.NamespaceableResourceInterface

	c   *Cluster
	gvr schema.GroupVersionResource
	ns  string
}

func (r *pseudoRes) Namespace(ns string) dynamic.ResourceInterface {
	return &pseudoRes{c: r.c, gvr: r.gvr, ns: ns}
}

func (r *pseudoRes) fail(a k8stesting.Action) error {
	r.c.recMx.Lock()
	r.c.calls = append(r.c.calls, Call{Source: SourceDyn, Action: a})
	r.c.recMx.Unlock()

	return apierrors.NewNotFound(r.gvr.GroupResource(), "")
}

func (r *pseudoRes) List(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return nil, r.fail(k8stesting.NewListAction(r.gvr, schema.GroupVersionKind{}, r.ns, metav1.ListOptions{}))
}

func (r *pseudoRes) Watch(context.Context, metav1.ListOptions) (watch.Interface, error) {
	return nil, r.fail(k8stesting.NewWatchAction(r.gvr, r.ns, metav1.ListOptions{}))
}

func (r *pseudoRes) Get(_ context.Context, n string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	return nil, r.fail(k8stesting.NewGetAction(r.gvr, r.ns, n))
}

func (r *pseudoRes) Delete(_ context.Context, n string, opts metav1.DeleteOptions, _ ...string) error {
	return r.fail(k8stesting.NewDeleteActionWithOptions(r.gvr, r.ns, n, opts))
}
