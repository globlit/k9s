// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e && linux

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/testutil/procstats"
	"github.com/stretchr/testify/require"
)

func appPID(h *H) int {
	h.t.Helper()
	output, err := exec.Command("tmux", "-S", h.socket, "display-message", "-p", "-t", h.session, "#{pane_pid}").Output()
	require.NoError(h.t, err)
	parent, err := strconv.Atoi(strings.TrimSpace(string(output)))
	require.NoError(h.t, err)
	want, err := filepath.EvalSymlinks(env.Bin)
	require.NoError(h.t, err)
	pid := 0
	h.Eventually(10*time.Second, "test-owned k9s process not found", func() bool {
		// Only inspect children of this test's private tmux pane.
		children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", parent, parent))
		if err != nil {
			return false
		}
		for _, child := range strings.Fields(string(children)) {
			exe, err := os.Readlink("/proc/" + child + "/exe")
			if err == nil && exe == want {
				pid, _ = strconv.Atoi(child)
				return pid > 0
			}
		}
		return false
	})
	return pid
}

func TestLogStreamResourceBudget(t *testing.T) {
	h := New(t)
	h.Apply("log-stream.yaml")
	h.Kubectl("wait", "-n", h.NS, "--for=condition=Ready", "pod/log-stream", "--timeout=90s")
	h.Start("-c", "pods")
	h.WaitFor("log-stream", 30*time.Second)
	h.SendKeys("l")
	h.WaitFor("stream-line-", 30*time.Second)
	pid := appPID(h)
	ticksRaw, err := exec.Command("getconf", "CLK_TCK").Output()
	require.NoError(t, err)
	ticks, err := strconv.ParseFloat(strings.TrimSpace(string(ticksRaw)), 64)
	require.NoError(t, err)
	require.Positive(t, ticks)
	linePattern := regexp.MustCompile(`stream-line-([0-9]+)`)
	latest := func() uint64 {
		var max uint64
		for _, match := range linePattern.FindAllStringSubmatch(h.Capture(), -1) {
			n, err := strconv.ParseUint(match[1], 10, 64)
			require.NoError(t, err)
			if n > max {
				max = n
			}
		}
		return max
	}
	// Fill and rotate the default 5000-line buffer before taking the baseline.
	h.Eventually(30*time.Second, "log stream did not fill its retention window", func() bool { return latest() >= 6000 })
	time.Sleep(5 * time.Second)
	baseline, err := procstats.Read(pid, ticks)
	require.NoError(t, err)
	samples := []procstats.Sample{baseline}
	t.Cleanup(func() {
		if env.Artifacts == "" {
			return
		}
		dir := filepath.Join(env.Artifacts, t.Name())
		require.NoError(t, os.MkdirAll(dir, 0o755))
		raw, err := json.MarshalIndent(samples, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "resources.json"), raw, 0o600))
	})
	before, previousLine := baseline, latest()
	for window := 1; window <= 3; window++ {
		time.Sleep(10 * time.Second)
		after, err := procstats.Read(pid, ticks)
		require.NoError(t, err)
		samples = append(samples, after)
		cpuCores := (after.CPUSeconds - before.CPUSeconds) / after.At.Sub(before.At).Seconds()
		t.Logf("window=%d cpu_cores=%.3f rss=%d fds=%d", window, cpuCores, after.RSSBytes, after.FDs)
		require.LessOrEqual(t, after.RSSBytes, baseline.RSSBytes+24*1024*1024, "RSS growth after log buffer warmup")
		require.LessOrEqual(t, after.FDs, baseline.FDs+4, "descriptor growth during steady log streaming")
		require.LessOrEqual(t, cpuCores, 0.75, "steady log stream exceeds 75%% of one CPU core")
		currentLine := latest()
		require.Greater(t, currentLine, previousLine, "log display stopped progressing")
		before, previousLine = after, currentLine
	}
}
