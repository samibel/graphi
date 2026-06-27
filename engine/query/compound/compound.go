// Package compound implements graphi's compound / Cypher-style graph-query
// capability (epic EP-011, gap G1).
//
// It composes ordered traversal steps (edge-kind filters + direction), a WHERE
// predicate over node attributes, and deterministic projection into a SINGLE
// request, collapsing flows that previously required several fixed-query
// round-trips (callers/callees/references/definition/neighborhood).
//
// Layering: compound is an engine package. It builds OVER the existing read-only
// engine/query.Service contract (the query.Reader) and reuses query.Result plus
// the canonical SortNodes/SortEdges comparators, so compound and fixed queries
// are byte-for-byte consumable by the same surfaces. It is CGo-free and performs
// zero network I/O (read-only local traversal). Output is deterministic by
// construction: the result is materialized-then-sorted by the canonical
// comparators and never depends on map-iteration order.
package compound

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// MaxCompoundDepth is the documented upper bound on the total number of traversal
// hops a compound query may expand. A query whose step count exceeds it is CLAMPED
// (never rejected); the effective (post-clamp) depth is reported in Result.Depth.
// The bound guarantees termination and keeps result size / token cost predictable.
const MaxCompoundDepth = 5

// Direction is the closed vocabulary for a traversal step's orientation relative
// to the current frontier node. The zero value is invalid and rejected at parse /
// validate time so an under-specified step is unrepresentable.
type Direction string

const (
	// DirOutbound — follow edges whose From == frontier node (e.g. callees).
	DirOutbound Direction = "outbound"
	// DirInbound — follow edges whose To == frontier node (e.g. callers).
	DirInbound Direction = "inbound"
	// DirBoth — follow edges incident in either direction (undirected hop).
	DirBoth Direction = "both"
)

// Query is the compound graph-query AST. A Query is a seed symbol plus an ordered
// list of traversal Steps, an optional Where predicate, and an optional MaxDepth
// override (clamped to MaxCompoundDepth). It is the structured API form; Parse
// produces the same value from text.
type Query struct {
	Seed     model.NodeId `json:"seed"`
	Steps    []Step       `json:"steps"`
	Where    *Where       `json:"where,omitempty"`
	MaxDepth int          `json:"max_depth,omitempty"`
}

// Step is one ordered traversal layer: one BFS hop over edges matching Direction
// and (when non-empty) the Kinds allowlist. An empty Kinds slice means "any edge
// kind".
type Step struct {
	Direction Direction `json:"direction"`
	Kinds     []string  `json:"kinds,omitempty"`
}

// Where is an optional predicate applied to the traversed node set after the
// final hop. An empty Where matches everything. Fields are ANDed.
type Where struct {
	// NodeKind, when non-empty, keeps only nodes of this kind.
	NodeKind string `json:"node_kind,omitempty"`
}

// ErrInvalidQuery is the typed sentinel returned by Validate/Parse for any
// malformed compound query. Callers distinguish "bad query" (this error) from an
// unresolved symbol (a query.Result with OutcomeNotFound, never an error) and
// from real infrastructure failures.
var ErrInvalidQuery = errors.New("compound: invalid query")

// Validate checks the AST for structural well-formedness. It returns a wrapped
// ErrInvalidQuery describing the first defect, or nil. Validate never panics.
func Validate(q Query) error {
	if strings.TrimSpace(string(q.Seed)) == "" {
		return fmt.Errorf("%w: seed must be non-empty", ErrInvalidQuery)
	}
	if len(q.Steps) == 0 {
		return fmt.Errorf("%w: at least one step is required", ErrInvalidQuery)
	}
	for i, s := range q.Steps {
		switch s.Direction {
		case DirOutbound, DirInbound, DirBoth:
		default:
			return fmt.Errorf("%w: step %d has invalid direction %q (want inbound|outbound|both)", ErrInvalidQuery, i, s.Direction)
		}
		for j, k := range s.Kinds {
			if strings.TrimSpace(k) == "" {
				return fmt.Errorf("%w: step %d kind %d is empty", ErrInvalidQuery, i, j)
			}
		}
	}
	return nil
}

// Execute runs a validated compound query over the read-only Reader and returns
// a query.Result using the SAME canonical comparators as the fixed operations,
// so compound and fixed-query results are byte-for-byte comparable.
//
// An unresolved seed yields an explicit not-found Result (OutcomeNotFound), never
// an error — matching the fixed operations' semantics. A malformed query yields
// a wrapped ErrInvalidQuery. Real infrastructure failures are returned as errors.
func Execute(ctx context.Context, r query.Reader, q Query) (query.Result, error) {
	const op = "compound"
	if err := Validate(q); err != nil {
		return query.Result{}, err
	}

	seed, err := r.GetNode(ctx, q.Seed)
	if err != nil {
		if errors.Is(err, graphstore.ErrNotFound) {
			return query.Result{Operation: op, Symbol: q.Seed, Outcome: query.OutcomeNotFound, Nodes: []query.ResultNode{}, Edges: []query.ResultEdge{}}, nil
		}
		return query.Result{}, err
	}

	maxDepth := q.MaxDepth
	if maxDepth <= 0 || maxDepth > MaxCompoundDepth {
		maxDepth = MaxCompoundDepth
	}

	// Load all edges once; traversal is backend-agnostic and read-only, mirroring
	// the Neighborhood operation. Reading the full set keeps compound decoupled
	// from any backend-specific adjacency index.
	allEdges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return query.Result{}, err
	}

	visited := map[model.NodeId]query.ResultNode{seed.ID(): nodeToResult(seed)}
	collected := map[model.EdgeId]query.ResultEdge{}
	expanded := map[model.NodeId]struct{}{}

	frontier := []model.NodeId{seed.ID()}

	requested := len(q.Steps)
	effective := requested
	if effective > maxDepth {
		effective = maxDepth // documented clamp
	}

	for i := 0; i < effective && len(frontier) > 0; i++ {
		step := q.Steps[i]
		kinds := toSet(step.Kinds) // empty == any
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			for _, e := range allEdges {
				if len(kinds) > 0 {
					if _, ok := kinds[e.Kind()]; !ok {
						continue
					}
				}
				other, ok := stepEndpoint(e, cur, step.Direction)
				if !ok {
					continue
				}
				collected[e.ID()] = edgeToResult(e)
				if _, seen := visited[other]; !seen {
					n, gerr := r.GetNode(ctx, other)
					if gerr != nil {
						if errors.Is(gerr, graphstore.ErrNotFound) {
							continue
						}
						return query.Result{}, gerr
					}
					visited[other] = nodeToResult(n)
					next = append(next, other)
				}
			}
		}
		frontier = next
	}

	// Apply the WHERE predicate to the traversed node set, then drop edges whose
	// endpoints did not survive the filter (keeps the subgraph consistent).
	survived := applyWhere(visited, q.Where)
	nodes := make([]query.ResultNode, 0, len(survived))
	for _, n := range survived {
		nodes = append(nodes, n)
	}
	edges := make([]query.ResultEdge, 0, len(collected))
	for _, e := range collected {
		if _, keepFrom := survived[e.From]; !keepFrom {
			continue
		}
		if _, keepTo := survived[e.To]; !keepTo {
			continue
		}
		edges = append(edges, e)
	}

	query.SortNodes(nodes)
	query.SortEdges(edges)

	eff := effective
	outcome := query.OutcomeFound
	if len(nodes) == 0 && len(edges) == 0 {
		outcome = query.OutcomeEmpty
	}
	return query.Result{
		Operation: op,
		Symbol:    q.Seed,
		Outcome:   outcome,
		Depth:     &eff,
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}

// stepEndpoint returns the opposite endpoint of e relative to cur for the given
// direction, or (_, false) when e is not incident to cur in that direction.
func stepEndpoint(e model.Edge, cur model.NodeId, dir Direction) (model.NodeId, bool) {
	switch dir {
	case DirOutbound:
		if e.From() == cur {
			return e.To(), true
		}
	case DirInbound:
		if e.To() == cur {
			return e.From(), true
		}
	case DirBoth:
		if e.From() == cur {
			return e.To(), true
		}
		if e.To() == cur {
			return e.From(), true
		}
	}
	return "", false
}

func applyWhere(nodes map[model.NodeId]query.ResultNode, w *Where) map[model.NodeId]query.ResultNode {
	if w == nil || strings.TrimSpace(w.NodeKind) == "" {
		return nodes
	}
	out := make(map[model.NodeId]query.ResultNode, len(nodes))
	for id, n := range nodes {
		if n.Kind == w.NodeKind {
			out[id] = n
		}
	}
	return out
}

func toSet(xs []string) map[string]struct{} {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		m[x] = struct{}{}
	}
	return m
}

func nodeToResult(n model.Node) query.ResultNode {
	return query.ResultNode{
		ID:            n.ID(),
		Kind:          n.Kind(),
		QualifiedName: n.QualifiedName(),
		SourcePath:    n.SourcePath(),
		Line:          n.Line(),
		Column:        n.Column(),
	}
}

func edgeToResult(e model.Edge) query.ResultEdge {
	return query.ResultEdge{
		ID:         e.ID(),
		From:       e.From(),
		To:         e.To(),
		Kind:       e.Kind(),
		Tier:       e.Tier(),
		Confidence: e.Confidence(),
		Reason:     e.Reason(),
		Evidence:   e.Evidence(),
	}
}

// ─── Text form (Cypher-style subset) ──────────────────────────────────────────
//
// Parse accepts a compact, line-oriented Cypher-STYLE query (inspired by Cypher,
// not full Cypher). Grammar (case-insensitive keywords):
//
//	SEED <nodeid>
//	HOP <in|out|both> [<kind>[,<kind>...]]
//	...
//	WHERE KIND <kind>
//
// At least one HOP is required. Blank lines and lines beginning with '#' are
// ignored. Unknown keywords, bad directions, and missing operands yield a wrapped
// ErrInvalidQuery (never a panic).
//
// Example:
//
//	SEED pkg.A
//	HOP out calls
//	HOP out references

// Parse parses the text form into a Query. It is the parser entry point; it
// rejects malformed input with typed ErrInvalidQuery and never panics.
func Parse(text string) (Query, error) {
	var (
		q       Query
		hops    int
		sawSeed bool
	)
	scanner := newLineScanner(text)
	for scanner.Next() {
		raw := scanner.Line()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// split keyword + rest on first run of whitespace
		idx := strings.IndexFunc(line, unicode.IsSpace)
		keyword := line
		rest := ""
		if idx >= 0 {
			keyword = line[:idx]
			rest = strings.TrimSpace(line[idx:])
		}
		switch strings.ToLower(keyword) {
		case "seed":
			if rest == "" {
				return Query{}, fmt.Errorf("%w: SEED requires a node id", ErrInvalidQuery)
			}
			q.Seed = model.NodeId(rest)
			sawSeed = true
		case "hop":
			st, err := parseHop(rest, hops)
			if err != nil {
				return Query{}, err
			}
			q.Steps = append(q.Steps, st)
			hops++
		case "where":
			w, err := parseWhere(rest)
			if err != nil {
				return Query{}, err
			}
			q.Where = w
		case "maxdepth":
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 0 {
				return Query{}, fmt.Errorf("%w: MAXDEPTH requires a non-negative integer", ErrInvalidQuery)
			}
			q.MaxDepth = n
		default:
			return Query{}, fmt.Errorf("%w: unknown keyword %q (want SEED|HOP|WHERE|MAXDEPTH)", ErrInvalidQuery, keyword)
		}
	}
	if !sawSeed {
		return Query{}, fmt.Errorf("%w: missing SEED", ErrInvalidQuery)
	}
	if err := Validate(q); err != nil {
		return Query{}, err
	}
	return q, nil
}

func parseHop(rest string, idx int) (Step, error) {
	if rest == "" {
		return Step{}, fmt.Errorf("%w: HOP %d requires a direction", ErrInvalidQuery, idx)
	}
	parts := splitFields(rest)
	dir, ok := parseDirection(parts[0])
	if !ok {
		return Step{}, fmt.Errorf("%w: HOP %d invalid direction %q (want in|out|both)", ErrInvalidQuery, idx, parts[0])
	}
	var kinds []string
	for _, k := range parts[1:] {
		kinds = append(kinds, strings.ToLower(k))
	}
	return Step{Direction: dir, Kinds: kinds}, nil
}

// parseDirection normalizes both the short (in|out|both) and long
// (inbound|outbound|both) spellings onto the canonical Direction constants.
func parseDirection(s string) (Direction, bool) {
	switch strings.ToLower(s) {
	case "in", "inbound":
		return DirInbound, true
	case "out", "outbound":
		return DirOutbound, true
	case "both":
		return DirBoth, true
	}
	return "", false
}

func parseWhere(rest string) (*Where, error) {
	if rest == "" {
		return nil, fmt.Errorf("%w: WHERE requires a predicate", ErrInvalidQuery)
	}
	parts := splitFields(rest)
	if len(parts) < 2 || strings.ToLower(parts[0]) != "kind" {
		return nil, fmt.Errorf("%w: WHERE predicate must be KIND <kind>", ErrInvalidQuery)
	}
	return &Where{NodeKind: parts[1]}, nil
}

// splitFields splits on runs of ASCII whitespace (spaces/tabs), dropping empties.
func splitFields(s string) []string {
	return strings.Fields(s)
}

// lineScanner is a minimal line iterator over the input text. It yields each
// line with any trailing CR stripped. It is allocation-light and avoids
// bufio.Scanner buffer limits.
type lineScanner struct {
	lines []string
	i     int
}

func newLineScanner(s string) *lineScanner {
	lines := strings.Split(s, "\n")
	return &lineScanner{lines: lines}
}

func (l *lineScanner) Next() bool {
	if l.i >= len(l.lines) {
		return false
	}
	l.i++
	return true
}

func (l *lineScanner) Line() string { return strings.TrimRight(l.lines[l.i-1], "\r") }

