// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	cleanup, err := Setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
