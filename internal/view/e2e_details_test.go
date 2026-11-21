// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/testutil/expect"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
)

func TestE2EYAML(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eGoto(":cm ns-a", "ConfigMap", "web-cfg")

	s.Keys('y')
	s.Eventually(func() bool { return s.TopView() == "YAML" }, "no yaml view")
	s.e2eWait("kind: ConfigMap", "name: web-cfg", "namespace: ns-a", "mode: prod")
	assert.Equal(t, "<configmap> <yaml>", s.e2eCrumbs())
	expect.Match(t, "e2e_yaml_cm.txt", e2eMask(s.Screen()))

	t.Run("search", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('/', "mode", tcell.KeyEnter)
		s.e2eWait("YAML(ns-a/web-cfg) </mode[1:1]>")
		s.Keys('n')
		s.e2eWait("YAML(ns-a/web-cfg) </mode[1:1]>")
	})
	t.Run("search-no-match", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('/', "zzz", tcell.KeyEnter)
		s.e2eWait("YAML(ns-a/web-cfg) </zzz>")
		s.e2eGone("</mode")
	})
	t.Run("back", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys(tcell.KeyEscape)
		s.Eventually(func() bool { return s.TopView() == "ConfigMap" }, "esc did not go back")
	})
	t.Run("secret", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":sec ns-a", "Secret", "web-tls")
		s.Keys('y')
		s.Eventually(func() bool { return s.TopView() == "YAML" }, "no yaml view")
		s.e2eWait("kind: Secret", "name: web-tls")
		s.Keys(tcell.KeyEscape)
	})
	t.Run("pod", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod ns-a /api", "Pod", "api-0")
		s.Keys('y')
		s.Eventually(func() bool { return s.TopView() == "YAML" }, "no yaml view")
		s.e2eWait("kind: Pod", "name: api-0", "image: nginx:1.27")
		s.Keys(tcell.KeyEscape)
	})
}

func TestE2EDescribe(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eGoto(":cm ns-a", "ConfigMap", "web-cfg")

	// The describer dials the REST config, so the fake cluster gives no text.
	s.Keys('d')
	s.Eventually(func() bool { return s.TopView() == "Describe" }, "no describe view")
	s.e2eWait("Describe(ns-a/web-cfg)")
	assert.Equal(t, "<configmap> <describe>", s.e2eCrumbs())

	s.Keys('/', "web", tcell.KeyEnter)
	s.e2eWait("Describe(ns-a/web-cfg) </web>")
	s.Keys(tcell.KeyEscape)
	s.Eventually(func() bool { return s.TopView() == "ConfigMap" }, "esc did not go back")
}

func TestE2EXray(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eGoto(":xray deploy ns-a", "XRay", "web", "web-7d9-aaaaa", "web-7d9-bbbbb")
	expect.Match(t, "e2e_xray_deploy.txt", e2eMask(s.Screen()))

	t.Run("filter", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('/', "aaaaa", tcell.KeyEnter)
		s.e2eWait("web-7d9-aaaaa")
		s.e2eGone("web-7d9-bbbbb")
		s.Keys(tcell.KeyEscape)
		s.e2eWait("web-7d9-aaaaa", "web-7d9-bbbbb")
	})
	// Match an invalid regex as a literal. No node id holds "aaaaa[".
	t.Run("literal-fallback", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('/', "aaaaa[", tcell.KeyEnter)
		s.e2eWait("No data...")
		s.e2eGone("web-7d9-aaaaa", "web-7d9-bbbbb")
		assert.Equal(t, "XRay", s.TopView())
		s.Keys(tcell.KeyEscape)
		s.e2eWait("web-7d9-aaaaa", "web-7d9-bbbbb")
	})
}
