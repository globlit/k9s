// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/globlit/k9s/internal/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SetPaused sets the requested Deployment state without toggling each item.
func (d *Deployment) SetPaused(ctx context.Context, path string, uid types.UID, paused bool) error {
	return patchWorkloadState(ctx, &d.Generic, path, uid, "paused", paused)
}

// SetSuspend sets the requested CronJob state without replacing its spec.
func (c *CronJob) SetSuspend(ctx context.Context, path string, uid types.UID, suspended bool) error {
	return patchWorkloadState(ctx, &c.Generic, path, uid, "suspend", suspended)
}

func patchWorkloadState(ctx context.Context, g *Generic, path string, uid types.UID, field string, value bool) error {
	if uid == "" {
		return fmt.Errorf("workload %s has no UID; refresh before changing its state", path)
	}
	ns, name := client.Namespaced(path)
	if ns == "" || name == "" || client.IsClusterScoped(ns) {
		return fmt.Errorf("invalid workload path %q", path)
	}
	auth, err := g.Client().CanI(ns, g.gvr, name, client.PatchAccess)
	if err != nil {
		return err
	}
	if !auth {
		return fmt.Errorf("user is not authorized to patch %s", path)
	}
	dial, err := g.dynClient()
	if err != nil {
		return err
	}
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": uid},
		{"op": "add", "path": "/spec/" + field, "value": value},
	})
	if err != nil {
		return err
	}
	_, err = dial.Namespace(ns).Patch(ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
	return err
}
