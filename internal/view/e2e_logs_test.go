// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/testutil/expect"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

const e2eLogTime = "2026-01-02T03:04:05.000000000Z"

// e2eLogs makes the fake log stream return three lines per container.
// The fake GetLogs action has no pod name, so lines name the container only.
func e2eLogs(fc *fakecluster.Cluster) {
	fc.Typed().PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "log" {
			return false, nil, nil
		}
		var opts corev1.PodLogOptions
		if ga, ok := a.(k8stesting.GenericAction); ok {
			if o, ok := ga.GetValue().(*corev1.PodLogOptions); ok {
				opts = *o
			}
		}
		var b strings.Builder
		for i, lvl := range []string{"INFO", "WARN", "ERROR"} {
			if opts.Timestamps {
				b.WriteString(e2eLogTime + " ")
			}
			fmt.Fprintf(&b, "%s %s/%s line-%d [x]\n", lvl, a.GetNamespace(), opts.Container, i)
		}

		return true, &runtime.Unknown{Raw: []byte(b.String())}, nil
	})
}

func e2eLogApp(t *testing.T) *SimApp {
	fc := e2eCluster(t)
	e2eLogs(fc)

	return NewSimApp(t, fc)
}

func TestE2ELogsPod(t *testing.T) {
	s := e2eLogApp(t)
	s.e2eGoto(":pod ns-a /api", "Pod", "api-0")
	s.Keys('l')
	s.Eventually(func() bool { return s.TopView() == "logs" }, "no logs view")
	s.e2eWait("Logs(ns-a/api-0:main)[tail]", "INFO ns-a/main line-0 [x]", "ERROR ns-a/main line-2 [x]",
		"stream closed: EOF for ns-a/api-0 (main)")
	assert.Equal(t, "<pod> <logs>", s.e2eCrumbs())
	expect.Match(t, "e2e_logs_pod.txt", e2eMask(s.Screen()))

	uu := []struct {
		name, filter string
		shows, hides []string
	}{
		{"regex", "WARN|ERROR", []string{"line-1", "line-2"}, []string{"line-0"}},
		{"case", "warn", []string{"line-1"}, []string{"line-0", "line-2"}},
		{"inverse", "!WARN", []string{"line-0", "line-2"}, []string{"line-1"}},
		{"fuzzy", "-f errln2", []string{"line-2"}, []string{"line-0", "line-1"}},
		{"invalid", "[x", []string{"line-0", "line-1", "line-2"}, nil},
	}
	for _, u := range uu {
		t.Run(u.name, func(t *testing.T) {
			defer s.Use(t)()
			s.Keys('/', u.filter, tcell.KeyEnter)
			s.e2eWait(u.shows...)
			if len(u.hides) > 0 {
				s.e2eGone(u.hides...)
			}
			s.Keys(tcell.KeyEscape)
			s.e2eWait("line-0", "line-1", "line-2")
			assert.Equal(t, "logs", s.TopView())
		})
	}

	t.Run("toggles", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('w')
		s.e2eWait("Wrap:On")
		s.Keys('w')
		s.e2eWait("Wrap:Off")
		s.Keys('s')
		s.e2eWait("Autoscroll:Off")
		s.Keys('s')
		s.e2eWait("Autoscroll:On")
		s.Keys('f')
		s.e2eWait("FullScreen:On")
		s.Keys('f')
		s.e2eWait("FullScreen:Off")
		s.Keys('L')
		s.e2eWait("ColumnLock:On")
		s.Keys('L')
		s.e2eWait("ColumnLock:Off")
	})
	t.Run("timestamps", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('t')
		s.e2eWait("Timestamps:On", e2eLogTime+" INFO ns-a/main line-0")
		s.Keys('t')
		s.e2eWait("Timestamps:Off")
		s.e2eGone(e2eLogTime)
	})
	t.Run("since", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('3')
		s.e2eWait("Logs(ns-a/api-0:main)[5m]", "line-2")
		s.Keys('1')
		s.e2eWait("Logs(ns-a/api-0:main)[head]", "line-2")
		s.Keys('0')
		s.e2eWait("Logs(ns-a/api-0:main)[tail]", "line-2")
	})
	t.Run("clear", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('C')
		s.e2eGone("line-0", "line-2")
	})
	t.Run("back", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys(tcell.KeyEscape)
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "esc did not go back")
	})
}

func TestE2ELogsMultiContainer(t *testing.T) {
	s := e2eLogApp(t)
	s.e2eGoto(":pod ns-a /web-7d9-a", "Pod", "web-7d9-aaaaa")

	s.Keys('l')
	s.Eventually(func() bool { return s.TopView() == "logs" }, "no logs view")
	s.e2eWait("Logs(ns-a/web-7d9-aaaaa)[tail]",
		"nginx INFO ns-a/nginx line-0", "nginx ERROR ns-a/nginx line-2",
		"sidecar INFO ns-a/sidecar line-0", "sidecar ERROR ns-a/sidecar line-2",
		"stream closed: EOF for ns-a/web-7d9-aaaaa (nginx)",
		"stream closed: EOF for ns-a/web-7d9-aaaaa (sidecar)")

	s.Keys('/', "sidecar", tcell.KeyEnter)
	s.e2eWait("sidecar ERROR ns-a/sidecar line-2")
	s.e2eGone("nginx INFO")
}

func TestE2ELogsContainer(t *testing.T) {
	s := e2eLogApp(t)
	s.e2eGoto(":pod ns-a /web-7d9-a", "Pod", "web-7d9-aaaaa")

	s.Keys(tcell.KeyEnter)
	s.Eventually(func() bool { return s.TopView() == "Containers" }, "no containers view")
	s.e2eWait("nginx", "sidecar")
	s.Keys('/', "sidecar", tcell.KeyEnter)
	s.e2eWait("containers(ns-a/web-7d9-aaaaa)[1]")
	s.Keys('l')
	s.Eventually(func() bool { return s.TopView() == "logs" }, "no logs view")
	s.e2eWait("Logs(ns-a/web-7d9-aaaaa:sidecar)[tail]", "INFO ns-a/sidecar line-0")
	s.e2eGone("ns-a/nginx")
	assert.Equal(t, "<pod> <containers> <logs>", s.e2eCrumbs())
}

func TestE2ELogsDeployment(t *testing.T) {
	s := e2eLogApp(t)
	s.e2eGoto(":deploy", "Deployment", "web")

	s.Keys('l')
	s.Eventually(func() bool { return s.TopView() == "logs" }, "no logs view")
	s.e2eWait("Logs(ns-a/web:nginx)[tail]",
		"web-7d9-aaaaa INFO ns-a/nginx line-0", "web-7d9-aaaaa ERROR ns-a/nginx line-2",
		"web-7d9-bbbbb INFO ns-a/nginx line-0", "web-7d9-bbbbb ERROR ns-a/nginx line-2")
	assert.Equal(t, "<deployment> <logs>", s.e2eCrumbs())

	s.Keys('/', "bbbbb", tcell.KeyEnter)
	s.e2eWait("web-7d9-bbbbb WARN")
	s.e2eGone("web-7d9-aaaaa WARN")
}
