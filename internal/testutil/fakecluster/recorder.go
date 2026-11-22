// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
)

// Call sources.
const (
	SourceTyped = "typed"
	SourceDyn   = "dyn"
)

// Call is one API call made on a fake client.
type Call struct {
	Source string
	Action k8stesting.Action
}

// DeleteCall is one recorded delete request.
type DeleteCall struct {
	GVR         schema.GroupVersionResource
	Subresource string
	NS          string
	Name        string
	Propagation *metav1.DeletionPropagation
	Grace       *int64
	Source      string
}

func (c *Cluster) record(src string) k8stesting.ReactionFunc {
	return func(a k8stesting.Action) (bool, runtime.Object, error) {
		c.recMx.Lock()
		c.calls = append(c.calls, Call{Source: src, Action: a.DeepCopy()})
		c.recMx.Unlock()

		return false, nil, nil
	}
}

// Calls returns all recorded calls in order, failed ones included.
func (c *Cluster) Calls() []Call {
	c.recMx.Lock()
	defer c.recMx.Unlock()

	return append([]Call(nil), c.calls...)
}

// Actions returns the recorded actions for verb and resource.
// Use "*" or "" to match any verb or resource.
func (c *Cluster) Actions(verb, resource string) []k8stesting.Action {
	var out []k8stesting.Action
	for _, call := range c.Calls() {
		a := call.Action
		if (verb == "" || verb == "*" || a.GetVerb() == verb) &&
			(resource == "" || resource == "*" || a.GetResource().Resource == resource) {
			out = append(out, a)
		}
	}

	return out
}

// Deletes returns all recorded delete calls.
func (c *Cluster) Deletes() []DeleteCall {
	var out []DeleteCall
	for _, call := range c.Calls() {
		da, ok := call.Action.(k8stesting.DeleteAction)
		if !ok || da.GetVerb() != "delete" {
			continue
		}
		opts := da.GetDeleteOptions()
		dc := DeleteCall{
			GVR:         da.GetResource(),
			Subresource: da.GetSubresource(),
			NS:          da.GetNamespace(),
			Name:        da.GetName(),
			Source:      call.Source,
		}
		if opts.PropagationPolicy != nil {
			p := *opts.PropagationPolicy
			dc.Propagation = &p
		}
		if opts.GracePeriodSeconds != nil {
			g := *opts.GracePeriodSeconds
			dc.Grace = &g
		}
		out = append(out, dc)
	}

	return out
}

// CanICalls returns how many times CanI was called.
func (c *Cluster) CanICalls() int { return int(c.canICalls.Load()) }

// Reset clears the recorded calls, REST calls and the CanI counter.
func (c *Cluster) Reset() {
	c.recMx.Lock()
	c.calls, c.rest, c.pseudoCanIs = nil, nil, nil
	c.recMx.Unlock()
	c.canICalls.Store(0)
	c.typed.ClearActions()
	c.dyn.ClearActions()
}
