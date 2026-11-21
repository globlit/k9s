// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package xray_test

import (
	"fmt"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/xray"
)

func fuzzRxFilter(q, path string) bool {
	rx, _ := internal.CompileFilterRx(q)
	for _, t := range strings.Split(path, xray.PathSeparator) {
		if rx.MatchString(t) {
			return true
		}
	}

	return false
}

func leafKeys(n *xray.TreeNode) map[string]int {
	kk := make(map[string]int)
	if n == nil {
		return kk
	}
	for _, s := range n.Flatten() {
		kk[s.AsGVR()+"|"+s.AsPath()]++
	}

	return kk
}

func FuzzTreeNodeFilter(f *testing.F) {
	for _, q := range []string{"", "c1", "c2", "coredns", "bozo", "[", "default/p1", "ok", "::", "^c"} {
		f.Add(q, "default/p9,c9")
		f.Add(q, "c1,c1,default/p1")
	}

	f.Fuzz(func(t *testing.T, q, ids string) {
		// Skip invalid UTF-8: FuzzCompileFilterRx covers that case.
		if !utf8.ValidString(q) {
			return
		}
		root := root3()
		nav := root
		for i, id := range strings.Split(ids, ",") {
			gvr := client.PodGVR
			if i%2 == 1 {
				gvr = client.CoGVR
			}
			c := xray.NewTreeNode(gvr, id)
			nav.Add(c)
			nav = c
		}

		before := leafKeys(root)
		var res *xray.TreeNode
		runWithin(t, 2*time.Second, func() {
			res = root.Filter(q, fuzzRxFilter)
		})
		for k := range leafKeys(res) {
			if before[k] == 0 {
				t.Fatalf("leaf %q is not in the source tree", k)
			}
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
