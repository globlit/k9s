// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal_test

import (
	"bytes"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncBuffer struct {
	mx sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mx.Lock()
	defer s.mx.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mx.Lock()
	defer s.mx.Unlock()

	return s.b.String()
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	var buf syncBuffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	return &buf
}

func TestLogPanic(t *testing.T) {
	uu := map[string]struct {
		val  any
		want string
	}{
		"string": {val: "boom", want: "panic=boom"},
		"error":  {val: errors.New("bang"), want: "panic=bang"},
		"int":    {val: 42, want: "panic=42"},
		"nil":    {val: nil, want: "panic called with nil argument"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			buf := captureLog(t)
			func() {
				defer internal.LogPanic("job-" + k)
				panic(u.val)
			}()

			out := buf.String()
			assert.Contains(t, out, "Recovered from panic in background goroutine")
			assert.Contains(t, out, "where=job-"+k)
			assert.Contains(t, out, u.want)
			assert.Contains(t, out, "recover_test.go")
		})
	}
}

func TestLogPanicNoPanic(t *testing.T) {
	buf := captureLog(t)
	func() {
		defer internal.LogPanic("calm")
	}()

	assert.Empty(t, buf.String())
}

func TestLogPanicGoroutine(t *testing.T) {
	buf := captureLog(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer internal.LogPanic("updater")
		var m map[string]int
		m["x"] = 1
	}()
	<-done

	assert.Contains(t, buf.String(), "where=updater")
	assert.Contains(t, buf.String(), "assignment to entry in nil map")
}

func TestLogPanicIndirectDoesNotRecover(t *testing.T) {
	captureLog(t)
	var outer any
	func() {
		defer func() { outer = recover() }()
		func() {
			defer func() { internal.LogPanic("wrapped") }()
			panic("escape")
		}()
	}()

	require.NotNil(t, outer)
	assert.Equal(t, "escape", outer)
}
