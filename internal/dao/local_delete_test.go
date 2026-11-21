// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mutFwd is a port-forward that counts Stop calls.
type mutFwd struct {
	watch.Forwarder

	id      string
	stopped atomic.Int32
}

func (f *mutFwd) ID() string { return f.id }
func (f *mutFwd) Stop()      { f.stopped.Add(1) }

func TestPortForwardDelete(t *testing.T) {
	fc := fakecluster.New(t)
	f := mutFactory(t, fc.Conn())
	ff := []*mutFwd{
		{id: "ns1/web-0|c1|8080:80"},
		{id: "ns1/web-0|c2|9090:90"},
		{id: "ns1/web-0-x|c1|8081:80"},
		{id: "ns2/web-0|c1|8082:80"},
	}
	for _, fw := range ff {
		f.AddForwarder(fw)
	}
	var pf PortForward
	pf.Init(f, client.PfGVR)

	require.NoError(t, pf.Delete(context.Background(), "ns1/web-0|c1|8080:80", new(metav1.DeletePropagationOrphan), ForceGrace))
	assert.Equal(t, int32(1), ff[0].stopped.Load())
	assert.Len(t, f.Forwarders(), 3)

	require.NoError(t, pf.Delete(context.Background(), "ns1/web-0", nil, DefaultGrace))
	assert.Equal(t, int32(1), ff[1].stopped.Load())
	ids := make([]string, 0, 2)
	for k := range f.Forwarders() {
		ids = append(ids, k)
	}
	slices.Sort(ids)
	assert.Equal(t, []string{"ns1/web-0-x|c1|8081:80", "ns2/web-0|c1|8082:80"}, ids)

	require.NoError(t, pf.Delete(context.Background(), "ns1/nope", nil, DefaultGrace))
	assert.Len(t, f.Forwarders(), 2)
	assert.Empty(t, fc.Calls())
}

func TestFileDelete(t *testing.T) {
	uu := map[string]Nuker{
		"benchmark":  new(Benchmark),
		"screendump": new(ScreenDump),
	}

	for k, n := range uu {
		t.Run(k, func(t *testing.T) {
			dir := t.TempDir()
			keep, gone := filepath.Join(dir, "keep.txt"), filepath.Join(dir, "gone.txt")
			for _, p := range []string{keep, gone} {
				require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
			}

			require.NoError(t, n.Delete(context.Background(), gone, new(metav1.DeletePropagationForeground), NowGrace))
			assert.NoFileExists(t, gone)
			assert.FileExists(t, keep)

			require.ErrorIs(t, n.Delete(context.Background(), gone, nil, DefaultGrace), os.ErrNotExist)
		})
	}
}
