// Package trust is the P1 trust core: the v1 snapshot model, its canonical
// byte-stable serialization, the pure snapshot-state derivation, and the
// assessment layer between facts and policies — the closed verdict/finding
// model (assess.go, findings.go), fail-closed target-scope resolution
// (scope.go), limitation and recommendation builders (limitations.go,
// recommend.go), and the three built-in v1 policies with their pure evaluator
// (policy.go). The governing contracts are
// docs/plan/2026-08-graphi-p1-trust-contract-v1.md (frozen v1 terminology,
// snapshot states, the closed finding-code registry) and
// docs/adr/0006-status-vs-trust-separation.md (state is a pure derivation of
// the shared freshness facts; atomicity is PRD §14.4 variant 3 — post-commit
// write, fail-closed UNAVAILABLE until complete).
//
// Layering: trust depends on core/model, core/graphstore, engine/link,
// engine/typeresolve, and internal/freshness — never on engine/ingest (the
// collector wiring maps ingest signals into this package's types, not the
// other way around). The package performs NO I/O; persistence of the encoded
// snapshot under the kv_meta keys below belongs to the wiring layer, following
// the metadata-only precedent of engine/analysis/intraproctaint.
package trust

import (
	"path"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/typeresolve"
)

// SnapshotSchemaVersion versions the persisted snapshot document. Bump only on
// breaking changes to the shape or value domain (contract doc §6).
const SnapshotSchemaVersion = 1

// SnapshotVersion is the frozen snapshot format identifier (contract doc §2.2).
const SnapshotVersion = "trust-v1"

// Graphstore kv_meta keys for the persisted snapshot. Metadata-only writes are
// snapshot/byte-parity safe: the graphstore Snapshot serializes only
// nodes/edges, so persisting here never perturbs the graph identity (same
// precedent as analysis.intraproc_taint.v1). An empty MetaSnapshot value is
// the cleared tombstone (the Graphstore interface has no metadata delete): a
// pass that cannot publish clears the triple, and Load reads it as absent.
const (
	// MetaSnapshot holds the canonical Encode bytes of the Snapshot.
	MetaSnapshot = "trust.snapshot.v1"
	// MetaSnapshotDigest holds Digest(canonical bytes) — "sha256:<hex>".
	MetaSnapshotDigest = "trust.snapshot.digest"
	// MetaSnapshotGeneration holds the full-pass generation the snapshot
	// observed; readers trust the snapshot only when it equals the live graph
	// generation (ADR 0006 D4).
	MetaSnapshotGeneration = "trust.snapshot.generation"
)

// Bounded-list caps. Both follow the store's evidence cap precedent (64):
// the snapshot carries a bounded illustrative sample, never an unbounded dump.
const (
	// MaxParsePaths caps ParseFacts.Paths.
	MaxParsePaths = 64
	// MaxDegradedUnits caps TypeResolutionFacts.DegradedUnits.
	MaxDegradedUnits = 64
	// MaxPathLength caps the length of any single emitted path.
	//
	// Capping the list COUNT is not enough. A path is repository-controlled
	// input: its length is chosen by whoever named the file, so an unbounded
	// path is both a snapshot-size risk (PRD v1.0 §4 budgets the snapshot at
	// ≤ 1 MB) and the "nutzerkontrollierte Pfade … längenbegrenzt" requirement
	// of §7. 240 leaves a normal deep repository path untouched while bounding
	// the pathological case.
	MaxPathLength = 240
)

// truncatePath bounds a single path to MaxPathLength, marking any truncation
// so a shortened path is never mistaken for a real one. The marker is ASCII
// and cannot occur in a path graphi produced itself.
func truncatePath(p string) string {
	if len(p) <= MaxPathLength {
		return p
	}
	const marker = "…[truncated]"
	return p[:MaxPathLength-len(marker)] + marker
}

// State is the closed snapshot-state enum (contract doc §1.6, PRD §11.6/§18).
// It qualifies snapshots only — never policy assessments (verdicts are a
// separate enum that never mixes into this field).
type State string

const (
	// StateCurrent — graph exists, warm-startable, no drift, snapshot
	// generation equals graph generation.
	StateCurrent State = "CURRENT"
	// StateStale — source drift, snapshot generation does not match, or the
	// graph changed after the snapshot.
	StateStale State = "STALE"
	// StateIncomplete — full-pass marker set, aborted ingest, or incomplete
	// snapshot.
	StateIncomplete State = "INCOMPLETE"
	// StateUnavailable — no graph, no snapshot, incompatible old graph
	// version, or trust data not migratable.
	StateUnavailable State = "UNAVAILABLE"
)

// Snapshot is the v1 persisted trust snapshot: the facts one full pass
// collected, bound to the generation that produced them. Contract rules
// (contract doc §2.3) apply to the serialized form: every field is always
// present, empty slices are never null, counts are non-negative, lists are
// canonically sorted, and no map appears on the wire — sorted slices make the
// canonical ordering part of the type instead of an encoder courtesy.
//
// Deliberately absent in v1: any wall-clock timestamp (CompletedAt or
// similar). The canonical bytes must be reproducible from the facts alone;
// provenance timestamps, if ever wanted, arrive via non-canonical channels.
type Snapshot struct {
	SchemaVersion   int                 `json:"schema_version"`
	SnapshotVersion string              `json:"snapshot_version"`
	Generation      GenerationRef       `json:"generation"`
	Graph           GraphFacts          `json:"graph"`
	External        ExternalFacts       `json:"external"`
	Link            LinkFacts           `json:"link"`
	Parse           ParseFacts          `json:"parse"`
	TypeResolution  TypeResolutionFacts `json:"type_resolution"`
}

// GenerationRef binds a snapshot to the pass that produced it.
// FullPassGeneration is the warm-start nonce (sidecar full_pass_generation =
// graph index.full_ingest_generation); it is the binding DeriveState checks.
// SourceCommit and Branch come from the sync stamp when present, "" otherwise;
// IndexProfile is the store's index.profile, "" when unset.
type GenerationRef struct {
	FullPassGeneration string `json:"full_pass_generation"`
	SourceCommit       string `json:"source_commit"`
	Branch             string `json:"branch"`
	IndexProfile       string `json:"index_profile"`
}

// KindCount is one edge-kind count in the canonical sorted-slice form.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// TierCounts carries the closed tier vocabulary as explicit fields — exactly
// the three keys the contract freezes (contract doc §1.1), no map.
type TierCounts struct {
	Confirmed int `json:"confirmed"`
	Derived   int `json:"derived"`
	Heuristic int `json:"heuristic"`
}

// GraphFacts are the whole-graph totals (PRD §13.8 shape, map-free).
type GraphFacts struct {
	NodesTotal  int         `json:"nodes_total"`
	EdgesTotal  int         `json:"edges_total"`
	EdgesByKind []KindCount `json:"edges_by_kind"`
	EdgesByTier TierCounts  `json:"edges_by_tier"`
}

// Boundary is one external boundary node ranked by incident edge count.
type Boundary struct {
	QualifiedName string `json:"qualified_name"`
	IncidentEdges int    `json:"incident_edges"`
}

// ExternalFacts are the external-boundary counts and the bounded top-boundary
// listing (PRD §13.9 shape as far as the store collects it today).
type ExternalFacts struct {
	Nodes         int        `json:"nodes"`
	Edges         int        `json:"edges"`
	TopBoundaries []Boundary `json:"top_boundaries"`
}

// LinkFacts mirror link.Stats — the linker's per-pass resolution counters.
type LinkFacts struct {
	ResolvedDerived   int `json:"resolved_derived"`
	ResolvedHeuristic int `json:"resolved_heuristic"`
	ResolvedExternal  int `json:"resolved_external"`
	Skipped           int `json:"skipped"`
	Ambiguous         int `json:"ambiguous"`
}

// ReasonCount is one skip-reason count in the canonical sorted-slice form.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// ParseFacts are the fail-closed parse-skip facts. Skipped is the full count;
// Paths is a bounded, sorted, repo-relative sample (contract doc §2.3 rules
// 8–9: normalized relative paths only, never absolute private paths).
type ParseFacts struct {
	Skipped  int           `json:"skipped"`
	ByReason []ReasonCount `json:"by_reason"`
	Paths    []string      `json:"paths"`
}

// DegradedUnit identifies one package unit that was not type-checked, with the
// reason the resolver recorded.
type DegradedUnit struct {
	Dir        string `json:"dir"`
	Name       string `json:"name"`
	Reason     string `json:"reason"`
	TypeErrors int    `json:"type_errors"`
}

// TypeResolutionFacts are the type-resolver pass facts: unit coverage,
// degradation, and the never-fabricate counters.
type TypeResolutionFacts struct {
	UnitsTotal     int            `json:"units_total"`
	UnitsDegraded  int            `json:"units_degraded"`
	TypeErrors     int            `json:"type_errors"`
	SkippedFiles   int            `json:"skipped_files"`
	DroppedIntents int            `json:"dropped_intents"`
	ConfirmedEdges int            `json:"confirmed_edges"`
	DegradedUnits  []DegradedUnit `json:"degraded_units"`
}

// NewGraphFacts converts the store aggregate into the canonical map-free form:
// EdgesByKind sorted ascending by kind (zero/negative counts dropped),
// EdgesByTier folded into the three explicit tier fields.
func NewGraphFacts(s graphstore.TrustStats) GraphFacts {
	kinds := make([]KindCount, 0, len(s.EdgesByKind))
	for k, n := range s.EdgesByKind {
		if n > 0 {
			kinds = append(kinds, KindCount{Kind: k, Count: n})
		}
	}
	sort.Slice(kinds, func(a, b int) bool { return kinds[a].Kind < kinds[b].Kind })
	return GraphFacts{
		NodesTotal:  s.NodesTotal,
		EdgesTotal:  s.EdgesTotal,
		EdgesByKind: kinds,
		EdgesByTier: TierCounts{
			Confirmed: s.EdgesByTier[model.TierConfirmed],
			Derived:   s.EdgesByTier[model.TierDerived],
			Heuristic: s.EdgesByTier[model.TierHeuristic],
		},
	}
}

// NewExternalFacts converts the store aggregate's external surface. The
// boundary list is re-sorted here (incident count descending, then qualified
// name ascending — the TrustAggregatePort contract order) so determinism is by
// construction, not by trusting the producer.
func NewExternalFacts(s graphstore.TrustStats) ExternalFacts {
	bounds := make([]Boundary, 0, len(s.TopBoundaries))
	for _, b := range s.TopBoundaries {
		bounds = append(bounds, Boundary{QualifiedName: b.QualifiedName, IncidentEdges: b.IncidentEdges})
	}
	sort.Slice(bounds, func(a, b int) bool {
		if bounds[a].IncidentEdges != bounds[b].IncidentEdges {
			return bounds[a].IncidentEdges > bounds[b].IncidentEdges
		}
		return bounds[a].QualifiedName < bounds[b].QualifiedName
	})
	return ExternalFacts{Nodes: s.ExternalNodes, Edges: s.ExternalEdges, TopBoundaries: bounds}
}

// NewLinkFacts copies the linker's per-pass counters.
func NewLinkFacts(s link.Stats) LinkFacts {
	return LinkFacts{
		ResolvedDerived:   s.ResolvedDerived,
		ResolvedHeuristic: s.ResolvedHeuristic,
		ResolvedExternal:  s.ResolvedExternal,
		Skipped:           s.Skipped,
		Ambiguous:         s.Ambiguous,
	}
}

// NewParseFacts builds the parse-skip facts from one pass's skip diagnostics:
// paths is the full skipped-path list (one entry per skip, repo-relative) and
// byReason the per-reason tally. Skipped counts every skip; Paths keeps only
// relative paths, sorted, capped at MaxParsePaths and each bounded to
// MaxPathLength — an absolute path is dropped from the sample (never from the
// count) rather than leaked.
func NewParseFacts(paths []string, byReason map[string]int) ParseFacts {
	reasons := make([]ReasonCount, 0, len(byReason))
	for r, n := range byReason {
		if n > 0 {
			reasons = append(reasons, ReasonCount{Reason: r, Count: n})
		}
	}
	sort.Slice(reasons, func(a, b int) bool { return reasons[a].Reason < reasons[b].Reason })

	rel := make([]string, 0, len(paths))
	for _, p := range paths {
		if !path.IsAbs(p) {
			rel = append(rel, truncatePath(p))
		}
	}
	sort.Strings(rel)
	if len(rel) > MaxParsePaths {
		rel = rel[:MaxParsePaths]
	}
	return ParseFacts{Skipped: len(paths), ByReason: reasons, Paths: rel}
}

// NewTypeResolutionFacts folds one Resolve pass into the persisted facts:
// totals over Units, the never-fabricate counters, ConfirmedEdges =
// len(r.Edges), and a bounded DegradedUnits sample sorted by (Dir, Name) and
// capped at MaxDegradedUnits. UnitsDegraded counts every degraded unit even
// when the sample is truncated.
func NewTypeResolutionFacts(r typeresolve.Result) TypeResolutionFacts {
	f := TypeResolutionFacts{
		UnitsTotal:     len(r.Units),
		SkippedFiles:   len(r.SkippedFiles),
		DroppedIntents: r.DroppedIntents,
		ConfirmedEdges: len(r.Edges),
		DegradedUnits:  []DegradedUnit{},
	}
	for _, u := range r.Units {
		f.TypeErrors += u.TypeErrors
		if u.Degraded != "" {
			f.UnitsDegraded++
			f.DegradedUnits = append(f.DegradedUnits, DegradedUnit{
				// Dir and Name are repository-controlled too — same bound.
				Dir: truncatePath(u.Dir), Name: truncatePath(u.Name), Reason: u.Degraded, TypeErrors: u.TypeErrors,
			})
		}
	}
	sort.Slice(f.DegradedUnits, func(a, b int) bool {
		if f.DegradedUnits[a].Dir != f.DegradedUnits[b].Dir {
			return f.DegradedUnits[a].Dir < f.DegradedUnits[b].Dir
		}
		return f.DegradedUnits[a].Name < f.DegradedUnits[b].Name
	})
	if len(f.DegradedUnits) > MaxDegradedUnits {
		f.DegradedUnits = f.DegradedUnits[:MaxDegradedUnits]
	}
	return f
}
