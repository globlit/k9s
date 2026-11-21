// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sahilm/fuzzy"
)

var regionRx = regexp.MustCompile(`<<<"[^"]*">>>`)

func FuzzLinesWithRegions(f *testing.F) {
	for _, s := range []string{
		"", "a", "fred", "e", "[", ".*", "x*", "ü+", "語.",
	} {
		f.Add(s, "fred\nblee é ü\n日本語\n\nfred fred")
	}

	f.Fuzz(func(t *testing.T, q, text string) {
		if strings.Contains(text, "<<<") || !utf8.ValidString(q) || !utf8.ValidString(text) {
			return
		}
		lines := strings.Split(text, "\n")
		for _, mm := range []fuzzy.Matches{fuzzy.Find(q, lines), fuzzRxMatches(q, lines)} {
			ll := linesWithRegions(lines, mm)
			if len(ll) != len(lines) {
				t.Fatalf("got %d lines, want %d", len(ll), len(lines))
			}
			for i, l := range ll {
				if !utf8.ValidString(l) {
					t.Fatalf("line %d: invalid UTF-8 %q", i, l)
				}
				if got := regionRx.ReplaceAllString(l, ""); got != lines[i] {
					t.Fatalf("line %d: got %q, want %q", i, got, lines[i])
				}
			}
		}
	})
}

// fuzzRxMatches copies the regex filter of the model package.
func fuzzRxMatches(q string, lines []string) fuzzy.Matches {
	rx, err := regexp.Compile(`(?i)` + q)
	if err != nil {
		return nil
	}
	var mm fuzzy.Matches
	for i, l := range lines {
		for _, loc := range rx.FindAllStringIndex(l, -1) {
			ii := make([]int, 0, loc[1]-loc[0])
			for v := loc[0]; v < loc[1]; v++ {
				ii = append(ii, v)
			}
			mm = append(mm, fuzzy.Match{Str: q, Index: i, MatchedIndexes: ii})
		}
	}

	return mm
}
