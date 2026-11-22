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

func message(h *H) string {
	h.t.Helper()
	return h.Kubectl("get", "-n", h.NS, "cm/demo-msg", "-o", "jsonpath={.data.message}")
}

func TestHelmRollbackUninstall(t *testing.T) {
	h := New(t)
	chart := filepath.Join("testdata", "chart")
	h.Helm("install", "demo", chart, "--wait")
	h.Helm("upgrade", "demo", chart, "--set", "message=two", "--wait")
	if got := message(h); got != "two" {
		t.Fatalf("message is %q after upgrade, want two", got)
	}

	h.Start("-c", "helm")
	h.WaitFor("helm("+h.NS+")", 30*time.Second)
	h.SendKeys("0")
	h.WaitFor("helm(all)", 10*time.Second)
	h.Filter("demo")
	h.WaitFor(" demo ", 10*time.Second)
	h.SendKeys("w")
	h.WaitFor("helm("+h.NS+")", 10*time.Second)
	h.Filter("demo")
	h.WaitFor(" demo ", 10*time.Second)
	h.SendKeys("Enter")
	h.WaitFor("helm-history(", 15*time.Second)
	h.Filter("superseded")
	h.WaitFor("[1]", 10*time.Second)
	h.SendKeys("r")
	h.WaitFor("Confirm Rollback", 10*time.Second)
	h.SendKeys("Tab", "Enter")

	h.Eventually(60*time.Second, "release is at revision 3", func() bool {
		return strings.Contains(h.Helm("history", "demo", "--max", "1"), "Rollback to 1")
	})
	if got := message(h); got != "one" {
		t.Fatalf("message is %q after rollback, want one", got)
	}

	h.SendKeys("Escape")
	h.WaitFor("helm("+h.NS+")", 10*time.Second)
	h.Filter("demo")
	h.WaitFor(" demo ", 10*time.Second)
	h.SendKeys("C-d")
	h.WaitFor("Delete", 10*time.Second)
	h.SendKeys("Tab", "Enter")

	h.Eventually(60*time.Second, "release is uninstalled", func() bool {
		return strings.TrimSpace(h.Helm("list", "-q")) == ""
	})
	h.Eventually(30*time.Second, "chart objects are gone", func() bool { return !h.Exists("cm/demo-msg") })
}
