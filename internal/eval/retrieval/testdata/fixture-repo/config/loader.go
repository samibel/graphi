// Package config loads the fixture's configuration from the environment.
// Every setting has a FIXTURE_ prefixed variable and a documented default;
// an unparseable value is an error rather than a silent fallback.
package config

import (
	"fmt"
	"time"
)

// Config is the fully resolved configuration.
type Config struct {
	// Addr is the listen address (FIXTURE_ADDR).
	Addr string
	// StoreKind selects the store implementation: "memory" or "file"
	// (FIXTURE_STORE).
	StoreKind string
	// StoreRoot is the FileStore root directory (FIXTURE_STORE_ROOT).
	StoreRoot string
	// TokenTTL is the bearer-token lifetime (FIXTURE_TOKEN_TTL).
	TokenTTL time.Duration
	// Secret signs and verifies tokens (FIXTURE_SECRET).
	Secret string
}

// Getenv is the lookup Load reads from; it is a parameter so tests can pass a
// map instead of mutating the process environment.
type Getenv func(key string) string

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		Addr:      ":8080",
		StoreKind: "memory",
		StoreRoot: "./data",
		TokenTTL:  time.Hour,
		Secret:    "fixture-secret",
	}
}

// Load reads the configuration from getenv on top of DefaultConfig. An empty
// variable keeps the default; a present but unparseable one is an error.
func Load(getenv Getenv) (Config, error) {
	cfg := DefaultConfig()
	if v := getenv("FIXTURE_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("FIXTURE_STORE"); v != "" {
		if v != "memory" && v != "file" {
			return Config{}, fmt.Errorf("config: FIXTURE_STORE must be memory or file, got %q", v)
		}
		cfg.StoreKind = v
	}
	if v := getenv("FIXTURE_STORE_ROOT"); v != "" {
		cfg.StoreRoot = v
	}
	if v := getenv("FIXTURE_TOKEN_TTL"); v != "" {
		ttl, err := parseTTL(v)
		if err != nil {
			return Config{}, err
		}
		cfg.TokenTTL = ttl
	}
	if v := getenv("FIXTURE_SECRET"); v != "" {
		cfg.Secret = v
	}
	return cfg, nil
}

// parseTTL parses a Go duration and rejects non-positive values.
func parseTTL(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: FIXTURE_TOKEN_TTL: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: FIXTURE_TOKEN_TTL must be positive, got %s", d)
	}
	return d, nil
}
