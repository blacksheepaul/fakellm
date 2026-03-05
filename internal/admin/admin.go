// Package admin exposes a runtime control plane over HTTP.
//
// Routes (registered by the caller):
//
//	GET  /admin/config   — return current config snapshot
//	PATCH /admin/config  — JSON merge-patch to hot-update config
//	GET  /admin/stats    — concurrency, queue depth, QPS
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"mockllm/internal/admission"
	"mockllm/internal/config"
	"mockllm/internal/queue"
	"mockllm/internal/tokenstream"

	"github.com/cloudwego/hertz/pkg/app"
)

// Admin holds references to the live components it needs to inspect/mutate.
type Admin struct {
	cfg      *config.Manager
	sema     *admission.Semaphore
	q        *queue.Queue
	streamer *tokenstream.Streamer
}

// New creates an Admin handler.
func New(cfg *config.Manager, sema *admission.Semaphore, q *queue.Queue, streamer *tokenstream.Streamer) *Admin {
	return &Admin{cfg: cfg, sema: sema, q: q, streamer: streamer}
}

// configJSON is the JSON representation of Config (adds QueueTimeoutSec for
// human-friendliness since time.Duration marshals as nanoseconds by default).
type configJSON struct {
	MaxConcurrent        int     `json:"max_concurrent"`
	MaxQueueDepth        int     `json:"max_queue_depth"`
	QueueTimeoutSec      float64 `json:"queue_timeout_sec"`
	TokensPerSecond      float64 `json:"tokens_per_second"`
	FixedDelayMs         int     `json:"fixed_delay_ms"`
	JitterMs             int     `json:"jitter_ms"`
	SlowdownQPSThreshold float64 `json:"slowdown_qps_threshold"`
	SlowdownFactor       float64 `json:"slowdown_factor"`
}

func toJSON(c *config.Config) configJSON {
	return configJSON{
		MaxConcurrent:        c.MaxConcurrent,
		MaxQueueDepth:        c.MaxQueueDepth,
		QueueTimeoutSec:      c.QueueTimeout.Seconds(),
		TokensPerSecond:      c.TokensPerSecond,
		FixedDelayMs:         c.FixedDelayMs,
		JitterMs:             c.JitterMs,
		SlowdownQPSThreshold: c.SlowdownQPSThreshold,
		SlowdownFactor:       c.SlowdownFactor,
	}
}

// GetConfig handles GET /admin/config.
func (a *Admin) GetConfig(ctx context.Context, c *app.RequestContext) {
	c.Response.Header.Set("Content-Type", "application/json")
	c.Response.SetStatusCode(http.StatusOK)
	body, _ := json.Marshal(toJSON(a.cfg.Load()))
	c.Response.SetBody(body)
}

// PatchConfig handles PATCH /admin/config.
// Accepts a partial configJSON; only non-zero fields overwrite the current value.
func (a *Admin) PatchConfig(ctx context.Context, c *app.RequestContext) {
	var patch configJSON
	if err := json.Unmarshal(c.Request.Body(), &patch); err != nil {
		c.Response.SetStatusCode(http.StatusBadRequest)
		c.Response.SetBodyString(`{"error":"invalid JSON"}`)
		return
	}

	newCfg := a.cfg.Patch(func(cfg *config.Config) {
		if patch.MaxConcurrent != 0 {
			cfg.MaxConcurrent = patch.MaxConcurrent
		}
		if patch.MaxQueueDepth != 0 {
			cfg.MaxQueueDepth = patch.MaxQueueDepth
		}
		if patch.QueueTimeoutSec != 0 {
			cfg.QueueTimeout = time.Duration(patch.QueueTimeoutSec * float64(time.Second))
		}
		if patch.TokensPerSecond != 0 {
			cfg.TokensPerSecond = patch.TokensPerSecond
		}
		if patch.FixedDelayMs != 0 {
			cfg.FixedDelayMs = patch.FixedDelayMs
		}
		if patch.JitterMs != 0 {
			cfg.JitterMs = patch.JitterMs
		}
		if patch.SlowdownQPSThreshold != 0 {
			cfg.SlowdownQPSThreshold = patch.SlowdownQPSThreshold
		}
		if patch.SlowdownFactor != 0 {
			cfg.SlowdownFactor = patch.SlowdownFactor
		}
	})

	c.Response.Header.Set("Content-Type", "application/json")
	c.Response.SetStatusCode(http.StatusOK)
	body, _ := json.Marshal(toJSON(newCfg))
	c.Response.SetBody(body)
}

// statsJSON is the response shape for GET /admin/stats.
type statsJSON struct {
	CurrentConcurrency int     `json:"current_concurrency"`
	QueueDepth         int     `json:"queue_depth"`
	QPS                float64 `json:"qps"`
}

// GetStats handles GET /admin/stats.
func (a *Admin) GetStats(ctx context.Context, c *app.RequestContext) {
	c.Response.Header.Set("Content-Type", "application/json")
	c.Response.SetStatusCode(http.StatusOK)
	body, _ := json.Marshal(statsJSON{
		CurrentConcurrency: a.sema.Current(),
		QueueDepth:         a.q.Depth(),
		QPS:                a.streamer.CurrentQPS(),
	})
	c.Response.SetBody(body)
}
