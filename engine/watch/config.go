// Package watch is graphi's pure-Go filesystem watcher and bounded worker-pool
// for deterministic parallel incremental parse (SW-101, EP-017 P10).
//
// Layering: watch is an engine package. It depends only inward on engine/ingest
// (the serialized canonical apply) and core parse primitives reached through it;
// nothing in core depends on watch. The daemon SURFACE (surfaces/daemon) wires a
// watcher into the running daemon via a structural interface, so the dependency
// direction stays cmd → surfaces → engine → core.
//
// Determinism contract (the heart of SW-101): scheduling nondeterminism is
// confined to a PURE parse phase. A bounded worker-pool parses each changed file
// into an isolated, immutable ingest.ParsedFile holding no graphstore handle;
// a SINGLE serialized goroutine then merges those results through
// engine/ingest.ApplyChangedParsed, which applies them in canonical
// repo-relative-path sorted order inside one transaction. Node/edge ids,
// adjacency, and hash keys derive from content + canonical path only, never from
// goroutine scheduling, wall-clock, or arrival/completion order. The result is
// therefore byte-identical to a full single-threaded parse of the same on-disk
// state, regardless of how the pool interleaved.
//
// Security: fsnotify is local-only — the watch+parse path opens no network
// socket and never executes file contents. Path sanitization is inherited from
// engine/ingest (watched set == ingestable set); the bounded pool + debounce +
// backpressure are the DoS mitigation against save-storms and directory churn.
package watch

import (
	"runtime"
	"time"
)

// Config carries the watcher's tunable knobs. The zero value is NOT usable
// directly; call Normalize (or DefaultConfig) to fill safe defaults. The field
// names map 1:1 to the documented project config keys:
//
//	watch.debounce_ms        -> DebounceMs
//	watch.pool_size          -> PoolSize        (0 = auto from GOMAXPROCS)
//	watch.pool_hard_cap      -> PoolHardCap
//	watch.reconcile_interval -> ReconcileInterval
type Config struct {
	// DebounceMs is the quiet window, in milliseconds, used to coalesce a burst
	// of events (editor save-storms, branch switches) into a single batch of the
	// final on-disk state. Default 200ms.
	DebounceMs int
	// PoolSize is the number of parse workers. 0 means auto: GOMAXPROCS clamped
	// to PoolHardCap. Negative is treated as 0.
	PoolSize int
	// PoolHardCap is the absolute upper bound on parse workers, so an
	// adversarial or misconfigured PoolSize can never spawn an unbounded pool.
	PoolHardCap int
	// ReconcileInterval is how often the lost-event safety net rescans the
	// workspace and repairs drift between on-disk hashes and the cache. It is
	// floored to a safe minimum by Normalize so it can never be tuned to a
	// self-DoS rescan loop.
	ReconcileInterval time.Duration
}

// Default knob values. These are conservative: a 200ms debounce absorbs typical
// save-storms without feeling stale, the pool hard cap bounds memory/goroutines,
// and a 30s reconcile floor keeps the safety-net cost negligible.
const (
	DefaultDebounceMs        = 200
	DefaultPoolHardCap       = 16
	DefaultReconcileInterval = 30 * time.Second
	// MinReconcileInterval floors ReconcileInterval so reconcile can never be
	// configured into a tight rescan loop (a self-DoS guard).
	MinReconcileInterval = time.Second
)

// DefaultConfig returns a Config populated with the safe defaults.
func DefaultConfig() Config {
	return Config{
		DebounceMs:        DefaultDebounceMs,
		PoolSize:          0,
		PoolHardCap:       DefaultPoolHardCap,
		ReconcileInterval: DefaultReconcileInterval,
	}
}

// Normalize fills unset/invalid fields with safe defaults and returns the
// result. It is idempotent.
func (c Config) Normalize() Config {
	if c.DebounceMs <= 0 {
		c.DebounceMs = DefaultDebounceMs
	}
	if c.PoolHardCap <= 0 {
		c.PoolHardCap = DefaultPoolHardCap
	}
	if c.PoolSize < 0 {
		c.PoolSize = 0
	}
	if c.ReconcileInterval <= 0 {
		c.ReconcileInterval = DefaultReconcileInterval
	}
	if c.ReconcileInterval < MinReconcileInterval {
		c.ReconcileInterval = MinReconcileInterval
	}
	return c
}

// Debounce returns the quiet window as a Duration.
func (c Config) Debounce() time.Duration {
	return time.Duration(c.DebounceMs) * time.Millisecond
}

// Workers resolves the effective bounded worker count: min(requested, hard cap),
// where requested defaults to GOMAXPROCS when PoolSize is 0. The result is always
// >= 1 and <= PoolHardCap, so the pool is provably bounded.
func (c Config) Workers() int {
	c = c.Normalize()
	n := c.PoolSize
	if n == 0 {
		n = runtime.GOMAXPROCS(0)
	}
	if n > c.PoolHardCap {
		n = c.PoolHardCap
	}
	if n < 1 {
		n = 1
	}
	return n
}
