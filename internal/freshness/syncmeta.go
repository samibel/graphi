package freshness

import (
	"context"
	"errors"
	"time"

	"github.com/samibel/graphi/core/graphstore"
)

// Sync metadata keys. Stamped into the graph store's kv_meta after every
// successful ingest (full or incremental) so `graphi status` and the branch
// banners can report when — and on which branch/commit — the graph was last
// brought up to date. The key strings are a persisted contract. Stamping
// (the write side) stays in cmd/internal/runtime.StampSyncMetadata at the
// cmd rank: the engine's byte-parity contract (full vs incremental) covers
// nodes/edges, never kv_meta, and engine/ingest stays git-free by design.
const (
	// MetaSyncTime is the RFC3339 UTC wall-clock of the last successful ingest.
	MetaSyncTime = "sync.last_time"
	// MetaSyncBranch is the git branch checked out at that time ("" when
	// detached or when the root is not a git repository).
	MetaSyncBranch = "sync.branch"
	// MetaSyncCommit is the git commit hex at that time ("" when unknown).
	MetaSyncCommit = "sync.commit"
)

// LastSync reads the sync stamp back. ok=false means the store was never
// stamped (pre-verb stores, or a store built by `graphi index -db` before this
// feature); branch/commit may be empty even when ok=true (non-git roots).
func LastSync(ctx context.Context, store graphstore.Graphstore) (t time.Time, branch, commit string, ok bool) {
	raw, err := store.Metadata(ctx, MetaSyncTime)
	if err != nil {
		return time.Time{}, "", "", false
	}
	t, perr := time.Parse(time.RFC3339, raw)
	if perr != nil {
		return time.Time{}, "", "", false
	}
	branch, err = store.Metadata(ctx, MetaSyncBranch)
	if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
		return time.Time{}, "", "", false
	}
	commit, err = store.Metadata(ctx, MetaSyncCommit)
	if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
		return time.Time{}, "", "", false
	}
	return t, branch, commit, true
}
