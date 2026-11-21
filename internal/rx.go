// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal

import (
	"regexp"
	"sync"
)

const rxCacheSize = 32

var rxCache = struct {
	sync.Mutex
	m map[string]*regexp.Regexp
}{m: make(map[string]*regexp.Regexp, rxCacheSize)}

// CompileFilterRx compiles a case-insensitive user filter. Compiled
// expressions are cached so hot paths (per row, per log line, per tree node)
// don't recompile on every call. When q is not a valid regular expression it
// falls back to a literal match and returns ok=false, so a stray `[` or `(`
// typed by a user can never crash the app.
func CompileFilterRx(q string) (rx *regexp.Regexp, ok bool) {
	rxCache.Lock()
	defer rxCache.Unlock()

	if rx, found := rxCache.m[q]; found {
		return rx, rx.String() == `(?i)`+q
	}
	rx, err := regexp.Compile(`(?i)` + q)
	ok = err == nil
	if !ok {
		rx = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(q))
	}
	if len(rxCache.m) >= rxCacheSize {
		clear(rxCache.m)
	}
	rxCache.m[q] = rx

	return rx, ok
}
