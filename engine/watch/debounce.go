package watch

import (
	"sort"
	"sync"
	"time"
)

// coalescer collapses a burst of raw filesystem events into a single batch of
// the FINAL on-disk state within a configurable quiet window. It keys pending
// work by repo-relative path (a save-storm of N writes to one file coalesces to
// one entry; a create-then-delete within the window nets to a single path whose
// final on-disk state the consumer re-stats). Each new event resets the quiet
// timer; the batch fires only once the workspace has been quiet for the full
// window.
//
// It is safe for concurrent Add calls. The fire callback runs on its own
// goroutine (the timer goroutine) and receives the sorted, de-duplicated path
// set; the consumer is responsible for re-stat'ing each path to resolve the
// final state (exists → parse, missing → delete).
type coalescer struct {
	window time.Duration
	fire   func(paths []string)

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
	stopped bool
}

// newCoalescer constructs a coalescer with the given quiet window and fire
// callback. window <= 0 is treated as a minimal positive window.
func newCoalescer(window time.Duration, fire func(paths []string)) *coalescer {
	if window <= 0 {
		window = time.Millisecond
	}
	return &coalescer{
		window:  window,
		fire:    fire,
		pending: make(map[string]struct{}),
	}
}

// Add records that relPath changed and (re)arms the quiet-window timer. Rapid
// successive Adds for the same or different paths within the window collapse
// into a single eventual fire of the accumulated set.
func (c *coalescer) Add(relPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.pending[relPath] = struct{}{}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(c.window, c.flush)
}

// flush snapshots and clears the pending set, then invokes fire outside the
// lock. A no-op when nothing is pending.
func (c *coalescer) flush() {
	c.mu.Lock()
	if c.stopped || len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	paths := make([]string, 0, len(c.pending))
	for p := range c.pending {
		paths = append(paths, p)
	}
	c.pending = make(map[string]struct{})
	c.timer = nil
	c.mu.Unlock()

	sort.Strings(paths) // deterministic batch order (cosmetic; apply re-sorts)
	if c.fire != nil {
		c.fire(paths)
	}
}

// Flush forces immediate emission of any pending batch, bypassing the quiet
// timer. It is used by tests to make debounce assertions deterministic without
// relying on wall-clock timing, and by Stop-time draining.
func (c *coalescer) Flush() {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	c.flush()
}

// Stop halts the coalescer; subsequent Adds are ignored and any armed timer is
// cancelled. Pending work is dropped (the reconcile safety net will recover it).
func (c *coalescer) Stop() {
	c.mu.Lock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.pending = make(map[string]struct{})
	c.mu.Unlock()
}
