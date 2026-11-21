// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ui

import "sync"

var (
	exitHooksMx sync.Mutex
	exitHooks   []func()
)

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
