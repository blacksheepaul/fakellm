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

// LoadFromEnv loads configuration from environment variables with defaults.
// It attempts to load .env file if present, then reads from environment.
// Panics if any environment variable is set but has an invalid format.
func LoadFromEnv() *Config {
	// Try to load .env file (ignore error if not exists)
	_ = godotenv.Load()

	return &Config{
		MaxConcurrent:        mustGetIntEnv("MAX_CONCURRENT", 10),
		MaxQueueDepth:        mustGetIntEnv("MAX_QUEUE_DEPTH", 100),
		QueueTimeout:         mustGetDurationEnv("QUEUE_TIMEOUT", 30*time.Second),
		TokensPerSecond:      mustGetFloatEnv("TOKENS_PER_SECOND", 20),
		FixedDelayMs:         mustGetIntEnv("FIXED_DELAY_MS", 0),
		JitterMs:             mustGetIntEnv("JITTER_MS", 0),
		SlowdownQPSThreshold: mustGetFloatEnv("SLOWDOWN_QPS_THRESHOLD", 50),
		SlowdownFactor:       mustGetFloatEnv("SLOWDOWN_FACTOR", 0.5),
	}
}

func mustGetIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		i, err := strconv.Atoi(value)
		if err != nil {
			panic(fmt.Sprintf("config: invalid value for %s: %q, expected integer", key, value))
		}
		return i
	}
	return defaultValue
}

func mustGetFloatEnv(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			panic(fmt.Sprintf("config: invalid value for %s: %q, expected number", key, value))
		}
		return f
	}
	return defaultValue
}

func mustGetDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			panic(fmt.Sprintf("config: invalid value for %s: %q, expected duration (e.g., 30s, 1m)", key, value))
		}
		return d
	}
	return defaultValue
}
