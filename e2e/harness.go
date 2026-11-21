// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

// Package e2e drives the real k9s binary in tmux against the kind cluster
// k9s-e2e. It refuses to run on any other cluster.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

const (
	// ClusterName is the only kind cluster the tests use.
	ClusterName = "k9s-e2e"
	// ContextName is the only kube context the tests use.
	ContextName = "kind-" + ClusterName

	cols, rows = 200, 50
)

// Env holds the state that all tests share.
type Env struct {
	Bin        string
	Kubeconfig string
	Dir        string
	Artifacts  string
}

var env Env

// Setup builds k9s and writes the scratch kubeconfig. Call it from TestMain.
func Setup() (func(), error) {
	dir, err := os.MkdirTemp("", "k9s-e2e-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	env = Env{Dir: dir, Artifacts: os.Getenv("K9S_E2E_ARTIFACTS")}

	if err := setup(dir); err != nil {
		cleanup()
		return nil, err
	}

	return cleanup, nil
}

func setup(dir string) error {
	for _, tool := range []string{"kind", "kubectl", "tmux", "helm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("tool %q is not on PATH: %w", tool, err)
		}
	}

	env.Bin = os.Getenv("K9S_E2E_BIN")
	if env.Bin == "" {
		env.Bin = filepath.Join(dir, "k9s")
		cmd := exec.Command("go", "build", "-o", env.Bin, "github.com/globlit/k9s")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build k9s: %w\n%s", err, out)
		}
	}

	raw, err := exec.Command("kind", "get", "kubeconfig", "--name", ClusterName).Output()
	if err != nil {
		return fmt.Errorf("kind cluster %q is not available (run make e2e-kind-up): %w", ClusterName, err)
	}
	if err := checkKubeconfig(raw); err != nil {
		return err
	}
	env.Kubeconfig = filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(env.Kubeconfig, raw, 0o600); err != nil {
		return err
	}
	out, err := kubectl(context.Background(), "config", "current-context")
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(out); got != ContextName {
		return fmt.Errorf("refuse to run: current context is %q, want %q", got, ContextName)
	}

	return nil
}

// checkKubeconfig refuses any kubeconfig that holds more than the kind
// context or that points away from the local host.
func checkKubeconfig(raw []byte) error {
	cfg, err := clientcmd.Load(raw)
	if err != nil {
		return fmt.Errorf("parse kubeconfig: %w", err)
	}
	if cfg.CurrentContext != ContextName {
		return fmt.Errorf("refuse to run: current context is %q, want %q", cfg.CurrentContext, ContextName)
	}
	if len(cfg.Contexts) != 1 || cfg.Contexts[ContextName] == nil {
		return fmt.Errorf("refuse to run: kubeconfig must hold only context %q", ContextName)
	}
	cl := cfg.Clusters[cfg.Contexts[ContextName].Cluster]
	if cl == nil || !regexp.MustCompile(`^https://(127\.0\.0\.1|localhost|\[::1\]):\d+$`).MatchString(cl.Server) {
		return errors.New("refuse to run: the kind API server must be on the local host")
	}

	return nil
}

func kubectl(ctx context.Context, args ...string) (string, error) {
	args = append([]string{"--kubeconfig", env.Kubeconfig, "--context", ContextName}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+env.Kubeconfig)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w\n%s", strings.Join(args[4:], " "), err, stderr.String())
	}

	return stdout.String(), nil
}

// H drives one k9s session in one test namespace.
type H struct {
	t       *testing.T
	NS      string
	home    string
	socket  string
	session string
	started bool
	last    string
}

// New makes a test namespace and removes it when the test ends.
func New(t *testing.T) *H {
	t.Helper()
	if env.Kubeconfig == "" {
		t.Fatal("e2e env is not set: call Setup from TestMain")
	}
	id := randID()
	name := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(t.Name(), "-"))
	if len(name) > 40 {
		name = name[:40]
	}
	h := H{
		t:       t,
		NS:      strings.Trim("e2e-"+name, "-") + "-" + id,
		home:    t.TempDir(),
		socket:  "k9s-e2e-" + id,
		session: "k9s-e2e-" + id,
	}
	h.Kubectl("create", "namespace", h.NS)
	t.Cleanup(h.cleanup)

	return &h
}

func (h *H) cleanup() {
	if h.t.Failed() && h.started {
		h.saveArtifacts()
	}
	if h.started {
		_ = exec.Command("tmux", "-L", h.socket, "kill-server").Run()
	}
	_, _ = kubectl(context.Background(), "delete", "namespace", h.NS, "--wait=false", "--ignore-not-found")
}

func (h *H) saveArtifacts() {
	if env.Artifacts == "" {
		h.t.Logf("last screen:\n%s", h.last)
		return
	}
	dir := filepath.Join(env.Artifacts, strings.ReplaceAll(h.t.Name(), "/", "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Log(err)
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "screen.txt"), []byte(h.Capture()), 0o600)
	if b, err := os.ReadFile(filepath.Join(h.home, "k9s.log")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "k9s.log"), b, 0o600)
	}
}

// Start runs k9s in a new detached tmux session on a private tmux socket.
func (h *H) Start(args ...string) {
	h.t.Helper()
	h.writeConfig()
	envs := map[string]string{
		"KUBECONFIG":                  env.Kubeconfig,
		"K9S_CONFIG_DIR":              filepath.Join(h.home, "config"),
		"XDG_CONFIG_HOME":             filepath.Join(h.home, "xdg-config"),
		"XDG_DATA_HOME":               filepath.Join(h.home, "xdg-data"),
		"XDG_STATE_HOME":              filepath.Join(h.home, "xdg-state"),
		"XDG_CACHE_HOME":              filepath.Join(h.home, "xdg-cache"),
		"HELM_CONFIG_HOME":            filepath.Join(h.home, "helm-config"),
		"HELM_CACHE_HOME":             filepath.Join(h.home, "helm-cache"),
		"HELM_DATA_HOME":              filepath.Join(h.home, "helm-data"),
		"K9S_FEATURE_GATE_NODE_SHELL": "true",
		"EDITOR":                      "true",
		"KUBE_EDITOR":                 "true",
		"TERM":                        "xterm-256color",
	}
	argv := append([]string{
		env.Bin,
		"--kubeconfig", env.Kubeconfig,
		"--context", ContextName,
		"--namespace", h.NS,
		"--logFile", filepath.Join(h.home, "k9s.log"),
		"--logLevel", "debug",
	}, args...)
	var cmd strings.Builder
	for k, v := range envs {
		fmt.Fprintf(&cmd, "%s=%s ", k, shellQuote(v))
	}
	for _, a := range argv {
		cmd.WriteString(shellQuote(a) + " ")
	}
	// Keep the pane open after k9s exits so the exit code stays visible.
	cmd.WriteString(`; echo "k9s exited: $?"; sleep 3600`)

	tmux := exec.Command("tmux", "-L", h.socket, "-f", "/dev/null",
		"new-session", "-d", "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows), "-s", h.session,
		"sh", "-c", "env "+cmd.String())
	tmux.Env = append(os.Environ(), "TERM=xterm-256color")
	if out, err := tmux.CombinedOutput(); err != nil {
		h.t.Fatalf("start tmux: %v\n%s", err, out)
	}
	h.started = true
}

func (h *H) writeConfig() {
	h.t.Helper()
	dir := filepath.Join(h.home, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	cfg := `k9s:
  refreshRate: 1
  skipLatestRevCheck: true
  ui:
    splashless: true
    logoless: true
  imageScans:
    enable: false
  shellPod:
    image: busybox:1.36
    namespace: ` + h.NS + `
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// SendKeys sends tmux key names, for example "C-d", "Tab" or "Enter".
func (h *H) SendKeys(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.tmux("send-keys", "-t", h.session, k)
		time.Sleep(150 * time.Millisecond)
	}
}

// Type sends literal text.
func (h *H) Type(s string) {
	h.t.Helper()
	h.tmux("send-keys", "-t", h.session, "-l", s)
	time.Sleep(150 * time.Millisecond)
}

// Cmd runs a k9s command such as "deploy" and waits for its view title.
func (h *H) Cmd(cmd, title string) {
	h.t.Helper()
	h.SendKeys(":")
	h.Type(cmd)
	h.SendKeys("Enter")
	h.WaitFor(title, 30*time.Second)
}

// Filter shows only the rows that match q.
func (h *H) Filter(q string) {
	h.t.Helper()
	h.SendKeys("/")
	h.Type(q)
	h.SendKeys("Enter")
}

// Capture returns the visible pane text.
func (h *H) Capture() string {
	h.t.Helper()
	out, err := exec.Command("tmux", "-L", h.socket, "capture-pane", "-p", "-t", h.session).Output()
	if err != nil {
		h.t.Logf("capture-pane: %v", err)
		return h.last
	}
	h.last = string(out)

	return h.last
}

// WaitFor waits until the screen holds sub and returns the screen.
func (h *H) WaitFor(sub string, timeout time.Duration) string {
	h.t.Helper()
	var scr string
	ok := poll(timeout, func() bool {
		scr = h.Capture()
		return strings.Contains(scr, sub)
	})
	if !ok {
		h.t.Fatalf("screen does not show %q after %s:\n%s", sub, timeout, scr)
	}

	return scr
}

// Eventually waits until cond is true.
func (h *H) Eventually(timeout time.Duration, msg string, cond func() bool) {
	h.t.Helper()
	if !poll(timeout, cond) {
		h.t.Fatalf("%s: not true after %s\nscreen:\n%s", msg, timeout, h.last)
	}
}

// Kubectl runs kubectl on the kind context and fails the test on error.
func (h *H) Kubectl(args ...string) string {
	h.t.Helper()
	out, err := h.TryKubectl(args...)
	if err != nil {
		h.t.Fatal(err)
	}

	return out
}

// TryKubectl runs kubectl on the kind context.
func (h *H) TryKubectl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	return kubectl(ctx, args...)
}

// Apply applies a fixture from testdata in the test namespace.
func (h *H) Apply(fixture string) {
	h.t.Helper()
	h.Kubectl("apply", "-n", h.NS, "-f", filepath.Join("testdata", fixture))
}

// Exists reports whether kubectl finds the resource in the test namespace.
func (h *H) Exists(res string) bool {
	h.t.Helper()
	out, err := h.TryKubectl("get", "-n", h.NS, res, "--ignore-not-found", "-o", "name")
	if err != nil {
		h.t.Fatal(err)
	}

	return strings.TrimSpace(out) != ""
}

// Helm runs the helm CLI on the kind context in the test namespace.
func (h *H) Helm(args ...string) string {
	h.t.Helper()
	args = append([]string{"--kubeconfig", env.Kubeconfig, "--kube-context", ContextName, "-n", h.NS}, args...)
	cmd := exec.Command("helm", args...)
	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+env.Kubeconfig,
		"HELM_CONFIG_HOME="+filepath.Join(h.home, "helm-config"),
		"HELM_CACHE_HOME="+filepath.Join(h.home, "helm-cache"),
		"HELM_DATA_HOME="+filepath.Join(h.home, "helm-data"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("helm %s: %v\n%s", strings.Join(args[5:], " "), err, out)
	}

	return string(out)
}

func (h *H) tmux(args ...string) {
	h.t.Helper()
	if out, err := exec.Command("tmux", append([]string{"-L", h.socket}, args...)...).CombinedOutput(); err != nil {
		h.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
}

func poll(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func randID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
