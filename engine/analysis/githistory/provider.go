package githistory

import (
	"context"
	"sort"
	"time"
)

// Commit represents a single git commit with the metadata needed to derive
// churn, bus-factor, and co-change signals. The provider is responsible for
// filling these fields from whatever backing store it reads (real .git, in-
// memory test data, etc.).
type Commit struct {
	// SHA is the full commit hash (hex-encoded, 40 chars for SHA-1).
	SHA string
	// Author is the commit author identity (name or email — the provider
	// decides the canonical form).
	Author string
	// Timestamp is the author date of the commit, used for age-based window
	// filtering.
	Timestamp time.Time
	// FilesChanged lists every file path touched by this commit. For churn
	// purposes each entry counts as one change to that file.
	FilesChanged []string
}

// GitProvider is the abstraction over git history access. Production
// implementations shell out to `git log`; tests use InMemoryProvider. The
// provider returns commits in reverse-chronological order (newest first),
// already bounded by maxCommits and since.
type GitProvider interface {
	// Log returns commits in reverse-chronological order. The implementation
	// MUST respect both bounds:
	//   - maxCommits: return at most this many commits (0 = no limit).
	//   - since: return only commits at or after this timestamp (zero = no
	//     time limit).
	// Whichever constraint is tighter wins (dual-constraint bounded window).
	Log(ctx context.Context, maxCommits int, since time.Time) ([]Commit, error)
}

// InMemoryProvider is a GitProvider backed by a pre-built slice of commits.
// It is the canonical test double: deterministic, no I/O, no git dependency.
type InMemoryProvider struct {
	// Commits must be pre-sorted in reverse-chronological order (newest first).
	Commits []Commit
}

// Log filters the in-memory commits by maxCommits and since, returning a new
// slice that satisfies both bounds.
func (p *InMemoryProvider) Log(_ context.Context, maxCommits int, since time.Time) ([]Commit, error) {
	var out []Commit
	for _, c := range p.Commits {
		// Age filter: skip commits older than since (when since is non-zero).
		if !since.IsZero() && c.Timestamp.Before(since) {
			continue
		}
		out = append(out, c)
		// Count filter: stop after maxCommits (when maxCommits > 0).
		if maxCommits > 0 && len(out) >= maxCommits {
			break
		}
	}
	return out, nil
}

// sortedKeys returns the keys of a map[string]T in sorted order for
// deterministic iteration. Used by churn, bus-factor, and co-change to convert
// map results into stable slices.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
