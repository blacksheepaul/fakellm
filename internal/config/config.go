package config

import (
	"sync/atomic"
	"time"
)

// Config holds all runtime-tunable parameters.
// It is treated as immutable once stored; mutations go through Store.
type Config struct {
	// Admission
	MaxConcurrent int // max in-flight requests; 0 = unlimited

	// Queue
	MaxQueueDepth int           // max waiting requests; 0 = unlimited
	QueueTimeout  time.Duration // max time a request waits in queue; 0 = no timeout

	// TokenStream
	TokensPerSecond float64 // base token emit rate
	FixedDelayMs    int     // fixed extra delay per token (ms)
	JitterMs        int     // random ±jitter per token (ms)

	// Slowdown: when global QPS exceeds SlowdownQPSThreshold,
	// effective rate = TokensPerSecond * SlowdownFactor (< 1.0 means slower).
	SlowdownQPSThreshold float64
	SlowdownFactor       float64
}

// Default returns a sensible out-of-the-box configuration.
func Default() *Config {
	return &Config{
		MaxConcurrent:        10,
		MaxQueueDepth:        100,
		QueueTimeout:         30 * time.Second,
		TokensPerSecond:      20,
		FixedDelayMs:         0,
		JitterMs:             0,
		SlowdownQPSThreshold: 50,
		SlowdownFactor:       0.5,
	}
}

// Manager holds the live config behind an atomic pointer, enabling
// lock-free reads and copy-on-write updates.
type Manager struct {
	ptr atomic.Pointer[Config]
}

// NewManager creates a Manager seeded with cfg.
func NewManager(cfg *Config) *Manager {
	m := &Manager{}
	m.ptr.Store(cfg)
	return m
}

// Load returns the current config snapshot. Callers must not mutate it.
func (m *Manager) Load() *Config {
	return m.ptr.Load()
}

// Store atomically replaces the config with a copy of cfg.
func (m *Manager) Store(cfg *Config) {
	clone := *cfg // copy-on-write
	m.ptr.Store(&clone)
}

// Patch applies a mutation function to a copy of the current config
// and atomically stores the result. It returns the new config.
func (m *Manager) Patch(fn func(*Config)) *Config {
	old := m.ptr.Load()
	next := *old // copy
	fn(&next)
	m.ptr.Store(&next)
	return &next
}
