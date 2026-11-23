// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These cases are prepared for the isolated kind harness; they are not unit tests.
func TestDailyBulkPauseDeployments(t *testing.T) {
	h := New(t)
	for _, name := range []string{"daily-a", "daily-b", "daily-c"} {
		h.Kubectl("create", "deployment", name, "-n", h.NS, "--image=busybox:1.37.0", "--replicas=0")
	}
	h.Kubectl("patch", "deployment/daily-a", "-n", h.NS, "--type=merge", "-p", `{"spec":{"paused":true}}`)
	h.Start("-c", "deploy")
	h.WaitFor(" daily-c ", 30*time.Second)
	h.SendKeys("Space", "Down", "Space", "Down", "Space", "P")
	h.WaitFor("Pause 3 deployments?", 10*time.Second)
	h.SendKeys("Tab", "Enter")
	h.Eventually(20*time.Second, "all deployments are paused", func() bool {
		return h.Kubectl("get", "deployments", "-n", h.NS, "-o", "jsonpath={.items[*].spec.paused}") == "true true true"
	})
	h.SendKeys("P")
	h.WaitFor("Resume 3 deployments?", 10*time.Second)
	h.SendKeys("Tab", "Enter")
	h.Eventually(20*time.Second, "all deployments are resumed", func() bool {
		return h.Kubectl("get", "deployments", "-n", h.NS, "-o", "jsonpath={.items[*].spec.paused}") == "false false false"
	})
}

func TestDailyBulkSuspendCronJobs(t *testing.T) {
	h := New(t)
	for _, name := range []string{"daily-a", "daily-b", "daily-c"} {
		h.Kubectl("create", "cronjob", name, "-n", h.NS, "--image=busybox:1.37.0", "--schedule=0 0 1 1 *", "--", "true")
	}
	h.Kubectl("patch", "cronjob/daily-a", "-n", h.NS, "--type=merge", "-p", `{"spec":{"suspend":true}}`)
	h.Start("-c", "cronjobs")
	h.WaitFor(" daily-c ", 30*time.Second)
	h.SendKeys("Space", "Down", "Space", "Down", "Space", "s")
	h.WaitFor("Suspend 3 cronjobs?", 10*time.Second)
	h.SendKeys("Tab", "Enter")
	h.Eventually(20*time.Second, "all cronjobs are suspended", func() bool {
		return h.Kubectl("get", "cronjobs", "-n", h.NS, "-o", "jsonpath={.items[*].spec.suspend}") == "true true true"
	})
	h.SendKeys("s")
	h.WaitFor("Resume 3 cronjobs?", 10*time.Second)
	h.SendKeys("Tab", "Enter")
	h.Eventually(20*time.Second, "all cronjobs are resumed", func() bool {
		return h.Kubectl("get", "cronjobs", "-n", h.NS, "-o", "jsonpath={.items[*].spec.suspend}") == "false false false"
	})
}

func TestDailyResourceEvents(t *testing.T) {
	h := New(t)
	h.Kubectl("create", "configmap", "daily", "-n", h.NS)
	uid := h.Kubectl("get", "configmap/daily", "-n", h.NS, "-o", "jsonpath={.metadata.uid}")
	for _, item := range []struct{ name, uid, message string }{{"matching", uid, "selected-object-event"}, {"unrelated", "old-uid", "unrelated-object-event"}} {
		event := map[string]any{"apiVersion": "v1", "kind": "Event", "metadata": map[string]any{"name": item.name, "namespace": h.NS}, "involvedObject": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "namespace": h.NS, "name": "daily", "uid": item.uid}, "type": "Warning", "reason": "DailyTest", "message": item.message, "lastTimestamp": time.Now().UTC().Format(time.RFC3339)}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "event.json")
		if err = os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		h.Kubectl("create", "-f", file)
	}
	h.Start("-c", "configmaps")
	h.WaitFor(" daily ", 30*time.Second)
	h.SendKeys("E")
	screen := h.WaitFor("selected-object-event", 20*time.Second)
	if strings.Contains(screen, "unrelated-object-event") {
		t.Fatal("events view included a different UID")
	}
	h.SendKeys("C-z")
	h.WaitFor("selected-object-event", 10*time.Second)
}

func TestDailyRemoveFinalizers(t *testing.T) {
	h := New(t)
	h.Kubectl("create", "configmap", "daily-stuck", "-n", h.NS)
	h.Kubectl("patch", "configmap/daily-stuck", "-n", h.NS, "--type=merge", "-p", `{"metadata":{"finalizers":["example.com/hold"]}}`)
	// Clear this test finalizer even if UI verification fails, so namespace cleanup can finish.
	t.Cleanup(func() {
		_, _ = h.TryKubectl("patch", "configmap/daily-stuck", "-n", h.NS, "--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
	})
	h.Kubectl("delete", "configmap/daily-stuck", "-n", h.NS, "--wait=false")
	h.Start("-c", "configmaps")
	h.WaitFor(" daily-stuck ", 30*time.Second)
	h.SendKeys("C-d")
	h.WaitFor("Remove finalizers:", 10*time.Second)
	h.SendKeys("BTab", "Space", "Tab", "Tab", "Enter")
	h.Eventually(20*time.Second, "finalized resource is gone", func() bool { return !h.Exists("configmap/daily-stuck") })
}
