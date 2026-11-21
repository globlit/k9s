// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

const rvPath = `jsonpath={range .items[*]}{.kind} {.metadata.namespace}/{.metadata.name} {.metadata.resourceVersion}{"\n"}{end}`

// snapshot returns the resource version of each object that k9s can change.
// Skip the namespaces of other tests: they can still be terminating.
func snapshot(h *H) string {
	h.t.Helper()
	objs := h.Kubectl("get", "-A", "deploy,rs,sts,ds,pods,jobs,cronjobs,configmaps,secrets,services", "-o", rvPath)
	var keep []string
	for _, l := range strings.Split(objs, "\n") {
		if ff := strings.Fields(l); len(ff) == 3 && (!strings.HasPrefix(ff[1], "e2e-") || strings.HasPrefix(ff[1], h.NS+"/")) {
			keep = append(keep, l)
		}
	}
	nodes := h.Kubectl("get", "nodes", "-o", `jsonpath={range .items[*]}{.metadata.name} {.spec.unschedulable}{"\n"}{end}`)

	return strings.Join(keep, "\n") + "\n" + nodes
}

var dialogMarks = []string{
	"<Delete>", "<Scale>", "<Drain>", "<PortForward>", "Confirm", "Propagation:",
	"Replicas:", "FieldManager:", "Ignore DaemonSets:", "Launching", "K9s-Shell",
	"pod(s)?", "Rollback apps",
}

func TestReadOnly(t *testing.T) {
	h := New(t)
	h.Apply("web.yaml")
	h.Apply("cronjob.yaml")
	rolledOut(h, "web")
	time.Sleep(3 * time.Second)
	before := snapshot(h)

	h.Start("--readonly", "-c", "deploy")
	h.WaitFor("deployments("+h.NS+")", 30*time.Second)
	for _, v := range []struct{ cmd, title, row string }{
		{"deploy", "deployments(", "web"},
		{"rs", "replicasets(", "web"},
		{"pods", "pods(", "web"},
		{"cronjobs", "cronjobs(", "tick"},
		{"nodes", "nodes(", "worker"},
	} {
		h.Cmd(v.cmd, v.title)
		h.Filter(v.row)
		h.WaitFor(v.row, 10*time.Second)
		for _, k := range []string{"C-d", "C-k", "s", "e", "C-l", "r", "t", "c", "u"} {
			h.SendKeys(k)
			time.Sleep(500 * time.Millisecond)
			scr := h.Capture()
			for _, m := range dialogMarks {
				if strings.Contains(scr, m) {
					t.Errorf("%s view: key %s opens %q in read-only mode", v.cmd, k, m)
				}
			}
			h.SendKeys("Escape")
			if !strings.Contains(h.Capture(), v.title) {
				h.Cmd(v.cmd, v.title)
				h.Filter(v.row)
			}
		}
	}

	time.Sleep(3 * time.Second)
	if after := snapshot(h); after != before {
		t.Fatalf("read-only k9s changed the cluster:\n%s", diff(before, after))
	}
}

func diff(a, b string) string {
	in := func(s string) map[string]bool {
		m := make(map[string]bool)
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	var out []string
	for l := range ma {
		if !mb[l] {
			out = append(out, "- "+l)
		}
	}
	for l := range mb {
		if !ma[l] {
			out = append(out, "+ "+l)
		}
	}

	return strings.Join(out, "\n")
}
