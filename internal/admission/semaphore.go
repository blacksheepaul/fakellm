// Package admission implements a semaphore-based concurrency gate.
// When MaxConcurrent == 0 the gate is open (no limit).
package admission

import "sync/atomic"

// Semaphore is a non-blocking concurrency gate.
type Semaphore struct {
	ch      chan struct{}
	current atomic.Int64
}

// New creates a Semaphore with the given capacity.
// capacity == 0 means unlimited (TryAcquire always succeeds).
func New(capacity int) *Semaphore {
	var ch chan struct{}
	if capacity > 0 {
		ch = make(chan struct{}, capacity)
	}
	return &Semaphore{ch: ch}
}

// TryAcquire attempts to acquire a slot without blocking.
// Returns true on success, false when the concurrency limit is reached.
func (s *Semaphore) TryAcquire() bool {
	if s.ch == nil {
		s.current.Add(1)
		return true
	}
	select {
	case s.ch <- struct{}{}:
		s.current.Add(1)
		return true
	default:
		return false
	}
}

// Release frees one slot. Must be called exactly once per successful TryAcquire.
func (s *Semaphore) Release() {
	if s.ch != nil {
		<-s.ch
	}
	s.current.Add(-1)
}

// Current returns the number of currently held slots.
func (s *Semaphore) Current() int {
	return int(s.current.Load())
}

// Resize returns a new Semaphore with updated capacity, transferring no state.
// The caller is responsible for draining / replacing the old semaphore.
func Resize(capacity int) *Semaphore {
	return New(capacity)
}
