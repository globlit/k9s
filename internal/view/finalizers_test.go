// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestFinalizerPreflightDoesNotBlockUI(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simDp("ns1", "dp-a")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var started, finished atomic.Bool
	fc.Dyn().PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		started.Store(true)
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		finished.Store(true)
		return false, nil, nil
	})
	s.Keys(tcell.KeyCtrlD)
	s.Eventually(started.Load, "preflight did not start")
	before := time.Now()
	s.Keys(tcell.KeyDown)
	s.Sync()
	assert.Less(t, time.Since(before), 200*time.Millisecond)
	assert.False(t, s.DialogOpen())
	s.Cmd(":pods")
	unblock()
	s.Eventually(finished.Load, "preflight did not finish")
	s.Sync()
	assert.False(t, s.DialogOpen(), "a completed preflight must not open on a different view")
	assert.Empty(t, fc.Deletes())
}
