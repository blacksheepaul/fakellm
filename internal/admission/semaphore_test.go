package admission

import (
	"sync"
	"testing"
)

// TestTryAcquire_UnderLimit verifies slots are granted while under capacity.
func TestTryAcquire_UnderLimit(t *testing.T) {
	s := New(3)
	for i := range 3 {
		if !s.TryAcquire() {
			t.Fatalf("expected TryAcquire to succeed on slot %d", i+1)
		}
	}
	if s.Current() != 3 {
		t.Fatalf("expected Current()=3, got %d", s.Current())
	}
}

// TestTryAcquire_AtLimit verifies the (capacity+1)th attempt is rejected.
func TestTryAcquire_AtLimit(t *testing.T) {
	s := New(2)
	s.TryAcquire()
	s.TryAcquire()
	if s.TryAcquire() {
		t.Fatal("expected TryAcquire to fail when at capacity")
	}
}

// TestRelease_FreesSlot verifies a released slot becomes available again.
func TestRelease_FreesSlot(t *testing.T) {
	s := New(1)
	if !s.TryAcquire() {
		t.Fatal("first acquire should succeed")
	}
	if s.TryAcquire() {
		t.Fatal("second acquire should fail while slot is held")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("acquire should succeed after release")
	}
}

// TestUnlimited verifies that capacity=0 never blocks.
func TestUnlimited(t *testing.T) {
	s := New(0)
	for range 1000 {
		if !s.TryAcquire() {
			t.Fatal("unlimited semaphore should never reject")
		}
	}
	if s.Current() != 1000 {
		t.Fatalf("expected Current()=1000, got %d", s.Current())
	}
}

// TestConcurrent verifies correct behaviour under concurrent access.
func TestConcurrent(t *testing.T) {
	const capacity = 10
	const goroutines = 100
	s := New(capacity)

	var wg sync.WaitGroup
	accepted := make(chan struct{}, goroutines)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.TryAcquire() {
				accepted <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(accepted)

	count := len(accepted)
	if count != capacity {
		t.Fatalf("expected exactly %d accepted, got %d", capacity, count)
	}
}
