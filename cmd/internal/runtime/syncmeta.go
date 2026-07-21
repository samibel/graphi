package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/gitinfo"
)

// Sync metadata keys. Stamped into the graph store's kv_meta after every
// successful ingest (full or incremental) so `graphi status` and the branch
// banners can report when — and on which branch/commit — the graph was last
// brought up to date. Stamping happens HERE, at the cmd rank: the engine's
// byte-parity contract (full vs incremental) covers nodes/edges, never
// kv_meta, and engine/ingest stays git-free by design.
const (
	// MetaSyncTime is the RFC3339 UTC wall-clock of the last successful ingest.
	MetaSyncTime = "sync.last_time"
	// MetaSyncBranch is the git branch checked out at that time ("" when
	// detached or when the root is not a git repository).
	MetaSyncBranch = "sync.branch"
	// MetaSyncCommit is the git commit hex at that time ("" when unknown).
	MetaSyncCommit = "sync.commit"
)

// StampSyncMetadata records when/where a successful ingest ran. Git resolution
// is best-effort: a non-git root stamps empty branch/commit rather than
// failing — the timestamp alone still tells `graphi status` a sync happened.
func StampSyncMetadata(ctx context.Context, store graphstore.Graphstore, root string, now time.Time) error {
	info, _ := gitinfo.Head(root) // ok=false → zero Info, stamp empties
	for key, value := range map[string]string{
		MetaSyncTime:   now.UTC().Format(time.RFC3339),
		MetaSyncBranch: info.Branch,
		MetaSyncCommit: info.Commit,
	} {
		if err := store.SetMetadata(ctx, key, value); err != nil {
			return fmt.Errorf("stamp sync metadata: %w", err)
		}
	}
	return nil
}

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
