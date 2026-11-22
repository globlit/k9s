// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run separately so unrelated package tests cannot distort process-wide counts.
func TestResourceLifecycle(t *testing.T) {
	if os.Getenv("K9S_RESOURCE_TEST") != "1" {
		t.Skip("run make test-resource")
	}
	snapshot := func() (int, int, uint64) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fds, err := os.ReadDir("/proc/self/fd")
		require.NoError(t, err, "resource checks require Linux /proc")
		return runtime.NumGoroutine(), len(fds), m.HeapAlloc
	}
	run := func(start int) {
		for i := start; i < start+10; i++ {
			if !t.Run(fmt.Sprintf("cycle-%d", i), func(t *testing.T) {
				s := NewSimApp(t, e2eCluster(t))
				s.e2eWait("web-7d9-aaaaa")
				s.e2eGoto(":deploy", "Deployment", "web")
				s.e2eGoto(":pod", "Pod", "web-7d9-aaaaa")
			}) {
				t.FailNow()
			}
		}
	}
	run(0) // Warm caches and allow all subtest cleanup handlers to finish.
	g0, f0, h0 := snapshot()
	t.Logf("baseline goroutines=%d fds=%d heap=%d", g0, f0, h0)
	for window := 1; window <= 3; window++ {
		run(window * 10)
		stable := assert.Eventually(t, func() bool {
			g, f, _ := snapshot()
			return g <= g0+2 && f <= f0+2
		}, 5*time.Second, 100*time.Millisecond, "goroutine or descriptor growth")
		g, f, h := snapshot()
		if !stable {
			_ = pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
		}
		t.Logf("window=%d goroutines=%d fds=%d retained_heap=%d baseline_heap=%d", window, g, f, h, h0)
		require.LessOrEqual(t, h, h0+8*1024*1024, "retained heap grew by more than 8 MiB after warmup")
	}
}
