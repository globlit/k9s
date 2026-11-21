// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Stray writes to the terminal while the TUI owns it corrupt the screen:
// tcell only repaints cells it believes changed, so the garbage stays until
// something redraws those rows. The main offender is client-go's exec
// credential plugin (e.g. kubelogin/OIDC), which inherits os.Stderr and
// prints a login URL whenever a token expires mid-session.
//
// client-go captures the os.Stderr *variable* when an authenticator is
// created, so k9s swaps os.Stderr for a pipe before any client is built and
// routes the output: straight to the terminal while the TUI is not drawing
// (startup, suspended shells), and to the log + flash bar while it is.
// Go runtime fatal errors still write to fd 2 directly and stay visible.

var (
	realStderr = os.Stderr

	// stderrCaptured is true while the TUI owns the terminal.
	stderrCaptured atomic.Bool

	stderrHandlerMx sync.RWMutex
	stderrHandler   func(line string)
)

// RealStderr returns the terminal's stderr, bypassing the capture pipe.
// Use it for child processes that legitimately own the terminal.
func RealStderr() *os.File {
	return realStderr
}

// CaptureStderr routes writes to os.Stderr through k9s. It must be called
// before any Kubernetes client is created. The returned func restores the
// original os.Stderr.
func CaptureStderr() (restore func(), err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}, err
	}
	realStderr = os.Stderr
	os.Stderr = w
	go pumpStderr(r)

	return func() {
		SetStderrCaptured(false)
		os.Stderr = realStderr
	}, nil
}

// SetStderrCaptured toggles whether stderr output is kept off the terminal.
// Set it while the TUI is drawing; clear it while the terminal is handed to
// a child process (shell, editor, plugin).
func SetStderrCaptured(b bool) {
	stderrCaptured.Store(b)
}

// SetStderrHandler registers a callback for captured stderr lines.
func SetStderrHandler(h func(line string)) {
	stderrHandlerMx.Lock()
	defer stderrHandlerMx.Unlock()

	stderrHandler = h
}

func notifyStderr(line string) {
	stderrHandlerMx.RLock()
	h := stderrHandler
	stderrHandlerMx.RUnlock()
	if h != nil {
		h(line)
	}
}

func pumpStderr(r *os.File) {
	buf := make([]byte, 32*1024)
	var pending bytes.Buffer
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if !stderrCaptured.Load() {
				// Pass through as-is (no line buffering) so interactive
				// prompts without a trailing newline still show up.
				flushPending(&pending)
				_, _ = realStderr.Write(chunk)
			} else {
				pending.Write(chunk)
				drainLines(&pending)
			}
		}
		if err != nil {
			flushPending(&pending)
			return
		}
	}
}

func drainLines(b *bytes.Buffer) {
	for {
		idx := bytes.IndexByte(b.Bytes(), '\n')
		if idx < 0 {
			// Guard against unbounded growth from output without newlines.
			if b.Len() > 64*1024 {
				handleCapturedLine(b.String())
				b.Reset()
			}
			return
		}
		line := string(b.Next(idx + 1))
		handleCapturedLine(line)
	}
}

func flushPending(b *bytes.Buffer) {
	if b.Len() == 0 {
		return
	}
	handleCapturedLine(b.String())
	b.Reset()
}

func handleCapturedLine(line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	slog.Warn("Captured stderr output", "line", line)
	notifyStderr(line)
}

// ExtractURL returns the first http(s) URL found in s, if any.
func ExtractURL(s string) (string, bool) {
	for _, p := range []string{"https://", "http://"} {
		if i := strings.Index(s, p); i >= 0 {
			u := s[i:]
			if j := strings.IndexAny(u, " \t\"'<>"); j >= 0 {
				u = u[:j]
			}
			return u, true
		}
	}

	return "", false
}
