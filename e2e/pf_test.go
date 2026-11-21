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
}
