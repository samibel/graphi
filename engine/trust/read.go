package trust

import (
	"context"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/freshness"
)

// Load reads the persisted snapshot triple back from the store's kv_meta.
// Strictly read-only. Outcomes, fail closed per ADR 0006 D4:
//
//   - MetaSnapshot missing entirely, or holding the empty tombstone a
//     port-less pass writes to clear an older publish (the Graphstore
//     interface has no metadata delete): rawFound=false (no snapshot was ever
//     published, the publish never got that far, or it was cleared) —
//     DeriveState maps it to UNAVAILABLE.
//   - Bytes present but the stored digest is missing, does not match Digest
//     over the raw bytes, or the bytes do not Decode (corrupt or an
//     unsupported schema): rawFound=true, digestOK=false, zero Snapshot. The
//     broken document is reported through digestOK — never masked behind an
//     error and never partially interpreted.
//   - Everything verifies: the decoded Snapshot with digestOK=true.
//
// generation is the MetaSnapshotGeneration stamp ("" when unset); a non-nil
// err is an operational store failure only.
func Load(ctx context.Context, store graphstore.Graphstore) (snap Snapshot, rawFound bool, digestOK bool, generation string, err error) {
	raw, err := store.Metadata(ctx, MetaSnapshot)
	if errors.Is(err, graphstore.ErrNotFound) {
		return Snapshot{}, false, false, "", nil
	}
	if err != nil {
		return Snapshot{}, false, false, "", fmt.Errorf("trust: read snapshot: %w", err)
	}
	if raw == "" {
		// Cleared tombstone. Checked on the BYTES key alone so a crash
		// mid-clear (bytes emptied, digest/generation still populated) already
		// reads absent — fail closed, never a verifying stale triple.
		return Snapshot{}, false, false, "", nil
	}

	generation, err = store.Metadata(ctx, MetaSnapshotGeneration)
	if errors.Is(err, graphstore.ErrNotFound) {
		generation = ""
	} else if err != nil {
		return Snapshot{}, true, false, "", fmt.Errorf("trust: read snapshot generation: %w", err)
	}

	stored, err := store.Metadata(ctx, MetaSnapshotDigest)
	if errors.Is(err, graphstore.ErrNotFound) {
		return Snapshot{}, true, false, generation, nil
	}
	if err != nil {
		return Snapshot{}, true, false, generation, fmt.Errorf("trust: read snapshot digest: %w", err)
	}
	if Digest([]byte(raw)) != stored {
		return Snapshot{}, true, false, generation, nil
	}
	s, decErr := Decode([]byte(raw))
	if decErr != nil {
		return Snapshot{}, true, false, generation, nil
	}
	return s, true, true, generation, nil
}

// Evaluate composes Load, the live graph aggregate, and DeriveState into the
// snapshot state a reader reports. f is the shared freshness probe result and
// liveGeneration the graph's live index.full_ingest_generation — both are
// facts the caller already read; Evaluate itself stays read-only.
//
// The live aggregate comes from TrustAggregatePort.TrustStats(ctx, 0) (topN=0
// keeps it cheap — DeriveState never compares the boundary listing). A store
// that does not implement the port has no live aggregate to cross-check
// against — and the writer clears rather than publishes for such a store — so
// Evaluate fails closed to UNAVAILABLE.
func Evaluate(ctx context.Context, store graphstore.Graphstore, f freshness.Report, liveGeneration string) (Snapshot, State, error) {
	snap, found, digestOK, snapGen, err := Load(ctx, store)
	if err != nil {
		return Snapshot{}, StateUnavailable, err
	}
	agg, ok := store.(graphstore.TrustAggregatePort)
	if !ok {
		return snap, StateUnavailable, nil
	}
	stats, err := agg.TrustStats(ctx, 0)
	if err != nil {
		return Snapshot{}, StateUnavailable, fmt.Errorf("trust: live counts: %w", err)
	}
	return snap, DeriveState(f, found, snapGen, liveGeneration, digestOK, stats, snap), nil
}
