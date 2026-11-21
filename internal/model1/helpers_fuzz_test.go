// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model1

import (
	"math"
	"testing"
)

func FuzzDurationToSeconds(f *testing.F) {
	for _, s := range []string{
		"", "22s", "22m", "12h", "3d", "3d9h", "2d22h3m50s", "3y", "1y2d",
		NAValue, "n/a", "<unknown>", "<none>", "<invalid>", "1x5s", "9999999999999999999y", "0", "5é",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		n := durationToSeconds(s)
		switch {
		case s == "":
			if n != 0 {
				t.Fatalf("empty: got %d", n)
			}
		case s[0] < '0' || s[0] > '9':
			if n != math.MaxInt64 {
				t.Fatalf("placeholder %q: got %d", s, n)
			}
		}
		runesToNum([]rune(s))
	})
}

func FuzzCapacityToNumber(f *testing.F) {
	for _, s := range []string{
		"", "  ", "1Gi", "10Mi", "n/a", "<none>", "1/3", "1e3", "100m", "1.5", "-1Ki", "9Ei", "1e400",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		capacityToNumber(s)
	})
}

func FuzzLess(f *testing.F) {
	for _, s := range [][2]string{
		{"1", "2"}, {"10", "9"}, {"1,000", "999"}, {"1,0", "10"}, {"01", "1"},
		{"2d", "3h"}, {"1m", "<unknown>"}, {"1Gi", "1024Mi"}, {"n/a", "1Mi"},
		{"fred", "blee"}, {"a1", "a01"}, {"", ""}, {"é", "e"},
	} {
		f.Add(s[0], s[1], "ns/a", "ns/b")
		f.Add(s[0], s[1], "ns/a", "ns/a")
	}

	f.Fuzz(func(t *testing.T, v1, v2, id1, id2 string) {
		for _, m := range [][3]bool{{false, false, false}, {true, false, false}, {false, true, false}, {false, false, true}} {
			l1 := Less(m[0], m[1], m[2], id1, id2, v1, v2)
			l2 := Less(m[0], m[1], m[2], id2, id1, v2, v1)
			if l1 && l2 {
				t.Fatalf("mode %v: %q/%q and %q/%q both sort first", m, id1, v1, id2, v2)
			}
		}
	})
}
