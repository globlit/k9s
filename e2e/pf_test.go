// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

func TestPortForward(t *testing.T) {
	h := New(t)
	h.Apply("netexec.yaml")
	h.Kubectl("wait", "-n", h.NS, "pod/netexec", "--for=condition=Ready", "--timeout=180s")
	port := freePort(t)

	h.Start("-c", "pods")
	h.WaitFor("pods("+h.NS+")", 30*time.Second)
	h.Filter("netexec")
	h.WaitFor(" netexec ", 10*time.Second)
	h.SendKeys("F")
	h.WaitFor("Local Port:", 10*time.Second)
	h.SendKeys("Tab", "C-u")
	h.Type(fmt.Sprint(port))
	h.SendKeys("Tab", "Tab", "Enter")

	url := fmt.Sprintf("http://127.0.0.1:%d/hostname", port)
	var body string
	h.Eventually(30*time.Second, "GET "+url+" answers", func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		return resp.StatusCode == http.StatusOK
	})
	if strings.TrimSpace(body) != "netexec" {
		t.Fatalf("hostname is %q, want netexec", body)
	}

	// Namespace navigation uses local rows and preserves the live tunnel.
	h.Cmd("pf", "portforwards(")
	h.SendKeys("0")
	h.WaitFor("portforwards(all)", 10*time.Second)
	h.Filter("netexec")
	h.WaitFor(" netexec ", 10*time.Second)
	h.SendKeys("w")
	h.WaitFor("portforwards("+h.NS+")", 10*time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("forwarder unavailable after namespace warp: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || strings.TrimSpace(string(data)) != "netexec" {
		t.Fatalf("forwarder changed after namespace warp: status=%d body=%q err=%v", resp.StatusCode, data, err)
	}
}
