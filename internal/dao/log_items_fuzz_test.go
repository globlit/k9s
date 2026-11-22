// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"fmt"
	"runtime/debug"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/globlit/tview"
)

func FuzzLogItemRender(f *testing.F) {
	for _, s := range []string{
		"",
		" ",
		"Testing 1,2,3...\n",
		"2018-12-14T10:36:43.326972-07:00 Testing 1,2,3...\n",
		"2018-12-14T10:36:43.326972-07:00 \x1b[31mred\x1b[0m",
		"nospace",
		" leading",
		"[red::b]tagged[-::-] text",
		"é ü",
	} {
		f.Add([]byte(s), "", "", false, false)
		f.Add([]byte(s), "fred", "blee", true, false)
		f.Add([]byte(s), "", "c1", false, true)
	}

	f.Fuzz(func(t *testing.T, bb []byte, pod, co string, showTime, single bool) {
		l := NewLogItem(bb)
		l.Pod, l.Container, l.SingleContainer = pod, co, single
		l.ID()
		l.Info()
		l.GetTimestamp()
		l.IsEmpty()

		var out bytes.Buffer
		l.Render("teal", RenderOpts{ShowTime: showTime}, &out)

		msg := bb
		if i := bytes.IndexByte(bb, ' '); i > 0 {
			msg = bb[i+1:]
		}
		msg = tview.EscapeBytes(msg)
		if !bytes.HasSuffix(out.Bytes(), msg) {
			t.Fatalf("output %q does not end with %q", out.Bytes(), msg)
		}
		if pod == "" && co == "" && !showTime && !bytes.Equal(out.Bytes(), msg) {
			t.Fatalf("got %q, want %q", out.Bytes(), msg)
		}
	})
}

func FuzzLogItemsFilter(f *testing.F) {
	for _, s := range []string{
		"", "zorg", "will", "c1", "blee", "-f zorg", "-fzorg", "!will", "!", "[", "(",
		"wi.l", "^Jean", "-f", "é", "1,2,3",
	} {
		f.Add(s, "Bumble bee tuna. will be back. will win.", false)
		f.Add(s, "2018-12-14T10:36:43.326972-07:00 Testing 1,2,3...", true)
	}

	f.Fuzz(func(t *testing.T, q, line string, showTime bool) {
		// Skip invalid UTF-8: FuzzCompileFilterRx covers that case.
		if !utf8.ValidString(q) {
			return
		}
		ii := NewLogItems()
		for _, s := range []string{
			line,
			"Jean Batiste Emmanuel Zorg. wili, will. will, will",
			"",
			"é ü 2026",
		} {
			it := NewLogItemFromString(s)
			it.Pod, it.Container = "fred/blee", "c1"
			ii.Add(it)
		}

		for _, index := range []int{-1, 0, 2, ii.Len(), ii.Len() + 1} {
			var (
				lines   [][]byte
				matches []int
				indices [][]int
				err     error
			)
			runWithin(t, 2*time.Second, func() {
				lines, matches, indices, err = ii.FilterLines(index, q, RenderOpts{ShowTime: showTime})
			})
			if err != nil {
				continue
			}
			if len(matches) != len(indices) {
				t.Fatalf("got %d matches, %d indices", len(matches), len(indices))
			}
			for i, m := range matches {
				if m < 0 || m >= len(lines) {
					t.Fatalf("match %d out of %d lines", m, len(lines))
				}
				for _, j := range indices[i] {
					if j < 0 || j >= len(lines[m]) {
						t.Fatalf("index %d out of line %q", j, lines[m])
					}
				}
			}
			ii.Filter(index, q, RenderOpts{ShowTime: showTime})
		}
	})
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
