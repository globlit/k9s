// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLoopbackAddr(t *testing.T) {
	uu := map[string]bool{
		"127.0.0.1:6060": true,
		"localhost:6060": true,
		"[::1]:6060":     true,
		":6060":          false,
		"0.0.0.0:6060":   false,
		"10.0.0.5:6060":  false,
		"[::]:6060":      false,
		"bad":            false,
	}
	for addr, e := range uu {
		assert.Equal(t, e, isLoopbackAddr(addr), addr)
	}
}
