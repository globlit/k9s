// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"fmt"
	"testing"
)

func BenchmarkRxFilter(b *testing.B) {
	lines := make([]string, 0, 5_000)
	for i := range 5_000 {
		lines = append(lines, fmt.Sprintf("  container-%d: image: registry.k8s.io/pause:3.10 ready=%t", i, i%2 == 0))
	}

	b.ReportAllocs()
	for b.Loop() {
		rxFilter(`pause:3\.[0-9]+`, lines)
	}
}
