// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package expect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyT struct {
	testing.TB
	failed bool
}

func (*spyT) Helper()                 {}
func (*spyT) Name() string            { return "spy" }
func (s *spyT) Errorf(string, ...any) { s.failed = true }
func (s *spyT) FailNow()              { s.failed = true; runtime.Goexit() }

func run(f func(t testing.TB)) bool {
	s := spyT{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(&s)
	}()
	<-done

	return s.failed
}

func TestMatch(t *testing.T) {
	t.Chdir(t.TempDir())

	assert.True(t, run(func(t testing.TB) { Match(t, "sub/screen.txt", "hello\n") }))

	*update = true
	Match(t, "sub/screen.txt", "hello\n")
	*update = false

	b, err := os.ReadFile(filepath.Join(Dir, "sub", "screen.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(b))

	assert.False(t, run(func(t testing.TB) { Match(t, "sub/screen.txt", "hello\n") }))
	assert.True(t, run(func(t testing.TB) { Match(t, "sub/screen.txt", "bye\n") }))
}
