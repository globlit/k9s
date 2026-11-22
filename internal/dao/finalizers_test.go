// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func finalizerObject() *unstructured.Unstructured {
	obj := mutObj(client.CmGVR, "ConfigMap", "ns1", "stuck")
	obj.SetUID("original")
	obj.SetResourceVersion("7")
	obj.SetFinalizers([]string{"example.com/hold"})
	now := metav1.Now()
	obj.SetDeletionTimestamp(&now)
	return obj
}

func TestDeleteWithFinalizers(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(finalizerObject()))
	var g Generic
	g.Init(mutFactory(t, fc.Conn()), client.CmGVR)
	prop := metav1.DeletePropagationForeground
	require.NoError(t, g.DeleteWithFinalizers(context.Background(), "ns1/stuck", "original", &prop, DefaultGrace, false))
	pp := fc.Actions("patch", "configmaps")
	require.Len(t, pp, 1)
	patch := pp[0].(k8stesting.PatchAction)
	assert.Equal(t, types.JSONPatchType, patch.GetPatchType())
	assert.JSONEq(t, `[{"op":"test","path":"/metadata/uid","value":"original"},{"op":"test","path":"/metadata/resourceVersion","value":"7"},{"op":"replace","path":"/metadata/finalizers","value":null}]`, string(patch.GetPatch()))
	dd := fc.Actions("delete", "configmaps")
	require.Len(t, dd, 1)
	opts := dd[0].(k8stesting.DeleteAction).GetDeleteOptions()
	require.NotNil(t, opts.Preconditions)
	assert.Equal(t, types.UID("original"), *opts.Preconditions.UID)
	assert.Equal(t, &prop, opts.PropagationPolicy)
	assert.Nil(t, opts.GracePeriodSeconds)
}

func TestDeleteWithFinalizersRejectsUnsafeRequests(t *testing.T) {
	for _, kind := range []string{"missing-uid", "replaced", "active", "missing-rv", "deny-get", "deny-patch", "deny-delete", "patch-error", "namespace"} {
		t.Run(kind, func(t *testing.T) {
			obj := finalizerObject()
			uid := types.UID("original")
			gvr, path := client.CmGVR, "ns1/stuck"
			var extra []fakecluster.Option
			switch kind {
			case "missing-uid":
				uid = ""
			case "replaced":
				uid = "old"
			case "active":
				obj.SetDeletionTimestamp(nil)
			case "missing-rv":
				obj.SetResourceVersion("")
			case "deny-get":
				extra = append(extra, fakecluster.DenyVerb("get"))
			case "deny-patch":
				extra = append(extra, fakecluster.DenyVerb("patch"))
			case "deny-delete":
				extra = append(extra, fakecluster.DenyVerb("delete"))
			case "patch-error":
				extra = append(extra, fakecluster.FailOn("patch", "configmaps", 0, errMut))
			case "namespace":
				namespaceGVR := *client.NsGVR
				gvr, path = &namespaceGVR, "-/stuck"
				obj.SetKind("Namespace")
				obj.SetNamespace("")
				obj.Object["spec"] = map[string]any{"finalizers": []any{"kubernetes"}}
			}
			fc := fakecluster.New(t, append([]fakecluster.Option{fakecluster.WithObjects(obj)}, extra...)...)
			var g Generic
			g.Init(mutFactory(t, fc.Conn()), gvr)
			require.Error(t, g.DeleteWithFinalizers(context.Background(), path, uid, nil, DefaultGrace, false))
			assert.Empty(t, fc.Actions("delete", gvr.R()))
			if kind != "patch-error" {
				assert.Empty(t, fc.Actions("patch", gvr.R()))
			}
		})
	}
}

func TestDeleteWithFinalizersForceAndGone(t *testing.T) {
	t.Run("force", func(t *testing.T) {
		obj := finalizerObject()
		obj.SetDeletionTimestamp(nil)
		fc := fakecluster.New(t, fakecluster.WithObjects(obj))
		var g Generic
		g.Init(mutFactory(t, fc.Conn()), client.CmGVR)
		require.NoError(t, g.DeleteWithFinalizers(context.Background(), "ns1/stuck", "original", nil, ForceGrace, true))
		actions := fc.Actions("delete", "configmaps")
		require.Len(t, actions, 1)
		assert.Equal(t, int64(0), *actions[0].(k8stesting.DeleteAction).GetDeleteOptions().GracePeriodSeconds)
	})
	t.Run("deleted-after-patch", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(finalizerObject()))
		fc.Dyn().PrependReactor("delete", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewNotFound(client.CmGVR.GVR().GroupResource(), "stuck")
		})
		var g Generic
		g.Init(mutFactory(t, fc.Conn()), client.CmGVR)
		require.NoError(t, g.DeleteWithFinalizers(context.Background(), "ns1/stuck", "original", nil, DefaultGrace, false))
	})
	t.Run("version-changed-before-patch", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(finalizerObject()))
		fc.Dyn().PrependReactor("patch", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
			obj := finalizerObject()
			obj.SetResourceVersion("8")
			require.NoError(t, fc.Dyn().Tracker().Update(client.CmGVR.GVR(), obj, "ns1"))
			return false, nil, nil
		})
		var g Generic
		g.Init(mutFactory(t, fc.Conn()), client.CmGVR)
		require.Error(t, g.DeleteWithFinalizers(context.Background(), "ns1/stuck", "original", nil, DefaultGrace, false))
		assert.Empty(t, fc.Actions("delete", "configmaps"))
	})
}
