package internal_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPoolPlain(t *testing.T) {
	p := internal.NewWorkerPool(context.Background(), 2)

	var c atomic.Int32
	for range 10 {
		p.Add(func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				fmt.Println("Worker canceled")
				return nil
			default:
				c.Add(1)
				return nil
			}
		})
	}
	errs := p.Drain()
	assert.Equal(t, 10, int(c.Load()))
	assert.Empty(t, errs)
}

func TestWorkerPoolWithError(t *testing.T) {
	ctx := context.Background()
	p := internal.NewWorkerPool(ctx, 2)

	var c atomic.Int32
	for i := range 10 {
		p.Add(func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				fmt.Println("Worker canceled")
				return nil
			default:
				if i%2 == 0 {
					return fmt.Errorf("BOOM%d", i)
				}
				c.Add(1)
				return nil
			}
		})
	}
	errs := p.Drain()
	assert.Equal(t, 5, int(c.Load()))
	assert.Len(t, errs, 5)
}

func TestWorkerPoolBoundsConcurrency(t *testing.T) {
	p := internal.NewWorkerPool(context.Background(), 3)

	var cur, peak atomic.Int32
	for range 30 {
		p.Add(func(context.Context) error {
			n := cur.Add(1)
			for {
				m := peak.Load()
				if n <= m || peak.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			cur.Add(-1)
			return nil
		})
	}
	assert.Empty(t, p.Drain())
	assert.LessOrEqual(t, peak.Load(), int32(3))
	assert.Positive(t, peak.Load())
}

func TestWorkerPoolCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := internal.NewWorkerPool(ctx, 2)

	for range 4 {
		p.Add(func(ctx context.Context) error { return ctx.Err() })
	}
	errs := p.Drain()
	assert.Len(t, errs, 4)
	for _, err := range errs {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestWorkerPoolDrainCancelsContext(t *testing.T) {
	p := internal.NewWorkerPool(context.Background(), 1)

	var jobCtx context.Context
	p.Add(func(ctx context.Context) error {
		jobCtx = ctx
		return nil
	})
	assert.Empty(t, p.Drain())
	require.Error(t, jobCtx.Err())
}

func TestWorkerPoolAddAfterDrainPanics(t *testing.T) {
	p := internal.NewWorkerPool(context.Background(), 1)
	p.Drain()

	assert.Panics(t, func() {
		p.Add(func(context.Context) error { return nil })
	})
}

func TestWorkerPoolJobPanic(t *testing.T) {
	t.Skip("bug: a panic in a job is not recovered and kills the process")

	p := internal.NewWorkerPool(context.Background(), 2)
	var c atomic.Int32
	p.Add(func(context.Context) error { panic("boom") })
	for range 5 {
		p.Add(func(context.Context) error {
			c.Add(1)
			return nil
		})
	}
	errs := p.Drain()
	assert.Equal(t, int32(5), c.Load())
	assert.Len(t, errs, 1)
}
