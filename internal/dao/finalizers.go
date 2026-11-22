// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/globlit/k9s/internal/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// DeleteWithFinalizers removes metadata finalizers from the confirmed object and
// deletes only that UID. Namespace spec.finalizers need a separate recovery flow.
func (g *Generic) DeleteWithFinalizers(ctx context.Context, path string, uid types.UID, propagation *metav1.DeletionPropagation, grace Grace, force bool) error {
	if uid == "" {
		return fmt.Errorf("cannot remove finalizers without a confirmed UID")
	}
	ns, name := client.Namespaced(path)
	auth, err := g.Client().CanI(ns, g.gvr, name, []string{client.GetVerb, client.PatchVerb, client.DeleteVerb})
	if err != nil {
		return err
	}
	if !auth {
		return fmt.Errorf("user is not authorized to get, patch and delete %s", path)
	}
	dial, err := g.dynClient()
	if err != nil {
		return err
	}
	var resource dynamic.ResourceInterface = dial
	if !client.IsClusterScoped(ns) {
		resource = dial.Namespace(ns)
	}
	obj, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if obj.GetUID() != uid {
		return fmt.Errorf("resource %s was replaced; select it again", path)
	}
	if !force && obj.GetDeletionTimestamp() == nil {
		return fmt.Errorf("resource %s is not terminating; select Force to remove finalizers", path)
	}
	if g.gvr.GVR() == client.NsGVR.GVR() {
		finalizers, _, err := unstructured.NestedStringSlice(obj.Object, "spec", "finalizers")
		if err != nil {
			return err
		}
		if len(finalizers) > 0 {
			return fmt.Errorf("namespace spec.finalizers require the finalize subresource; removal is not supported")
		}
	}
	if len(obj.GetFinalizers()) > 0 {
		if obj.GetResourceVersion() == "" {
			return fmt.Errorf("resource %s has no resource version", path)
		}
		patch, err := json.Marshal([]map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": uid},
			{"op": "test", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()},
			{"op": "replace", "path": "/metadata/finalizers", "value": nil},
		})
		if err != nil {
			return err
		}
		_, err = resource.Patch(ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	opts := metav1.DeleteOptions{PropagationPolicy: propagation, Preconditions: &metav1.Preconditions{UID: &uid}}
	if grace != DefaultGrace {
		opts.GracePeriodSeconds = (*int64)(&grace)
	}
	err = resource.Delete(ctx, name, opts)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
