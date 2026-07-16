// Package resolve is the shared target-resolution seam for the EP-020
// agent-first tools (explain_symbol, related_files, change_risk). It turns a
// free-form reference — node id, repo-relative path, qualified name, or search
// text — into concrete graph nodes, with an explicit ambiguous outcome instead
// of guessing. It is read-only by construction: it consumes only the query
// service's Reader and the lexical search service.
package resolve

import (
	"context"
	"errors"
	"regexp"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// Deps carries the engine services the agent tools consume. Both fields are
// read-only surfaces; a tool given a Deps with nil members must degrade to the
// contract's "unavailable" outcome rather than erroring.
type Deps struct {
	Query  *query.Service
	Search *search.Service
}

// Available reports whether the graph-backed toolchain can run at all.
func (d Deps) Available() bool { return d.Query != nil }

// Method is how a reference resolved. It is reported verbatim in tool
// summaries so agents know whether a result is exact or heuristic.
type Method string

const (
	MethodNodeID    Method = "node_id"
	MethodFile      Method = "file"
	MethodExactName Method = "exact_name"
	MethodSearch    Method = "search" // heuristic: lexical-search match
	MethodDiff      Method = "diff"
)

// Exact reports whether the method is an exact (non-heuristic) resolution.
func (m Method) Exact() bool { return m == MethodNodeID || m == MethodFile || m == MethodExactName }

// Candidate is one possible target for an ambiguous reference.
type Candidate struct {
	Node model.Node
	Rank float64 // FTS rank when it came from search (lower = better); 0 otherwise
}

// Resolution is the outcome of resolving a reference.
//
// Exactly one of the three states holds:
//   - resolved:   len(Nodes) > 0 and Candidates == nil
//   - ambiguous:  Candidates != nil (Nodes empty)
//   - not found:  both empty
type Resolution struct {
	Method     Method
	Nodes      []model.Node
	Candidates []Candidate
}

// Resolved reports whether the reference resolved to concrete nodes.
func (r Resolution) Resolved() bool { return len(r.Nodes) > 0 }

// Ambiguous reports whether the reference matched several distinct symbols.
func (r Resolution) Ambiguous() bool { return len(r.Candidates) > 0 }

var nodeIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Strict resolves ref for tools that must not guess (explain_symbol,
// change_risk). Order: node id → file path → exact qualified name → lexical
// search. A single search hit resolves heuristically; several distinct hits
// return candidates (ambiguous) instead of picking one.
func Strict(ctx context.Context, d Deps, ref string) (Resolution, error) {
	if r, done, err := resolveExact(ctx, d, ref); done || err != nil {
		return r, err
	}
	matches, err := searchMatches(ctx, d, ref, 10)
	if err != nil {
		return Resolution{}, err
	}
	switch len(matches) {
	case 0:
		return Resolution{}, nil
	case 1:
		nodes, err := nodesForMatches(ctx, d, matches)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Method: MethodSearch, Nodes: nodes}, nil
	default:
		nodes, err := nodesForMatches(ctx, d, matches)
		if err != nil {
			return Resolution{}, err
		}
		cands := make([]Candidate, 0, len(matches))
		for i, m := range matches {
			cands = append(cands, Candidate{Node: nodes[i], Rank: m.Rank})
		}
		return Resolution{Method: MethodSearch, Candidates: cands}, nil
	}
}

// Seeds resolves ref for ranking tools (related_files) where a free-text task
// query is legitimate input: instead of declaring ambiguity, the top-k distinct
// search hits all become seeds. Exact forms (node id, path, qualified name)
// still win outright.
func Seeds(ctx context.Context, d Deps, ref string, k int) (Resolution, error) {
	if r, done, err := resolveExact(ctx, d, ref); done || err != nil {
		return r, err
	}
	if k <= 0 {
		k = 5
	}
	matches, err := searchMatches(ctx, d, ref, k)
	if err != nil {
		return Resolution{}, err
	}
	if len(matches) == 0 {
		return Resolution{}, nil
	}
	nodes, err := nodesForMatches(ctx, d, matches)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{Method: MethodSearch, Nodes: nodes}, nil
}

// resolveExact runs the three exact resolution forms shared by Strict and
// Seeds. done=true means the caller should return r as-is (resolved or
// ambiguous-by-exact-name); done=false means fall through to lexical search.
func resolveExact(ctx context.Context, d Deps, ref string) (Resolution, bool, error) {
	if ref == "" {
		return Resolution{}, true, errors.New("empty reference")
	}
	if d.Query == nil {
		// Callers gate on Deps.Available(); this guard keeps a direct misuse
		// from dereferencing nil.
		return Resolution{}, true, errors.New("resolve: query service is nil")
	}
	// CORE-02 (ADR 0003 D7): the exact resolutions are selective index lookups
	// against the ports both shipped backends provide — never a full node scan.
	reader := d.Query.Reader()
	lookup, lok := reader.(graphstore.GraphLookup)
	symbols, sok := reader.(graphstore.SymbolLookupPort)
	if !lok || !sok {
		return Resolution{}, true, query.ErrSelectiveLookupUnavailable
	}

	if nodeIDPattern.MatchString(ref) {
		ns, err := lookup.NodesByID(ctx, []model.NodeId{model.NodeId(ref)})
		if err != nil {
			return Resolution{}, true, err
		}
		if len(ns) == 1 {
			return Resolution{Method: MethodNodeID, Nodes: ns}, true, nil
		}
		// Absent id: fall through to path/name resolution, as before.
	}

	inFile, err := symbols.SourcePath(ctx, model.NormalizePath(ref))
	if err != nil {
		return Resolution{}, true, err
	}
	if len(inFile) > 0 {
		sortNodes(inFile)
		return Resolution{Method: MethodFile, Nodes: inFile}, true, nil
	}

	byName, err := symbols.QualifiedName(ctx, ref)
	if err != nil {
		return Resolution{}, true, err
	}
	sortNodes(byName)
	switch len(byName) {
	case 0:
		return Resolution{}, false, nil
	case 1:
		return Resolution{Method: MethodExactName, Nodes: byName}, true, nil
	default:
		cands := make([]Candidate, 0, len(byName))
		for _, n := range byName {
			cands = append(cands, Candidate{Node: n})
		}
		return Resolution{Method: MethodExactName, Candidates: cands}, true, nil
	}
}

// searchMatches runs the lexical search, tolerating a missing search service
// (nil → no matches, no error: exact resolution already had its chance).
func searchMatches(ctx context.Context, d Deps, ref string, limit int) ([]search.Match, error) {
	if d.Search == nil {
		return nil, nil
	}
	resp, err := d.Search.Search(ctx, ref, limit)
	if err != nil {
		// SearchNodes already turns user text into a quoted, parameterized FTS
		// expression. Any remaining error is cancellation, closure, corruption,
		// or another infrastructure failure and must not become a false not-found.
		return nil, err
	}
	return resp.Matches, nil
}

// nodesForMatches upgrades search matches to full model nodes in one selective
// batch. Using Reader.GetNode here would force SQLite's legacy whole-graph
// cache to materialize for a free-text lookup. A match whose row vanished
// between search and hydration is rebuilt from the match fields, preserving
// the existing referential-drift behavior without hiding infrastructure errors.
func nodesForMatches(ctx context.Context, d Deps, matches []search.Match) ([]model.Node, error) {
	lookup, ok := d.Query.Reader().(graphstore.GraphLookup)
	if !ok {
		return nil, query.ErrSelectiveLookupUnavailable
	}
	ids := make([]model.NodeId, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, model.NodeId(m.NodeID))
	}
	hydrated, err := lookup.NodesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[model.NodeId]model.Node, len(hydrated))
	for _, n := range hydrated {
		byID[n.ID()] = n
	}
	out := make([]model.Node, 0, len(matches))
	for _, m := range matches {
		if n, found := byID[model.NodeId(m.NodeID)]; found {
			out = append(out, n)
			continue
		}
		n, err := model.NewNode(m.Kind, m.QualifiedName, m.SourcePath, m.Line, m.Column)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func sortNodes(nodes []model.Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
}
