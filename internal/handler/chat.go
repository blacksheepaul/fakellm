// Package handler wires the admission/queue/tokenstream pipeline into
// a Hertz HTTP handler for POST /v1/chat/completions.
package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"mockllm/internal/admission"
	"mockllm/internal/config"
	"mockllm/internal/queue"
	"mockllm/internal/tokenstream"
	"mockllm/pkg/openai"

	"github.com/cloudwego/hertz/pkg/app"
)

// Handler holds the shared dependencies for the chat completions endpoint.
type Handler struct {
	cfg      *config.Manager
	sema     *admission.Semaphore
	q        *queue.Queue
	streamer *tokenstream.Streamer
}

// New creates a Handler. The semaphore and queue must already be initialised
// with the correct capacities from cfg.
func New(cfg *config.Manager, sema *admission.Semaphore, q *queue.Queue, streamer *tokenstream.Streamer) *Handler {
	return &Handler{cfg: cfg, sema: sema, q: q, streamer: streamer}
}

// ChatCompletions handles POST /v1/chat/completions.
func (h *Handler) ChatCompletions(ctx context.Context, c *app.RequestContext) {
	// 1. Parse request.
	var req openai.ChatRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "could not parse request body")
		return
	}
	if !req.Stream {
		writeError(c, http.StatusBadRequest, "invalid_request", "only stream=true is supported")
		return
	}
	model := req.Model
	if model == "" {
		model = "mock-llm"
	}

	// 2. Admission gate.
	if !h.sema.TryAcquire() {
		writeError(c, http.StatusTooManyRequests, "concurrency_limit", "concurrency limit reached")
		return
	}
	defer h.sema.Release()

	// 3. Enqueue with queue-wait timeout from config.
	cfg := h.cfg.Load()

	// Use a pipe: tokenstream writes to pw, hertz reads from pr.
	pr, pw := io.Pipe()

	// The work function runs inside the queue worker.
	work := func(qCtx context.Context) {
		defer pw.Close()
		h.streamer.RecordRequest()
		if err := h.streamer.Stream(qCtx, pw, model); err != nil {
			// Client disconnect or timeout — pipe close propagates to reader.
			pw.CloseWithError(err)
		}
	}

	if err := h.q.Enqueue(ctx, cfg.QueueTimeout, work); err != nil {
		pr.Close()
		switch err {
		case queue.ErrFull:
			writeError(c, http.StatusServiceUnavailable, "queue_full", "request queue is full")
		default:
			writeError(c, http.StatusGatewayTimeout, "queue_timeout", "timed out waiting in queue")
		}
		return
	}

	// 4. Set SSE headers and stream the response body.
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("X-Accel-Buffering", "no")
	c.Response.Header.Set("Connection", "keep-alive")
	c.Response.SetStatusCode(http.StatusOK)

	// SetBodyStream(-1) tells hertz to use chunked transfer encoding.
	c.Response.SetBodyStream(pr, -1)
}

// writeError writes an OpenAI-style JSON error response.
func writeError(c *app.RequestContext, status int, errType, message string) {
	c.Response.SetStatusCode(status)
	c.Response.Header.Set("Content-Type", "application/json")
	body, _ := json.Marshal(openai.ErrorResponse{
		Error: openai.ErrorDetail{
			Type:    errType,
			Message: message,
		},
	})
	c.Response.SetBody(body)
}
