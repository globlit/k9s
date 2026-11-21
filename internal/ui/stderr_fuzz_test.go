// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func FuzzExtractURL(f *testing.F) {
	for _, s := range []string{
		"",
		"https://",
		"http://x",
		"Please visit https://login.example.com/device?code=AB12 to sign in",
		"go to \"http://127.0.0.1:8000/cb\" now",
		"<https://a.b/c>",
		"http://a https://b",
		"HTTPS://UPPER",
		"é https://ü.example/é\t",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		u, ok := ExtractURL(s)
		if !ok {
			if u != "" || strings.Contains(s, "https://") || strings.Contains(s, "http://") {
				t.Fatalf("missed URL in %q, got %q", s, u)
			}
			return
		}
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			t.Fatalf("bad scheme %q", u)
		}
		if !strings.Contains(s, u) {
			t.Fatalf("%q is not in %q", u, s)
		}
		if strings.ContainsAny(u, " \t\"'<>") {
			t.Fatalf("delimiter in %q", u)
		}
	})
}

func FuzzDrainLines(f *testing.F) {
	for _, s := range []string{
		"",
		"\n",
		"one\ntwo\n",
		"one\r\ntwo",
		"  \n\t\n",
		"no newline",
		"a\n\nb\n",
		"é\nü\r\n",
	} {
		f.Add(s, uint8(3))
	}

	var got []string
	SetStderrHandler(func(l string) { got = append(got, l) })
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.Cleanup(func() {
		SetStderrHandler(nil)
		slog.SetDefault(prev)
	})

	f.Fuzz(func(t *testing.T, s string, step uint8) {
		got = got[:0]
		n := int(step)%16 + 1
		var b bytes.Buffer
		for i := 0; i < len(s); i += n {
			b.WriteString(s[i:min(i+n, len(s))])
			drainLines(&b)
		}
		flushPending(&b)
		if b.Len() != 0 {
			t.Fatalf("%d bytes left", b.Len())
		}
		// Skip long input: drainLines splits it at 64KiB.
		if len(s) > 64*1024 {
			return
		}

		var want []string
		for _, l := range strings.SplitAfter(s, "\n") {
			if l = strings.TrimRight(l, "\r\n"); strings.TrimSpace(l) != "" {
				want = append(want, l)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("line %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})
}
