package interproc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Summary is a Sharir-Pnueli functional summary for a single procedure. It
// captures the input-to-output transfer relation: given an input abstract state
// (set of labels reaching formal parameters), the summary records which labels
// reach the return/output positions. The summary is keyed by the procedure ID
// and fingerprinted by the content-addressed hash of its input state.
type Summary struct {
	// ProcID is the procedure (graph node) identifier.
	ProcID string `json:"proc_id"`
	// InputHash is the SHA-256 content-addressed key of the input state that
	// produced this summary. Two runs with identical inputs yield the same hash.
	InputHash string `json:"input_hash"`
	// InputLabels is the abstract input state (sorted label set) that was used
	// to compute this summary.
	InputLabels []string `json:"input_labels"`
	// OutputLabels is the abstract output state (sorted label set) — the labels
	// that survive propagation through the procedure body.
	OutputLabels []string `json:"output_labels"`
	// Approximate is true when the summary was computed with widening or
	// conservative over-approximation (e.g. cap-hit or recursive cycle).
	Approximate bool `json:"approximate,omitempty"`
	// Iterations records how many fixpoint iterations were needed.
	Iterations int `json:"iterations"`
}

// Equal reports whether two summaries have the same output labels.
func (s Summary) Equal(other Summary) bool {
	if len(s.OutputLabels) != len(other.OutputLabels) {
		return false
	}
	for i := range s.OutputLabels {
		if s.OutputLabels[i] != other.OutputLabels[i] {
			return false
		}
	}
	return true
}

// ContentKey computes the SHA-256 content-addressed cache key for a procedure's
// input state. The key is deterministic: procedure ID and input labels are
// normalized (sorted, lowercased) before hashing.
func ContentKey(procID string, inputLabels []string) string {
	sorted := make([]string, len(inputLabels))
	copy(sorted, inputLabels)
	sort.Strings(sorted)
	payload := fmt.Sprintf("proc=%s;inputs=%s", procID, strings.Join(sorted, ","))
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// CacheStats tracks content-addressed summary cache performance.
type CacheStats struct {
	Hits      int `json:"hits"`
	Misses    int `json:"misses"`
	Evictions int `json:"evictions"`
	Size      int `json:"size"`
}

// SummaryCache is a concurrency-safe, content-addressed cache of procedure
// summaries keyed by SHA-256 of (procID, inputLabels). It enforces a maximum
// entry count; when the cap is exceeded, the oldest entry is evicted (FIFO).
type SummaryCache struct {
	mu      sync.RWMutex
	entries map[string]Summary // key → summary
	order   []string           // insertion order for FIFO eviction
	maxSize int
	stats   CacheStats
}

// NewSummaryCache creates a cache with the given maximum entry count. If
// maxSize <= 0, the cache is unbounded (no evictions).
func NewSummaryCache(maxSize int) *SummaryCache {
	return &SummaryCache{
		entries: make(map[string]Summary),
		maxSize: maxSize,
	}
}

// Get retrieves a cached summary by its content-addressed key. Returns the
// summary and whether it was found. Thread-safe.
func (c *SummaryCache) Get(key string) (Summary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.entries[key]
	return s, ok
}

// Put stores a summary under its content-addressed key. If the cache is at
// capacity, the oldest entry is evicted. Thread-safe.
func (c *SummaryCache) Put(key string, s Summary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		// Update in place; no eviction needed.
		c.entries[key] = s
		return
	}
	// Evict if at capacity.
	if c.maxSize > 0 && len(c.entries) >= c.maxSize {
		c.evictOldest()
	}
	c.entries[key] = s
	c.order = append(c.order, key)
}

// RecordHit increments the cache-hit counter. Thread-safe.
func (c *SummaryCache) RecordHit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Hits++
}

// RecordMiss increments the cache-miss counter. Thread-safe.
func (c *SummaryCache) RecordMiss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Misses++
}

// Stats returns a snapshot of the cache statistics. Thread-safe.
func (c *SummaryCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CacheStats{
		Hits:      c.stats.Hits,
		Misses:    c.stats.Misses,
		Evictions: c.stats.Evictions,
		Size:      len(c.entries),
	}
}

// evictOldest removes the oldest entry (FIFO). Caller must hold c.mu.
func (c *SummaryCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, oldest)
	c.stats.Evictions++
}
