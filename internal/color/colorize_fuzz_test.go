// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package color_test

import (
	"bytes"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/globlit/k9s/internal/color"
)

var ansiRx = regexp.MustCompile("\x1b\\[38;5;-?[0-9]+m|\x1b\\[0m")

func FuzzHighlight(f *testing.F) {
	for _, s := range []struct {
		bb []byte
		ii []byte
	}{
		{[]byte(""), []byte{0}},
		{[]byte("fred"), []byte{0, 2}},
		{[]byte("fred"), []byte{3, 1, 200}},
		{[]byte("é ü"), []byte{0, 3}},
		{[]byte("日本語"), []byte{3, 4, 5}},
		{[]byte{0xff, 0xc3}, []byte{0, 1}},
	} {
		f.Add(s.bb, s.ii, 196)
	}

	f.Fuzz(func(t *testing.T, bb, raw []byte, c int) {
		ii := make([]int, 0, len(raw)+1)
		for _, b := range raw {
			ii = append(ii, int(int8(b)))
		}
		ii = append(ii, len(bb))

		out := color.Highlight(bb, ii, c)
		if !utf8.Valid(bb) {
			return
		}
		if !utf8.Valid(out) {
			t.Fatalf("invalid UTF-8 %q for %q at %v", out, bb, ii)
		}
		if bytes.IndexByte(bb, 0x1b) >= 0 {
			return
		}
		if got := ansiRx.ReplaceAll(out, nil); !bytes.Equal(got, bb) {
			t.Fatalf("got %q, want %q", got, bb)
		}
	})
}
