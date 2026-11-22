// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestBulkWorkloadState(t *testing.T) {
	for _, deployment := range []bool{true, false} {
		t.Run(map[bool]string{true: "deployment", false: "cronjob"}[deployment], func(t *testing.T) {
			var objects []runtime.Object
			cmd, res, field, key := "cj", "cronjobs", "suspend", ui.KeyS
			for i, name := range []string{"a", "b", "c"} {
				if deployment {
					obj := mutDeploy(name, 0)
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
				for _, patch := range patches(fc, res) {
					assert.JSONEq(t, `{"spec":{"`+field+`":`+state+`}}`, patch)
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
