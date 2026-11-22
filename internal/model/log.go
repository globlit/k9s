// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/color"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/slogs"
)

// LogsListener represents a log model listener.
type LogsListener interface {
	// LogChanged notifies the model changed.
	LogChanged([][]byte)

	// LogCleared indicates logs are cleared.
	LogCleared()

	// LogFailed indicates a log failure.
	LogFailed(error)

	// LogStop indicates logging was canceled.
	LogStop()

	// LogResume indicates logging has resumed.
	LogResume()

	// LogCanceled indicates no more logs will come.
	LogCanceled()
}

// Log represents a resource logger.
type Log struct {
	factory    dao.Factory
	lines      *dao.LogItems
	listeners  []LogsListener
	gvr        *client.GVR
	logOptions *dao.LogOptions
	cancelFn   context.CancelFunc
	mx         sync.RWMutex
	// lmx guards listeners, separate from mx to fire under mx.
	lmx          sync.RWMutex
	filter       string
	flushTimeout time.Duration
	// maxLines caps the buffer (logger.buffer). Zero uses the tail count.
	maxLines      int
	renderOptions dao.RenderOpts
	// total counts appended lines; sent holds total at last flush. Both
	// survive buffer rotation, so flushes never skip lines.
	total, sent uint64
	// flushCtx identifies the running flusher.
	flushCtx context.Context
	// notifyMx serializes flushes to keep lines in order.
	notifyMx sync.Mutex
}

// NewLog returns a new model.
func NewLog(gvr *client.GVR, opts *dao.LogOptions, flushTimeout time.Duration) *Log {
	return &Log{
		gvr:          gvr,
		logOptions:   opts,
		lines:        dao.NewLogItems(),
		flushTimeout: flushTimeout,
	}
}

func (l *Log) GVR() *client.GVR {
	return l.gvr
}

func (l *Log) LogOptions() *dao.LogOptions {
	return l.logOptions
}

// SinceSeconds returns since seconds option.
func (l *Log) SinceSeconds() int64 {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return l.logOptions.SinceSeconds
}

// IsHead returns log head option.
func (l *Log) IsHead() bool {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return l.logOptions.Head
}

// ToggleShowTimestamp toggles to logs timestamps.
func (l *Log) ToggleShowTimestamp(b bool) {
	l.mx.Lock()
	l.logOptions.ShowTimestamp = b
	l.renderOptions.ShowTime = b
	l.mx.Unlock()
	l.Refresh()
}

func (l *Log) Head(ctx context.Context) {
	l.mx.Lock()
	l.logOptions.Head = true
	l.mx.Unlock()
	l.Restart(ctx)
}

// SetSinceSeconds sets the logs retrieval time.
func (l *Log) SetSinceSeconds(ctx context.Context, i int64) {
	l.logOptions.SinceSeconds, l.logOptions.Head = i, false
	l.Restart(ctx)
}

// Configure sets logger configuration.
func (l *Log) Configure(opts config.Logger) {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.logOptions.Lines = opts.TailCount
	l.logOptions.SinceSeconds = opts.SinceSeconds
	l.logOptions.LogBufferSize = opts.LogBufferSize
	l.maxLines = opts.BufferSize
	l.renderOptions.JSON = opts.JSON
	l.renderOptions.Fields = append([]string(nil), opts.JSONFields...)
}

// capacity returns the max buffered lines. Caller holds mx.
func (l *Log) capacity() int {
	c := l.maxLines
	if tail := int(l.logOptions.Lines); tail > c {
		c = tail
	}
	if c <= 0 {
		c = config.MaxLogThreshold
	}

	return c
}

// GetPath returns resource path.
func (l *Log) GetPath() string {
	return l.logOptions.Path
}

// GetContainer returns the resource container if any or "" otherwise.
func (l *Log) GetContainer() string {
	return l.logOptions.Container
}

// HasDefaultContainer returns true if the pod has a default container, false otherwise.
func (l *Log) HasDefaultContainer() bool {
	return l.logOptions.DefaultContainer != ""
}

// Init initializes the model.
func (l *Log) Init(f dao.Factory) {
	l.factory = f
}

// Clear the logs.
func (l *Log) Clear() {
	l.mx.Lock()
	l.lines.Clear()
	l.total, l.sent = 0, 0
	l.mx.Unlock()

	l.fireLogCleared()
}

// Refresh refreshes the logs.
func (l *Log) Refresh() {
	l.fireAll()
}

// Restart restarts the logger.
func (l *Log) Restart(ctx context.Context) {
	l.Stop()
	l.Clear()
	l.fireLogResume()
	l.Start(ctx)
}

// Start starts logging.
func (l *Log) Start(ctx context.Context) {
	if err := l.load(ctx); err != nil {
		slog.Error("Tail logs failed!", slogs.Error, err)
		l.fireLogError(err)
	}
}

// Stop terminates logging.
func (l *Log) Stop() {
	l.cancel()
}

// Set sets the log lines (for testing only!)
func (l *Log) Set(lines *dao.LogItems) {
	l.mx.Lock()
	l.lines.Merge(lines)
	l.total += uint64(lines.Len())
	l.mx.Unlock()

	l.fireAll()
}

// ClearFilter resets the log filter if any.
func (l *Log) ClearFilter() {
	l.mx.Lock()
	l.filter = ""
	l.mx.Unlock()

	l.fireAll()
}

// Filter filters the model using either fuzzy or regexp.
func (l *Log) Filter(q string) {
	l.mx.Lock()
	l.filter = q
	l.mx.Unlock()

	l.fireAll()
}

func (l *Log) cancel() {
	l.mx.Lock()
	defer l.mx.Unlock()
	if l.cancelFn != nil {
		l.cancelFn()
		l.cancelFn = nil
	}
}

func (l *Log) load(ctx context.Context) error {
	accessor, err := dao.AccessorFor(l.factory, l.gvr)
	if err != nil {
		return err
	}
	loggable, ok := accessor.(dao.Loggable)
	if !ok {
		return fmt.Errorf("resource %s is not Loggable", l.gvr)
	}

	l.cancel()
	ctx = context.WithValue(ctx, internal.KeyFactory, l.factory)
	ctx, cancel := context.WithCancel(ctx)
	l.mx.Lock()
	l.cancelFn = cancel
	l.mx.Unlock()

	cc, err := loggable.TailLogs(ctx, l.logOptions)
	if err != nil {
		slog.Error("Tail logs failed", slogs.Error, err)
		l.cancel()
		l.fireLogError(err)
	}
	for _, c := range cc {
		go l.updateLogs(ctx, c)
	}

	return nil
}

// Append adds a log line.
func (l *Log) Append(line *dao.LogItem) {
	if line == nil || line.IsEmpty() {
		return
	}
	l.mx.Lock()
	defer l.mx.Unlock()

	l.logOptions.SinceTime = line.GetTimestamp()
	l.total++
	if l.lines.Len() < l.capacity() {
		l.lines.Add(line)
		return
	}
	l.lines.Shift(line)
}

// Notify sends lines appended since the last flush.
func (l *Log) Notify() {
	l.notifyMx.Lock()
	defer l.notifyMx.Unlock()

	l.mx.Lock()
	pending := l.total - l.sent
	if pending == 0 {
		l.mx.Unlock()
		return
	}
	n := l.lines.Len()
	index := 0
	if pending < uint64(n) {
		index = n - int(pending)
	}
	if pending > uint64(n) {
		slog.Debug("Log lines rotated out before display", slogs.Count, pending-uint64(n))
	}
	l.sent = l.total
	// Snapshot new items: render and filter off-lock.
	batch := l.lines.Subset(index)
	q, opts := l.filter, l.renderOptions
	opts.ShowTime = l.logOptions.ShowTimestamp
	l.mx.Unlock()

	l.fireBatch(batch, q, opts)
}

// ToggleAllContainers toggles to show all containers logs.
func (l *Log) ToggleAllContainers(ctx context.Context) {
	l.logOptions.ToggleAllContainers()
	l.Restart(ctx)
}

// updateLogs reads one stream into the buffer. One flusher serves all streams.
func (l *Log) updateLogs(ctx context.Context, c dao.LogChan) {
	defer internal.LogPanic("log updater")
	l.ensureFlusher(ctx)
	for {
		select {
		case item, ok := <-c:
			if !ok {
				l.Append(item)
				l.Notify()
				return
			}
			if item == dao.ItemEOF {
				l.Notify()
				l.fireCanceled()
				return
			}
			l.Append(item)
		case <-ctx.Done():
			return
		}
	}
}

// ensureFlusher starts the flusher for ctx unless already running.
func (l *Log) ensureFlusher(ctx context.Context) {
	l.mx.Lock()
	if l.flushCtx == ctx {
		l.mx.Unlock()
		return
	}
	l.flushCtx = ctx
	l.mx.Unlock()

	go l.flusher(ctx)
}

// flusher pushes new lines on a ticker shared by all streams: busy streams
// flush promptly and the UI gets one batch per tick.
func (l *Log) flusher(ctx context.Context) {
	defer internal.LogPanic("log flusher")
	d := l.flushTimeout
	if d <= 0 {
		d = 50 * time.Millisecond
	}
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Notify()
		}
	}
}

// AddListener adds a new model listener.
func (l *Log) AddListener(listener LogsListener) {
	l.lmx.Lock()
	defer l.lmx.Unlock()

	ll := make([]LogsListener, 0, len(l.listeners)+1)
	ll = append(ll, l.listeners...)
	l.listeners = append(ll, listener)
}

// RemoveListener delete a listener from the list.
func (l *Log) RemoveListener(listener LogsListener) {
	l.lmx.Lock()
	defer l.lmx.Unlock()

	ll := make([]LogsListener, 0, len(l.listeners))
	for _, lis := range l.listeners {
		if lis != listener {
			ll = append(ll, lis)
		}
	}
	l.listeners = ll
}

// snapshotListeners returns current listeners. Copy on write: never mutated.
func (l *Log) snapshotListeners() []LogsListener {
	l.lmx.RLock()
	defer l.lmx.RUnlock()

	return l.listeners
}

// fireBatch renders and filters items, then notifies listeners.
func (l *Log) fireBatch(items *dao.LogItems, q string, opts dao.RenderOpts) {
	var ll [][]byte
	if q == "" {
		ll = items.RenderFrom(0, opts)
	} else {
		lines, matches, indices, err := items.FilterLines(0, q, opts)
		if err != nil {
			l.fireLogError(err)
			return
		}
		ll = highlight(lines, matches, indices)
	}
	if len(ll) > 0 {
		l.fireLogChanged(ll)
	}
}

func highlight(lines [][]byte, matches []int, indices [][]int) [][]byte {
	// No filter!
	if matches == nil {
		return lines
	}
	filtered := make([][]byte, 0, len(matches))
	for i, idx := range matches {
		if idx < 0 || idx >= len(lines) {
			continue
		}
		filtered = append(filtered, color.Highlight(lines[idx], indices[i], 209))
	}

	return filtered
}

// fireAll clears listeners and re-sends the whole buffer.
func (l *Log) fireAll() {
	l.notifyMx.Lock()
	defer l.notifyMx.Unlock()

	l.mx.Lock()
	l.sent = l.total
	batch := l.lines.Subset(0)
	q, opts := l.filter, l.renderOptions
	opts.ShowTime = l.logOptions.ShowTimestamp
	l.mx.Unlock()

	l.fireLogCleared()
	l.fireBatch(batch, q, opts)
}

func (l *Log) fireLogResume() {
	for _, lis := range l.snapshotListeners() {
		lis.LogResume()
	}
}

func (l *Log) fireCanceled() {
	for _, lis := range l.snapshotListeners() {
		lis.LogCanceled()
	}
}

func (l *Log) fireLogError(err error) {
	for _, lis := range l.snapshotListeners() {
		lis.LogFailed(err)
	}
}

func (l *Log) fireLogChanged(lines [][]byte) {
	for _, lis := range l.snapshotListeners() {
		lis.LogChanged(lines)
	}
}

func (l *Log) fireLogCleared() {
	for _, lis := range l.snapshotListeners() {
		lis.LogCleared()
	}
}

// SetJSON toggles structured rendering of the retained log buffer.
func (l *Log) SetJSON(enabled bool) {
	l.mx.Lock()
	l.renderOptions.JSON = enabled
	l.mx.Unlock()
	l.Refresh()
}

// SetJSONPalette updates the colors used by subsequent renders.
func (l *Log) SetJSONPalette(palette config.JSONLog) {
	l.mx.Lock()
	changed := l.renderOptions.Palette != palette
	l.renderOptions.Palette = palette
	l.mx.Unlock()
	if changed {
		l.Refresh()
	}
}
