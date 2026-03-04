// Package queue implements a channel-based request queue with context-aware
// cancellation and configurable timeout.
package queue

import (
	"context"
	"sync/atomic"
	"time"
)

// Request is a unit of work waiting in the queue.
type Request struct {
	Ctx      context.Context
	Fn       func(ctx context.Context) // work to execute
	enqueued time.Time
}

// Queue is a bounded FIFO queue consumed by a pool of worker goroutines.
type Queue struct {
	ch    chan Request
	depth atomic.Int64
}

// New creates a Queue with the given buffer depth and starts workers goroutines
// to drain it. depth == 0 means unbounded (large internal buffer used).
func New(depth, workers int) *Queue {
	if depth <= 0 {
		depth = 1 << 20 // effectively unbounded
	}
	if workers <= 0 {
		workers = 1
	}
	q := &Queue{
		ch: make(chan Request, depth),
	}
	for range workers {
		go q.drain()
	}
	return q
}

// drain is the worker loop.
func (q *Queue) drain() {
	for req := range q.ch {
		q.depth.Add(-1)
		if req.Ctx.Err() != nil {
			continue // client already gone
		}
		req.Fn(req.Ctx)
	}
}

// Enqueue attempts to add fn to the queue.
//
//   - If the queue is full it returns ErrFull immediately (non-blocking).
//   - If timeout > 0 the fn is wrapped so it no-ops when the queue-wait
//     budget is exhausted before a worker picks it up.
//   - The caller's ctx cancellation is also respected inside fn.
func (q *Queue) Enqueue(ctx context.Context, timeout time.Duration, fn func(ctx context.Context)) error {
	var wrappedFn func(ctx context.Context)

	if timeout > 0 {
		enqueued := time.Now()
		deadline := enqueued.Add(timeout)
		wrappedFn = func(ctx context.Context) {
			if time.Now().After(deadline) {
				return // timed out in queue, silently discard
			}
			fn(ctx)
		}
	} else {
		wrappedFn = fn
	}

	req := Request{Ctx: ctx, Fn: wrappedFn, enqueued: time.Now()}

	select {
	case q.ch <- req:
		q.depth.Add(1)
		return nil
	default:
		return ErrFull
	}
}

// Depth returns the current number of items waiting in the queue.
func (q *Queue) Depth() int {
	return int(q.depth.Load())
}

// Close shuts down the worker goroutines. Pending items are dropped.
func (q *Queue) Close() {
	close(q.ch)
}
