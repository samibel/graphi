// Package hybridsearch implements the search_hybrid agent tool (labs, P3
// repository search): multi-token queries ranked WITHOUT embeddings. It
// combines four deterministic signals — lexical retrieval, identifier
// similarity (camelCase/snake_case segment matching), path relevance, and
// graph proximity (bounded inbound degree) — into an integer composite score
// with the per-signal breakdown printed in every reason (the audited
// suggest_reviewers scoring discipline).
//
// "authentication token validation" ranks AuthFilter, TokenValidator and
// JwtProvider ahead of accidental substring hits — no vector database, no
// model, no egress. The optional semantic search (search_semantic) remains a
// separate, explicitly-configured opt-in; this tool never touches it.
package hybridsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const tool = "search_hybrid"

// MethodVersion stamps the ranking logic version into the summary.
const MethodVersion = "search_hybrid/1"

// Retrieval bounds.
const (
	maxTokens        = 6
	perTokenLimit    = 15
	candidateCap     = 60
	degreeReadCap    = 32
	expansionSeedCap = 20
	expansionEdgeCap = 16
	DefaultMaxItems  = 20
)

// hybridWeights is the fixed integer weight model, hashed into the summary.
type hybridWeights struct {
	SegmentExact  int `json:"segment_exact"`  // query token == identifier segment
	SegmentPrefix int `json:"segment_prefix"` // token (len>=3) is a segment prefix
	NameSubstring int `json:"name_substring"` // token appears anywhere in the name
	PathSegment   int `json:"path_segment"`   // token matches a path segment
	FullCoverage  int `json:"full_coverage"`  // every token matched somewhere
	DegreePoint   int `json:"degree_point"`   // per bounded inbound edge
}

var defaultWeights = hybridWeights{
	SegmentExact:  100,
	SegmentPrefix: 40,
	NameSubstring: 15,
	PathSegment:   30,
	FullCoverage:  50,
	DegreePoint:   2,
}

// WeightsHash is the auditable stamp of the active weight model.
func WeightsHash() string {
	b, _ := json.Marshal(defaultWeights)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// Params carries the search_hybrid inputs.
type Params struct {
	// Query is the free-text query ("authentication token validation").
	Query string
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
	Deps     resolve.Deps
}

// stopwords dropped from queries (mirrors taskctx's fallback set).
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "are": true, "was": true, "not": true, "you": true,
	"into": true, "from": true, "when": true, "then": true, "how": true,
	"where": true, "what": true, "does": true, "a": true, "an": true,
	"of": true, "in": true, "on": true, "to": true, "is": true,
}

// Search retrieves candidates per token and re-ranks them deterministically.
func Search(ctx context.Context, p Params) (*contract.Result, error) {
	if strings.TrimSpace(p.Query) == "" {
		return nil, fmt.Errorf("missing query text")
	}
	if !p.Deps.Available() || p.Deps.Search == nil {
		return shape.Unavailable(tool), nil
	}
	lookup, lok := p.Deps.Query.Reader().(graphstore.GraphLookup)
	bounded, bok := p.Deps.Query.Reader().(graphstore.BoundedGraphLookup)
	if !lok || !bok {
		return shape.Unavailable(tool), nil
	}

	tokens := tokenize(p.Query)
	if len(tokens) == 0 {
		return shape.Empty(tool, p.Query), nil
	}

	// Retrieval: the full query first (a phrase may hit directly), then each
	// token. The backend rank is used for RETRIEVAL ONLY — the ranking below
	// is this package's own deterministic scoring.
	seen := map[model.NodeId]struct{}{}
	var ids []model.NodeId
	collect := func(q string) error {
		resp, err := p.Deps.Search.Search(ctx, q, perTokenLimit)
		if err != nil {
			return err
		}
		for _, m := range resp.Matches {
			id := model.NodeId(m.NodeID)
			if _, dup := seen[id]; dup {
				continue
			}
			if len(ids) >= candidateCap {
				return nil
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return nil
	}
	if err := collect(p.Query); err != nil {
		return nil, err
	}
	for _, tok := range tokens {
		if err := collect(tok); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return shape.Empty(tool, p.Query), nil
	}

	// Graph expansion: one bounded hop around the retrieved candidates. This
	// is what makes the search HYBRID — the graph compensates for lexical
	// index limits (an FTS backend that finds `Greeter` but not
	// `SpanishGreeter` for the token "greeter" reaches the implementers
	// through their edges). Expansion candidates still have to earn a
	// token-signal score below, so this widens recall without admitting noise.
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	seedCount := len(ids)
	if seedCount > expansionSeedCap {
		seedCount = expansionSeedCap
	}
	for _, id := range ids[:seedCount] {
		if len(ids) >= candidateCap {
			break
		}
		in, _, err := bounded.IncomingBounded(ctx, id, expansionEdgeCap)
		if err != nil {
			return nil, err
		}
		out, _, err := bounded.OutgoingBounded(ctx, id, expansionEdgeCap)
		if err != nil {
			return nil, err
		}
		edges := append(in, out...)
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID() < edges[j].ID() })
		for _, e := range edges {
			for _, neighbor := range []model.NodeId{e.From(), e.To()} {
				if _, dup := seen[neighbor]; dup {
					continue
				}
				if len(ids) >= candidateCap {
					break
				}
				seen[neighbor] = struct{}{}
				ids = append(ids, neighbor)
			}
		}
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nodes, err := lookup.NodesByID(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Deterministic re-rank.
	type row struct {
		node                              model.Node
		score, name, path, degree, tokens int
	}
	truncated := false
	rows := make([]row, 0, len(nodes))
	for _, n := range nodes {
		if n.SourcePath() == "" {
			continue // external/package artifacts are not navigable results
		}
		segments := splitIdentifier(n.QualifiedName())
		pathSegs := splitPath(n.SourcePath())
		lowerName := strings.ToLower(n.QualifiedName())

		r := row{node: n}
		for _, tok := range tokens {
			matched := false
			switch {
			case containsString(segments, tok):
				r.name += defaultWeights.SegmentExact
				matched = true
			case len(tok) >= 3 && hasPrefixAny(segments, tok):
				r.name += defaultWeights.SegmentPrefix
				matched = true
			case strings.Contains(lowerName, tok):
				r.name += defaultWeights.NameSubstring
				matched = true
			}
			if containsString(pathSegs, tok) {
				r.path += defaultWeights.PathSegment
				matched = true
			}
			if matched {
				r.tokens++
			}
		}
		if r.tokens == 0 {
			continue // retrieval noise with no own-signal support
		}
		if r.tokens == len(tokens) {
			r.name += defaultWeights.FullCoverage
		}
		in, trunc, err := bounded.IncomingBounded(ctx, n.ID(), degreeReadCap)
		if err != nil {
			return nil, err
		}
		truncated = truncated || trunc
		r.degree = len(in) * defaultWeights.DegreePoint
		r.score = r.name + r.path + r.degree
		if r.score >= 1<<20 {
			r.score = 1<<20 - 1
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return shape.Empty(tool, p.Query), nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].node.ID() < rows[j].node.ID()
	})

	ev := shape.NewEvidenceSet()
	items := make([]contract.Item, 0, len(rows))
	for _, r := range rows {
		evID := ev.Add(r.node.SourcePath(), r.node.Line(), "match")
		items = append(items, contract.Item{
			RefID: string(r.node.ID()),
			Rank:  r.score,
			Reason: fmt.Sprintf("match: %s %s (%s:%d) score %d [tokens %d/%d, name %d, path %d, degree %d]",
				r.node.Kind(), r.node.QualifiedName(), r.node.SourcePath(), r.node.Line(),
				r.score, r.tokens, len(tokens), r.name, r.path, r.degree),
			EvidenceRefIDs: []string{evID},
		})
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("search_hybrid: %d result(s) for %q — top %s (score %d); signals: identifier segments, path, bounded degree (%s; weights %s)",
			len(rows), p.Query, rows[0].node.QualifiedName(), rows[0].score, MethodVersion, WeightsHash()),
		Items:    items,
		Evidence: ev.List(),
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"heuristic": 1},
			Top:          "heuristic",
			Method:       "hybrid_ranking",
		},
	}
	out, err := shape.Finish(r, p.MaxItems)
	if err != nil {
		return nil, err
	}
	if truncated {
		out.Limits.Truncated = true
	}
	return out, nil
}

// tokenize lowercases, strips punctuation, drops stopwords and short tokens.
func tokenize(q string) []string {
	fields := strings.Fields(strings.ToLower(q))
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}\"'`")
		if len(f) < 2 || stopwords[f] {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
		if len(out) >= maxTokens {
			break
		}
	}
	return out
}

// splitIdentifier splits a qualified name into lowercase word segments:
// dot/underscore/dash separators plus camelCase boundaries
// ("auth.TokenValidator" → [auth token validator]).
func splitIdentifier(qn string) []string {
	var segs []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(qn)
	for i, r := range runes {
		switch {
		case r == '.' || r == '_' || r == '-' || r == '/' || r == ':':
			flush()
		case unicode.IsUpper(r):
			// camelCase boundary: split before an upper that follows a lower,
			// or before the last upper of an acronym run (HTTPServer → http server).
			if i > 0 && (unicode.IsLower(runes[i-1]) ||
				(unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return segs
}

// splitPath splits a repo-relative path into lowercase segments including the
// extension-less basename parts.
func splitPath(p string) []string {
	var segs []string
	for _, seg := range strings.Split(strings.ToLower(p), "/") {
		if i := strings.LastIndex(seg, "."); i > 0 {
			seg = seg[:i]
		}
		for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return r == '_' || r == '-' }) {
			if part != "" {
				segs = append(segs, part)
			}
		}
	}
	return segs
}

func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

func hasPrefixAny(set []string, prefix string) bool {
	for _, v := range set {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}
