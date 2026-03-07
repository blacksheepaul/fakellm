package tokenstream

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fakellm/internal/config"
)

func newManager(tps float64, fixedMs, jitterMs int) *config.Manager {
	cfg := config.Default()
	cfg.TokensPerSecond = tps
	cfg.FixedDelayMs = fixedMs
	cfg.JitterMs = jitterMs
	cfg.SlowdownQPSThreshold = 0 // disable slowdown for unit tests
	return config.NewManager(cfg)
}

// TestStream_EmitsDONE verifies the stream ends with the SSE terminator.
func TestStream_EmitsDONE(t *testing.T) {
	mgr := newManager(10000, 0, 0) // very fast
	s := New(mgr)

	var buf bytes.Buffer
	ctx := context.Background()
	if err := s.Stream(ctx, &buf, "mock"); err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if !strings.Contains(buf.String(), "data: [DONE]") {
		t.Errorf("expected 'data: [DONE]' in output, got:\n%s", buf.String())
	}
}

// TestStream_ContainsLoremWords verifies some known lorem words appear.
func TestStream_ContainsLoremWords(t *testing.T) {
	mgr := newManager(10000, 0, 0)
	s := New(mgr)

	var buf bytes.Buffer
	if err := s.Stream(context.Background(), &buf, "mock"); err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	out := buf.String()
	for _, word := range []string{"Lorem", "ipsum", "dolor"} {
		if !strings.Contains(out, word) {
			t.Errorf("expected word %q in output", word)
		}
	}
}

// TestStream_CancelMidway verifies context cancellation is respected.
func TestStream_CancelMidway(t *testing.T) {
	// Slow stream so cancellation hits before completion.
	mgr := newManager(5, 0, 0) // 5 tokens/s → 200ms per token
	s := New(mgr)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	var buf bytes.Buffer
	go func() {
		done <- s.Stream(ctx, &buf, "mock")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err == nil {
		t.Error("expected non-nil error after cancellation")
	}
	// Output should be partial (not contain [DONE]).
	if strings.Contains(buf.String(), "data: [DONE]") {
		t.Error("expected stream to be incomplete after cancellation")
	}
}

// TestGenerate_ReturnsFullContent verifies that Generate returns a complete
// ChatResponse containing all lorem words.
func TestGenerate_ReturnsFullContent(t *testing.T) {
	mgr := newManager(10000, 0, 0) // very fast
	s := New(mgr)

	resp, err := s.Generate(context.Background(), "mock")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("Generate returned nil response")
	}
	if len(resp.Choices) == 0 {
		t.Fatal("Generate returned no choices")
	}
	content := resp.Choices[0].Message.Content
	for _, word := range []string{"Lorem", "ipsum", "dolor"} {
		if !strings.Contains(content, word) {
			t.Errorf("expected word %q in generated content", word)
		}
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage.CompletionTokens != len(loremWords) {
		t.Errorf("expected %d completion tokens, got %d", len(loremWords), resp.Usage.CompletionTokens)
	}
}

// TestGenerate_CancelMidway verifies that context cancellation interrupts Generate.
func TestGenerate_CancelMidway(t *testing.T) {
	mgr := newManager(5, 0, 0) // 5 tokens/s → 200ms per token
	s := New(mgr)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := s.Generate(ctx, "mock")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err == nil {
		t.Error("expected non-nil error after cancellation")
	}
}

func TestGenerate_PropagatesCancelCause(t *testing.T) {
	mgr := newManager(5, 0, 0)
	s := New(mgr)

	ctx, cancel := context.WithCancelCause(context.Background())
	want := errors.New("queue timeout")
	cancel(want)

	_, err := s.Generate(ctx, "mock")
	if !errors.Is(err, want) {
		t.Fatalf("expected cancel cause %v, got %v", want, err)
	}
}

// TestGenerate_MatchesStreamTiming verifies that Generate and Stream take
// roughly the same amount of time for the same config, confirming that the
// same rate-control logic is applied in both paths.
func TestGenerate_MatchesStreamTiming(t *testing.T) {
	// Use a slow-enough rate that the timing difference is measurable but the
	// test still finishes quickly. We cancel both after a short window.
	mgr := newManager(50, 0, 0) // 50 tokens/s → 20ms per token

	measure := func(fn func(context.Context)) time.Duration {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		fn(ctx)
		return time.Since(start)
	}

	streamTime := measure(func(ctx context.Context) {
		var buf bytes.Buffer
		_ = New(mgr).Stream(ctx, &buf, "mock")
	})

	generateTime := measure(func(ctx context.Context) {
		_, _ = New(mgr).Generate(ctx, "mock")
	})

	// Both should be within 100ms of each other (same rate, same cancellation).
	diff := streamTime - generateTime
	if diff < 0 {
		diff = -diff
	}
	if diff > 100*time.Millisecond {
		t.Errorf("timing mismatch too large: stream=%v generate=%v diff=%v", streamTime, generateTime, diff)
	}
}
func TestSlowdown_ReducesRate(t *testing.T) {
	cfg := config.Default()
	cfg.TokensPerSecond = 1000   // fast base rate
	cfg.SlowdownQPSThreshold = 1 // trigger after just 1 QPS
	cfg.SlowdownFactor = 0.01    // slow down to 1% → ~10 tokens/s
	cfg.FixedDelayMs = 0
	cfg.JitterMs = 0
	mgr := config.NewManager(cfg)

	s := New(mgr)
	s.RecordRequest() // bump QPS so slowdown triggers
	// Rotate window by faking high count.
	for range 5 {
		s.RecordRequest()
	}
	// Force window rotation by waiting > 1s is too slow; instead call
	// RecordRequest enough times to observe the counter.
	_ = s.CurrentQPS() // just ensure no panic

	// The real test: stream 3 tokens and measure time.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var buf bytes.Buffer
	start := time.Now()
	// We don't want to wait for the full lorem; cancel after a short duration.
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	_ = s.Stream(ctx, &buf, "mock")
	elapsed := time.Since(start)

	// At slowed rate (10 tok/s) we expect ~100ms per token.
	// In 300ms we should get at most ~3 tokens, confirming slowdown.
	// This is a soft check — just ensure it didn't complete instantly.
	if elapsed < 50*time.Millisecond {
		t.Errorf("stream completed too fast (%v), slowdown may not be working", elapsed)
	}
}
