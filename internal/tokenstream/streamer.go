// Package tokenstream generates a fake token stream from a lorem ipsum corpus
// and emits each token as an OpenAI-compatible SSE frame.
//
// Rate control pipeline per request:
//
//	base_interval = 1s / (TokensPerSecond * load_efficiency * request_variance)
//	per_token_sleep = base_interval + FixedDelayMs + rand(-JitterMs, +JitterMs)
//
// Load efficiency follows a three-stage curve based on concurrency:
// - Low load (0-30%): 75%-100% efficiency (GPU underutilization)
// - Optimal (30-80%): 100% efficiency
// - High load (80%+): 100%-60% efficiency (memory pressure, has floor!)
package tokenstream

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fakellm/internal/admission"
	"fakellm/internal/config"
	"fakellm/internal/queue"

	"encoding/json"
)

// lorem is the token corpus. Words are emitted round-robin.
var loremWords = strings.Fields(`Lorem ipsum dolor sit amet consectetur adipiscing elit sed do
eiusmod tempor incididunt ut labore et dolore magna aliqua Ut enim ad minim veniam
quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat
Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu
fugiat nulla pariatur Excepteur sint occaecat cupidatat non proident sunt in culpa
qui officia deserunt mollit anim id est laborum Sed ut perspiciatis unde omnis iste
natus error sit voluptatem accusantium doloremque laudantium totam rem aperiam eaque
ipsa quae ab illo inventore veritatis et quasi architecto beatae vitae dicta sunt
explicabo Nemo enim ipsam voluptatem quia voluptas sit aspernatur aut odit aut fugit
sed quia consequuntur magni dolores eos qui ratione voluptatem sequi nesciunt neque
porro quisquam est qui dolorem ipsum quia dolor sit amet consectetur adipisci velit`)

// fileWordsCache holds cached words loaded from file.
var (
	fileWordsCache     []string
	cachedFilePath     string
	fileWordsCacheLock sync.RWMutex
)

// loadFileWords loads and returns words from the specified file.
// It caches the result to avoid re-reading the file.
func loadFileWords(filePath string) []string {
	fileWordsCacheLock.RLock()
	if cachedFilePath == filePath && len(fileWordsCache) > 0 {
		defer fileWordsCacheLock.RUnlock()
		return fileWordsCache
	}
	fileWordsCacheLock.RUnlock()

	fileWordsCacheLock.Lock()
	defer fileWordsCacheLock.Unlock()

	// Double-check after acquiring write lock
	if cachedFilePath == filePath && len(fileWordsCache) > 0 {
		return fileWordsCache
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		// Fallback to lorem if file cannot be read
		return loremWords
	}

	if len(data) == 0 {
		return loremWords
	}

	// Pick a random starting position
	segmentSize := 2000
	if len(data) < segmentSize {
		segmentSize = len(data)
	}

	maxStart := len(data) - segmentSize
	if maxStart > 0 {
		start := rand.IntN(maxStart)
		data = data[start : start+segmentSize]
	}

	// Clean up the text
	text := string(data)
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

	// Remove HTML-like tags
	for {
		start := strings.Index(text, "<")
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], ">")
		if end == -1 {
			break
		}
		text = text[:start] + " " + text[start+end+1:]
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return loremWords
	}

	fileWordsCache = words
	cachedFilePath = filePath
	return fileWordsCache
}

// getWords returns the appropriate word list based on config.
func (s *Streamer) getWords() []string {
	cfg := s.cfg.Load()
	if cfg.TextSource == "file" && cfg.FilePath != "" {
		return loadFileWords(cfg.FilePath)
	}
	return loremWords
}

// Streamer manages QPS tracking and token emission.
type Streamer struct {
	cfg  *config.Manager
	sema *admission.Semaphore
	q    *queue.Queue

	// 1-second sliding window QPS counter.
	windowStart atomic.Int64 // UnixNano of window start
	windowCount atomic.Int64 // requests in current window
	qps         atomic.Int64 // last measured QPS (integer approximation)
}

// New creates a Streamer backed by the given config manager.
func New(cfg *config.Manager, sema *admission.Semaphore, q *queue.Queue) *Streamer {
	s := &Streamer{cfg: cfg, sema: sema, q: q}
	s.windowStart.Store(time.Now().UnixNano())
	return s
}

// RecordRequest increments the QPS counter.
// Should be called once at the start of each streaming request.
func (s *Streamer) RecordRequest() {
	now := time.Now().UnixNano()
	windowNs := int64(time.Second)
	ws := s.windowStart.Load()

	if now-ws >= windowNs {
		// Rotate window: snapshot count as QPS, reset.
		count := s.windowCount.Swap(0)
		s.qps.Store(count)
		s.windowStart.Store(now)
	}
	s.windowCount.Add(1)
}

// CurrentQPS returns the measured QPS from the last completed window.
func (s *Streamer) CurrentQPS() float64 {
	return float64(s.qps.Load())
}

// computeLoadEfficiency calculates TPS efficiency using a sigmoid curve.
// The sigmoid smoothly transitions from MinEfficiency (low load) to 1.0 (optimal)
// and back down to MinEfficiency (high load).
//
// Formula: MinEfficiency + (1-MinEfficiency) / (1 + exp((load-center)*steepness))
//
// This models real GPU behavior where:
// - Low load: GPU underutilization (~60-75% efficiency)
// - Optimal load: full efficiency (100%)
// - High load: memory pressure, but has floor (~60%)
func (s *Streamer) computeLoadEfficiency(cfg *config.Config) float64 {
	maxConcurrent := s.cfg.Load().MaxConcurrent
	if maxConcurrent <= 0 {
		return 1.0
	}

	current := s.sema.Current()
	loadFactor := float64(current) / float64(maxConcurrent)

	center := cfg.LoadCurveCenter
	if center <= 0 {
		center = 0.6
	}
	steepness := cfg.LoadCurveSteepness
	if steepness <= 0 {
		steepness = 5.0
	}
	minEff := cfg.MinEfficiency
	if minEff <= 0 {
		minEff = 0.6
	}

	// Sigmoid: returns value in [minEff, 1.0]
	// At loadFactor = center: efficiency = (minEff + 1.0) / 2
	sigmoid := 1.0 / (1.0 + math.Exp((loadFactor-center)*steepness))
	return minEff + (1.0-minEff)*sigmoid
}

// computeQueuePenalty calculates TTFT penalty based on queue depth.
func (s *Streamer) computeQueuePenalty(cfg *config.Config) float64 {
	if !cfg.QueuePenaltyEnabled || s.q == nil {
		return 1.0
	}
	depth := s.q.Depth()
	if depth <= 0 {
		return 1.0
	}
	// Each 10 queued requests adds QueuePenaltyFactor to TTFT
	return 1.0 + float64(depth)*cfg.QueuePenaltyFactor/10.0
}

// generate is the single source of truth for rate-controlled token emission.
// It iterates over the lorem corpus, sleeping the configured interval between
// tokens, and calls onToken for each token content string.
// Callers decide what to do with each token (write SSE frames, accumulate, etc.).
// ctx cancellation is respected at every sleep boundary.
func (s *Streamer) generate(ctx context.Context, onToken func(string) error) error {
	ctxErr := func() error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return ctx.Err()
	}

	cfg := s.cfg.Load()

	// Compute effective tokens-per-second.
	tps := cfg.TokensPerSecond
	if tps <= 0 {
		tps = 20
	}

	// Apply load-based efficiency curve based on current concurrency.
	// This simulates GPU underutilization at low load and memory pressure at high load.
	loadEfficiency := s.computeLoadEfficiency(cfg)
	tps *= loadEfficiency

	// Apply per-request TPS variance to simulate different batch sizes.
	if cfg.TPSVariance > 0 {
		// rand.Float64() returns [0.0, 1.0), map to [-variance, +variance)
		variance := (rand.Float64()*2 - 1) * cfg.TPSVariance
		tps *= (1 + variance)
	}

	baseInterval := time.Duration(float64(time.Second) / tps)

	// Apply first token delay with optional queue penalty.
	firstTokenDelay := cfg.FirstTokenDelayMs
	if firstTokenDelay > 0 {
		// Queue penalty simulates waiting time when system is overloaded.
		// This affects TTFT (Time To First Token) more than TPS.
		penalty := s.computeQueuePenalty(cfg)
		firstTokenDelay = int(float64(firstTokenDelay) * penalty)
	}
	if firstTokenDelay > 0 {
		timer := time.NewTimer(time.Duration(firstTokenDelay) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctxErr()
		case <-timer.C:
		}
	}

	for i, word := range loremWords {
		select {
		case <-ctx.Done():
			return ctxErr()
		default:
		}

		sleep := baseInterval +
			time.Duration(cfg.FixedDelayMs)*time.Millisecond +
			jitter(cfg.JitterMs)

		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctxErr()
		case <-timer.C:
		}

		content := word
		if i < len(loremWords)-1 {
			content += " "
		}
		if err := onToken(content); err != nil {
			return err
		}
	}
	return nil
}

// Stream writes the lorem token stream as SSE to w.
// It respects ctx cancellation (client disconnect).
// model is echoed back in each chunk.
func (s *Streamer) Stream(ctx context.Context, w io.Writer, model string) error {
	reqID := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	// Send role delta in the first chunk.
	if err := writeChunk(w, openai.StreamChunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.StreamChoice{
			{Index: 0, Delta: openai.Delta{Role: "assistant"}, FinishReason: nil},
		},
	}); err != nil {
		return err
	}
	flush(w)

	if err := s.generate(ctx, func(content string) error {
		err := writeChunk(w, openai.StreamChunk{
			ID:      reqID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openai.StreamChoice{
				{Index: 0, Delta: openai.Delta{Content: content}, FinishReason: nil},
			},
		})
		if err != nil {
			return err
		}
		flush(w)
		return nil
	}); err != nil {
		return err
	}

	// Send finish chunk.
	stopReason := "stop"
	if err := writeChunk(w, openai.StreamChunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.StreamChoice{
			{Index: 0, Delta: openai.Delta{}, FinishReason: &stopReason},
		},
	}); err != nil {
		return err
	}
	flush(w)

	// SSE stream terminator.
	_, err := fmt.Fprintf(w, "data: [DONE]\n\n")
	flush(w)
	return err
}

// Generate runs the full rate-controlled token generation and returns a
// non-streaming OpenAI ChatResponse. The client blocks for the same duration
// as a streaming request would take, faithfully simulating real LLM latency.
func (s *Streamer) Generate(ctx context.Context, model string) (*openai.ChatResponse, error) {
	reqID := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	var sb strings.Builder
	var tokenCount int

	if err := s.generate(ctx, func(content string) error {
		sb.WriteString(content)
		tokenCount++
		return nil
	}); err != nil {
		return nil, err
	}

	return &openai.ChatResponse{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Message:      openai.ChatMessage{Role: "assistant", Content: strings.TrimRight(sb.String(), " ")},
				FinishReason: "stop",
			},
		},
		Usage: openai.Usage{
			PromptTokens:     0,
			CompletionTokens: tokenCount,
			TotalTokens:      tokenCount,
		},
	}, nil
}

// writeChunk serialises chunk as a single SSE data line.
func writeChunk(w io.Writer, chunk openai.StreamChunk) error {
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// flush calls Flush on w if it implements http.Flusher or hertz's equivalent.
func flush(w io.Writer) {
	type flusher interface{ Flush() }
	if f, ok := w.(flusher); ok {
		f.Flush()
	}
}

// jitter returns a random duration in [-ms, +ms].
func jitter(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	// rand.IntN returns [0, 2*ms), shift to [-ms, ms).
	return time.Duration(rand.IntN(2*ms)-ms) * time.Millisecond
}
