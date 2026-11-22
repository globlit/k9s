// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"strings"
	"sync"

	"github.com/globlit/tview"
)

// LogChan represents a channel for logs.
type LogChan chan *LogItem

var ItemEOF = new(LogItem)

// LogItem represents a container log line.
type LogItem struct {
	Pod, Container  string
	SingleContainer bool
	Bytes           []byte
	IsError         bool
	// Log items are immutable after publication to the stream.
	jsonOnce   sync.Once
	jsonFields map[string]any
	jsonKeys   []string
}

// NewLogItem returns a new item.
func NewLogItem(bb []byte) *LogItem {
	return &LogItem{
		Bytes: bb,
	}
}

// NewLogItemFromString returns a new item.
func NewLogItemFromString(s string) *LogItem {
	return &LogItem{
		Bytes: []byte(s),
	}
}

// ID returns pod and or container based id.
func (l *LogItem) ID() string {
	if l.Pod != "" {
		return l.Pod
	}
	return l.Container
}

// GetTimestamp fetch log lime timestamp
func (l *LogItem) GetTimestamp() string {
	index := bytes.Index(l.Bytes, []byte{' '})
	if index < 0 {
		return ""
	}
	return string(l.Bytes[:index])
}

// Info returns pod and container information.
func (l *LogItem) Info() string {
	return l.Pod + "::" + l.Container
}

// IsEmpty checks if the entry is empty.
func (l *LogItem) IsEmpty() bool {
	return len(l.Bytes) == 0
}

// Size returns the size of the item.
func (l *LogItem) Size() int {
	return 100 + len(l.Bytes) + len(l.Pod) + len(l.Container)
}

// Render returns a log line as string.
func (l *LogItem) Render(paint string, opts RenderOpts, bb *bytes.Buffer) {
	index := bytes.Index(l.Bytes, []byte{' '})
	if opts.JSON && bytes.HasPrefix(bytes.TrimSpace(l.Bytes), []byte{'{'}) && l.parsedJSON() != nil {
		index = -1
	}
	if opts.ShowTime && index > 0 {
		bb.WriteString("[gray::b]")
		bb.Write(escapeLogBytes(l.Bytes[:index]))
		bb.WriteString(" ")
		if l := 30 - len(l.Bytes[:index]); l > 0 {
			bb.Write(bytes.Repeat([]byte{' '}, l))
		}
		bb.WriteString("[-::-]")
	}

	if l.Pod != "" {
		bb.WriteString("[" + paint + "::]" + escapeLogText(l.Pod))
	}

	if !l.SingleContainer && l.Container != "" {
		if l.Pod != "" {
			bb.WriteString(" ")
		}
		bb.WriteString("[" + paint + "::b]" + escapeLogText(l.Container) + "[-::-] ")
	} else if l.Pod != "" {
		bb.WriteString("[-::] ")
	}

	if opts.JSON && !l.IsError && l.renderJSON(opts, bb) {
		return
	}
	payload := l.Bytes
	if index > 0 {
		payload = payload[index+1:]
	}
	if !l.IsError {
		payload = escapeLogBytes(payload)
	}
	bb.Write(payload)
}

// Avoid the regexp allocation in tview's escape functions for ordinary logs.
func escapeLogBytes(text []byte) []byte {
	if bytes.IndexByte(text, '[') < 0 {
		return text
	}
	return tview.EscapeBytes(text)
}

func escapeLogText(text string) string {
	if !strings.ContainsRune(text, '[') {
		return text
	}
	return tview.Escape(text)
}
