// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// e2eCrumbs returns the crumbs row.
func (s *SimApp) e2eCrumbs() string {
	_, h := s.scr.Size()
	return strings.Join(strings.Fields(s.Row(h-2)), " ")
}

func (s *SimApp) e2eGoto(cmd, view string, shows ...string) {
	s.t.Helper()
	s.Cmd(cmd)
	s.Eventually(func() bool { return s.TopView() == view }, cmd+": view is not "+view)
	if len(shows) > 0 {
		s.e2eWait(shows...)
	}
}

func TestE2EStartup(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))

	assert.Equal(t, "Pod", s.TopView())
	s.e2eWait("pods(all)[5]", "web-7d9-aaaaa", "api-0", "db-0", "cache-0")
	scr := s.Screen()
	assert.Contains(t, scr, "Context: fake")
	assert.Contains(t, scr, "Cluster: fake")
	assert.Contains(t, scr, "K8s Rev: v1.37.2")
	assert.Equal(t, "<pod>", s.e2eCrumbs())
	s.e2eMatch("e2e_startup.txt")
}

func TestE2ENavigation(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("web-7d9-aaaaa")

	uu := []struct {
		cmd, view string
		shows     []string
	}{
		{":ns", "Namespace", []string{"ns-a", "ns-b", "default"}},
		{":deploy", "Deployment", []string{"web"}},
		{":dp", "Deployment", []string{"web"}},
		{":svc", "Service", []string{"web-svc", "10.96.0.10"}},
		{":sts", "StatefulSet", []string{"db"}},
		{":ds", "DaemonSet", []string{"agent"}},
		{":cj", "CronJob", []string{"backup", "0 1 * * *"}},
		{":job", "Job", []string{"backup-1"}},
		{":jo", "Job", []string{"backup-1"}},
		{":cm", "ConfigMap", []string{"web-cfg"}},
		{":sec", "Secret", []string{"web-tls", "Opaque"}},
		{":no", "Node", []string{"node-1", "Ready"}},
		{":crd", "CustomResourceDefinition", []string{"customresourcedefinitions(all)[0]"}},
		{":rs", "ReplicaSet", []string{"web-7d9"}},
		{":v1/pods", "Pod", []string{"web-7d9-aaaaa"}},
		{":po", "Pod", []string{"web-7d9-aaaaa"}},
	}
	for _, u := range uu {
		t.Run(u.cmd, func(t *testing.T) {
			defer s.Use(t)()
			s.e2eGoto(u.cmd, u.view, u.shows...)
			assert.Equal(t, "<"+strings.ToLower(u.view)+">", s.e2eCrumbs())
			assert.False(t, s.DialogOpen())
		})
	}
}

func TestE2EDrillDown(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eGoto(":deploy", "Deployment", "web")

	s.Keys(tcell.KeyEnter)
	s.Eventually(func() bool { return s.TopView() == "Pod" }, "no pods view")
	s.e2eWait("web-7d9-aaaaa", "web-7d9-bbbbb")
	s.e2eGone("api-0", "db-0")
	assert.Equal(t, "<deployment> <pod>", s.e2eCrumbs())
	s.e2eMatch("e2e_deploy_pods.txt")

	s.Keys(tcell.KeyEscape)
	s.Eventually(func() bool { return s.TopView() == "Deployment" }, "esc did not go back")
	assert.Equal(t, "<deployment>", s.e2eCrumbs())
}

func TestE2EHistory(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("web-7d9-aaaaa")
	s.e2eGoto(":deploy", "Deployment", "web")
	s.e2eGoto(":svc", "Service", "web-svc")
	s.e2eGoto(":cm", "ConfigMap", "web-cfg")

	uu := []struct {
		key  rune
		view string
	}{
		{'[', "Service"},
		{'[', "Deployment"},
		{']', "Service"},
		{'-', "ConfigMap"},
		{'[', "Service"},
		{'[', "Deployment"},
		{'[', "Pod"},
	}
	for _, u := range uu {
		s.Keys(u.key)
		s.Eventually(func() bool { return s.TopView() == u.view }, string(u.key)+" did not show "+u.view)
	}
	s.Keys('[')
	s.e2eWait("Can't go back any further")
	assert.Equal(t, "Pod", s.TopView())
}

func TestE2EUnknownCommand(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("web-7d9-aaaaa")

	s.Cmd(":bogus")
	s.e2eWait("`bogus` command not found")
	assert.Equal(t, "Pod", s.TopView())
	s.e2eMatch("e2e_unknown_cmd.txt")
}

// e2eViewNames maps viewers whose top view name is not their meta kind.
var e2eViewNames = map[string]string{
	"benchmarks":   "Benchmarks",
	"portforwards": "PortForwards",
	"pulses":       "Pulses",
	"screendumps":  "ScreenDumps",
}

func TestE2EEveryViewer(t *testing.T) {
	cr := rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Rules:      []rbacv1.PolicyRule{{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}},
	}
	s := NewSimApp(t, e2eCluster(t, fakecluster.WithObjects(&cr)))
	s.e2eWait("web-7d9-aaaaa")

	// Reach the viewers that have no command from a parent view.
	reach := map[*client.GVR]func(){
		client.CoGVR: func() {
			s.e2eGoto(":po ns-a /api-0", "Pod", "api-0")
			s.Keys(tcell.KeyEnter)
		},
		client.RbacGVR: func() {
			s.e2eGoto(":clusterroles", "ClusterRole", "reader")
			s.Keys(tcell.KeyEnter)
		},
		client.RefGVR: func() {
			s.e2eGoto(":cm ns-a", "ConfigMap", "web-cfg")
			s.Keys('u')
		},
		client.ScnGVR: func() {
			s.OnLoop(func() { require.NoError(t, s.App().inject(NewImageScan(client.ScnGVR), false)) })
		},
	}

	gg := slices.SortedFunc(func(yield func(*client.GVR) bool) {
		for g := range loadCustomViewers() {
			if !yield(g) {
				return
			}
		}
	}, func(a, b *client.GVR) int { return strings.Compare(a.String(), b.String()) })
	for _, gvr := range gg {
		t.Run(gvr.String(), func(t *testing.T) {
			defer s.Use(t)()
			want, ok := e2eViewNames[gvr.String()]
			if !ok {
				m, err := dao.MetaAccess.MetaFor(gvr)
				require.NoError(t, err, "add %s to e2eViewNames", gvr)
				want = m.Kind
			}
			if f, ok := reach[gvr]; ok {
				f()
			} else {
				aa := slices.Sorted(maps.Keys(s.App().command.AliasesFor(gvr)))
				require.NotEmpty(t, aa, "no command for %s: add it to reach", gvr)
				s.Cmd(":" + aa[0])
			}
			s.Eventually(func() bool { return s.TopView() == want }, gvr.String()+": view is not "+want)
			assert.False(t, s.DialogOpen())
			assert.Equal(t, -1, s.ExitCode())
		})
	}
}

func TestE2EBenchmarksWithoutDir(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eGoto(":bench", "Benchmarks")

	assert.DirExists(t, benchDir(s.App().Config))
}

func TestE2ENamespaceSwitch(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("web-7d9-aaaaa", "db-0")

	t.Run("command", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod ns-b", "Pod", "pods(ns-b)[2]", "db-0", "cache-0")
		s.e2eGone("web-7d9-aaaaa")
		assert.Equal(t, "ns-b", s.App().Config.ActiveNamespace())
	})
	t.Run("deploy", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":deploy ns-a", "Deployment", "deployments(ns-a)[1]")
		s.e2eWait("<2> ns-b")
	})
	t.Run("hotkey", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod", "Pod", "pods(ns-a)[3]")
		s.Keys('2')
		s.e2eWait("pods(ns-b)[2]", "db-0")
		s.Keys('0')
		s.e2eWait("pods(all)[5]", "web-7d9-aaaaa", "db-0")
		assert.Equal(t, client.NamespaceAll, s.App().Config.ActiveNamespace())
	})
	t.Run("all", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod ns-a", "Pod", "pods(ns-a)[3]")
		s.e2eGoto(":pod all", "Pod", "pods(all)[5]")
	})
	t.Run("cluster-scoped", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod ns-b", "Pod", "pods(ns-b)[2]")
		s.e2eGoto(":no", "Node", "nodes(all)[1]")
		assert.Equal(t, "ns-b", s.App().Config.ActiveNamespace())
	})
	t.Run("unknown", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod ns-x", "Pod", "pods(ns-x)[0]")
	})
}

func TestE2EQuit(t *testing.T) {
	uu := []string{":q", ":q!", ":Q"}
	for _, u := range uu {
		t.Run(u, func(t *testing.T) {
			s := NewSimApp(t, e2eCluster(t))
			s.Cmd(u)
			s.Eventually(s.Stopped, "app still running")
			assert.Equal(t, 0, s.ExitCode())
		})
	}
}
