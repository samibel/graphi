package analysis

import (
	"context"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// DefaultConceptLimit is the documented bound on FTS hits considered for concept
// resolution. It keeps the lexical-join cost predictable; the ranked output is
// bounded by this limit.
const DefaultConceptLimit = 100

// conceptAnalyzer answers "where is X handled": it joins EP-001's lexical search
// (FTS5 / in-memory substring) against the graph's reference/call edges and
// classifies each location as a definition, a handler, or an ordinary reference
// via a documented pure-string heuristic. It is the third analyzer on the SW-022
// registry. Scope is lexical+graph only — explicitly NOT semantic/embedding
// search (OQ6) and not flow-sensitive taint (EP-005).
type conceptAnalyzer struct {
	searcher Searcher // nil when the reader lacks SearchNodes (graceful: empty)
}

func (conceptAnalyzer) Name() string { return "concept" }

// Analyze resolves a concept term to a ranked set of classified locations. The
// matched symbols are definitions; nodes that reference/call them are references
// unless they match the handler heuristic (then handlers). Ranking is
// definition > handler > reference, tie-broken by provenance tier then node id.
func (a conceptAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "concept"

	if a.searcher == nil || p.Concept == "" {
		// No lexical capability or empty term -> explicit no-matches (not error).
		return Analysis{Analyzer: op, Outcome: query.OutcomeEmpty, Symbol: model.NodeId(p.Concept), Locations: []Location{}}, nil
	}

	limit := DefaultConceptLimit
	ranked, err := a.searcher.SearchNodes(ctx, p.Concept, limit)
	if err != nil {
		return Analysis{}, err
	}

	// Matched FTS nodes are definitions of the concept.
	matched := make(map[model.NodeId]struct{}, len(ranked))
	byID := make(map[model.NodeId]query.ResultNode, len(ranked))
	for _, rn := range ranked {
		n := nodeToResult(rn.Node)
		matched[n.ID] = struct{}{}
		byID[n.ID] = n
	}

	// Referrers: nodes with a references/calls edge INTO a matched node.
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Analysis{}, err
	}
	referrerEdge := make(map[model.NodeId]query.ResultEdge)
	for _, e := range edges {
		if e.Kind() != string(query.EdgeKindReferences) && e.Kind() != string(query.EdgeKindCalls) {
			continue
		}
		if _, ok := matched[e.To()]; !ok {
			continue
		}
		from := e.From()
		re := edgeToResult(e)
		// Keep the best-tier reaching edge per referrer (deterministic).
		if existing, ok := referrerEdge[from]; !ok || edgeBetter(re, existing) {
			referrerEdge[from] = re
		}
	}

	// Build locations, classifying referrers as handler-or-reference.
	locs := make([]Location, 0, len(matched)+len(referrerEdge))
	for id := range matched {
		// A matched node that is itself a handler still reports as a definition
		// (it IS the concept's definition); handlers are a property of REFERRERS.
		locs = append(locs, Location{Node: byID[id], Kind: KindDefinition, ReachedVia: nil})
	}
	for from, edge := range referrerEdge {
		// Avoid emitting a self-reference location for a matched node that also
		// refers to itself: the definition entry already represents it.
		if _, isMatched := matched[from]; isMatched {
			continue
		}
		node, err := r.GetNode(ctx, from)
		if err != nil {
			// Referential drift: referrer endpoint no longer exists; skip.
			continue
		}
		rn := nodeToResult(node)
		kind := KindReference
		if isHandler(rn) {
			kind = KindHandler
		}
		e := edge
		locs = append(locs, Location{Node: rn, Kind: kind, ReachedVia: &e})
	}

	// Dedup by node id keeping the highest-ranked kind (definition>handler>ref).
	// (A node could appear once as a matched definition and not as a referrer
	// due to the self-reference guard above, but dedup is a cheap safety net.)
	dedup := make(map[model.NodeId]Location, len(locs))
	for _, l := range locs {
		if cur, ok := dedup[l.Node.ID]; !ok || kindRank(l.Kind) < kindRank(cur.Kind) {
			dedup[l.Node.ID] = l
		}
	}
	out := make([]Location, 0, len(dedup))
	for _, l := range dedup {
		out = append(out, l)
	}
	sortLocations(out)

	outcome := query.OutcomeFound
	if len(out) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer:  op,
		Outcome:   outcome,
		Symbol:    model.NodeId(p.Concept),
		Locations: out,
	}, nil
}
