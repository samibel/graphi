package diagnostic

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// Analyzer kind identifiers. These are the stable names callers pass to select
// which analyzers run; an unknown name is recorded as Unavailable rather than
// erroring.
//
// Note on "unresolved reference": graphi's graph store enforces referential
// integrity — PutEdge rejects unknown endpoints and DeleteNode cascades — so a
// literally dangling edge cannot exist. An *unresolved* reference is instead
// represented as a best-effort edge the resolver could not confirm, carried at
// the heuristic confidence tier. KindUnresolvedRef keys on exactly that.
const (
	KindUnresolvedRef = "unresolved_reference"
	KindDeadSymbol    = "dead_symbol"
)

// allKinds is the canonical, ordered set of built-in analyzer kinds, used when a
// caller requests "all" (empty selection).
var allKinds = []string{KindUnresolvedRef, KindDeadSymbol}

// knownKinds is the membership set for selection resolution.
var knownKinds = map[string]bool{KindUnresolvedRef: true, KindDeadSymbol: true}

// referencingEdgeKinds are the edge kinds that count as a live inbound reference
// for dead-symbol analysis. A node reachable by any of these is not dead.
var referencingEdgeKinds = map[string]bool{
	query.EdgeKindCalls:      true,
	query.EdgeKindReferences: true,
	query.EdgeKindImplements: true,
	query.EdgeKindInherits:   true,
	query.EdgeKindOverrides:  true,
}

// deadCandidateKinds restricts dead-symbol reporting to node kinds where "no
// inbound reference" is a meaningful signal, avoiding noise from package/file
// container nodes.
var deadCandidateKinds = map[string]bool{
	"function": true,
	"method":   true,
	"type":     true,
	"class":    true,
}

// resolveKinds maps the caller's selection onto known analyzers, returning the
// ordered list to run (always in canonical analyzer order, independent of the
// caller's order so output is deterministic) and the canonically-ordered list of
// unknown (unavailable) kinds. An empty selection means "all built-ins".
func resolveKinds(kinds []string) (run []string, unavailable []string) {
	if len(kinds) == 0 {
		return append([]string(nil), allKinds...), []string{}
	}
	want := map[string]bool{}
	unavailable = []string{}
	for _, k := range kinds {
		if knownKinds[k] {
			want[k] = true
		} else {
			unavailable = append(unavailable, k)
		}
	}
	for _, k := range allKinds {
		if want[k] {
			run = append(run, k)
		}
	}
	sortStrings(unavailable)
	return run, unavailable
}

// analyzeUnresolvedRefs reports unresolved/best-effort references — edges carried
// at the heuristic confidence tier (a confirmed dangling edge cannot exist; see
// the package note). It aggregates BY TARGET: on a monorepo, a single external
// symbol may be referenced by thousands of heuristic edges, and emitting one
// finding per edge buries the signal (the WP-12 de-noise). Instead it emits ONE
// diagnostic per unresolved target node, with OccurrenceCount = the number of
// referencing edges into that target and Evidence = the merged (deduped, sorted,
// bounded) evidence of those edges. The representative Symbol/File/Line are taken
// from one DETERMINISTIC source edge (the referrer with the smallest NodeId), and
// TargetSymbol is the shared target. Output is sorted by target (QualifiedName,
// then NodeId) so the result is byte-stable. Findings carry ConfidenceHeuristic.
func analyzeUnresolvedRefs(ctx context.Context, r query.Reader) ([]Diagnostic, error) {
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("diagnostic: list edges: %w", err)
	}

	// Group heuristic edges by target node. For each target track the referring
	// edges so we can pick a deterministic representative and merge evidence.
	type group struct {
		target   model.Node
		count    int
		evidence []string
		// repFrom is the referrer (From) node of the representative source edge:
		// the one with the lexicographically smallest From NodeId (ties broken by
		// edge kind then edge id), so Symbol/File/Line are deterministic.
		repFrom   model.Node
		repFromID model.NodeId
		repKind   string
		repEdgeID model.EdgeId
		hasRep    bool
	}
	groups := map[model.NodeId]*group{}
	for _, e := range edges {
		if e.Tier() != model.TierHeuristic {
			continue
		}
		from, ferr := r.GetNode(ctx, e.From())
		if ferr != nil {
			continue
		}
		to, terr := r.GetNode(ctx, e.To())
		if terr != nil {
			continue
		}
		g := groups[to.ID()]
		if g == nil {
			g = &group{target: to}
			groups[to.ID()] = g
		}
		g.count++
		g.evidence = append(g.evidence, e.Evidence()...)
		// Deterministic representative: smallest (From id, kind, edge id).
		cand := repLess(e.From(), e.Kind(), e.ID(), g.repFromID, g.repKind, g.repEdgeID)
		if !g.hasRep || cand {
			g.hasRep = true
			g.repFrom = from
			g.repFromID = e.From()
			g.repKind = e.Kind()
			g.repEdgeID = e.ID()
		}
	}

	out := make([]Diagnostic, 0, len(groups))
	for _, g := range groups {
		out = append(out, Diagnostic{
			Severity:        SeverityWarning,
			Code:            "unresolved_reference",
			Reason:          ReasonUnresolvedExternalImport,
			Message:         fmt.Sprintf("%d unresolved references to %q (heuristic confidence only)", g.count, g.target.QualifiedName()),
			Symbol:          g.repFrom.ID(),
			TargetSymbol:    g.target.ID(),
			File:            g.repFrom.SourcePath(),
			Line:            g.repFrom.Line(),
			Column:          g.repFrom.Column(),
			Actions:         []CodeAction{},
			Confidence:      ConfidenceHeuristic,
			Evidence:        graphstore.CompactEvidence(g.evidence),
			OccurrenceCount: g.count,
		})
	}
	// Deterministic order: by target qualified name, then target NodeId.
	sort.Slice(out, func(i, j int) bool {
		ti, tj := groups[out[i].TargetSymbol].target, groups[out[j].TargetSymbol].target
		if ti.QualifiedName() != tj.QualifiedName() {
			return ti.QualifiedName() < tj.QualifiedName()
		}
		return out[i].TargetSymbol < out[j].TargetSymbol
	})
	return out, nil
}

// repLess reports whether candidate source edge (fromID, kind, edgeID) sorts
// before the current representative (curFrom, curKind, curEdge) under the total
// order (From NodeId, edge kind, edge id). Used to pick a stable representative.
func repLess(fromID model.NodeId, kind string, edgeID model.EdgeId, curFrom model.NodeId, curKind string, curEdge model.EdgeId) bool {
	if fromID != curFrom {
		return fromID < curFrom
	}
	if kind != curKind {
		return kind < curKind
	}
	return edgeID < curEdge
}

// analyzeDeadSymbols flags referenceable symbols (functions, methods, types,
// classes) with zero live inbound references. It is a warning, not an error:
// entrypoints legitimately have no inbound edges, so the finding carries a
// safe_delete_symbol code-action for the caller to gate through SW-093 rather
// than implying the symbol is definitely removable. Dead-symbol analysis is
// based on the presence of live inbound references, which are derived or
// confirmed edges, so findings carry ConfidenceExact.
func analyzeDeadSymbols(ctx context.Context, r query.Reader) ([]Diagnostic, error) {
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("diagnostic: list nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("diagnostic: list edges: %w", err)
	}
	inbound := map[model.NodeId]bool{}
	for _, e := range edges {
		if referencingEdgeKinds[e.Kind()] {
			inbound[e.To()] = true
		}
	}
	out := []Diagnostic{}
	for _, n := range nodes {
		if !deadCandidateKinds[n.Kind()] || inbound[n.ID()] {
			continue
		}
		// Entry-point de-noise (WP-11): a framework/language entry point (annotated
		// @Test/@Bean/…, a main, or a test-path symbol) legitimately has no in-graph
		// inbound reference. Rather than a false dead_symbol WARNING, downgrade it to
		// an INFO-severity entrypoint_candidate and attach NO delete action — the
		// symbol is live, so deleting it would break the build.
		if IsEntryPoint(n) {
			out = append(out, Diagnostic{
				Severity:        SeverityInfo,
				Code:            "dead_symbol",
				Reason:          ReasonEntrypointCandidate,
				Message:         fmt.Sprintf("%s %q has no live inbound references but looks like an entry point", n.Kind(), n.QualifiedName()),
				Symbol:          n.ID(),
				TargetSymbol:    n.ID(),
				File:            n.SourcePath(),
				Line:            n.Line(),
				Column:          n.Column(),
				Actions:         []CodeAction{},
				Confidence:      ConfidenceExact,
				Evidence:        []string{fmt.Sprintf("%s:%d", n.SourcePath(), n.Line())},
				OccurrenceCount: 1,
			})
			continue
		}
		out = append(out, Diagnostic{
			Severity:     SeverityWarning,
			Code:         "dead_symbol",
			Reason:       ReasonDeadInternalSymbol,
			Message:      fmt.Sprintf("%s %q has no live inbound references", n.Kind(), n.QualifiedName()),
			Symbol:       n.ID(),
			TargetSymbol: n.ID(),
			File:         n.SourcePath(),
			Line:         n.Line(),
			Column:       n.Column(),
			Actions: []CodeAction{{
				Kind:         ActionSafeDeleteSymbol,
				TargetSymbol: n.ID(),
			}},
			Confidence:      ConfidenceExact,
			Evidence:        []string{fmt.Sprintf("%s:%d", n.SourcePath(), n.Line())},
			OccurrenceCount: 1,
		})
	}
	return out, nil
}
