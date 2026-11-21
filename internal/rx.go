// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal

import (
	"regexp"
	"strings"
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
		rx = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(validUTF8(q)))
	}
	if len(rxCache.m) >= rxCacheSize {
		clear(rxCache.m)
	}
	rxCache.m[q] = rx

	return rx, ok
}

// validUTF8 sets U+FFFD for each invalid byte. The regexp engine reads an
// invalid byte as U+FFFD, thus the literal continues to match q.
func validUTF8(q string) string {
	return strings.Map(func(r rune) rune { return r }, q)
}
