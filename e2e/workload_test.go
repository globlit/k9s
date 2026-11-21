// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func replicas(h *H, name string) string {
	h.t.Helper()
	return h.Kubectl("get", "-n", h.NS, "deploy/"+name, "-o", "jsonpath={.spec.replicas}/{.status.readyReplicas}")
}

func (h *H) scale(name, n string) {
	h.t.Helper()
	h.Filter(name)
	h.WaitFor(" "+name+" ", 10*time.Second)
	h.SendKeys("s")
	h.WaitFor("Replicas:", 10*time.Second)
	h.SendKeys("BSpace")
	h.Type(n)
	h.SendKeys("Tab", "Enter")
	h.WaitFor("scaled successfully", 10*time.Second)
}

func TestScaleDeploy(t *testing.T) {
	h := New(t)
	h.Apply("web.yaml")
	rolledOut(h, "web")
	h.Start("-c", "deploy")
	h.WaitFor("deployments("+h.NS+")", 30*time.Second)

	h.scale("web", "0")
	h.Eventually(60*time.Second, "web has 0 replicas", func() bool {
		return replicas(h, "web") == "0/" && names(h, "pods") == ""
	})

	h.WaitFor(" 0/0 ", 15*time.Second)
	h.scale("web", "3")
	h.Eventually(90*time.Second, "web has 3 ready replicas", func() bool { return replicas(h, "web") == "3/3" })
}

func TestRestartDeploy(t *testing.T) {
	h := New(t)
	h.Apply("web.yaml")
	rolledOut(h, "web")
	h.Start("-c", "deploy")
	h.WaitFor("deployments("+h.NS+")", 30*time.Second)
	h.Filter("web")
	h.WaitFor(" web ", 10*time.Second)

	h.SendKeys("r")
	h.WaitFor("Restart deployment", 10*time.Second)
	h.SendKeys("Tab", "Enter")

	h.Eventually(15*time.Second, "pod template has restartedAt", func() bool {
		out := h.Kubectl("get", "-n", h.NS, "deploy/web", "-o",
			`jsonpath={.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`)
		_, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
		return err == nil
	})
	rolledOut(h, "web")
}

func TestTriggerCronJob(t *testing.T) {
	h := New(t)
	h.Apply("cronjob.yaml")
	h.Start("-c", "cronjobs")
	h.WaitFor("cronjobs("+h.NS+")", 30*time.Second)
	h.Filter("tick")
	h.WaitFor(" tick ", 10*time.Second)

	h.SendKeys("t")
	h.WaitFor("Trigger CronJob", 10*time.Second)
	h.SendKeys("Tab", "Enter")

	h.Eventually(30*time.Second, "a job exists", func() bool {
		return strings.HasPrefix(names(h, "jobs"), "job.batch/tick-")
	})
	owner := h.Kubectl("get", "-n", h.NS, "jobs", "-o", "jsonpath={.items[0].metadata.ownerReferences[0].name}")
	if owner != "tick" {
		t.Fatalf("job owner is %q, want tick", owner)
	}
}
