package trust

import (
	"slices"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/freshness"
)

// DeriveState computes the snapshot state as a pure function of the shared
// freshness facts and the snapshot's generation binding — never measured
// independently (ADR 0006 D3: a status/trust contradiction is unrepresentable
// because state is a derivation, not a second probe). No I/O happens here;
// every input is a fact the caller already read.
//
// Inputs: f is the shared freshness probe; snapshotFound reports whether a
// persisted snapshot exists under MetaSnapshot; snapshotGeneration is its
// MetaSnapshotGeneration stamp; liveGeneration is the graph's live
// index.full_ingest_generation; digestOK reports whether the stored
// MetaSnapshotDigest matches Digest over the stored bytes; live is the graph's
// current aggregate (TrustAggregatePort — topN is irrelevant, the boundary
// listing is never compared); snap is the decoded snapshot.
//
// Invariant (ADR 0006 D3): f.Current == false can NEVER yield StateCurrent —
// the drift rule maps it to StateStale before the CURRENT fall-through, so
// missing evidence always maps away from CURRENT, never toward it.
func DeriveState(f freshness.Report, snapshotFound bool, snapshotGeneration, liveGeneration string, digestOK bool, live graphstore.TrustStats, snap Snapshot) State {
	// No graph, no snapshot, a corrupt snapshot (digest mismatch), or a graph
	// with no generation to bind to: fail closed to UNAVAILABLE (ADR 0006 D4 —
	// the §14.4 variant 3 window and any broken binding read "no answer",
	// never a wrong answer; contract doc §1.6).
	if !f.Index.Exists || !snapshotFound || !digestOK || liveGeneration == "" {
		return StateUnavailable
	}
	// A pass running or crashed (full-pass marker, held lock) or a store that
	// cannot warm-start is an incomplete index: INCOMPLETE wins over any
	// staleness comparison because the graph itself is not settled (ADR 0006
	// D3 "a full-pass marker or running ingest reads INCOMPLETE").
	if f.Index.FullPassInProgress || f.Index.LockHeld || !f.Index.WarmStartable {
		return StateIncomplete
	}
	// Generation mismatch: the snapshot describes a different pass than the
	// live graph (ADR 0006 D4 — an old snapshot on a new graph and a new
	// snapshot on an old graph both fail this equality). BOTH bindings must
	// match: the store key (what the writer stamps last) and the
	// digest-protected copy inside the document — the digest cannot see a
	// kv_meta key rebound around unrefreshed bytes (a partial metadata
	// restore), so the inner binding is the one a key-level rewrite cannot
	// satisfy.
	if snapshotGeneration != liveGeneration || snap.Generation.FullPassGeneration != liveGeneration {
		return StateStale
	}
	// Source drift since the last sync (contract doc §1.6 STALE; the D3
	// invariant term: status-current=false never reads CURRENT).
	if !f.Current {
		return StateStale
	}
	// Aggregate cross-check: a pass that mutated the graph but died before
	// rebinding the snapshot leaves matching generations over a moved graph —
	// "the graph changed after the snapshot" reads STALE (contract doc §1.6).
	// The whole recomputable aggregate is compared (totals, per-kind,
	// per-tier, external counts), never the boundary listing (topN-bounded on
	// the write side). Residual limitation, accepted and documented: a
	// crash-window mutation that preserves this ENTIRE distribution (e.g. a
	// pure symbol rename) is indistinguishable from current by the recorded
	// facts and reads CURRENT until the next successful pass republishes.
	if !graphFactsEqual(snap.Graph, NewGraphFacts(live)) ||
		snap.External.Nodes != live.ExternalNodes || snap.External.Edges != live.ExternalEdges {
		return StateStale
	}
	return StateCurrent
}

// graphFactsEqual compares the canonical graph aggregate field-wise.
// slices.Equal (unlike reflect.DeepEqual) treats a nil and an empty kind list
// as equal, so a decoded snapshot ("[]" unmarshals non-nil) never diverges
// from a freshly built aggregate over the same facts.
func graphFactsEqual(a, b GraphFacts) bool {
	return a.NodesTotal == b.NodesTotal &&
		a.EdgesTotal == b.EdgesTotal &&
		a.EdgesByTier == b.EdgesByTier &&
		slices.Equal(a.EdgesByKind, b.EdgesByKind)
}
