// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func simDp(ns, n string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n}}
}

func topViewer(t *testing.T, s *SimApp) ResourceViewer {
	t.Helper()
	var v ResourceViewer
	s.onLoop(func() { v, _ = s.app.Content.Top().(ResourceViewer) })
	require.NotNil(t, v)

	return v
}

func selected(t *testing.T, s *SimApp) (cur string, marks []string) {
	t.Helper()
	v := topViewer(t, s)
	s.onLoop(func() {
		cur = v.GetTable().GetSelectedItem()
		marks = v.GetTable().GetSelectedItems()
	})
	slices.Sort(marks)

	return cur, marks
}

// selectPath moves the cursor to the row for path. It waits for the row.
func selectPath(t *testing.T, s *SimApp, path string) {
	t.Helper()
	v := topViewer(t, s)
	s.Eventually(func() bool {
		var found bool
		s.onLoop(func() {
			tb := v.GetTable()
			for r := 1; r < tb.GetRowCount(); r++ {
				tb.Select(r, 0)
				if tb.GetSelectedItem() == path {
					found = true
					return
				}
			}
		})
		return found
	}, "row "+path+" not found")
}

// rowShown reports if a table row starts with name.
func rowShown(s *SimApp, name string) bool {
	return strings.Contains(s.Screen(), "│ "+name+" ")
}

func markPaths(t *testing.T, s *SimApp, pp ...string) {
	t.Helper()
	for _, p := range pp {
		selectPath(t, s, p)
		s.Keys(' ')
	}
}

func clearMarks(t *testing.T, s *SimApp) {
	t.Helper()
	v := topViewer(t, s)
	s.onLoop(func() { v.GetTable().ClearMarks() })
}

func restoreDps(t *testing.T, s *SimApp, fc *fakecluster.Cluster, nn ...string) {
	t.Helper()
	for _, n := range nn {
		if _, err := fc.DynGet(client.DpGVR, "ns1", n); err != nil {
			fc.Add(simDp("ns1", n))
		}
	}
	s.Eventually(func() bool {
		for _, n := range nn {
			if !rowShown(s, n) {
				return false
			}
		}
		return true
	}, "deployments not restored")
	fc.Reset()
}

func deleteNames(fc *fakecluster.Cluster) []string {
	var nn []string
	for _, d := range fc.Deletes() {
		nn = append(nn, d.Name)
	}
	slices.Sort(nn)

	return nn
}

func TestBrowserDeleteDialog(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simDp("ns1", "dp-a"), simDp("ns1", "dp-b"), simDp("ns1", "dp-c")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	restoreDps(t, s, fc, "dp-a", "dp-b", "dp-c")

	for _, k := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape} {
		t.Run(fmt.Sprintf("dismiss-%d", k), func(t *testing.T) {
			selectPath(t, s, "ns1/dp-a")
			s.Keys(tcell.KeyCtrlD)
			require.True(t, s.DialogOpen())
			assert.Contains(t, s.Screen(), "Delete deployments ns1/dp-a?")

			s.Keys(k)
			s.Sync()

			assert.False(t, s.DialogOpen())
			assert.Empty(t, fc.Deletes())
		})
	}

	props := map[string]*metav1.DeletionPropagation{
		"Background": new(metav1.DeletePropagationBackground),
		"Foreground": new(metav1.DeletePropagationForeground),
		"Orphan":     new(metav1.DeletePropagationOrphan),
		"None":       nil,
	}
	for name, prop := range props {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-force=%t", name, force), func(t *testing.T) {
				selectPath(t, s, "ns1/dp-b")
				s.Keys(tcell.KeyCtrlD)
				s.ConfirmDelete(name, force)
				s.Eventually(func() bool { return len(fc.Deletes()) == 1 }, "no delete call")

				var grace *int64
				if force {
					grace = new(int64(0))
				}
				assert.Equal(t, fakecluster.DeleteCall{
					GVR: client.DpGVR.GVR(), NS: "ns1", Name: "dp-b",
					Propagation: prop, Grace: grace, Source: fakecluster.SourceDyn,
				}, fc.Deletes()[0])
				s.Eventually(func() bool { return strings.Contains(s.Flash(), "Delete resource apps/v1/deployments ns1/dp-b") }, "no flash")
				restoreDps(t, s, fc, "dp-b")
			})
		}
	}

	t.Run("marked", func(t *testing.T) {
		markPaths(t, s, "ns1/dp-a", "ns1/dp-b", "ns1/dp-c")
		s.Keys(tcell.KeyCtrlD)
		assert.Contains(t, s.Screen(), "Delete 3 marked apps/v1/deployments?")
		assert.NotContains(t, s.Screen(), "hidden by filter")
		s.ConfirmDelete("Foreground", false)
		s.Eventually(func() bool { return len(fc.Deletes()) == 3 }, "marked items not deleted")

		assert.Equal(t, []string{"dp-a", "dp-b", "dp-c"}, deleteNames(fc))
		for _, d := range fc.Deletes() {
			assert.Equal(t, metav1.DeletePropagationForeground, *d.Propagation)
		}
		s.Eventually(func() bool {
			_, mm := selected(t, s)
			return len(mm) <= 1
		}, "marks not cleared")
		restoreDps(t, s, fc, "dp-a", "dp-b", "dp-c")
	})

	t.Run("hidden-by-filter", func(t *testing.T) {
		markPaths(t, s, "ns1/dp-a", "ns1/dp-b", "ns1/dp-c")
		s.Keys("/dp-a", tcell.KeyEnter)
		s.Eventually(func() bool { return !rowShown(s, "dp-c") }, "filter not applied")

		s.Keys(tcell.KeyCtrlD)
		assert.Contains(t, s.Screen(), "Delete 3 marked apps/v1/deployments? (2 currently")
		assert.Contains(t, s.Screen(), "hidden by filter)")
		s.Keys(tcell.KeyEscape)
		s.Sync()

		assert.Empty(t, fc.Deletes())
		s.Keys(tcell.KeyEscape)
		clearMarks(t, s)
	})
}

func TestBrowserDeletePartialFailure(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simDp("ns1", "dp-a"), simDp("ns1", "dp-b"), simDp("ns1", "dp-c")),
		fakecluster.FailOn("delete", "deployments", 2, errors.New("boom")),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	restoreDps(t, s, fc, "dp-a", "dp-b", "dp-c")

	markPaths(t, s, "ns1/dp-a", "ns1/dp-b", "ns1/dp-c")
	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Background", false)
	s.Eventually(func() bool { return len(fc.Deletes()) == 3 }, "not all deletes sent")

	failed := fc.Deletes()[1].Name
	s.Eventually(func() bool { return strings.Contains(s.Flash(), "delete failed for 1/3: boom") }, "no failure flash")
	for _, d := range fc.Deletes() {
		_, err := fc.DynGet(client.DpGVR, "ns1", d.Name)
		assert.Equal(t, d.Name == failed, err == nil, d.Name)
	}
	_, marks := selected(t, s)
	assert.Equal(t, []string{"ns1/" + failed}, marks, "failed item stays marked")
}

func TestBrowserDeleteSingleFailure(t *testing.T) {
	fc := fakecluster.New(t,
		fakecluster.WithObjects(simDp("ns1", "dp-a")),
		fakecluster.FailOn("delete", "deployments", 0, errors.New("boom")),
	)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")

	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Background", false)

	s.Eventually(func() bool { return strings.Contains(s.Flash(), "Delete failed with `boom") }, "no failure flash")
	_, err := fc.DynGet(client.DpGVR, "ns1", "dp-a")
	require.NoError(t, err)
}

func TestBrowserDeleteDoesNotBlockUI(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simDp("ns1", "dp-a")))
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var started atomic.Bool
	fc.Dyn().PrependReactor("delete", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		started.Store(true)
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		return false, nil, nil
	})
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("deploy"))
	selectPath(t, s, "ns1/dp-a")

	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Background", false)
	s.Eventually(started.Load, "delete not started")
	t0 := time.Now()
	s.Keys(tcell.KeyDown)
	s.Sync()

	assert.Less(t, time.Since(t0), 200*time.Millisecond)
	assert.Empty(t, fc.Deletes())
	unblock()
	s.Eventually(func() bool { return len(fc.Deletes()) == 1 }, "delete did not finish")
}

// simFwd is a port-forward that records Stop calls.
type simFwd struct {
	watch.Forwarder

	id      string
	stopped atomic.Bool
}

func (f *simFwd) ID() string                 { return f.id }
func (f *simFwd) Stop()                      { f.stopped.Store(true) }
func (*simFwd) Container() string            { return "c1" }
func (*simFwd) Port() string                 { return "8080:80" }
func (*simFwd) Address() string              { return "localhost" }
func (f *simFwd) FQN() string                { return strings.Split(f.id, "|")[0] }
func (*simFwd) Active() bool                 { return true }
func (*simFwd) SetActive(bool)               {}
func (*simFwd) Age() time.Time               { return time.Now() }
func (*simFwd) HasPortMapping(string) bool   { return true }
func (f *simFwd) String() string             { return f.id }
func newSimFwd(fqn string) *simFwd           { return &simFwd{id: fqn + "|c1|8080:80"} }
func fwdIDs(f *watch.Factory) (out []string) { return slices.Sorted(mapsKeys(f.Forwarders())) }

func mapsKeys(ff watch.Forwarders) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range ff {
			if !yield(k) {
				return
			}
		}
	}
}

func TestPodDeleteRemovesPortForwards(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(simPod("ns1", "pod-a"), simPod("ns1", "pod-b")))
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimView("pods"))
	fa, fb := newSimFwd("ns1/pod-a"), newSimFwd("ns1/pod-b")
	s.app.factory.AddForwarder(fa)
	s.app.factory.AddForwarder(fb)
	selectPath(t, s, "ns1/pod-a")

	s.Keys(tcell.KeyCtrlD)
	s.ConfirmDelete("Background", false)

	s.Eventually(fa.stopped.Load, "pod-a forward not stopped")
	assert.False(t, fb.stopped.Load())
	assert.Equal(t, []string{fb.id}, fwdIDs(s.app.factory))
}

func TestSimpleDeleteLocalViews(t *testing.T) {
	fc := fakecluster.New(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"))

	t.Run("screendump", func(t *testing.T) {
		dir := s.app.Config.K9s.ContextScreenDumpDir()
		require.NoError(t, os.MkdirAll(dir, 0o700))
		for _, n := range []string{"dump-a.csv", "dump-b.csv"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600))
		}
		s.Cmd(":sd")
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "dump-b.csv") }, "dumps not shown")
		selectPath(t, s, filepath.Join(dir, "dump-a.csv"))

		s.Keys(tcell.KeyCtrlD)
		require.True(t, s.DialogOpen())
		assert.Contains(t, s.Screen(), "Confirm Delete")
		s.Keys(tcell.KeyTab, tcell.KeyEnter)

		s.Eventually(func() bool {
			_, err := os.Stat(filepath.Join(dir, "dump-a.csv"))
			return errors.Is(err, os.ErrNotExist)
		}, "dump not deleted")
		assert.FileExists(t, filepath.Join(dir, "dump-b.csv"))
	})

	t.Run("benchmark", func(t *testing.T) {
		dir := benchDir(s.app.Config)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		p := filepath.Join(dir, "ns1_pod-a_1.txt")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		s.Cmd(":bench")
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-a") }, "bench not shown")
		selectPath(t, s, p)

		s.Keys(tcell.KeyCtrlD, tcell.KeyEnter)
		assert.FileExists(t, p, "cancel keeps the file")
		s.Keys(tcell.KeyCtrlD, tcell.KeyTab, tcell.KeyEnter)

		s.Eventually(func() bool {
			_, err := os.Stat(p)
			return errors.Is(err, os.ErrNotExist)
		}, "bench not deleted")
	})

	t.Run("portforward", func(t *testing.T) {
		fa, fb := newSimFwd("ns1/pod-a"), newSimFwd("ns1/pod-b")
		s.app.factory.AddForwarder(fa)
		s.app.factory.AddForwarder(fb)
		s.Cmd(":pf")
		s.Eventually(func() bool { return strings.Contains(s.Screen(), "pod-b") }, "forwards not shown")
		selectPath(t, s, fa.id)

		s.Keys(tcell.KeyCtrlD, tcell.KeyTab, tcell.KeyEnter)

		s.Eventually(fa.stopped.Load, "forward not stopped")
		assert.False(t, fb.stopped.Load())
		assert.Equal(t, []string{fb.id}, fwdIDs(s.app.factory))
		assert.Empty(t, fc.Deletes())
	})
}
