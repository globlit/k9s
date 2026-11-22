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
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func TestSetWorkloadState(t *testing.T) {
	for _, deployment := range []bool{true, false} {
		gvr, field := client.CjGVR, "suspend"
		var obj runtime.Object = mutCronJob("job", new(false))
		if deployment {
			gvr, field = client.DpGVR, "paused"
			obj = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: "ns1"}}
		}
		for _, state := range []bool{true, false} {
			for _, deny := range []bool{false, true} {
				opts := []fakecluster.Option{fakecluster.WithObjects(obj)}
				if deny {
					opts = append(opts, fakecluster.DenyVerb("patch"))
				}
				fc := fakecluster.New(t, opts...)
				f := mutFactory(t, fc.Conn())
				var apply func(context.Context, string, bool) error
				if deployment {
					var d Deployment
					d.Init(f, gvr)
					apply = d.SetPaused
				} else {
					var c CronJob
					c.Init(f, gvr)
					apply = c.SetSuspend
				}
				err := apply(context.Background(), "ns1/job", state)
				if deny {
					require.ErrorContains(t, err, "not authorized")
					assert.Empty(t, fc.Actions("patch", gvr.R()))
					continue
				}
				require.NoError(t, err)
				actions := fc.Actions("patch", gvr.R())
				require.Len(t, actions, 1)
				patch := actions[0].(k8stesting.PatchAction)
				assert.Equal(t, types.MergePatchType, patch.GetPatchType())
				want := "false"
				if state {
					want = "true"
				}
				assert.JSONEq(t, `{"spec":{"`+field+`":`+want+`}}`, string(patch.GetPatch()))
			}
		}
	}
}

func TestWorkloadStateErrors(t *testing.T) {
	for _, kind := range []string{"missing", "patch", "auth", "dial", "invalid-path"} {
		t.Run(kind, func(t *testing.T) {
			opts := []fakecluster.Option{fakecluster.WithObjects(mutCronJob("job", nil))}
			if kind == "patch" {
				opts = append(opts, fakecluster.FailOn("patch", "cronjobs", 0, errMut))
			}
			fc := fakecluster.New(t, opts...)
			var conn client.Connection = fc.Conn()
			if kind == "auth" {
				conn = &mutConn{Connection: conn, canIErr: errMut}
			}
			if kind == "dial" {
				conn = &mutConn{Connection: conn, dynErr: errMut}
			}
			path := "ns1/job"
			if kind == "missing" {
				path = "ns1/missing"
			}
			if kind == "invalid-path" {
				path = ""
			}
			var c CronJob
			c.Init(mutFactory(t, conn), client.CjGVR)
			require.Error(t, c.SetSuspend(context.Background(), path, true))
			if kind != "missing" && kind != "patch" {
				assert.Empty(t, fc.Actions("patch", "cronjobs"))
			}
		})
	}
}
