// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodKillConfirms(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		simPod("ns1", "pod-a"), simPod("ns1", "pod-b"), simPod("ns1", "pod-c"),
	))
	s := NewSimApp(t, fc, SimNamespace("ns1"))
	s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-c") }, "pods not shown")
	s.Keys(' ', tcell.KeyDown, ' ')
	fc.Reset()

	s.Keys(tcell.KeyCtrlK)
	require.True(t, s.DialogOpen(), "no kill confirmation")
	assert.Contains(t, s.Screen(), "Kill 2 pod(s)?")
	s.Keys(tcell.KeyEnter)
	assert.False(t, s.DialogOpen())
	s.Sync()
	assert.Empty(t, fc.Deletes())

	s.Keys(tcell.KeyCtrlK, tcell.KeyTab, tcell.KeyEnter)
	s.Eventually(func() bool { return len(fc.Deletes()) == 2 }, "pods not killed")
	names := make([]string, 0, 2)
	for _, d := range fc.Deletes() {
		names = append(names, d.Name)
		require.NotNil(t, d.Grace)
		assert.Equal(t, int64(1), *d.Grace)
		assert.Nil(t, d.Propagation)
	}
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, names)
}
