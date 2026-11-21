// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLoopbackAddr(t *testing.T) {
	uu := map[string]bool{
		"127.0.0.1:6060": true,
		"localhost:6060": true,
		"[::1]:6060":     true,
		":6060":          false,
		"0.0.0.0:6060":   false,
		"10.0.0.5:6060":  false,
		"[::]:6060":      false,
		"bad":            false,
	}
	for addr, e := range uu {
		assert.Equal(t, e, isLoopbackAddr(addr), addr)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return &buf
}

func TestStartProfilingCPU(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	t.Setenv(envCPUProfile, path)
	t.Setenv(envPprofAddr, "")

	stop := startProfiling()
	for i := 0; i < 1_000_000; i++ {
		_ = fmt.Sprint(i)
	}
	stop()
	stop()

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, fi.Size())
}

func TestStartProfilingBadCPUPath(t *testing.T) {
	buf := captureLog(t)
	t.Setenv(envCPUProfile, filepath.Join(t.TempDir(), "nope", "cpu.pprof"))
	t.Setenv(envPprofAddr, "")

	startProfiling()()
	assert.Contains(t, buf.String(), "CPU profile disabled")
}

func TestServePprofRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0", "10.1.2.3:6060", "nope"} {
		buf := captureLog(t)
		servePprof(addr)
		assert.Contains(t, buf.String(), "Refusing pprof", addr)
		assert.NotContains(t, buf.String(), "pprof listening", addr)
	}
}

func TestServePprofLoopback(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	servePprof(addr)
	var resp *http.Response
	require.Eventually(t, func() bool {
		resp, err = http.Get("http://" + addr + "/debug/pprof/")
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApplyMemoryLimit(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })

	t.Run("env-set", func(t *testing.T) {
		t.Setenv("GOMEMLIMIT", "123MiB")
		debug.SetMemoryLimit(123 << 20)
		applyMemoryLimit()
		assert.Equal(t, int64(123<<20), debug.SetMemoryLimit(-1))
	})

	t.Run("env-unset", func(t *testing.T) {
		t.Setenv("GOMEMLIMIT", "")
		require.NoError(t, os.Unsetenv("GOMEMLIMIT"))
		debug.SetMemoryLimit(123 << 20)
		applyMemoryLimit()
		assert.Equal(t, int64(defaultMemoryLimit), debug.SetMemoryLimit(-1))
		assert.Equal(t, int64(2<<30), int64(defaultMemoryLimit))
	})
}
