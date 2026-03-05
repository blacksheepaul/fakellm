package queue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnqueue_Basic verifies a function is executed by a worker.
func TestEnqueue_Basic(t *testing.T) {
	q := New(10, 2)
	defer q.Close()

	done := make(chan struct{})
	err := q.Enqueue(context.Background(), 0, func(_ context.Context) {
		close(done)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("work function was not called within timeout")
	}
}

// TestEnqueue_Full verifies ErrFull is returned when queue is at capacity.
func TestEnqueue_Full(t *testing.T) {
	// depth=1, workers=0 means no draining — queue fills immediately.
	q := &Queue{ch: make(chan Request, 1)}

	block := make(chan struct{})
	// Fill the single slot with a blocking job (never actually runs without workers).
	err := q.Enqueue(context.Background(), 0, func(_ context.Context) { <-block })
	if err != nil {
		t.Fatalf("first enqueue should succeed, got: %v", err)
	}

	// Second enqueue must fail.
	err = q.Enqueue(context.Background(), 0, func(_ context.Context) {})
	if err != ErrFull {
		t.Fatalf("expected ErrFull, got: %v", err)
	}
	close(block)
}

// TestEnqueue_ContextCancel verifies that a cancelled context causes the
// work function to be skipped by the worker.
func TestEnqueue_ContextCancel(t *testing.T) {
	q := New(10, 2)
	defer q.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before enqueue

	var called atomic.Bool
	_ = q.Enqueue(ctx, 0, func(_ context.Context) {
		called.Store(true)
	})

	time.Sleep(50 * time.Millisecond)
	if called.Load() {
		t.Fatal("work function should not be called for a cancelled context")
	}
}

// TestEnqueue_QueueTimeout verifies that a job arriving after its deadline is dropped.
func TestEnqueue_QueueTimeout(t *testing.T) {
	// No workers — items sit in queue and expire.
	q := &Queue{ch: make(chan Request, 10)}

	var called atomic.Bool
	err := q.Enqueue(context.Background(), 1*time.Millisecond, func(_ context.Context) {
		called.Store(true)
	})
	if err != nil {
		t.Fatalf("enqueue should succeed: %v", err)
	}

	// Wait well past the timeout, then manually drain the queue.
	time.Sleep(20 * time.Millisecond)
	req := <-q.ch
	req.Fn(req.Ctx) // execute — should be a no-op because deadline passed

	if called.Load() {
		t.Fatal("work function should not execute after queue-wait timeout")
	}
}

// TestDepth verifies the depth counter tracks enqueued items.
func TestDepth(t *testing.T) {
	q := New(100, 0) // 0 workers — nothing drains
	// Replace channel to avoid worker goroutines from the New() call draining it.
	// Use a fresh queue manually.
	q2 := &Queue{ch: make(chan Request, 5)}

	for range 3 {
		_ = q2.Enqueue(context.Background(), 0, func(_ context.Context) {})
	}
	if q2.Depth() != 3 {
		t.Fatalf("expected depth=3, got %d", q2.Depth())
	}
	_ = q
}
