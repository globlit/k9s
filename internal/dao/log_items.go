// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/derailed/k9s/internal"
	"github.com/sahilm/fuzzy"
)

type podColors map[string]string

var podPalette = []string{
	"teal",
	"green",
	"purple",
	"lime",
	"blue",
	"yellow",
	"fushia",
	"aqua",
}

// LogItems represents a collection of log items.
type LogItems struct {
	items     []*LogItem
	podColors podColors
	mx        sync.RWMutex
	// colorMx guards podColors; renders hold a read lock only.
	colorMx sync.Mutex
}

// NewLogItems returns a new instance.
func NewLogItems() *LogItems {
	return &LogItems{
		podColors: make(map[string]string),
	}
}

// Items returns the log items.
func (l *LogItems) Items() []*LogItem {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return l.items
}

// Len returns the items length.
func (l *LogItems) Len() int {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return len(l.items)
}

// Clear removes all items.
func (l *LogItems) Clear() {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.items = l.items[:0]
	l.colorMx.Lock()
	for k := range l.podColors {
		delete(l.podColors, k)
	}
	l.colorMx.Unlock()
}

// Shift scrolls the lines by one.
func (l *LogItems) Shift(i *LogItem) {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.items = append(l.items[1:], i)
}

// Subset return a subset of logitems.
func (l *LogItems) Subset(index int) *LogItems {
	l.mx.RLock()
	defer l.mx.RUnlock()

	l.colorMx.Lock()
	cc := make(podColors, len(l.podColors))
	for k, v := range l.podColors {
		cc[k] = v
	}
	l.colorMx.Unlock()

	return &LogItems{
		items:     append([]*LogItem(nil), l.items[index:]...),
		podColors: cc,
	}
}

// Merge merges two logitems list.
func (l *LogItems) Merge(n *LogItems) {
	n.mx.RLock()
	items := append([]*LogItem(nil), n.items...)
	n.mx.RUnlock()
	n.colorMx.Lock()
	cc := make(podColors, len(n.podColors))
	for k, v := range n.podColors {
		cc[k] = v
	}
	n.colorMx.Unlock()

	l.mx.Lock()
	defer l.mx.Unlock()
	l.items = append(l.items, items...)
	l.colorMx.Lock()
	for k, v := range cc {
		l.podColors[k] = v
	}
	l.colorMx.Unlock()
}

// Add augments the items.
func (l *LogItems) Add(ii ...*LogItem) {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.items = append(l.items, ii...)
}

func (l *LogItems) podColorFor(id string) string {
	l.colorMx.Lock()
	defer l.colorMx.Unlock()

	if color, ok := l.podColors[id]; ok {
		return color
	}
	var idx int
	for i, r := range id {
		idx += i * int(r)
	}
	color := podPalette[idx%len(podPalette)]
	l.podColors[id] = color

	return color
}

// renderLocked renders up to len(ll) items from index. Caller holds l.mx.
func (l *LogItems) renderLocked(index int, showTime bool, ll [][]byte) {
	if index < 0 || index > len(l.items) {
		return
	}
	for i, item := range l.items[index:] {
		if i >= len(ll) {
			return
		}
		bb := bytes.NewBuffer(make([]byte, 0, item.Size()))
		item.Render(l.podColorFor(item.ID()), showTime, bb)
		ll[i] = bb.Bytes()
	}
}

// RenderFrom renders lines from index. Size and render under one lock.
func (l *LogItems) RenderFrom(index int, showTime bool) [][]byte {
	l.mx.RLock()
	defer l.mx.RUnlock()

	if index < 0 || index > len(l.items) {
		return nil
	}
	ll := make([][]byte, len(l.items)-index)
	l.renderLocked(index, showTime, ll)

	return ll
}

// Lines returns a collection of log lines.
func (l *LogItems) Lines(index int, showTime bool, ll [][]byte) {
	l.mx.RLock()
	defer l.mx.RUnlock()

	l.renderLocked(index, showTime, ll)
}

// StrLines returns a collection of log lines.
func (l *LogItems) StrLines(index int, showTime bool) []string {
	ll := l.RenderFrom(index, showTime)
	ss := make([]string, len(ll))
	for i, b := range ll {
		ss[i] = string(b)
	}

	return ss
}

// Render returns logs as a collection of strings.
func (l *LogItems) Render(index int, showTime bool, ll [][]byte) {
	l.mx.RLock()
	defer l.mx.RUnlock()

	l.renderLocked(index, showTime, ll)
}

// DumpDebug for debugging.
func (l *LogItems) DumpDebug(m string) {
	fmt.Println(m + strings.Repeat("-", 50))
	for i, line := range l.items {
		fmt.Println(i, string(line.Bytes))
	}
}

// Filter filters out log items based on given filter.
func (l *LogItems) Filter(index int, q string, showTime bool) (matches []int, indices [][]int, err error) {
	_, matches, indices, err = l.FilterLines(index, q, showTime)

	return
}

// FilterLines filters items from index and returns matched rendered lines.
// Callers never index into a moved buffer.
func (l *LogItems) FilterLines(index int, q string, showTime bool) (lines [][]byte, matches []int, indices [][]int, err error) {
	if q == "" {
		return
	}
	lines = l.RenderFrom(index, showTime)
	if f, ok := internal.IsFuzzySelector(q); ok {
		matches, indices = fuzzyFilter(lines, f)
		return
	}
	matches, indices, err = filterLogs(lines, q)

	return
}

func fuzzyFilter(lines [][]byte, q string) (matches []int, indices [][]int) {
	q = strings.TrimSpace(q)
	ss := make([]string, len(lines))
	for i, b := range lines {
		ss[i] = string(b)
	}
	matches, indices = make([]int, 0, len(lines)), make([][]int, 0, len(lines))
	for _, m := range fuzzy.Find(q, ss) {
		matches = append(matches, m.Index)
		indices = append(indices, m.MatchedIndexes)
	}

	return matches, indices
}

func filterLogs(lines [][]byte, q string) (matches []int, indices [][]int, err error) {
	var invert bool
	if internal.IsInverseSelector(q) {
		invert = true
		q = q[1:]
	}
	rx, ok := internal.CompileFilterRx(q)
	if !ok {
		_, err = regexp.Compile(`(?i)` + q)
		return nil, nil, err
	}
	matches, indices = make([]int, 0, len(lines)), make([][]int, 0, len(lines))
	for i, line := range lines {
		locs := rx.FindAllIndex(line, -1)
		if locs != nil && invert {
			continue
		}
		if locs == nil && !invert {
			continue
		}
		matches = append(matches, i)
		ii := make([]int, 0, 10)
		for _, loc := range locs {
			for j := loc[0]; j < loc[1]; j++ {
				ii = append(ii, j)
			}
		}
		indices = append(indices, ii)
	}

	return matches, indices, nil
}
