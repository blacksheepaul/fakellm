// Package config provides the externally-facing configuration API types.
// This package is located in pkg/ so that external test clients can import it.
package config

// AdminConfig is the JSON representation used for the Admin API.
// It mirrors internal/config.Config but uses JSON-friendly types
// (float64 seconds instead of time.Duration).
type AdminConfig struct {
	MaxConcurrent        int     `json:"max_concurrent"`
	MaxQueueDepth        int     `json:"max_queue_depth"`
	QueueTimeoutSec      float64 `json:"queue_timeout_sec"`
	TokensPerSecond      float64 `json:"tokens_per_second"`
	FirstTokenDelayMs    int     `json:"first_token_delay_ms"`
	FixedDelayMs         int     `json:"fixed_delay_ms"`
	JitterMs             int     `json:"jitter_ms"`
	SlowdownQPSThreshold float64 `json:"slowdown_qps_threshold"`
	SlowdownFactor       float64 `json:"slowdown_factor"`
	TPSVariance          float64 `json:"tps_variance"`
}
