package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/freshness"
	"github.com/samibel/graphi/internal/gitinfo"
)

// StampSyncMetadata records when/where a successful ingest ran. Stamping
// happens HERE, at the cmd rank: the engine's byte-parity contract (full vs
// incremental) covers nodes/edges, never kv_meta, and engine/ingest stays
// git-free by design. The key names and the read side (LastSync) live in
// internal/freshness. Git resolution is best-effort: a non-git root stamps
// empty branch/commit rather than failing — the timestamp alone still tells
// `graphi status` a sync happened.
func StampSyncMetadata(ctx context.Context, store graphstore.Graphstore, root string, now time.Time) error {
	info, _ := gitinfo.Head(root) // ok=false → zero Info, stamp empties
	for key, value := range map[string]string{
		freshness.MetaSyncTime:   now.UTC().Format(time.RFC3339),
		freshness.MetaSyncBranch: info.Branch,
		freshness.MetaSyncCommit: info.Commit,
	} {
		if err := store.SetMetadata(ctx, key, value); err != nil {
			return fmt.Errorf("stamp sync metadata: %w", err)
		}
	}
	return nil
}
