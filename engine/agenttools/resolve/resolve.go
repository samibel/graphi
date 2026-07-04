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
		n, err := nodeForMatch(ctx, d, matches[0])
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Method: MethodSearch, Nodes: []model.Node{n}}, nil
	default:
		cands := make([]Candidate, 0, len(matches))
		for _, m := range matches {
			n, err := nodeForMatch(ctx, d, m)
			if err != nil {
				return Resolution{}, err
			}
			cands = append(cands, Candidate{Node: n, Rank: m.Rank})
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
	nodes := make([]model.Node, 0, len(matches))
	for _, m := range matches {
		n, err := nodeForMatch(ctx, d, m)
		if err != nil {
			return Resolution{}, err
		}
		nodes = append(nodes, n)
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
	reader := d.Query.Reader()

	if nodeIDPattern.MatchString(ref) {
		n, err := reader.GetNode(ctx, model.NodeId(ref))
		if err == nil {
			return Resolution{Method: MethodNodeID, Nodes: []model.Node{n}}, true, nil
		}
		if !errors.Is(err, graphstore.ErrNotFound) {
			return Resolution{}, true, err
		}
	}

	all, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return Resolution{}, true, err
	}

	norm := model.NormalizePath(ref)
	var inFile []model.Node
	for _, n := range all {
		if n.SourcePath() == norm {
			inFile = append(inFile, n)
		}
	}
	if len(inFile) > 0 {
		sortNodes(inFile)
		return Resolution{Method: MethodFile, Nodes: inFile}, true, nil
	}

	var byName []model.Node
	for _, n := range all {
		if n.QualifiedName() == ref {
			byName = append(byName, n)
		}
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
		// FTS syntax errors on odd inputs are a resolution miss, not an
		// infrastructure failure: exact forms were already tried.
		return nil, nil
	}
	return resp.Matches, nil
}

// nodeForMatch upgrades a search match to the full model node so downstream
// consumers work with one type. A match whose node vanished (referential
// drift) is rebuilt from the match fields.
func nodeForMatch(ctx context.Context, d Deps, m search.Match) (model.Node, error) {
	n, err := d.Query.Reader().GetNode(ctx, model.NodeId(m.NodeID))
	if err == nil {
		return n, nil
	}
	if errors.Is(err, graphstore.ErrNotFound) {
		return model.NewNode(m.Kind, m.QualifiedName, m.SourcePath, m.Line, m.Column)
	}
	return model.Node{}, err
}

func sortNodes(nodes []model.Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
}
