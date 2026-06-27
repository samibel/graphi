package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// tierRank assigns a stable total order to the closed ConfidenceTier enum,
// consistent with query's edge comparator (see engine/query/compare.go), so the
// most trustworthy edges/nodes lead the result. Higher confidence sorts FIRST.
//
//	confirmed (0) < derived (1) < heuristic (2) < unknown (3)
func tierRank(t string) int {
	switch t {
	case "confirmed":
		return 0
	case "derived":
		return 1
	case "heuristic":
		return 2
	default:
		return 3
	}
}

// edgeBetter reports whether a ranks strictly before b by the canonical edge
// comparator: confidence tier (most confident first), then a stable
// lexicographic cascade over from, to, kind, and finally the content-addressed
// edge id (a unique total-order backstop). The ordering matches query.sortEdges
// so analysis and query agree on "best edge".
func edgeBetter(a, b query.ResultEdge) bool {
	if ra, rb := tierRank(string(a.Tier)), tierRank(string(b.Tier)); ra != rb {
		return ra < rb
	}
	if a.From != b.From {
		return a.From < b.From
	}
	if a.To != b.To {
		return a.To < b.To
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}

// sortReached sorts reached nodes by the canonical analysis comparator:
// reaching-edge tier rank first (most trustworthy provenance leads), then the
// content-addressed node id (a unique total-order backstop). This is the ONLY
// place analysis node ordering is decided; combined with Marshal it makes
// results byte-identical regardless of map-iteration or traversal order.
func sortReached(nodes []ReachedNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if ra, rb := tierRank(string(nodes[i].ReachedVia.Tier)), tierRank(string(nodes[j].ReachedVia.Tier)); ra != rb {
			return ra < rb
		}
		return nodes[i].Node.ID < nodes[j].Node.ID
	})
}

// sortPaths sorts call-chain (and similar) paths by the canonical path
// comparator: shortest first (fewest edges), then lexicographic over the
// content-addressed edge-id sequence. The edge-id cascade is a stable total
// order for equal-length paths, so identical inputs/graph state yield
// byte-identical path ordering regardless of traversal or map-iteration order.
func sortPaths(paths [][]query.ResultEdge) {
	sort.Slice(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		for k := 0; k < len(a); k++ {
			if a[k].ID != b[k].ID {
				return a[k].ID < b[k].ID
			}
		}
		return false // equal paths are stable
	})
}

// handlerKinds and handlerNamePatterns form the DOCUMENTED, deterministic,
// pure-string handler-classification heuristic (no embeddings — semantic search
// stays off per OQ6). A location is a handler if its node Kind is in the kind
// set OR its lowercased QualifiedName contains one of the name patterns. Both
// sets are deliberately conservative and extensible.
var (
	handlerKinds = map[string]struct{}{
		"handler":           {},
		"error_handler":     {},
		"route_handler":     {},
		"event_handler":     {},
		"exception_handler": {},
	}
	handlerNamePatterns = []string{
		"handler",
		"handle",
		"servehttp",
		"recover",
		"onerror",
		"onerr",
		"middleware",
	}
)

// isHandler reports whether n classifies as a handler by the documented
// heuristic. Pure, deterministic, no I/O.
func isHandler(n query.ResultNode) bool {
	if _, ok := handlerKinds[strings.ToLower(n.Kind)]; ok {
		return true
	}
	q := strings.ToLower(n.QualifiedName)
	for _, p := range handlerNamePatterns {
		if strings.Contains(q, p) {
			return true
		}
	}
	return false
}

// kindRank orders concept-resolution location kinds so definitions lead, then
// handlers, then ordinary references (AC: definitions > handlers > references).
func kindRank(kind string) int {
	switch kind {
	case KindDefinition:
		return 0
	case KindHandler:
		return 1
	default: // KindReference and any unknown -> last
		return 2
	}
}

// locTierRank is the effective provenance tier rank for a location. Definitions
// are found via lexical search (no graph edge); to make them rank strictly
// above even confirmed references they get rank -1 (one better than confirmed's
// 0). References/handlers use the reaching edge's tier rank.
func locTierRank(loc Location) int {
	if loc.ReachedVia == nil {
		return -1
	}
	return tierRank(string(loc.ReachedVia.Tier))
}

// sortLocations orders concept results by kind rank (definition > handler >
// reference), then by effective provenance tier rank (most trustworthy first),
// then by node id (unique total-order backstop). Deterministic.
func sortLocations(locs []Location) {
	sort.Slice(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if ta, tb := locTierRank(a), locTierRank(b); ta != tb {
			return ta < tb
		}
		return a.Node.ID < b.Node.ID
	})
}

// metricKindRank fixes a stable Kind ordering for metrics output (hub, then
// bridge, then centrality) so the serialized list is byte-stable regardless of
// insertion order.
func metricKindRank(kind string) int {
	switch kind {
	case "hub":
		return 0
	case "bridge":
		return 1
	case "centrality":
		return 2
	default:
		return 3
	}
}

// sortMetrics orders metric scores by Kind (hub<bridge<centrality), then score
// DESC (highest signal first), then node id ASC (deterministic tie-break).
func sortMetrics(scores []NodeScore) {
	sort.Slice(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		if ra, rb := metricKindRank(a.Kind), metricKindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Node.ID < b.Node.ID
	})
}

// sortCommunities enforces the SW-104 `communities` envelope total order at the
// encoder boundary: communities by deterministic id (then key as a backstop), and
// members within a community by node id. This is the ONLY place community output
// ordering is decided, so the serialized bytes are identical regardless of
// detector/map-discovery order or the path (full vs incremental) taken to reach
// the graph state.
func sortCommunities(r *CommunitiesReport) {
	if r == nil {
		return
	}
	sort.Slice(r.Communities, func(i, j int) bool {
		if r.Communities[i].ID != r.Communities[j].ID {
			return r.Communities[i].ID < r.Communities[j].ID
		}
		return r.Communities[i].Key < r.Communities[j].Key
	})
	for k := range r.Communities {
		sort.Strings(r.Communities[k].Members)
	}
}

// sortNotebookCells enforces the SW-104 `notebook-ingest` envelope total order:
// cells by source path, then cell index (nbformat array order), then symbol id,
// then line. Deterministic regardless of graph edge enumeration order.
func sortNotebookCells(r *NotebookReport) {
	if r == nil {
		return
	}
	sort.Slice(r.Cells, func(i, j int) bool {
		a, b := r.Cells[i], r.Cells[j]
		if a.SourcePath != b.SourcePath {
			return a.SourcePath < b.SourcePath
		}
		if a.CellIndex != b.CellIndex {
			return a.CellIndex < b.CellIndex
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		return a.Line < b.Line
	})
}

// sortWatchRoots enforces the SW-104 `watcher-status` envelope total order: roots
// by path. The report carries no wall-clock field, so ordering is the only
// determinism concern.
func sortWatchRoots(r *WatcherStatusReport) {
	if r == nil {
		return
	}
	sort.Slice(r.Roots, func(i, j int) bool { return r.Roots[i].Root < r.Roots[j].Root })
}

// nodeToResult copies the canonical node fields verbatim into the query result
// primitive (never re-derived). Mirrors the unexported converter in query so the
// analysis result carries the identical node shape surfaces already serialize.
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

// edgeToResult copies the canonical edge fields — including full provenance —
// verbatim into the query result primitive. Provenance is passed through, never
// re-derived or downgraded.
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

// notFound is the explicit, typed not-found result for an unresolved symbol. It
// is a normal Analysis value (Outcome = not_found), never an error.
func notFound(analyzer string, symbol model.NodeId) Analysis {
	return Analysis{
		Analyzer: analyzer,
		Outcome:  query.OutcomeNotFound,
		Symbol:   symbol,
		Nodes:    []ReachedNode{},
	}
}

// Marshal is the single canonical serializer for analysis results, shared by
// every surface (CLI, MCP, …). It re-sorts defensively so the output is
// canonical even if a caller hands in an unsorted Analysis, disables HTML
// escaping, and trims the trailing newline — byte-for-byte stable across runs
// and across surfaces (mirrors query.Marshal).
func Marshal(a Analysis) ([]byte, error) {
	// SW-039: the pr-risk scorer carries a versioned RiskReport. When present,
	// the canonical output IS that report (its own byte-stable serializer), so
	// MCP and CLI emit the identical risk-record shape through this one path.
	if a.RiskReport != nil {
		return MarshalRisk(*a.RiskReport)
	}
	// SW-040: the pr-signals detector carries a versioned SignalReport. When
	// present, the canonical output IS that report (its own byte-stable
	// serializer), so MCP and CLI emit the identical signal-record shape through
	// this one path.
	if a.SignalReport != nil {
		return MarshalSignals(*a.SignalReport)
	}
	// SW-041: the pr-questions generator carries a versioned QuestionReport. When
	// present, the canonical output IS that report (its own byte-stable
	// serializer), so MCP and CLI emit the identical question shape through this
	// one path.
	if a.QuestionReport != nil {
		return MarshalQuestions(*a.QuestionReport)
	}

	nodes := make([]ReachedNode, len(a.Nodes))
	copy(nodes, a.Nodes)
	sortReached(nodes)

	paths := make([][]query.ResultEdge, len(a.Paths))
	for i := range a.Paths {
		paths[i] = make([]query.ResultEdge, len(a.Paths[i]))
		copy(paths[i], a.Paths[i])
	}
	sortPaths(paths)

	locs := make([]Location, len(a.Locations))
	copy(locs, a.Locations)
	sortLocations(locs)

	metrics := make([]NodeScore, len(a.Metrics))
	copy(metrics, a.Metrics)
	sortMetrics(metrics)

	// SW-104: defensively enforce the four new envelopes' stable sort keys at the
	// encoder boundary (the analyzers already emit sorted output; this is the
	// single ordering authority, mirroring the sortReached/sortPaths discipline).
	// The reports are freshly built per dispatch, so in-place sorting is safe.
	sortCommunities(a.Communities)
	sortNotebookCells(a.Notebook)
	sortWatchRoots(a.WatcherStatus)

	out := Analysis{
		Analyzer:       a.Analyzer,
		Outcome:        a.Outcome,
		Symbol:         a.Symbol,
		Truncated:      a.Truncated,
		Nodes:          nodes,
		Paths:          paths,
		Metrics:        metrics,
		Locations:      locs,
		InterprocTaint: a.InterprocTaint,
		Communities:    a.Communities,
		Notebook:       a.Notebook,
		WatcherStatus:  a.WatcherStatus,
	}
	if out.Nodes == nil {
		out.Nodes = []ReachedNode{}
	}
	if out.Locations == nil && len(a.Locations) == 0 && a.Analyzer == "concept" {
		out.Locations = []Location{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
