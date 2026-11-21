// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractURL(t *testing.T) {
	uu := map[string]struct {
		s, e string
		ok   bool
	}{
		"none": {s: "nothing to see"},
		"kubelogin": {
			s:  "Please visit the following URL in your browser: https://login.example.com/auth?x=1&y=2",
			e:  "https://login.example.com/auth?x=1&y=2",
			ok: true,
		},
		"trailing": {
			s:  `open "http://127.0.0.1:8000/" to continue`,
			e:  "http://127.0.0.1:8000/",
			ok: true,
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			e, ok := ExtractURL(u.s)
			assert.Equal(t, u.ok, ok)
			assert.Equal(t, u.e, e)
		})
	}
}

func TestStderrRouting(t *testing.T) {
	term, err := os.CreateTemp(t.TempDir(), "term")
	require.NoError(t, err)
	r, w, err := os.Pipe()
	require.NoError(t, err)

	saved := realStderr
	realStderr = term
	defer func() {
		realStderr = saved
		SetStderrCaptured(false)
		SetStderrHandler(nil)
	}()

	var (
		mx    sync.Mutex
		lines []string
	)
	SetStderrHandler(func(l string) {
		mx.Lock()
		defer mx.Unlock()
		lines = append(lines, l)
	})
	done := make(chan struct{})
	go func() {
		pumpStderr(r)
		close(done)
	}()

	// TUI active: keep output off the terminal.
	SetStderrCaptured(true)
	_, _ = w.WriteString("Please visit https://login.example.com\n")
	assert.Eventually(t, func() bool {
		mx.Lock()
		defer mx.Unlock()
		return len(lines) == 1
	}, time.Second, 10*time.Millisecond)

	// TUI suspended: pass output through unbuffered.
	SetStderrCaptured(false)
	_, _ = w.WriteString("Password: ")
	assert.Eventually(t, func() bool {
		bb, _ := os.ReadFile(term.Name())
		return string(bb) == "Password: "
	}, time.Second, 10*time.Millisecond)

	_ = w.Close()
	<-done

	mx.Lock()
	defer mx.Unlock()
	assert.Equal(t, []string{"Please visit https://login.example.com"}, lines)
}
