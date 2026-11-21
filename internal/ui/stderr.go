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

// Stray stderr writes corrupt the TUI: tcell repaints changed cells only.
// Exec credential plugins (kubelogin/OIDC) print login URLs on token expiry.
//
// client-go reads the os.Stderr variable when building authenticators: swap
// it for a pipe first. Route output to the terminal while the TUI is idle,
// else to the log and flash bar. Runtime fatal errors still reach fd 2.

var (
	realStderr = os.Stderr

	// stderrCaptured tracks TUI terminal ownership.
	stderrCaptured atomic.Bool

	stderrHandlerMx sync.RWMutex
	stderrHandler   func(line string)
)

// RealStderr returns the terminal stderr, bypassing capture. Use it for
// child processes owning the terminal.
func RealStderr() *os.File {
	return realStderr
}

// CaptureStderr routes os.Stderr writes through k9s. Call it before
// creating clients. Returns a restore func.
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

// SetStderrCaptured toggles keeping stderr off the terminal. Set it while
// the TUI draws, clear it for child processes (shell, editor, plugin).
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
				// Pass through unbuffered: show prompts without newlines.
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
			// Bound growth on output without newlines.
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

// ExtractURL returns the first http(s) URL in s.
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
