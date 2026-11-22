// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These scenarios are prepared for the coordinated daily-driver E2E pass.
func TestDailyJSONLogs(t *testing.T) {
	h := New(t)
	h.Apply("json-logs.yaml")
	h.Kubectl("wait", "-n", h.NS, "--for=condition=Ready", "pod/json-logs", "--timeout=90s")
	h.Start("-c", "pods")
	h.WaitFor("json-logs", 30*time.Second)
	h.Filter("json-logs")
	h.SendKeys("l")
	h.WaitFor("ordinary-message", 15*time.Second)
	h.SendKeys("J")
	h.WaitFor("JSON:On", 10*time.Second)
	h.WaitFor(`level="error"`, 10*time.Second)
	h.WaitFor("9007199254740993", 10*time.Second)
	h.Filter("-j level=error")
	h.Eventually(10*time.Second, "only error JSON items remain", func() bool {
		screen := h.Capture()
		return strings.Contains(screen, "error-message [red]") && !strings.Contains(screen, "ordinary-message")
	})
}

func TestDailyHelmDiff(t *testing.T) {
	h := New(t)
	chart := filepath.Join("testdata", "chart")
	h.Helm("install", "demo", chart, "--wait")
	h.Helm("upgrade", "demo", chart, "--set", "message=two", "--wait")
	h.Start("-c", "helm")
	h.WaitFor("helm("+h.NS+")", 30*time.Second)
	h.Filter("demo")
	h.SendKeys("Enter")
	h.WaitFor("helm-history(", 15*time.Second)
	h.Filter("deployed")
	h.SendKeys("D")
	h.WaitFor("Diff Values", 15*time.Second)
	h.WaitFor("+message: two", 10*time.Second)
	h.SendKeys("m")
	h.WaitFor("Diff Manifest", 10*time.Second)
	h.WaitFor("message:", 10*time.Second)
}

func TestDailyReplicaSetDiff(t *testing.T) {
	h := New(t)
	h.Apply("web.yaml")
	rolledOut(h, "web")
	old := strings.TrimSpace(h.Kubectl("get", "-n", h.NS, "rs", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}"))
	h.Kubectl("patch", "-n", h.NS, "deployment/web", "--type=merge", "-p", `{"spec":{"template":{"metadata":{"annotations":{"daily-diff":"updated"}}}}}`)
	rolledOut(h, "web")
	h.Start("-c", "rs")
	h.WaitFor("replicasets("+h.NS+")", 30*time.Second)
	h.Filter(old)
	h.SendKeys("D")
	h.WaitFor("Diff", 15*time.Second)
	h.WaitFor("+    daily-diff: updated", 10*time.Second)
}
