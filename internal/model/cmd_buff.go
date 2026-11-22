// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"sync"
	"time"

	"github.com/globlit/k9s/internal"
)

const (
	maxBuff = 10

	keyEntryDelay = 100 * time.Millisecond

	// CommandBuffer represents a command buffer.
	CommandBuffer BufferKind = 1 << iota
	// FilterBuffer represents a filter buffer.
	FilterBuffer
)

type (
	// BufferKind indicates a buffer type.
	BufferKind int8

	// BuffWatcher represents a command buffer listener.
	BuffWatcher interface {
		// BufferCompleted indicates input was accepted.
		BufferCompleted(text, suggestion string)

		// BufferChanged indicates the buffer was changed.
		BufferChanged(text, suggestion string)

		// BufferActive indicates the buff activity changed.
		BufferActive(state bool, kind BufferKind)
	}
)

// CmdBuff represents user command input.
type CmdBuff struct {
	buff       []rune
	suggestion string
	listeners  map[BuffWatcher]struct{}
	hotKey     rune
	kind       BufferKind
	active     bool
	cancel     context.CancelFunc
	gen        uint64
	queue      func(func())
	mx         sync.RWMutex
}

// NewCmdBuff returns a new command buffer.
func NewCmdBuff(key rune, kind BufferKind) *CmdBuff {
	return &CmdBuff{
		hotKey:    key,
		kind:      kind,
		buff:      make([]rune, 0, maxBuff),
		listeners: make(map[BuffWatcher]struct{}),
	}
}

// InCmdMode checks if a command exists and the buffer is active.
func (c *CmdBuff) InCmdMode() bool {
	c.mx.RLock()
	defer c.mx.RUnlock()

	if !c.active {
		return false
	}

	return len(c.buff) > 0
}

// IsActive checks if command buffer is active.
func (c *CmdBuff) IsActive() bool {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.active
}

// SetActive toggles cmd buffer active state.
func (c *CmdBuff) SetActive(b bool) {
	c.mx.Lock()
	c.active = b
	c.mx.Unlock()

	c.fireActive(b)
}

// GetText returns the current text.
func (c *CmdBuff) GetText() string {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return string(c.buff)
}

// GetKind returns the buffer kind.
func (c *CmdBuff) GetKind() BufferKind {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.kind
}

// GetSuggestion returns the current suggestion.
func (c *CmdBuff) GetSuggestion() string {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.suggestion
}

// SetQueue runs the debounced apply through q, for example the UI event loop.
func (c *CmdBuff) SetQueue(q func(func())) {
	c.mx.Lock()
	c.queue = q
	c.mx.Unlock()
}

func (c *CmdBuff) debounce(delay time.Duration) {
	c.mx.Lock()
	defer c.mx.Unlock()
	if c.cancel != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), delay)
	c.cancel = cancel
	gen, queue := c.gen, c.queue

	go func() {
		defer internal.LogPanic("cmd buffer")
		<-ctx.Done()
		if queue == nil {
			c.applyPending(gen)
			return
		}
		queue(func() { c.applyPending(gen) })
	}()
}

// applyPending sends the current text.
// It does nothing when a clear occurred after the debounce started.
func (c *CmdBuff) applyPending(gen uint64) {
	c.mx.Lock()
	if c.gen != gen {
		c.mx.Unlock()
		return
	}
	c.cancel = nil
	text, suggestion := string(c.buff), c.suggestion
	c.mx.Unlock()

	c.fireBufferCompleted(text, suggestion)
}

// SetText initializes the buffer with a command.
func (c *CmdBuff) SetText(text, suggestion string, wipe bool) {
	c.mx.Lock()
	if wipe {
		c.buff, c.suggestion = []rune(text), suggestion
	} else {
		c.buff, c.suggestion = append(c.buff, []rune(text)...), suggestion
	}
	c.mx.Unlock()
	c.fireBufferCompleted(c.GetText(), c.GetSuggestion())
}

// Add adds a new character to the buffer.
func (c *CmdBuff) Add(r rune) {
	c.mx.Lock()
	c.buff = append(c.buff, r)
	c.mx.Unlock()
	c.fireBufferChanged(c.GetText(), c.GetSuggestion())
	c.debounce(keyEntryDelay)
}

// Delete removes the last character from the buffer.
func (c *CmdBuff) Delete() {
	c.mx.RLock()
	if len(c.buff) == 0 {
		c.mx.RUnlock()
		return
	}
	text := string(c.buff[:len(c.buff)-1])
	c.mx.RUnlock()
	c.SetText(text, "", true)
	c.fireBufferChanged(c.GetText(), c.GetSuggestion())
	c.debounce(800 * time.Millisecond)
}

// ClearText clears out command buffer.
func (c *CmdBuff) ClearText(fire bool) {
	c.mx.Lock()
	c.buff, c.suggestion = c.buff[:0], ""
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.gen++
	c.mx.Unlock()

	if fire {
		c.fireBufferCompleted(c.GetText(), c.GetSuggestion())
	}
}

// Reset clears out the command buffer and deactivates it.
func (c *CmdBuff) Reset() {
	c.ClearText(true)
	c.SetActive(false)
	c.fireBufferCompleted(c.GetText(), c.GetSuggestion())
}

// Empty returns true if no cmd, false otherwise.
func (c *CmdBuff) Empty() bool {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return len(c.buff) == 0
}

// ----------------------------------------------------------------------------
// Event Listeners...

// AddListener registers a cmd buffer listener.
func (c *CmdBuff) AddListener(w BuffWatcher) {
	c.mx.Lock()
	c.listeners[w] = struct{}{}
	c.mx.Unlock()
}

// RemoveListener removes a listener.
func (c *CmdBuff) RemoveListener(l BuffWatcher) {
	c.mx.Lock()
	delete(c.listeners, l)
	c.mx.Unlock()
}

func (c *CmdBuff) snapshotListeners() []BuffWatcher {
	c.mx.RLock()
	defer c.mx.RUnlock()

	ll := make([]BuffWatcher, 0, len(c.listeners))
	for l := range c.listeners {
		ll = append(ll, l)
	}

	return ll
}

func (c *CmdBuff) fireBufferCompleted(t, s string) {
	for _, l := range c.snapshotListeners() {
		l.BufferCompleted(t, s)
	}
}

func (c *CmdBuff) fireBufferChanged(t, s string) {
	for _, l := range c.snapshotListeners() {
		l.BufferChanged(t, s)
	}
}

func (c *CmdBuff) fireActive(b bool) {
	kind := c.GetKind()
	for _, l := range c.snapshotListeners() {
		l.BufferActive(b, kind)
	}
}
