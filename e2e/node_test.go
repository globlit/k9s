// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

const worker = ClusterName + "-worker"

func unschedulable(h *H) string {
	h.t.Helper()
	return h.Kubectl("get", "node", worker, "-o", "jsonpath={.spec.unschedulable}")
}

func TestDrainNode(t *testing.T) {
	h := New(t)
	t.Cleanup(func() { _, _ = kubectl(context.Background(), "uncordon", worker) })
	h.Apply("web.yaml")
	rolledOut(h, "web")
	h.Start("-c", "nodes")
	h.WaitFor("nodes(all)", 30*time.Second)
	h.Filter("worker")
	h.WaitFor(" "+worker+" ", 10*time.Second)

	h.SendKeys("r")
	h.WaitFor("Ignore DaemonSets:", 10*time.Second)
	h.SendKeys("Tab", "Tab", "Space", "Tab", "Tab", "Tab", "Tab", "Tab", "Enter")

	h.Eventually(15*time.Second, "worker is cordoned", func() bool { return unschedulable(h) == "true" })
	h.Eventually(60*time.Second, "web pods are evicted", func() bool {
		out := h.Kubectl("get", "pods", "-n", h.NS, "-o", "jsonpath={.items[*].spec.nodeName}")
		return !strings.Contains(out, worker)
	})

	h.SendKeys("Escape")
	h.WaitFor("nodes(all)", 10*time.Second)
	h.Filter("worker")
	h.WaitFor(" "+worker+" ", 10*time.Second)
	h.SendKeys("u")
	h.WaitFor("Uncordon "+worker, 10*time.Second)
	h.SendKeys("Tab", "Enter")
	h.Eventually(15*time.Second, "worker is schedulable", func() bool { return unschedulable(h) == "" })
	rolledOut(h, "web")
}

func shellPods(h *H) string {
	h.t.Helper()
	out := h.Kubectl("get", "pods", "-n", h.NS, "-o", "name")
	var pp []string
	for _, p := range strings.Fields(out) {
		if strings.HasPrefix(p, "pod/k9s-shell-") {
			pp = append(pp, p)
		}
	}

	return strings.Join(pp, " ")
}

func TestNodeShell(t *testing.T) {
	h := New(t)
	h.Start("-c", "nodes")
	h.WaitFor("nodes(all)", 30*time.Second)
	h.Filter("worker")
	h.WaitFor(" "+worker+" ", 10*time.Second)

	h.SendKeys("s")
	h.WaitFor("/ #", 120*time.Second)
	if shellPods(h) == "" {
		t.Fatal("no node shell pod while the shell is open")
	}
	h.Type("exit")
	h.SendKeys("Enter")
	h.WaitFor("nodes(all)", 30*time.Second)

	h.Eventually(60*time.Second, "node shell pod is gone", func() bool { return shellPods(h) == "" })
}
