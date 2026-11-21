// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal

import (
	"log/slog"
	"runtime/debug"
)

// LogPanic recovers and logs background goroutine panics with a stack.
// Defer it directly: `defer internal.LogPanic("updater")`.
func LogPanic(where string) {
	if r := recover(); r != nil {
		slog.Error("Recovered from panic in background goroutine",
			"where", where,
			"panic", r,
			"stack", string(debug.Stack()),
		)
	}
}
