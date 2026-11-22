// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestDailyQuotaRollout uses only the existing isolated kind harness. It is
// prepared for the coordinated E2E pass; unit-test runs do not execute it.
func TestDailyQuotaRollout(t *testing.T) {
	h := New(t)
	h.Kubectl("create", "quota", "daily-quota", "-n", h.NS, "--hard=pods=2")
	h.Apply("web.yaml")
	h.Kubectl("scale", "deployment/web", "-n", h.NS, "--replicas=5")
	h.Eventually(90*time.Second, "deployment reports quota failure", func() bool {
		return strings.Contains(h.Kubectl("get", "deployment/web", "-n", h.NS, "-o", "jsonpath={.status.conditions[*].message}"), "exceeded quota")
	})
	h.Start("-c", "deploy")
	h.WaitFor("QuotaExceeded", 30*time.Second)
	h.SendKeys("C-z")
	h.WaitFor("QuotaExceeded", 15*time.Second)
	h.SendKeys(":")
	h.Type("resourcequotas")
	h.SendKeys("Enter")
	h.WaitFor("daily-quota", 30*time.Second)
	h.WaitFor("2/2 (100%)", 30*time.Second)
	h.SendKeys("C-z")
	h.WaitFor("daily-quota", 15*time.Second)
}
