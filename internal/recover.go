// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal

import (
	"log/slog"
	"runtime/debug"
)

// LogPanic recovers from a panic in a long-lived background goroutine and
// logs it with a stack trace instead of crashing the whole application.
// It must be called directly via defer: `defer internal.LogPanic("updater")`.
func LogPanic(where string) {
	if r := recover(); r != nil {
		slog.Error("Recovered from panic in background goroutine",
			"where", where,
			"panic", r,
			"stack", string(debug.Stack()),
		)
	}
}
