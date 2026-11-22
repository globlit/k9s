// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// deleteDeploy deletes deployment name from the k9s deploy view. Set steps
// to the number of Down keys that select the propagation in the list.
func deleteDeploy(h *H, name string, steps int) {
	h.t.Helper()
	h.Start("-c", "deploy")
	h.WaitFor("deployments("+h.NS+")", 30*time.Second)
	h.Filter(name)
	h.WaitFor(" "+name+" ", 10*time.Second)
	h.SendKeys("C-d")
	h.WaitFor("Propagation:", 10*time.Second)
	h.SendKeys("BTab", "BTab", "Enter")
	for range steps {
		h.SendKeys("Down")
	}
	h.SendKeys("Enter", "Tab", "Tab", "Tab", "Enter")
}

func rolledOut(h *H, name string) {
	h.t.Helper()
	h.Kubectl("rollout", "status", "-n", h.NS, "deploy/"+name, "--timeout=120s")
}

func names(h *H, res string) string {
	h.t.Helper()
	return strings.TrimSpace(h.Kubectl("get", "-n", h.NS, res, "-o", "name"))
}

func TestDeleteDeployOrphan(t *testing.T) {
	h := New(t)
	h.Apply("slow.yaml")
	rolledOut(h, "slow")

	deleteDeploy(h, "slow", 2)

	h.Eventually(30*time.Second, "deployment is gone", func() bool { return !h.Exists("deploy/slow") })
	time.Sleep(3 * time.Second)
	if rs := names(h, "rs"); rs == "" {
		t.Fatal("orphan delete removed the replicasets")
	}
	owners := h.Kubectl("get", "-n", h.NS, "rs", "-o", "jsonpath={.items[*].metadata.ownerReferences}")
	if strings.TrimSpace(owners) != "" {
		t.Fatalf("orphan replicasets still have owners: %s", owners)
	}
	if pods := strings.Fields(names(h, "pods")); len(pods) != 2 {
		t.Fatalf("want 2 orphan pods, got %v", pods)
	}
}

func TestDeleteDeployForeground(t *testing.T) {
	h := New(t)
	h.Apply("slow.yaml")
	rolledOut(h, "slow")

	deleteDeploy(h, "slow", 1)

	h.Eventually(15*time.Second, "owner has the foregroundDeletion finalizer", func() bool {
		out, err := h.TryKubectl("get", "-n", h.NS, "deploy/slow", "-o", "jsonpath={.metadata.finalizers}")
		return err == nil && strings.Contains(out, "foregroundDeletion")
	})
	if names(h, "pods") == "" {
		t.Fatal("owner has the finalizer but its pods are already gone")
	}
	h.Eventually(90*time.Second, "pods are gone", func() bool { return names(h, "pods") == "" })
	h.Eventually(30*time.Second, "deployment is gone", func() bool { return !h.Exists("deploy/slow") })
	if rs := names(h, "rs"); rs != "" {
		t.Fatalf("replicasets remain: %s", rs)
	}
}

func TestDeleteDeployBackground(t *testing.T) {
	h := New(t)
	h.Apply("slow.yaml")
	rolledOut(h, "slow")

	deleteDeploy(h, "slow", 0)

	h.Eventually(30*time.Second, "deployment is gone", func() bool { return !h.Exists("deploy/slow") })
	h.Eventually(30*time.Second, "replicasets are gone", func() bool { return names(h, "rs") == "" })
	h.Eventually(90*time.Second, "pods are gone", func() bool { return names(h, "pods") == "" })
}

func TestForceDeletePod(t *testing.T) {
	h := New(t)
	h.Apply("sleeper.yaml")
	h.Kubectl("wait", "-n", h.NS, "pod/sleeper", "--for=condition=Ready", "--timeout=120s")

	h.Start("-c", "pods")
	h.WaitFor("pods("+h.NS+")", 30*time.Second)
	h.Filter("sleeper")
	h.WaitFor(" sleeper ", 10*time.Second)
	h.SendKeys("C-d")
	h.WaitFor("Force:", 10*time.Second)
	h.SendKeys("BTab", "Space", "Tab", "Tab", "Enter")

	// The pod has a grace period of 120s: only a force delete is this fast.
	h.Eventually(20*time.Second, "pod is gone", func() bool { return !h.Exists("pod/sleeper") })
}
