// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func FuzzToAgeHuman(f *testing.F) {
	for _, s := range []string{
		"", "2026-04-22T12:00:00Z", "2026-04-22T12:00:00+02:00", "0001-01-01T00:00:00Z",
		"9999-12-31T23:59:59Z", "2026-13-40T99:00:00Z", "bozo", "1m",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		v := toAgeHuman(s)
		switch {
		case s == "":
			if v != UnknownValue {
				t.Fatalf("empty: got %q", v)
			}
		case v == "":
			t.Fatalf("empty age for %q", s)
		}
	})
}

func FuzzTruncatePad(f *testing.F) {
	for _, s := range []string{"", "fred", "é ü", "日本語テキスト", "a\tb", "\x00\xff", "[red]x[-]"} {
		f.Add(s, 0)
		f.Add(s, 1)
		f.Add(s, 5)
		f.Add(s, 80)
	}

	f.Fuzz(func(t *testing.T, s string, w int) {
		w %= 256
		if w < 0 {
			w = -w
		}
		tr := Truncate(s, w)
		if w > 0 && runewidth.StringWidth(tr) > w {
			t.Fatalf("Truncate(%q, %d) = %q is too wide", s, w, tr)
		}
		p := Pad(s, w)
		if len(s) <= w && len(p) != w {
			t.Fatalf("Pad(%q, %d) = %q has len %d", s, w, p, len(p))
		}
		if a := AsPerc(s); !strings.HasPrefix(a, "(") || !strings.HasSuffix(a, ")") {
			t.Fatalf("AsPerc(%q) = %q", s, a)
		}
	})
}

func FuzzMapToStr(f *testing.F) {
	f.Add("app", "fred", "tier", "be")
	f.Add("", "", "", "")
	f.Add("a=b", "c,d", "é", "ü")

	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		m := map[string]string{k1: v1, k2: v2}
		s := mapToStr(m)
		for k, v := range m {
			if !strings.Contains(s, k+"="+v) {
				t.Fatalf("%q misses %q=%q", s, k, v)
			}
		}
	})
}

func FuzzToMcMi(f *testing.F) {
	for _, v := range []int64{0, 1, -1, 1 << 20, -(1 << 20), 1<<63 - 1, -1 << 63} {
		f.Add(v)
	}

	f.Fuzz(func(t *testing.T, v int64) {
		for _, s := range []string{toMc(v), toMi(v)} {
			if v == 0 {
				if s != ZeroValue {
					t.Fatalf("got %q for 0", s)
				}
				continue
			}
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				t.Fatalf("got %q for %d: %v", s, v, err)
			}
		}
	})
}
