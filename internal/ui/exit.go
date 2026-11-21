// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import (
	"os"
	"sync"
	"sync/atomic"
)

var (
	exitHooksMx sync.Mutex
	exitHooks   []func()
	exitFn      atomic.Pointer[func(int)]
)

// Exit ends the process with code, through the exit func.
func Exit(code int) {
	if f := exitFn.Load(); f != nil {
		(*f)(code)
		return
	}
	os.Exit(code)
}

// SetExitFunc replaces os.Exit for tests. Call the returned func to restore it.
func SetExitFunc(f func(int)) func() {
	old := exitFn.Swap(&f)
	return func() { exitFn.Store(old) }
}

// OnExit registers f to run on BailOut: os.Exit skips deferred calls.
func OnExit(f func()) {
	exitHooksMx.Lock()
	defer exitHooksMx.Unlock()

	exitHooks = append(exitHooks, f)
}

// RunExitHooks runs registered exit hooks once, most recent first.
func RunExitHooks() {
	exitHooksMx.Lock()
	hh := exitHooks
	exitHooks = nil
	exitHooksMx.Unlock()

	for i := len(hh) - 1; i >= 0; i-- {
		hh[i]()
	}
}
