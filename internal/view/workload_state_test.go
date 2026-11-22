// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func TestBulkWorkloadState(t *testing.T) {
	for _, deployment := range []bool{true, false} {
		t.Run(map[bool]string{true: "deployment", false: "cronjob"}[deployment], func(t *testing.T) {
			var objects []runtime.Object
			cmd, res, field, key := "cj", "cronjobs", "suspend", ui.KeyS
			for i, name := range []string{"a", "b", "c"} {
				if deployment {
					obj := mutDeploy(name, 0)
					obj.UID = types.UID("uid-" + name)
					obj.Spec.Paused = i == 0
					objects = append(objects, obj)
				} else {
					obj := simCronJob(name)
					obj.Spec.Suspend = new(i == 0)
					objects = append(objects, obj)
				}
			}
			if deployment {
				cmd, res, field, key = "deploy", "deployments", "paused", ui.KeyShiftP
			}
			fc := fakecluster.New(t, fakecluster.WithObjects(objects...))
			s := NewSimApp(t, fc, SimNamespace("ns1"), SimView(cmd))
			markPaths(t, s, "ns1/a", "ns1/b", "ns1/c")
			for _, state := range []string{"true", "false"} {
				fc.Reset()
				s.Keys(key)
				s.Eventually(s.DialogOpen, "no state confirmation")
				confirmOK(t, s)
				s.Eventually(func() bool { return len(patches(fc, res)) == 3 }, "not all marked resources patched")
				for _, action := range fc.Actions("patch", res) {
					patch := action.(k8stesting.PatchAction)
					name := patch.GetName()
					assert.JSONEq(t, `[{"op":"test","path":"/metadata/uid","value":"uid-`+name+`"},{"op":"add","path":"/spec/`+field+`","value":`+state+`}]`, string(patch.GetPatch()))
				}
			}
			s.Keys(key)
			s.Eventually(s.DialogOpen, "no cancellation dialog")
			fc.Reset()
			s.Keys(tcell.KeyEscape)
			assert.Empty(t, patches(fc, res))
		})
	}
}

func TestWorkloadStateRejectsReplacement(t *testing.T) {
	for _, deployment := range []bool{true, false} {
		t.Run(map[bool]string{true: "deployment", false: "cronjob"}[deployment], func(t *testing.T) {
			var object runtime.Object = simCronJob("job")
			cmd, gvr, field, key := "cj", client.CjGVR, "suspend", ui.KeyS
			if deployment {
				obj := mutDeploy("job", 0)
				obj.UID = "uid-job"
				object = obj
				cmd, gvr, field, key = "deploy", client.DpGVR, "paused", ui.KeyShiftP
			}
			fc := fakecluster.New(t, fakecluster.WithObjects(object))
			s := NewSimApp(t, fc, SimNamespace("ns1"), SimView(cmd))
			selectPath(t, s, "ns1/job")
			s.Keys(key)
			s.Eventually(s.DialogOpen, "no state confirmation")
			replacement, err := fc.DynGet(gvr, "ns1", "job")
			require.NoError(t, err)
			replacement.SetUID("replacement")
			_, err = fc.Dyn().Resource(gvr.GVR()).Namespace("ns1").Update(context.Background(), replacement, metav1.UpdateOptions{})
			require.NoError(t, err)
			confirmOK(t, s)
			s.Eventually(flashHas(s, "test failed"), "replacement patch did not fail its UID check")
			current, err := fc.DynGet(gvr, "ns1", "job")
			require.NoError(t, err)
			state, _, err := unstructured.NestedBool(current.Object, "spec", field)
			require.NoError(t, err)
			assert.False(t, state)
			assert.Equal(t, types.UID("replacement"), current.GetUID())
		})
	}
}
