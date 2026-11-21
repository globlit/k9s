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

// CompileFilterRx compiles and caches a case-insensitive user filter.
// Invalid regexes fall back to a literal match with ok=false.
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
