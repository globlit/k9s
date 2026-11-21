// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func xraySelect(t *testing.T, s *SimApp, path string) {
	t.Helper()
	for range 10 {
		var sel string
		s.onLoop(func() {
			if x, ok := s.app.Content.Top().(*Xray); ok {
				if spec := x.selectedSpec(); spec != nil {
					sel = spec.Path()
				}
			}
		})
		if sel == path {
			return
		}
		s.Keys(tcell.KeyDown)
	}
	t.Fatalf("xray node %q not found\n%s", path, s.Screen())
}

func TestXrayDeletePassesPropagation(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Cmd(":xray po")
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-a") }, "xray pod not shown")
	xraySelect(t, s, "ns1/pod-a")
	fc.Reset()

	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Orphan", true)
	s.Eventually(func() bool { return len(fc.Deletes()) > 0 }, "no delete call")

	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, client.PodGVR.GVR(), dd[0].GVR)
	assert.Equal(t, "pod-a", dd[0].Name)
	require.NotNil(t, dd[0].Propagation)
	assert.Equal(t, metav1.DeletePropagationOrphan, *dd[0].Propagation)
	require.NotNil(t, dd[0].Grace)
	assert.Equal(t, int64(0), *dd[0].Grace)
}
