package diagnostic

import (
	"context"
	"fmt"

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

// analyzeUnresolvedRefs flags every edge the resolver could not confirm — i.e.
// edges carried at the heuristic confidence tier. These are the graph's
// representation of an unresolved/best-effort reference (a confirmed dangling
// edge cannot exist; see the package note). The diagnostic is anchored at the
// referrer (From) node. It carries no auto-action: resolving an unconfirmed
// reference needs agent judgment, so the finding is advisory. Findings carry
// ConfidenceHeuristic because they are produced from best-effort edges.
func analyzeUnresolvedRefs(ctx context.Context, r query.Reader) ([]Diagnostic, error) {
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("diagnostic: list edges: %w", err)
	}
	out := []Diagnostic{}
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
		out = append(out, Diagnostic{
			Severity:        SeverityWarning,
			Code:            "unresolved_reference",
			Reason:          ReasonUnresolvedExternalImport,
			Message:         fmt.Sprintf("%s edge from %q is unresolved (heuristic confidence only)", e.Kind(), from.QualifiedName()),
			Symbol:          from.ID(),
			TargetSymbol:    to.ID(),
			File:            from.SourcePath(),
			Line:            from.Line(),
			Column:          from.Column(),
			Actions:         []CodeAction{},
			Confidence:      ConfidenceHeuristic,
			OccurrenceCount: 1,
		})
	}
	return out, nil
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
		out = append(out, Diagnostic{
			Severity:        SeverityWarning,
			Code:            "dead_symbol",
			Reason:          ReasonDeadInternalSymbol,
			Message:         fmt.Sprintf("%s %q has no live inbound references", n.Kind(), n.QualifiedName()),
			Symbol:          n.ID(),
			TargetSymbol:    n.ID(),
			File:            n.SourcePath(),
			Line:            n.Line(),
			Column:          n.Column(),
			Actions: []CodeAction{{
				Kind:         ActionSafeDeleteSymbol,
				TargetSymbol: n.ID(),
			}},
			Confidence:      ConfidenceExact,
			OccurrenceCount: 1,
		})
	}
	return out, nil
}
