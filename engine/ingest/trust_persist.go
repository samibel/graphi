package ingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/freshness"
)

// trustBoundaryTopN bounds the external-boundary listing collected into the
// trust snapshot.
const trustBoundaryTopN = 16

// collectTrustSnapshot folds the store aggregate and this pass's in-memory
// signals into the canonical v1 snapshot, bound to generation. ok=false means
// the store does not implement TrustAggregatePort: without the aggregate there
// are no graph facts to bind, and fail-closed (ADR 0006 D4) means NO snapshot
// rather than a partial one — the caller then CLEARS any older publish (see
// persistTrustSnapshot) so readers derive UNAVAILABLE ("no answer"), never a
// wrong answer built from guessed counts or a predecessor's leftovers.
//
// Convergence contract: Graph and External derive from the committed graph, so
// they match a full pass over the same source by construction (the store's
// full-vs-incremental byte-parity discipline). The pass-scoped sections
// describe THIS pass: Link and Parse cover the (re)processed set — which on an
// incremental pass equals the full-pass view exactly when the cascade covers
// every affected file, the property the cascade exists to guarantee — and
// TypeResolution always recomputes over the whole walked snapshot when it runs
// at all (a non-Go change set legitimately publishes zero resolver facts).
func (i *Ingester) collectTrustSnapshot(ctx context.Context, generation string) (trust.Snapshot, bool, error) {
	agg, ok := i.store.(graphstore.TrustAggregatePort)
	if !ok {
		return trust.Snapshot{}, false, nil
	}
	stats, err := agg.TrustStats(ctx, trustBoundaryTopN)
	if err != nil {
		return trust.Snapshot{}, false, fmt.Errorf("ingest: trust snapshot aggregate: %w", err)
	}

	skips := i.SkippedDiagnostics()
	paths := make([]string, 0, len(skips))
	byReason := make(map[string]int, len(skips))
	for _, s := range skips {
		paths = append(paths, s.Path)
		byReason[string(s.Reason)]++
	}

	// The sync stamp trails the pass that is running right now (the runtime
	// stamps AFTER a successful ingest), so commit/branch describe the last
	// certified sync — "" until first stamped.
	var branch, commit string
	if _, b, c, stamped := freshness.LastSync(ctx, i.store); stamped {
		branch, commit = b, c
	}
	prof := string(i.profile)
	if prof == "" {
		stored, perr := i.store.Metadata(ctx, "index.profile")
		if perr != nil && !errors.Is(perr, graphstore.ErrNotFound) {
			return trust.Snapshot{}, false, fmt.Errorf("ingest: trust snapshot profile: %w", perr)
		}
		prof = stored
	}

	return trust.Snapshot{
		SchemaVersion:   trust.SnapshotSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		Generation: trust.GenerationRef{
			FullPassGeneration: generation,
			SourceCommit:       commit,
			Branch:             branch,
			IndexProfile:       prof,
		},
		Graph:          trust.NewGraphFacts(stats),
		External:       trust.NewExternalFacts(stats),
		Link:           trust.NewLinkFacts(i.lastLinkStats),
		Parse:          trust.NewParseFacts(paths, byReason),
		TypeResolution: i.combinedTypeResolutionFacts(),
	}, true, nil
}

// persistTrustSnapshot publishes the three snapshot keys AFTER the graph's own
// commits (PRD §14.4 variant 3, ADR 0006 D4). Metadata-only writes: the
// graphstore Snapshot serializes nodes/edges only, so the graph's byte
// identity is untouched (the intraproctaint precedent). Write order is part of
// the crash contract — bytes, then digest, then the generation binding LAST —
// so a crash inside the publish leaves a binding that fails verification
// (digest mismatch reads UNAVAILABLE; a prior generation reads STALE), never a
// certified partial snapshot. Any error fails the pass loudly; a silent
// partial publish is the one outcome variant 3 forbids.
func (i *Ingester) persistTrustSnapshot(ctx context.Context, generation string) error {
	snap, ok, err := i.collectTrustSnapshot(ctx, generation)
	if err != nil {
		return err
	}
	if !ok {
		// Decision of record for the port-missing path: the pass must not
		// RETAIN an older publish either. An incremental pass never moves the
		// generation binding, so a leftover snapshot from an earlier
		// port-capable writer can satisfy every DeriveState equality over a
		// graph this pass just changed — a false CURRENT through any reader
		// whose own store handle implements the port. Clearing turns "cannot
		// publish" into "no answer" (UNAVAILABLE), the only ADR 0006 D4
		// failure direction.
		return i.clearTrustSnapshot(ctx)
	}
	b, err := trust.Encode(snap)
	if err != nil {
		return fmt.Errorf("ingest: encode trust snapshot: %w", err)
	}
	if err := i.store.SetMetadata(ctx, trust.MetaSnapshot, string(b)); err != nil {
		return fmt.Errorf("ingest: persist trust snapshot: %w", err)
	}
	if err := i.store.SetMetadata(ctx, trust.MetaSnapshotDigest, trust.Digest(b)); err != nil {
		return fmt.Errorf("ingest: persist trust snapshot digest: %w", err)
	}
	if err := i.store.SetMetadata(ctx, trust.MetaSnapshotGeneration, generation); err != nil {
		return fmt.Errorf("ingest: persist trust snapshot generation: %w", err)
	}
	return nil
}

// clearTrustSnapshot tombstones the three snapshot keys with empty values
// (the Graphstore interface has no metadata delete; trust.Load reads an empty
// MetaSnapshot as absent). The BYTES key is cleared first, mirroring the
// publish order's crash contract: a crash mid-clear leaves at worst emptied
// bytes beside a populated digest/generation — already absent to Load — never
// a verifying stale triple.
func (i *Ingester) clearTrustSnapshot(ctx context.Context) error {
	for _, key := range []string{trust.MetaSnapshot, trust.MetaSnapshotDigest, trust.MetaSnapshotGeneration} {
		if err := i.store.SetMetadata(ctx, key, ""); err != nil {
			return fmt.Errorf("ingest: clear trust snapshot (%s): %w", key, err)
		}
	}
	return nil
}

// persistTrustSnapshotLive rebinds the snapshot after a successful incremental
// mutation: the same three keys, stamped with the CURRENT live generation
// (index.full_ingest_generation — an incremental pass never mints one). A
// store no full pass ever certified has no generation to bind to; publishing
// nothing keeps readers at UNAVAILABLE instead of minting an unbindable
// snapshot.
func (i *Ingester) persistTrustSnapshotLive(ctx context.Context) error {
	generation, err := i.store.Metadata(ctx, graphFullPassGenerationKey)
	if errors.Is(err, graphstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ingest: read live generation for trust snapshot: %w", err)
	}
	if generation == "" {
		return nil
	}
	return i.persistTrustSnapshot(ctx, generation)
}
