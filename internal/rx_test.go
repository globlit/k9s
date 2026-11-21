// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetRxCache(t *testing.T) {
	t.Helper()
	rxCache.Lock()
	clear(rxCache.m)
	rxCache.Unlock()
	t.Cleanup(func() {
		rxCache.Lock()
		clear(rxCache.m)
		rxCache.Unlock()
	})
}

func rxCacheLen() int {
	rxCache.Lock()
	defer rxCache.Unlock()

	return len(rxCache.m)
}

func TestCompileFilterRx(t *testing.T) {
	uu := map[string]struct {
		q        string
		ok       bool
		rx       string
		match    []string
		mismatch []string
	}{
		"plain": {
			q: "fred", ok: true, rx: "(?i)fred",
			match:    []string{"fred", "FRED", "xFrEdx"},
			mismatch: []string{"fre", "blee"},
		},
		"regex": {
			q: "^po-[0-9]+$", ok: true, rx: "(?i)^po-[0-9]+$",
			match:    []string{"po-12", "PO-3"},
			mismatch: []string{"po-", "xpo-1", "po-1a"},
		},
		"empty": {
			q: "", ok: true, rx: "(?i)",
			match: []string{"", "anything"},
		},
		"alternation": {
			q: "a|b", ok: true, rx: "(?i)a|b",
			match:    []string{"A", "xb"},
			mismatch: []string{"c"},
		},
		"open-paren": {
			q: "fred(", rx: `(?i)fred\(`,
			match:    []string{"FRED(", "xfred(y"},
			mismatch: []string{"fred"},
		},
		"open-bracket": {
			q: "[a-", rx: `(?i)\[a-`,
			match:    []string{"x[A-y"},
			mismatch: []string{"a"},
		},
		"bad-repeat": {
			q: "*fred", rx: `(?i)\*fred`,
			match:    []string{"*fred"},
			mismatch: []string{"fred"},
		},
		"bad-escape": {
			q: `\`, rx: `(?i)\\`,
			match:    []string{`a\b`},
			mismatch: []string{"ab"},
		},
		"bad-flag": {
			q: "(?z)", rx: `(?i)\(\?z\)`,
			match: []string{"(?z)"},
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			resetRxCache(t)
			for _, pass := range []string{"miss", "hit"} {
				rx, ok := CompileFilterRx(u.q)
				require.NotNil(t, rx, pass)
				assert.Equal(t, u.ok, ok, pass)
				assert.Equal(t, u.rx, rx.String(), pass)
				for _, s := range u.match {
					assert.True(t, rx.MatchString(s), "%s: %q must match %q", pass, u.q, s)
				}
				for _, s := range u.mismatch {
					assert.False(t, rx.MatchString(s), "%s: %q must not match %q", pass, u.q, s)
				}
			}
		})
	}
}

func TestCompileFilterRxCacheHit(t *testing.T) {
	resetRxCache(t)

	rx1, _ := CompileFilterRx("fred")
	rx2, _ := CompileFilterRx("fred")
	assert.Same(t, rx1, rx2)
	assert.Equal(t, 1, rxCacheLen())

	rx3, _ := CompileFilterRx("FRED")
	assert.NotSame(t, rx1, rx3)
	assert.Equal(t, 2, rxCacheLen())
}

func TestCompileFilterRxCacheBound(t *testing.T) {
	resetRxCache(t)

	first, _ := CompileFilterRx("q-0")
	for i := 1; i < rxCacheSize; i++ {
		_, _ = CompileFilterRx(fmt.Sprintf("q-%d", i))
	}
	assert.Equal(t, rxCacheSize, rxCacheLen())

	again, _ := CompileFilterRx("q-0")
	assert.Same(t, first, again)
	assert.Equal(t, rxCacheSize, rxCacheLen())

	_, _ = CompileFilterRx("overflow")
	assert.Equal(t, 1, rxCacheLen())

	fresh, ok := CompileFilterRx("q-0")
	assert.True(t, ok)
	assert.NotSame(t, first, fresh)
	assert.Equal(t, first.String(), fresh.String())
	assert.Equal(t, 2, rxCacheLen())

	for i := range 10 * rxCacheSize {
		_, _ = CompileFilterRx(fmt.Sprintf("n-%d", i))
		assert.LessOrEqual(t, rxCacheLen(), rxCacheSize)
	}
}

func TestCompileFilterRxConcurrent(t *testing.T) {
	resetRxCache(t)

	qq := []string{"fred", "blee(", "^a.*z$", "[x", "dup", "dup"}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				q := qq[(g+i)%len(qq)]
				if i%7 == 0 {
					q = fmt.Sprintf("g%d-%d", g, i)
				}
				rx, ok := CompileFilterRx(q)
				if !assert.NotNil(t, rx) {
					return
				}
				want := q == "fred" || q == "^a.*z$" || q == "dup" || q[0] == 'g'
				assert.Equal(t, want, ok, q)
			}
		})
	}
	wg.Wait()
	assert.LessOrEqual(t, rxCacheLen(), rxCacheSize)
}
