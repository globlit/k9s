// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"sync"
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func workloadDelete(t *testing.T, s *SimApp, bad bool) {
	t.Helper()
	s.onLoop(func() {
		w, ok := s.app.Content.Top().(*Workload)
		require.True(t, ok)
		sels := []string{"v1/pods|ns1|pod-a"}
		if bad {
			sels = append([]string{"bad-path"}, sels...)
		}
		w.resourceDelete(sels, "Delete?")
	})
	s.Sync()
}

func TestWorkloadDeleteSkipsBadPath(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("wk"))
	s.Eventually(func() bool { return s.TopView() == "Workload" }, "no workload view")
	fc.Reset()

	workloadDelete(t, s, true)
	s.ConfirmDelete("Background", false)
	s.Eventually(func() bool { return len(fc.Deletes()) == 1 }, "pod-a not deleted")
	assert.Equal(t, "pod-a", fc.Deletes()[0].Name)
}

func TestWorkloadDeleteDoesNotBlockUI(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a")))
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fc.Dyn().PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		<-release
		return false, nil, nil
	})
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("wk"))
	s.Eventually(func() bool { return s.TopView() == "Workload" }, "no workload view")
	fc.Reset()

	workloadDelete(t, s, false)
	s.ConfirmDelete("Background", false)
	s.Sync()
	assert.Empty(t, fc.Deletes())

	unblock()
	s.Eventually(func() bool { return len(fc.Deletes()) == 1 }, "pod-a not deleted")
}
