// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal_test

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal"
)

var filterSeeds = []string{
	"",
	"fred",
	"FRED",
	"fr.d",
	"^fred$",
	"[",
	"(",
	"a{2,",
	"a{1001}",
	"(?i)fred",
	"\\",
	"!fred",
	"!",
	"-f fred",
	"-ffred",
	"-f",
	"-l app=fred",
	"-lapp=fred",
	"app=fred",
	"app!=fred",
	"app==fred",
	"app=fred,tier=be",
	"app in (a,b)",
	"fred blee",
	"é",
}

func runWithin(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
				return
			}
			done <- nil
		}()
		fn()
	}()
	select {
	case r := <-done:
		if r != nil {
			t.Fatal(r)
		}
	case <-time.After(d):
		t.Fatalf("no result after %v", d)
	}
}

func FuzzCompileFilterRx(f *testing.F) {
	for _, s := range filterSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, q string) {
		runWithin(t, 2*time.Second, func() {
			_, err := regexp.Compile(`(?i)` + q)
			rx, ok := internal.CompileFilterRx(q)
			if rx == nil {
				t.Errorf("nil regexp for %q", q)
				return
			}
			if ok != (err == nil) {
				t.Errorf("ok %t, compile error %v", ok, err)
			}
			if ok && rx.String() != `(?i)`+q {
				t.Errorf("got %q, want %q", rx.String(), `(?i)`+q)
			}
			if !ok && !rx.MatchString(q) {
				t.Errorf("literal %q does not match itself", q)
			}

			rx1, ok1 := internal.CompileFilterRx(q)
			if ok1 != ok || rx1.String() != rx.String() {
				t.Errorf("cached: got (%q, %t), want (%q, %t)", rx1.String(), ok1, rx.String(), ok)
			}
		})
	})
}

func FuzzSelectors(f *testing.F) {
	for _, s := range filterSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if internal.IsInverseSelector(s) != strings.HasPrefix(s, "!") {
			t.Errorf("inverse %q", s)
		}
		if strings.HasPrefix(s, "-l") && !internal.IsLabelSelector(s) {
			t.Errorf("label %q", s)
		}
		q, ok := internal.IsFuzzySelector(s)
		if !ok {
			if q != "" {
				t.Errorf("fuzzy %q: got %q without match", s, q)
			}
			return
		}
		if !strings.HasPrefix(s, "-f") || q == "" || !strings.Contains(s, q) {
			t.Errorf("fuzzy %q: got %q", s, q)
		}
	})
}
