// Package handler wires the admission/queue/tokenstream pipeline into
// a Hertz HTTP handler for POST /v1/chat/completions.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"fakellm/internal/admission"
	"fakellm/internal/config"
	"fakellm/internal/queue"
	"fakellm/internal/tokenstream"
	"fakellm/pkg/openai"

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

	if req.Stream {
		// --- Streaming path: SSE via io.Pipe ---
		pr, pw := io.Pipe()

		work := func(qCtx context.Context) {
			defer pw.Close()
			h.streamer.RecordRequest()
			if err := h.streamer.Stream(qCtx, pw, model); err != nil {
				pw.CloseWithError(err)
			}
		}

		if err := h.q.Enqueue(ctx, cfg.QueueTimeout, work); err != nil {
			pr.Close()
			writeQueueError(c, err)
			return
		}

		c.Response.Header.Set("Content-Type", "text/event-stream")
		c.Response.Header.Set("Cache-Control", "no-cache")
		c.Response.Header.Set("X-Accel-Buffering", "no")
		c.Response.Header.Set("Connection", "keep-alive")
		c.Response.SetStatusCode(http.StatusOK)
		c.Response.SetBodyStream(pr, -1)
	} else {
		// --- Non-streaming path: block until full response is generated ---
		type result struct {
			resp *openai.ChatResponse
			err  error
		}
		ch := make(chan result, 1)

		work := func(qCtx context.Context) {
			h.streamer.RecordRequest()
			resp, err := h.streamer.Generate(qCtx, model)
			ch <- result{resp, err}
		}

		if err := h.q.Enqueue(ctx, cfg.QueueTimeout, work); err != nil {
			writeQueueError(c, err)
			return
		}

		res := <-ch
		if res.err != nil {
			if errors.Is(res.err, queue.ErrTimeout) {
				writeQueueError(c, queue.ErrTimeout)
				return
			}
			writeError(c, http.StatusInternalServerError, "generation_error", res.err.Error())
			return
		}

		body, err := json.Marshal(res.resp)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "serialization_error", err.Error())
			return
		}
		c.Response.Header.Set("Content-Type", "application/json")
		c.Response.SetStatusCode(http.StatusOK)
		c.Response.SetBody(body)
	}
}

// writeQueueError maps queue enqueue errors to HTTP responses.
func writeQueueError(c *app.RequestContext, err error) {
	switch err {
	case queue.ErrFull:
		writeError(c, http.StatusServiceUnavailable, "queue_full", "request queue is full")
	default:
		writeError(c, http.StatusGatewayTimeout, "queue_timeout", "timed out waiting in queue")
	}
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
