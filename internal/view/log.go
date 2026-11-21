// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/color"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/data"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/slogs"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/k9s/internal/view/cmd"
	"github.com/globlit/tview"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	logTitle            = "logs"
	logMessage          = "Waiting for logs...\n"
	logFmt              = "([hilite:bg:]%s[-:bg:-])[[green:bg:b]%s[-:bg:-]] "
	logCoFmt            = "([hilite:bg:]%s:[hilite:bg:b]%s[-:bg:-])[[green:bg:b]%s[-:bg:-]] "
	defaultFlushTimeout = 50 * time.Millisecond
)

// Log represents a generic log viewer.
type Log struct {
	*tview.Flex

	app               *App
	logs              *Logger
	indicator         *LogIndicator
	ansiWriter        io.Writer
	model             *model.Log
	cancelFn          context.CancelFunc
	cancelUpdates     bool
	mx                sync.Mutex
	follow            bool
	columnLock        bool
	requestOneRefresh bool
	// placeholder tracks the "waiting for logs" message. UI goroutine only.
	placeholder bool
	feed        logFeed
}

// logFeed coalesces log updates: keep at most one UI update pending, paced
// by the previous draw cost. One draw per flush freezes chatty streams.
type logFeed struct {
	mx        sync.Mutex
	pending   [][]byte
	clear     bool
	scheduled bool
	lastEnd   time.Time
	cost      time.Duration
	maxLines  int
}

const (
	minLogDrawGap = 50 * time.Millisecond
	maxLogDrawGap = time.Second
)

// add queues lines, dropping the oldest beyond maxLines.
func (f *logFeed) add(lines [][]byte) {
	f.mx.Lock()
	defer f.mx.Unlock()

	f.pending = append(f.pending, lines...)
	if f.maxLines > 0 && len(f.pending) > f.maxLines {
		f.pending = append([][]byte(nil), f.pending[len(f.pending)-f.maxLines:]...)
	}
}

// reset drops pending lines and requests a view clear.
func (f *logFeed) reset() {
	f.mx.Lock()
	defer f.mx.Unlock()

	f.pending, f.clear = nil, true
}

// next reports whether to schedule an update, and its delay.
func (f *logFeed) next() (bool, time.Duration) {
	f.mx.Lock()
	defer f.mx.Unlock()

	if f.scheduled || (len(f.pending) == 0 && !f.clear) {
		return false, 0
	}
	f.scheduled = true
	gap := min(max(f.cost, minLogDrawGap), maxLogDrawGap)

	return true, max(gap-time.Since(f.lastEnd), 0)
}

func (f *logFeed) take() (lines [][]byte, clear bool) {
	f.mx.Lock()
	defer f.mx.Unlock()

	lines, clear = f.pending, f.clear
	f.pending, f.clear = nil, false

	return
}

func (f *logFeed) done(cost time.Duration) {
	f.mx.Lock()
	defer f.mx.Unlock()

	f.cost, f.lastEnd, f.scheduled = cost, time.Now(), false
}

var _ model.Component = (*Log)(nil)

// NewLog returns a new viewer.
func NewLog(gvr *client.GVR, opts *dao.LogOptions) *Log {
	return &Log{
		Flex:  tview.NewFlex(),
		model: model.NewLog(gvr, opts, defaultFlushTimeout),
	}
}

func (*Log) SetCommand(*cmd.Interpreter)            {}
func (*Log) SetFilter(string, bool)                 {}
func (*Log) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the viewer.
func (l *Log) Init(ctx context.Context) (err error) {
	if l.app, err = extractApp(ctx); err != nil {
		return err
	}
	l.model.Configure(l.app.Config.K9s.Logger)

	l.SetBorder(true)
	l.SetDirection(tview.FlexRow)

	l.indicator = NewLogIndicator(l.app.Config, l.app.Styles, l.isContainerLogView())
	l.AddItem(l.indicator, 1, 1, false)
	if !l.model.HasDefaultContainer() {
		l.indicator.ToggleAllContainers()
	}
	l.indicator.Refresh()

	l.logs = NewLogger(l.app)
	if e := l.logs.Init(ctx); e != nil {
		return e
	}
	l.logs.SetBorderPadding(0, 0, 1, 1)
	l.logs.SetText("[orange::d]" + logMessage)
	l.placeholder = true
	l.logs.SetWrap(l.app.Config.K9s.Logger.TextWrap)
	l.logs.SetMaxLines(l.app.Config.K9s.Logger.BufferSize)
	l.feed.maxLines = l.app.Config.K9s.Logger.BufferSize

	l.ansiWriter = tview.ANSIWriter(l.logs, l.app.Styles.Views().Log.FgColor.String(), l.app.Styles.Views().Log.BgColor.String())
	l.AddItem(l.logs, 0, 1, true)
	l.bindKeys()

	l.StylesChanged(l.app.Styles)
	l.toggleFullScreen()

	l.model.Init(l.app.factory)
	l.updateTitle()

	l.follow = !l.app.Config.K9s.Logger.DisableAutoscroll
	l.columnLock = l.app.Config.K9s.Logger.ColumnLock

	l.model.ToggleShowTimestamp(l.app.Config.K9s.Logger.ShowTime)

	return nil
}

// InCmdMode checks if prompt is active.
func (l *Log) InCmdMode() bool {
	return l.logs.cmdBuff.InCmdMode()
}

// LogCanceled indicates no more logs are coming.
func (l *Log) LogCanceled() {
	slog.Debug("Logs watcher canceled!")
	l.app.QueueUpdateDraw(func() {
		l.Flush([][]byte{[]byte("\n🏁 [red::b]Stream exited! No more logs...")})
	})
}

// LogStop disables log flushes.
func (l *Log) LogStop() {
	slog.Debug("Logs watcher stopped!")
	l.mx.Lock()
	defer l.mx.Unlock()

	l.cancelUpdates = true
}

// LogResume resume log flushes.
func (l *Log) LogResume() {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.cancelUpdates = false
}

// clearPlaceholder removes the waiting message. UI goroutine only.
func (l *Log) clearPlaceholder() {
	if l.placeholder {
		l.placeholder = false
		l.logs.Clear()
	}
}

// LogCleared clears the logs.
func (l *Log) LogCleared() {
	// Route through the feed: never wipe lines queued after the clear.
	l.feed.reset()
	l.scheduleFeed()
}

func (l *Log) scheduleFeed() {
	ok, delay := l.feed.next()
	if !ok {
		return
	}
	time.AfterFunc(delay, func() {
		l.app.QueueUpdateDraw(l.drainFeed)
	})
}

// drainFeed writes pending lines. Runs on the UI goroutine.
func (l *Log) drainFeed() {
	start := time.Now()
	lines, clear := l.feed.take()
	if clear {
		l.placeholder = false
		l.logs.Clear()
	}
	if len(lines) > 0 {
		l.clearPlaceholder()
		l.Flush(lines)
	}
	// Queue behind the draw to measure update+draw cost for pacing.
	l.app.QueueUpdate(func() {
		l.feed.done(time.Since(start))
		l.scheduleFeed()
	})
}

// LogFailed notifies an error occurred.
func (l *Log) LogFailed(err error) {
	l.app.QueueUpdateDraw(func() {
		l.app.Flash().Err(err)
		l.clearPlaceholder()
		if _, err = l.ansiWriter.Write([]byte(tview.Escape(color.Colorize(err.Error(), color.Red)))); err != nil {
			slog.Error("Log line write failed", slogs.Error, err)
		}
	})
}

// LogChanged updates the logs.
func (l *Log) LogChanged(lines [][]byte) {
	l.feed.add(lines)
	l.scheduleFeed()
}

// BufferCompleted indicates input was accepted.
func (l *Log) BufferCompleted(text, _ string) {
	// Runs on the command buffer debounce goroutine.
	l.model.Filter(text)
	l.app.QueueUpdateDraw(l.updateTitle)
}

// BufferChanged indicates the buffer was changed.
func (*Log) BufferChanged(_, _ string) {}

// BufferActive indicates the buff activity changed.
func (l *Log) BufferActive(state bool, k model.BufferKind) {
	l.app.BufferActive(state, k)
}

// StylesChanged reports skin changes.
func (l *Log) StylesChanged(s *config.Styles) {
	l.SetBackgroundColor(s.Views().Log.BgColor.Color())
	l.logs.SetTextColor(s.Views().Log.FgColor.Color())
	l.logs.SetBackgroundColor(s.Views().Log.BgColor.Color())
}

// GetModel returns the log model.
func (l *Log) GetModel() *model.Log {
	return l.model
}

// Hints returns a collection of menu hints.
func (l *Log) Hints() model.MenuHints {
	return l.logs.Actions().Hints()
}

// ExtraHints returns additional hints.
func (*Log) ExtraHints() map[string]string {
	return nil
}

func (l *Log) cancel() {
	l.mx.Lock()
	defer l.mx.Unlock()
	if l.cancelFn != nil {
		l.cancelFn()
		l.cancelFn = nil
	}
}

func (l *Log) getContext() context.Context {
	l.cancel()
	ctx := context.Background()
	ctx, l.cancelFn = context.WithCancel(ctx)

	return ctx
}

// Start runs the component.
func (l *Log) Start() {
	l.model.Start(l.getContext())
	l.model.AddListener(l)
	l.app.Styles.AddListener(l)
	l.logs.cmdBuff.AddListener(l)
	l.logs.cmdBuff.AddListener(l.app.Prompt())
	l.updateTitle()
}

// Stop terminates the component.
func (l *Log) Stop() {
	l.model.RemoveListener(l)
	l.model.Stop()
	l.cancel()
	l.app.Styles.RemoveListener(l)
	l.logs.cmdBuff.RemoveListener(l)
	l.logs.cmdBuff.RemoveListener(l.app.Prompt())
}

// Name returns the component name.
func (*Log) Name() string { return logTitle }

func (l *Log) bindKeys() {
	l.logs.Actions().Bulk(ui.KeyMap{
		ui.Key0:         ui.NewKeyAction("tail", l.sinceCmd(-1), true),
		ui.Key1:         ui.NewKeyAction("head", l.sinceCmd(0), true),
		ui.Key2:         ui.NewKeyAction("1m", l.sinceCmd(60), true),
		ui.Key3:         ui.NewKeyAction("5m", l.sinceCmd(5*60), true),
		ui.Key4:         ui.NewKeyAction("15m", l.sinceCmd(15*60), true),
		ui.Key5:         ui.NewKeyAction("30m", l.sinceCmd(30*60), true),
		ui.Key6:         ui.NewKeyAction("1h", l.sinceCmd(60*60), true),
		tcell.KeyEnter:  ui.NewSharedKeyAction("Filter", l.filterCmd, false),
		tcell.KeyEscape: ui.NewKeyAction("Back", l.resetCmd, false),
		ui.KeyQ:         ui.NewKeyAction("Back", l.resetCmd, false),
		ui.KeyShiftC:    ui.NewKeyAction("Clear", l.clearCmd, true),
		ui.KeyM:         ui.NewKeyAction("Mark", l.markCmd, true),
		ui.KeyS:         ui.NewKeyAction("Toggle AutoScroll", l.toggleAutoScrollCmd, true),
		ui.KeyShiftL:    ui.NewKeyAction("Toggle ColumnLock", l.toggleColumnLockCmd, true),
		ui.KeyF:         ui.NewKeyAction("Toggle FullScreen", l.toggleFullScreenCmd, true),
		ui.KeyT:         ui.NewKeyAction("Toggle Timestamp", l.toggleTimestampCmd, true),
		ui.KeyW:         ui.NewKeyAction("Toggle Wrap", l.toggleTextWrapCmd, true),
		tcell.KeyCtrlS:  ui.NewKeyAction("Save", l.SaveCmd, true),
		ui.KeyC:         ui.NewKeyAction("Copy", cpCmd(l.app.Flash(), l.logs.TextView), true),
	})
	if l.model.HasDefaultContainer() {
		l.logs.Actions().Add(ui.KeyA, ui.NewKeyAction("Toggle AllContainers", l.toggleAllContainers, true))
	}
}

func (l *Log) resetCmd(evt *tcell.EventKey) *tcell.EventKey {
	if !l.logs.cmdBuff.IsActive() {
		if l.logs.cmdBuff.GetText() == "" {
			return l.app.PrevCmd(evt)
		}
	}

	l.logs.cmdBuff.Reset()
	l.logs.cmdBuff.SetActive(false)
	l.model.Filter(l.logs.cmdBuff.GetText())
	l.updateTitle()

	return nil
}

// SendStrokes (testing only!)
func (l *Log) SendStrokes(s string) {
	l.app.Prompt().SendStrokes(s)
}

// SendKeys (testing only!)
func (l *Log) SendKeys(kk ...tcell.Key) {
	for _, k := range kk {
		l.logs.keyboard(tcell.NewEventKey(k, ' ', tcell.ModNone))
	}
}

// Indicator returns the scroll mode viewer.
func (l *Log) Indicator() *LogIndicator {
	return l.indicator
}

func (l *Log) updateTitle() {
	sinceSeconds, since := l.model.SinceSeconds(), "tail"
	if sinceSeconds > 0 && sinceSeconds < 60*60 {
		since = fmt.Sprintf("%dm", sinceSeconds/60)
	}
	if sinceSeconds >= 60*60 {
		since = fmt.Sprintf("%dh", sinceSeconds/(60*60))
	}
	if l.model.IsHead() {
		since = "head"
	}

	title := " Logs"
	if l.model.LogOptions().Previous {
		title = " Previous Logs"
	}
	var (
		path, co = l.model.GetPath(), l.model.GetContainer()
		styles   = l.app.Styles.Frame()
	)
	if co == "" {
		title += ui.SkinTitle(fmt.Sprintf(logFmt, path, since), &styles)
	} else {
		title += ui.SkinTitle(fmt.Sprintf(logCoFmt, path, co, since), &styles)
	}

	buff := l.logs.cmdBuff.GetText()
	if buff != "" {
		title += ui.SkinTitle(fmt.Sprintf(ui.SearchFmt, buff), &styles)
	}
	l.SetTitle(title)
}

// Logs returns the log viewer.
func (l *Log) Logs() *Logger {
	return l.logs
}

// EOL tracks end of lines.
var EOL = []byte{'\n'}

// Flush write logs to viewer.
func (l *Log) Flush(lines [][]byte) {
	defer func() {
		if l.cancelUpdates {
			l.cancelUpdates = false
		}
	}()

	if len(lines) == 0 || (!l.requestOneRefresh && !l.indicator.AutoScroll()) || l.cancelUpdates {
		return
	}
	if l.requestOneRefresh {
		l.requestOneRefresh = false
	}
	// Write once per batch: each Write costs an ANSI parse and a lock.
	_, _ = l.ansiWriter.Write(bytes.Join(lines, nil))
	if l.follow {
		if l.columnLock {
			// Enables end tracking without resetting column
			l.logs.SetScrollable(false).SetScrollable(true)
		} else {
			l.logs.ScrollToEnd()
		}
	}
}

// ----------------------------------------------------------------------------
// Actions...

func (l *Log) sinceCmd(n int) func(evt *tcell.EventKey) *tcell.EventKey {
	return func(*tcell.EventKey) *tcell.EventKey {
		l.logs.Clear()
		ctx := l.getContext()
		if n == 0 {
			l.model.Head(ctx)
		} else {
			l.model.SetSinceSeconds(ctx, int64(n))
		}
		l.requestOneRefresh = true
		l.updateTitle()

		return nil
	}
}

func (l *Log) toggleAllContainers(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}
	l.indicator.ToggleAllContainers()
	l.model.ToggleAllContainers(l.getContext())
	l.updateTitle()

	return nil
}

func (l *Log) filterCmd(evt *tcell.EventKey) *tcell.EventKey {
	if !l.logs.cmdBuff.IsActive() {
		_, _ = fmt.Fprintln(l.ansiWriter)
		return evt
	}

	l.logs.cmdBuff.SetActive(false)
	l.model.Filter(l.logs.cmdBuff.GetText())
	l.updateTitle()

	return nil
}

// SaveCmd dumps the logs to file.
func (l *Log) SaveCmd(*tcell.EventKey) *tcell.EventKey {
	path, err := saveData(l.app.Config.K9s.ContextScreenDumpDir(), l.model.GetPath(), l.logs.GetText(true))
	if err != nil {
		l.app.Flash().Err(err)
		return nil
	}
	l.app.Flash().Infof("Log %s saved successfully!", path)

	return nil
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0744)
}

func saveData(dir, fqn, logs string) (string, error) {
	if err := ensureDir(dir); err != nil {
		return "", err
	}

	f := fmt.Sprintf("%s-%d.log", fqn, time.Now().UnixNano())
	path := filepath.Join(dir, data.SanitizeFileName(f))
	mod := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	file, err := os.OpenFile(path, mod, 0600)
	if err != nil {
		slog.Error("Unable to save log file",
			slogs.Path, path,
			slogs.Error, err,
		)
		return "", err
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Closing Log file failed",
				slogs.Path, path,
				slogs.Error, err,
			)
		}
	}()
	if _, err := file.WriteString(logs); err != nil {
		return "", err
	}

	return path, nil
}

func (l *Log) clearCmd(*tcell.EventKey) *tcell.EventKey {
	l.model.Clear()
	return nil
}

func (l *Log) markCmd(*tcell.EventKey) *tcell.EventKey {
	_, _, w, _ := l.GetRect()
	_, _ = fmt.Fprintf(l.ansiWriter, "[%s:-:b]%s[-:-:-]\n", l.app.Styles.Views().Log.FgColor.String(), strings.Repeat("-", w-4))
	l.follow = true

	return nil
}

func (l *Log) toggleTimestampCmd(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}

	l.indicator.ToggleTimestamp()
	l.model.ToggleShowTimestamp(l.indicator.showTime)
	l.indicator.Refresh()

	return nil
}

func (l *Log) toggleTextWrapCmd(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}

	l.indicator.ToggleTextWrap()
	l.logs.SetWrap(l.indicator.textWrap)
	l.indicator.Refresh()

	return nil
}

// ToggleAutoScrollCmd toggles autoscroll status.
func (l *Log) toggleAutoScrollCmd(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}

	l.indicator.ToggleAutoScroll()
	l.follow = l.indicator.AutoScroll()
	l.indicator.Refresh()

	return nil
}

func (l *Log) toggleColumnLockCmd(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}

	l.indicator.ToggleColumnLock()
	l.columnLock = l.indicator.ColumnLock()
	l.indicator.Refresh()

	return nil
}

func (l *Log) toggleFullScreenCmd(evt *tcell.EventKey) *tcell.EventKey {
	if l.app.InCmdMode() {
		return evt
	}
	l.indicator.ToggleFullScreen()
	l.toggleFullScreen()
	l.indicator.Refresh()

	return nil
}

func (l *Log) toggleFullScreen() {
	l.SetFullScreen(l.indicator.FullScreen())
	l.SetBorder(!l.indicator.FullScreen())
	if l.indicator.FullScreen() {
		l.logs.SetBorderPadding(0, 0, 0, 0)
	} else {
		l.logs.SetBorderPadding(0, 0, 1, 1)
	}
}

func (l *Log) isContainerLogView() bool {
	return l.model.HasDefaultContainer()
}
