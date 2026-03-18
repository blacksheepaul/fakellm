package config

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
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
	TokensPerSecond    float64 // base token emit rate
	FirstTokenDelayMs  int     // delay before emitting first token (ms)
	FixedDelayMs       int     // fixed extra delay per token (ms), applied to every token
	JitterMs           int     // random ±jitter per token (ms)

	// Slowdown: when global QPS exceeds SlowdownQPSThreshold,
	// effective rate = TokensPerSecond * SlowdownFactor (< 1.0 means slower).
	SlowdownQPSThreshold float64
	SlowdownFactor       float64

	// TPSVariance adds per-request variance to the base TokensPerSecond.
	// e.g., 0.15 means each request gets TPS in [85%, 115%] of base.
	// This simulates real GPU clusters where different requests experience
	// different batch sizes and scheduling delays.
	TPSVariance float64
}

// Default returns a sensible out-of-the-box configuration.
func Default() *Config {
	return &Config{
		MaxConcurrent:        10,
		MaxQueueDepth:        100,
		QueueTimeout:         30 * time.Second,
		TokensPerSecond:      20,
		FirstTokenDelayMs:    0,
		FixedDelayMs:         0,
		JitterMs:             0,
		SlowdownQPSThreshold: 50,
		SlowdownFactor:       0.5,
		TPSVariance:          0.0,
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

// LoadFromEnv loads configuration from .env file.
// Panics if .env file does not exist or if any variable is missing or invalid.
func LoadFromEnv() *Config {
	// Load .env file, panic if not exists
	if err := godotenv.Load(); err != nil {
		panic(fmt.Sprintf("config: .env file not found: %v", err))
	}

	return &Config{
		MaxConcurrent:        mustGetIntEnv("MAX_CONCURRENT"),
		MaxQueueDepth:        mustGetIntEnv("MAX_QUEUE_DEPTH"),
		QueueTimeout:         mustGetDurationEnv("QUEUE_TIMEOUT"),
		TokensPerSecond:      mustGetFloatEnv("TOKENS_PER_SECOND"),
		FirstTokenDelayMs:    mustGetIntEnv("FIRST_TOKEN_DELAY_MS"),
		FixedDelayMs:         mustGetIntEnv("FIXED_DELAY_MS"),
		JitterMs:             mustGetIntEnv("JITTER_MS"),
		SlowdownQPSThreshold: mustGetFloatEnv("SLOWDOWN_QPS_THRESHOLD"),
		SlowdownFactor:       mustGetFloatEnv("SLOWDOWN_FACTOR"),
		TPSVariance:          mustGetFloatEnv("TPS_VARIANCE"),
	}
}

func mustGetIntEnv(key string) int {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("config: required environment variable %s is not set", key))
	}
	i, err := strconv.Atoi(value)
	if err != nil {
		panic(fmt.Sprintf("config: invalid value for %s: %q, expected integer", key, value))
	}
	return i
}

func mustGetFloatEnv(key string) float64 {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("config: required environment variable %s is not set", key))
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		panic(fmt.Sprintf("config: invalid value for %s: %q, expected number", key, value))
	}
	return f
}

func mustGetDurationEnv(key string) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("config: required environment variable %s is not set", key))
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		panic(fmt.Sprintf("config: invalid value for %s: %q, expected duration (e.g., 30s, 1m)", key, value))
	}
	return d
}
