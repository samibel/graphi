package query

import "github.com/samibel/graphi/core/model"

// Outcome is the explicit resolution status of a structural query. It
// distinguishes the two non-error terminal states required by the story: a
// resolved symbol with zero matches (Empty) versus an unresolved/nonexistent
// symbol (NotFound). Neither is an error and neither is a partial guess; both are
// represented with identical markers across every surface.
type Outcome string

const (
	// OutcomeFound — the symbol resolved and at least one match was produced.
	OutcomeFound Outcome = "found"
	// OutcomeEmpty — the symbol resolved but the query produced zero matches
	// (e.g. a symbol with no callers). Distinct from NotFound.
	OutcomeEmpty Outcome = "empty"
	// OutcomeNotFound — the requested symbol id does not exist / could not be
	// resolved in the graph. No traversal is attempted.
	OutcomeNotFound Outcome = "not_found"
)

// ResultNode is a node appearing in a result. It carries the canonical node
// fields verbatim from core/model — never re-derived.
//
// ParentKind/ParentName carry the immediate structural parent (resolved from the
// node's inbound "defines" edge) and are populated ONLY by the search_ast
// pattern query. They are `omitempty`, so every other (symbol-input) query —
// which never sets them — serializes byte-identically to before this field
// existed: the SW-082 addition is invisible on the existing query paths.
type ResultNode struct {
	ID            model.NodeId `json:"id"`
	Kind          string       `json:"kind"`
	QualifiedName string       `json:"qualified_name"`
	SourcePath    string       `json:"source_path"`
	Line          int          `json:"line"`
	Column        int          `json:"column"`
	ParentKind    string       `json:"parent_kind,omitempty"`
	ParentName    string       `json:"parent_name,omitempty"`
}

// ResultEdge is an edge appearing in a result. Its provenance fields
// (confidence_tier, confidence, reason, evidence) are passed through VERBATIM
// from the model edge — the query service never re-derives or mutates them. The
// evidence slice preserves the model's canonical ordering.
type ResultEdge struct {
	ID         model.EdgeId         `json:"id"`
	From       model.NodeId         `json:"from"`
	To         model.NodeId         `json:"to"`
	Kind       string               `json:"kind"`
	Tier       model.ConfidenceTier `json:"confidence_tier"`
	Confidence float64              `json:"confidence"`
	Reason     string               `json:"reason"`
	Evidence   []string             `json:"evidence"`
}

// Result is the canonical, surface-agnostic answer to any structural query. It
// is the single result schema returned to every surface, carrying the queried
// symbol, the operation, the resolution outcome, and the matched nodes/edges
// with provenance attached. Nodes and Edges are always materialized-then-sorted
// by the canonical comparator before the Result is returned, so the value is
// deterministic regardless of map-iteration order.
type Result struct {
	Operation string       `json:"operation"`
	Symbol    model.NodeId `json:"symbol"`
	Outcome   Outcome      `json:"outcome"`
	// Depth is set only for neighborhood queries: the effective (post-clamp)
	// depth actually traversed. It is omitted for the direct lookups.
	Depth *int         `json:"depth,omitempty"`
	Nodes []ResultNode `json:"nodes"`
	Edges []ResultEdge `json:"edges"`
}

// Found reports whether the symbol resolved (outcome Found or Empty). A NotFound
// result returns false. This is the explicit, non-error "did the symbol exist?"
// signal callers use instead of inspecting an error.
func (r Result) Found() bool { return r.Outcome != OutcomeNotFound }

func nodeToResult(n model.Node) ResultNode {
	return ResultNode{
		ID:            n.ID(),
		Kind:          n.Kind(),
		QualifiedName: n.QualifiedName(),
		SourcePath:    n.SourcePath(),
		Line:          n.Line(),
		Column:        n.Column(),
	}
}

func edgeToResult(e model.Edge) ResultEdge {
	return ResultEdge{
		ID:         e.ID(),
		From:       e.From(),
		To:         e.To(),
		Kind:       e.Kind(),
		Tier:       e.Tier(),
		Confidence: e.Confidence(),
		Reason:     e.Reason(),
		Evidence:   e.Evidence(), // model returns a defensive, canonically-sorted copy
	}
}
