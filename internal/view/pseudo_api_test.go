// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var pseudoViews = []roView{
	{name: "portforward", cmd: "pf"},
	{name: "screendump", cmd: "sd"},
	{name: "benchmark", cmd: "bench"},
	{name: "aliases", cmd: "aliases"},
	{name: "contexts", cmd: "ctx"},
	{name: "workloads", cmd: "wk"},
	{name: "pulse", cmd: "pulse"},
	{name: "xray", cmd: "xray deploy"},
	{name: "dir", cmd: "dir %s"},
	{name: "helm", cmd: "helm"},
	{name: "users", cmd: "users"},
	{name: "groups", cmd: "groups"},
	{name: "policies", cmd: "can u:bob"},
}

func mkDumpDirs(t *testing.T, s *SimApp) []string {
	t.Helper()
	dd := []string{s.app.Config.K9s.ContextScreenDumpDir(), benchDir(s.app.Config)}
	for _, d := range dd {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}

	return dd
}

func seedPseudo(t *testing.T, s *SimApp, fc *fakecluster.Cluster) {
	t.Helper()
	s.app.factory.AddForwarder(newSimFwd("ns1/pod-a"))
	for _, d := range mkDumpDirs(t, s) {
		require.NoError(t, os.WriteFile(filepath.Join(d, "ns1_pod-a_1.txt"), []byte("x"), 0o600))
	}
	fc.Add(&rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "cr1"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}},
	})
	fc.Add(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "crb1"},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "cr1"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "bob"}, {Kind: "Group", Name: "developers"}},
	})
}

func TestPseudoViewsSkipAPI(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("x: 1\n"), 0o600))

	for _, v := range pseudoViews {
		for _, mode := range []string{"empty", "seeded"} {
			t.Run(v.name+"/"+mode, func(t *testing.T) {
				fc := roCluster(t)
				s := NewSimApp(t, fc, SimNamespace("ns1"))
				mkDumpDirs(t, s)
				if mode == "seeded" {
					seedPseudo(t, s, fc)
				}
				openRoView(t, s, v, dir)
				settledActions(t, s, "")
				s.Keys(tcell.KeyCtrlR, '0', '1', tcell.KeyDown, 'w')
				// Let the next refresh run.
				time.Sleep(300 * time.Millisecond)

				assert.Empty(t, fc.PseudoCalls())
				assert.Empty(t, fc.PseudoCanIs())
			})
		}
	}
}

func TestRBACViewsBindNoEdit(t *testing.T) {
	for _, v := range pseudoViews[len(pseudoViews)-3:] {
		t.Run(v.name, func(t *testing.T) {
			fc := roCluster(t)
			s := NewSimApp(t, fc, SimNamespace("ns1"))
			seedPseudo(t, s, fc)
			openRoView(t, s, v, "")
			s.Eventually(func() bool { return s.Row(4) != "" }, "no rows")

			for _, a := range settledActions(t, s, "") {
				assert.NotContains(t, []string{"Edit", "Delete"}, a.desc, a.key)
			}
		})
	}
}

func TestNamespaceKeysOnResources(t *testing.T) {
	t.Run("warp", func(t *testing.T) {
		s := NewSimApp(t, roCluster(t), SimNamespace("ns1"), SimView("pods"))
		settledActions(t, s, "")
		s.Keys('0')
		s.Eventually(func() bool { return s.app.Config.ActiveNamespace() == "all" }, "namespace not all")
		selectPath(t, s, "ns1/pod-a")
		s.Keys('w')

		s.Eventually(func() bool { return s.app.Config.ActiveNamespace() == "ns1" }, "no warp to ns1")
	})

	t.Run("denied", func(t *testing.T) {
		fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")), fakecluster.DenyNS("default"))
		s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
		settledActions(t, s, "")
		n := fc.CanICalls()
		s.Keys('2')

		s.Eventually(func() bool { return strings.Contains(s.Flash(), "access denied") }, "no access error")
		assert.Equal(t, "ns1", s.app.Config.ActiveNamespace())
		assert.Greater(t, fc.CanICalls(), n)
	})
}

func TestPortForwardNavigationKeepsSession(t *testing.T) {
	fc := roCluster(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	fwd := newSimFwd("ns1/pod-a")
	s.app.factory.AddForwarder(fwd)
	s.Cmd("pf")
	settledActions(t, s, "")
	s.Keys('0')
	s.Eventually(func() bool { return s.app.Config.ActiveNamespace() == "all" }, "namespace not all")
	selectPath(t, s, fwd.ID())
	s.Keys('w')
	assert.Eventually(t, func() bool { return s.app.Config.ActiveNamespace() == "ns1" }, 2*time.Second, 20*time.Millisecond, "local port-forward warp failed")
	found, ok := s.app.factory.ForwarderFor(fwd.ID())
	assert.True(t, ok, "namespace navigation removed the forwarder")
	assert.Same(t, fwd, found)
	assert.False(t, fwd.stopped.Load(), "namespace navigation stopped the forwarder")
	assert.Empty(t, fc.PseudoCalls())
	assert.Empty(t, fc.PseudoCanIs())
}

func TestRealRBACViewsKeepEditDelete(t *testing.T) {
	for _, cmd := range []string{"roles", "clusterroles", "rolebindings", "clusterrolebindings"} {
		t.Run(cmd, func(t *testing.T) {
			s := NewSimApp(t, roCluster(t), SimNamespace("ns1"))
			openRoView(t, s, roView{name: cmd, cmd: cmd}, "")
			actions := settledActions(t, s, "")
			var edit, del bool
			for _, a := range actions {
				edit = edit || a.desc == "Edit"
				del = del || a.desc == "Delete"
			}
			assert.True(t, edit, "real RBAC resource lost Edit")
			assert.True(t, del, "real RBAC resource lost Delete")
		})
	}
}
