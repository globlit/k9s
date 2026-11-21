// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/stretchr/testify/assert"
)

func TestUpdateLogs(t *testing.T) {
	size := 100
	m := NewLog(client.NewGVR("fred"), makeLogOpts(size), 10*time.Millisecond)
	m.Init(fakeFactory(t, client.DefaultNamespace))

	v := newMockLogView()
	m.AddListener(v)

	c := make(dao.LogChan, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.updateLogs(ctx, c)

	for i := range 2 * size {
		c <- dao.NewLogItemFromString("line" + strconv.Itoa(i))
	}
	// Expect at least a full buffer, and never lose the last line.
	assert.Eventually(t, func() bool {
		return strings.Contains(v.getLast(), "line"+strconv.Itoa(2*size-1))
	}, 2*time.Second, 10*time.Millisecond)
	assert.GreaterOrEqual(t, v.getCount(), size)
}

func BenchmarkUpdateLogs(b *testing.B) {
	size := 100
	m := NewLog(client.NewGVR("fred"), makeLogOpts(size), 10*time.Millisecond)
	m.Init(fakeFactory(b, client.DefaultNamespace))

	v := newMockLogView()
	m.AddListener(v)

	c := make(dao.LogChan)
	go func() {
		m.updateLogs(context.Background(), c)
	}()
	item := dao.NewLogItem([]byte("\033[0;38m2018-12-14T10:36:43.326972-07:00 \033[0;32mblee line"))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c <- item
	}
	close(c)
}

// Helpers...

func makeLogOpts(count int) *dao.LogOptions {
	return &dao.LogOptions{
		Path:      "fred",
		Container: "blee",
		Lines:     int64(count),
	}
}

type mockLogView struct {
	mx    sync.Mutex
	count int
	last  string
}

func (t *mockLogView) getLast() string {
	t.mx.Lock()
	defer t.mx.Unlock()

	return t.last
}

func (t *mockLogView) getCount() int {
	t.mx.Lock()
	defer t.mx.Unlock()

	return t.count
}

func newMockLogView() *mockLogView {
	return &mockLogView{}
}

func (t *mockLogView) LogChanged(ll [][]byte) {
	t.mx.Lock()
	defer t.mx.Unlock()
	t.count += len(ll)
	if len(ll) > 0 {
		t.last = string(ll[len(ll)-1])
	}
}
func (*mockLogView) LogStop()        {}
func (*mockLogView) LogCanceled()    {}
func (*mockLogView) LogResume()      {}
func (*mockLogView) LogCleared()     {}
func (*mockLogView) LogFailed(error) {}

// Flush streams with no lulls while they flow.
func TestUpdateLogsSteadyStreamFlushes(t *testing.T) {
	m := NewLog(client.NewGVR("fred"), makeLogOpts(1000), 50*time.Millisecond)
	m.Init(fakeFactory(t, client.DefaultNamespace))
	v := newMockLogView()
	m.AddListener(v)

	c := make(dao.LogChan, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.updateLogs(ctx, c)

	deadline := time.Now().Add(400 * time.Millisecond)
	var i int
	for time.Now().Before(deadline) {
		c <- dao.NewLogItemFromString("line" + strconv.Itoa(i))
		i++
		time.Sleep(5 * time.Millisecond)
	}
	// Still streaming: expect flushed lines.
	assert.Positive(t, v.getCount())
}
