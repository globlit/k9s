// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model1

import (
	"fmt"
	"runtime/debug"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/globlit/k9s/internal/client"
)

func fuzzTable(field string) *TableData {
	return NewTableDataWithRows(
		client.NewGVR("v1/pods"),
		Header{
			HeaderColumn{Name: "NAMESPACE"},
			HeaderColumn{Name: "NAME"},
			HeaderColumn{Name: "STATUS"},
			HeaderColumn{Name: "IP", Attrs: Attrs{Wide: true}},
			HeaderColumn{Name: "VALID", Attrs: Attrs{Wide: true}},
			HeaderColumn{Name: "AGE", Attrs: Attrs{Time: true}},
		},
		NewRowEventsWithEvts(
			RowEvent{Row: Row{ID: "default/fred", Fields: Fields{"default", "fred", "Running", "10.0.0.1", "", "1m"}}},
			RowEvent{Row: Row{ID: "kube-system/coredns-1", Fields: Fields{"kube-system", "coredns-1", "Pending", "10.0.0.2", "bad", "2d"}}},
			RowEvent{Row: Row{ID: "app/blee", Fields: Fields{"app", "blee", "CrashLoopBackOff", "", "", "3h"}}},
			RowEvent{Row: Row{ID: "app/x", Fields: Fields{"app", field, field, field, field, field}}},
			RowEvent{Row: Row{ID: "zorg/é-ü", Fields: Fields{"zorg", "é-ü", "Running", "::1", "", "<unknown>"}}},
		),
	)
}

func FuzzTableDataFilter(f *testing.F) {
	for _, s := range []string{
		"", "fred", "FRED", "!fred", "!", "fr.d", "^app", "[", "(", "a{2,",
		"-f fred", "-ffred", "-f", "-f ", "-l app=fred", "app=fred",
		"fred blee", "!-f", "é", "10\\.0", "running|pending",
	} {
		f.Add(s, "x", false)
		f.Add(s, s, true)
	}

	f.Fuzz(func(t *testing.T, q, field string, toast bool) {
		// Skip invalid UTF-8: FuzzCompileFilterRx covers that case.
		if !utf8.ValidString(q) {
			return
		}
		td := fuzzTable(field)
		ids := make(map[string]struct{}, td.RowCount())
		td.RowsRange(func(_ int, re RowEvent) bool {
			ids[re.Row.ID] = struct{}{}
			return true
		})

		var res *TableData
		runWithin(t, 2*time.Second, func() {
			res = td.Filter(FilterOpts{Filter: q, Invert: toast, Toast: toast})
		})
		if res == nil {
			t.Fatal("nil table")
		}
		if res.RowCount() > td.RowCount() {
			t.Fatalf("got %d rows from %d", res.RowCount(), td.RowCount())
		}
		seen := make(map[string]struct{}, res.RowCount())
		res.RowsRange(func(_ int, re RowEvent) bool {
			if _, ok := ids[re.Row.ID]; !ok {
				t.Errorf("unknown row %q", re.Row.ID)
			}
			if _, ok := seen[re.Row.ID]; ok {
				t.Errorf("duplicate row %q", re.Row.ID)
			}
			seen[re.Row.ID] = struct{}{}
			return true
		})
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
