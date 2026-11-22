// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
)

// Use own context names: a context switch saves the context config to disk.
func TestE2EContextSwitch(t *testing.T) {
	fc := e2eCluster(t, fakecluster.WithContext("e2e-a"), fakecluster.WithContext("e2e-b"))
	s := NewSimApp(t, fc, SimView("pod"))
	s.e2eWait("Context: e2e-b", "web-7d9-aaaaa")

	t.Run("view", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":ctx", "Contexts", "contexts(all)[3]", "e2e-a", "e2e-b(*)")
		s.e2eMatch("e2e_contexts.txt")
		s.e2eGoto(":pod", "Pod")
	})
	t.Run("command", func(t *testing.T) {
		defer s.Use(t)()
		// Let the debounce run before the switch reloads the styles.
		s.Keys(":ctx e2e-a")
		s.e2eSettle()
		s.Keys(tcell.KeyEnter)
		s.e2eWait("Context: e2e-a")
		s.Eventually(func() bool { return s.App().Config.ActiveContextName() == "e2e-a" }, "context not switched")
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "no pods view after switch")
	})
	t.Run("enter", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":ctx", "Contexts", "e2e-a(*)")
		s.e2eFilter("e2e-b")
		s.e2eWait("contexts(all)[1]")
		s.Keys(tcell.KeyEnter)
		s.e2eWait("Context: e2e-b")
		s.Eventually(func() bool { return s.App().Config.ActiveContextName() == "e2e-b" }, "context not switched")
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "no pods view after switch")
	})
	t.Run("unknown", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys(":ctx nope")
		s.e2eSettle()
		s.Keys(tcell.KeyEnter)
		s.Sync()
		s.e2eWait("Context: e2e-b")
		assert.Equal(t, "e2e-b", s.App().Config.ActiveContextName())
		assert.Equal(t, -1, s.ExitCode())
	})
}

func TestE2EContextSwitchRace(t *testing.T) {
	t.Skip("bug: the command buffer debounce goroutine draws the prompt while a context switch reloads the styles; run with -race")
	fc := e2eCluster(t, fakecluster.WithContext("e2e-a"), fakecluster.WithContext("e2e-b"))
	s := NewSimApp(t, fc, SimView("pod"))
	s.e2eWait("Context: e2e-b")

	s.Cmd(":ctx e2e-a")
	s.e2eWait("Context: e2e-a")
	s.e2eSettle()
}

func TestE2EAliasesHelpHeader(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("web-7d9-aaaaa")

	t.Run("aliases", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys(tcell.KeyCtrlA)
		s.Eventually(func() bool { return s.TopView() == "Aliases" }, "no aliases view")
		s.e2eWait("RESOURCE", "deploy", "apps/v1/deployments")
		s.e2eFilter("configmaps")
		s.e2eWait("v1/configmaps", "cm")
		s.e2eGone("apps/v1/deployments")
		s.Keys(tcell.KeyEscape)
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "esc did not close aliases")
		s.Keys(tcell.KeyCtrlA)
		s.Eventually(func() bool { return s.TopView() == "Aliases" }, "no aliases view")
		s.Keys(tcell.KeyCtrlA)
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "ctrl-a did not close aliases")
	})
	t.Run("aliases-enter", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys(tcell.KeyCtrlA)
		s.Eventually(func() bool { return s.TopView() == "Aliases" }, "no aliases view")
		s.e2eFilter("statefulsets")
		s.e2eWait("apps/v1/statefulsets")
		s.Keys(tcell.KeyEnter)
		s.Eventually(func() bool { return s.TopView() == "StatefulSet" }, "enter did not open the alias")
		s.e2eWait("db")
		s.e2eGoto(":pod", "Pod")
	})
	t.Run("help", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('?')
		s.Eventually(func() bool { return s.TopView() == "help" }, "no help view")
		s.e2eWait("RESOURCE", "GENERAL", "NAVIGATION", "Sort Name", "Kill")
		s.e2eMatch("e2e_help_pods.txt")
		s.Keys(tcell.KeyEscape)
		s.Eventually(func() bool { return s.TopView() == "Pod" }, "esc did not close help")
	})
	t.Run("help-command", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":deploy", "Deployment", "web")
		s.Cmd(":help")
		s.Eventually(func() bool { return s.TopView() == "help" }, "no help view")
		s.e2eWait("Scale", "Restart")
		s.Keys(tcell.KeyEscape)
		s.Eventually(func() bool { return s.TopView() == "Deployment" }, "esc did not close help")
	})
	t.Run("header", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eWait("Context: fake")
		s.Keys(tcell.KeyCtrlE)
		s.e2eGone("Context: fake", "K8s Rev")
		s.e2eMatch("e2e_no_header.txt")
		s.Keys(tcell.KeyCtrlE)
		s.e2eWait("Context: fake", "K8s Rev")
	})
}

func TestE2ECrumbsToggle(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eWait("<pod>")

	s.Keys(tcell.KeyCtrlG)
	s.e2eGone("<pod>")
	s.Keys(tcell.KeyCtrlG)
	s.e2eWait("<pod>")
}

func TestE2ETerminalSizes(t *testing.T) {
	uu := []struct {
		w, h int
	}{
		{80, 24},
		{240, 60},
	}
	for _, u := range uu {
		t.Run(fmt.Sprintf("%dx%d", u.w, u.h), func(t *testing.T) {
			s := NewSimApp(t, e2eCluster(t), SimSize(u.w, u.h))
			s.e2eWait("web-7d9-aaaaa")
			s.e2eMatch(fmt.Sprintf("e2e_pods_%dx%d.txt", u.w, u.h))

			for _, c := range []string{":deploy", ":svc", ":no", ":ns", ":ctx", ":cm", ":pu", ":wk"} {
				s.Cmd(c)
				s.Sync()
			}
			s.e2eGoto(":pod", "Pod", "web-7d9-aaaaa")
			s.Keys('?')
			s.Eventually(func() bool { return s.TopView() == "help" }, "no help view")
			s.Keys(tcell.KeyEscape, tcell.KeyDown, 'd')
			s.Eventually(func() bool { return s.TopView() == "Describe" }, "no describe view")
			s.Keys(tcell.KeyEscape, 'y')
			s.Eventually(func() bool { return s.TopView() == "YAML" }, "no yaml view")
			s.Keys(tcell.KeyEscape)
			s.Eventually(func() bool { return s.TopView() == "Pod" }, "no pods view")
			assert.Equal(t, -1, s.ExitCode())
		})
	}

	t.Run("resize", func(t *testing.T) {
		s := NewSimApp(t, e2eCluster(t))
		s.e2eWait("web-7d9-aaaaa")
		for _, sz := range [][2]int{{80, 24}, {40, 10}, {1, 1}, {240, 60}, {160, 50}} {
			s.Resize(sz[0], sz[1])
		}
		s.e2eWait("web-7d9-aaaaa", "Context: fake")
		assert.False(t, s.Stopped())
	})
}
