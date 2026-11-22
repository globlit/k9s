// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	simWidth   = 160
	simHeight  = 50
	simTimeout = 5 * time.Second
)

// SimApp runs a real App on a simulation screen against a fake cluster.
// Do not use it in parallel tests: it replaces the global exit func.
type SimApp struct {
	t    testing.TB
	app  *App
	scr  tcell.SimulationScreen
	fc   *fakecluster.Cluster
	done chan struct{}
	exit atomic.Int64
}

type simOpts struct {
	readOnly bool
	ns       string
	view     string
	w, h     int
}

// SimOption configures a SimApp.
type SimOption func(*simOpts)

// SimReadOnly starts the app in read-only mode.
func SimReadOnly() SimOption { return func(o *simOpts) { o.readOnly = true } }

// SimNamespace sets the active namespace. The default is all namespaces.
func SimNamespace(ns string) SimOption { return func(o *simOpts) { o.ns = ns } }

// SimView sets the start view command, for example "deploy".
func SimView(v string) SimOption { return func(o *simOpts) { o.view = v } }

// SimSize sets the screen size. The default is 160x50.
func SimSize(w, h int) SimOption { return func(o *simOpts) { o.w, o.h = w, h } }

// NewSimApp starts an App on fc and waits for the first view.
func NewSimApp(t testing.TB, fc *fakecluster.Cluster, opts ...SimOption) *SimApp {
	t.Helper()
	o := simOpts{ns: client.NamespaceAll, w: simWidth, h: simHeight}
	for _, f := range opts {
		f(&o)
	}

	cfg := config.NewConfig(fc.Conn().Config())
	cfg.SetConnection(fc.Conn())
	_, err := cfg.K9s.ActivateContext(fc.Conn().ActiveContext())
	require.NoError(t, err)
	require.NoError(t, cfg.SetActiveNamespace(o.ns))
	if o.view != "" {
		cfg.SetActiveView(o.view)
	}
	cfg.K9s.UI.Splashless = true
	cfg.K9s.SkipLatestRevCheck = true
	cfg.K9s.ImageScans.Enable = false
	cfg.K9s.ReadOnly = o.readOnly

	restoreMetas := saveMetas()
	s := SimApp{t: t, fc: fc, done: make(chan struct{})}
	s.exit.Store(-1)
	restore := ui.SetExitFunc(func(code int) { s.exit.Store(int64(code)) })

	s.scr = tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, s.scr.Init())
	s.scr.SetSize(o.w, o.h)

	s.app = NewApp(cfg)
	require.NoError(t, s.app.Init("v0.0.0-test", 1))
	s.app.SetScreen(s.scr)
	go func() {
		defer close(s.done)
		if err := s.app.Run(); err != nil {
			t.Errorf("app run: %v", err)
		}
	}()

	t.Cleanup(func() {
		s.app.Stop()
		select {
		case <-s.done:
		case <-time.After(simTimeout):
			t.Errorf("app did not stop in time")
		}
		s.app.Halt()
		if s.app.factory != nil {
			s.app.factory.Terminate()
		}
		restore()
		restoreMetas()
		ExitStatus = ""
	})
	s.Eventually(func() bool { return s.TopView() != "" }, "no view at startup")

	return &s
}

// saveMetas keeps the global resource metas. The app reloads them from the
// fake discovery, and other tests rely on the registered ones.
func saveMetas() func() {
	saved := make(map[*client.GVR]*metav1.APIResource)
	for _, gvr := range dao.MetaAccess.AllGVRs() {
		if m, err := dao.MetaAccess.MetaFor(gvr); err == nil {
			saved[gvr] = m
		}
	}

	return func() {
		_ = dao.MetaAccess.LoadResources(nil)
		for gvr, m := range saved {
			dao.MetaAccess.RegisterMeta(gvr.String(), m)
		}
	}
}

// Use sends failures to t until the returned func runs. Call it in subtests.
func (s *SimApp) Use(t testing.TB) func() {
	old := s.t
	s.t = t

	return func() { s.t = old }
}

// App returns the app under test.
func (s *SimApp) App() *App { return s.app }

// ExitCode returns the code given to the exit func, or -1.
func (s *SimApp) ExitCode() int { return int(s.exit.Load()) }

// onLoop runs f on the event loop and waits for it. It skips f once the
// app has stopped.
func (s *SimApp) onLoop(f func()) {
	s.t.Helper()
	c := make(chan struct{})
	go func() {
		s.app.Application.QueueUpdate(f)
		close(c)
	}()
	select {
	case <-c:
	case <-s.done:
	case <-time.After(simTimeout):
		s.t.Fatalf("event loop blocked")
	}
}

// Keys sends keys in order. Each arg is a tcell.Key, a rune, a string or
// a *tcell.EventKey.
func (s *SimApp) Keys(kk ...any) {
	s.t.Helper()
	for _, k := range kk {
		switch k := k.(type) {
		case *tcell.EventKey:
			s.key(k)
		case tcell.Key:
			s.key(tcell.NewEventKey(k, 0, tcell.ModNone))
		case rune:
			s.key(tcell.NewEventKey(tcell.KeyRune, k, tcell.ModNone))
		case string:
			for _, r := range k {
				s.key(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
			}
		default:
			s.t.Fatalf("unsupported key %T", k)
		}
	}
	s.Sync()
}

// key handles one key like the tview event loop does.
func (s *SimApp) key(evt *tcell.EventKey) {
	s.t.Helper()
	s.onLoop(func() {
		if c := s.app.GetInputCapture(); c != nil {
			if evt = c(evt); evt == nil {
				return
			}
		}
		if root := s.app.Main; root.HasFocus() {
			root.InputHandler()(evt, func(p tview.Primitive) { s.app.SetFocus(p) })
		}
	})
}

// Cmd types a command such as ":deploy" and presses Enter.
func (s *SimApp) Cmd(c string) {
	s.t.Helper()
	if !strings.HasPrefix(c, ":") {
		c = ":" + c
	}
	s.Keys(c, tcell.KeyEnter)
}

// Sync waits for queued UI updates and the next draw.
func (s *SimApp) Sync() {
	s.t.Helper()
	c := make(chan struct{})
	s.app.QueueUpdateDraw(func() { close(c) })
	select {
	case <-c:
	case <-s.done:
		return
	case <-time.After(simTimeout):
		s.t.Fatalf("sync timed out")
	}
	if !s.Stopped() {
		s.onLoop(func() {})
	}
}

// Stopped reports if the event loop has ended.
func (s *SimApp) Stopped() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Eventually calls Sync until fn returns true. It fails after 5s.
func (s *SimApp) Eventually(fn func() bool, msg string) {
	s.t.Helper()
	deadline := time.Now().Add(simTimeout)
	for {
		s.Sync()
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s\n%s", msg, s.Screen())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Screen returns the screen text, one line per row, right spaces trimmed.
// It reads the cells on the event loop, which also draws them.
func (s *SimApp) Screen() string {
	var (
		scr  string
		done bool
	)
	if !s.Stopped() {
		s.onLoop(func() { scr, done = s.screen(), true })
	}
	if !done {
		scr = s.screen()
	}

	return scr
}

func (s *SimApp) screen() string {
	cells, w, h := s.scr.GetContents()
	var b strings.Builder
	for y := range h {
		var line strings.Builder
		for x := range w {
			c := cells[y*w+x]
			if len(c.Runes) == 0 {
				line.WriteRune(' ')
				continue
			}
			line.WriteRune(c.Runes[0])
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}

	return b.String()
}

// Row returns screen row i.
func (s *SimApp) Row(i int) string {
	rr := strings.Split(s.Screen(), "\n")
	if i < 0 || i >= len(rr) {
		return ""
	}

	return rr[i]
}

// Flash returns the flash message row.
func (s *SimApp) Flash() string {
	_, h := s.scr.Size()
	return strings.TrimSpace(s.Row(h - 1))
}

// Resize sets a new screen size and waits for the redraw.
func (s *SimApp) Resize(w, h int) {
	s.t.Helper()
	s.scr.SetSize(w, h)
	s.Sync()
}

// Top returns the top content component.
func (s *SimApp) Top() model.Component {
	s.t.Helper()
	var c model.Component
	s.onLoop(func() { c = s.app.Content.Top() })

	return c
}

// OnLoop runs f on the event loop and waits for it.
func (s *SimApp) OnLoop(f func()) {
	s.t.Helper()
	s.onLoop(f)
}

// TopView returns the name of the top content view.
func (s *SimApp) TopView() string {
	s.t.Helper()
	var n string
	s.onLoop(func() {
		if top := s.app.Content.Top(); top != nil {
			n = top.Name()
		}
	})

	return n
}

// DialogOpen reports if a dialog is on top of the content.
func (s *SimApp) DialogOpen() bool {
	s.t.Helper()
	var ok bool
	s.onLoop(func() { ok = s.app.Content.IsTopDialog() })

	return ok
}

func (s *SimApp) focus() tview.Primitive {
	s.t.Helper()
	var p tview.Primitive
	s.onLoop(func() { p = s.app.GetFocus() })

	return p
}

// ConfirmDelete fills the open delete dialog and presses OK.
// prop is Background, Foreground, Orphan or None.
func (s *SimApp) ConfirmDelete(prop string, force bool) {
	s.t.Helper()
	require.True(s.t, s.DialogOpen(), "no delete dialog")

	s.Keys(tcell.KeyBacktab, tcell.KeyBacktab)
	dd, ok := s.focus().(*tview.DropDown)
	require.True(s.t, ok, "propagation field not focused")
	var found bool
	s.onLoop(func() {
		for i := 0; !found; i++ {
			dd.SetCurrentOption(i)
			idx, txt := dd.GetCurrentOption()
			if idx < 0 {
				return
			}
			found = txt == prop
		}
	})
	require.True(s.t, found, "no propagation option %q", prop)

	s.Keys(tcell.KeyTab)
	cb, ok := s.focus().(*tview.Checkbox)
	require.True(s.t, ok, "force field not focused")
	if force != cb.IsChecked() {
		s.Keys(' ')
	}

	s.Keys(tcell.KeyTab, tcell.KeyTab)
	b, ok := s.focus().(*tview.Button)
	require.True(s.t, ok && b.GetLabel() == "OK", "OK button not focused")
	s.Keys(tcell.KeyEnter)
}
