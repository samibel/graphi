package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// OpSearchAst is the canonical name of the AST structural-search query. Unlike
// the fixed symbol-input operations in Operations, search_ast is a PATTERN-input
// query: its input is a structural pattern, not a symbol id. So — exactly like
// the lexical `search` query — it is NOT routed through the symbol-based
// Dispatch and is NOT a member of Operations; the surfaces advertise it as a
// singleton alongside the fixed queries (the format=ast selector wiring lands in
// SW-085). Keeping it out of Operations preserves the invariant that every
// member of that list is dispatchable from a single NodeId.
const OpSearchAst = "search_ast"

// NameMatcher constrains a node's qualified name. At most ONE of Eq/Glob/Regex
// may be set; setting more than one is an InvalidPattern. An all-empty
// NameMatcher matches every name. Glob uses path.Match semantics ("*" matches a
// run of non-separator chars); Regex uses RE2 (regexp.MatchString).
type NameMatcher struct {
	Eq    string `json:"eq,omitempty"`
	Glob  string `json:"glob,omitempty"`
	Regex string `json:"regex,omitempty"`
}

// AstPattern is the closed, JSON-serialisable structural-search pattern. It is a
// deterministic value: the same pattern over the same index always selects the
// same nodes in the same canonical (NodeId-ascending) order, so a result can be
// persisted, replayed, and compared byte-identically across full and incremental
// indexes. The field set is CLOSED — ParseAstPattern rejects unknown fields as a
// typed InvalidPattern so a typo fails loudly rather than silently matching
// nothing.
type AstPattern struct {
	// Kind, when non-empty, restricts matches to nodes of this kind (e.g.
	// "function"). It is pushed down to the store's NodeKind filter.
	Kind string `json:"kind,omitempty"`
	// Name, when non-nil, constrains the node's qualified name.
	Name *NameMatcher `json:"name,omitempty"`
	// ParentKind, when non-empty, requires the node's immediate structural
	// parent (its inbound "defines" edge source) to be of this kind.
	ParentKind string `json:"parent_kind,omitempty"`
}

// InvalidPattern is the typed error returned when a search_ast pattern is
// malformed: an unknown field, an unparseable regex, or more than one name
// matcher. It always carries the offending FieldPath so the caller can point at
// the exact problem. It is never a panic and never a generic failure — surfaces
// render it as a typed error envelope (SW-085).
type InvalidPattern struct {
	FieldPath string
	Reason    string
}

func (e *InvalidPattern) Error() string {
	if e.FieldPath == "" {
		return fmt.Sprintf("query: invalid search_ast pattern: %s", e.Reason)
	}
	return fmt.Sprintf("query: invalid search_ast pattern at %q: %s", e.FieldPath, e.Reason)
}

// ParseAstPattern decodes and validates a raw JSON pattern. Unknown fields,
// conflicting name matchers, and unparseable regexes all return a typed
// *InvalidPattern carrying the offending field path. A valid pattern round-trips
// deterministically.
func ParseAstPattern(raw []byte) (AstPattern, error) {
	var p AstPattern
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return AstPattern{}, &InvalidPattern{FieldPath: unknownFieldPath(err), Reason: err.Error()}
	}
	if err := p.validate(); err != nil {
		return AstPattern{}, err
	}
	return p, nil
}

// validate enforces the cross-field rules ParseAstPattern cannot express in the
// struct tags alone. It is also called by SearchAst so a hand-built AstPattern
// (not parsed from JSON) is held to the same contract.
func (p AstPattern) validate() error {
	if p.Name != nil {
		set := 0
		if p.Name.Eq != "" {
			set++
		}
		if p.Name.Glob != "" {
			set++
		}
		if p.Name.Regex != "" {
			set++
		}
		if set > 1 {
			return &InvalidPattern{FieldPath: "name", Reason: "at most one of eq/glob/regex may be set"}
		}
		if p.Name.Regex != "" {
			if _, err := regexp.Compile(p.Name.Regex); err != nil {
				return &InvalidPattern{FieldPath: "name.regex", Reason: err.Error()}
			}
		}
	}
	return nil
}

// unknownFieldPath extracts the offending field name from a DisallowUnknownFields
// decode error (`json: unknown field "x"`). It returns "" when the error is not
// an unknown-field error (e.g. a type mismatch), in which case the full decode
// message is still surfaced as the Reason.
func unknownFieldPath(err error) string {
	const marker = "unknown field "
	msg := err.Error()
	if i := strings.Index(msg, marker); i >= 0 {
		return strings.Trim(msg[i+len(marker):], "\"")
	}
	return ""
}

// SearchAst returns every AST node matching the structural pattern, with its
// immediate structural parent attached — and NEVER a file body or line-window
// blob (the result carries only node identity + parent context). It reuses the
// existing AST node table (no new parser, no new ingest) and the store's
// canonical NodeId ordering, so two runs over the same index are byte-identical
// and a full index and a caught-up incremental index produce identical bytes.
//
// limit, when > 0, truncates to the first `limit` matches in canonical order
// (applied AFTER the canonical sort, so truncation is itself deterministic). An
// empty result is the typed Empty outcome, not a nil slice. Read-only.
func (s *Service) SearchAst(ctx context.Context, pattern AstPattern, limit int) (Result, error) {
	if err := pattern.validate(); err != nil {
		return Result{}, err
	}

	var nameRe *regexp.Regexp
	if pattern.Name != nil && pattern.Name.Regex != "" {
		// validate() already proved this compiles; compile once for matching.
		nameRe = regexp.MustCompile(pattern.Name.Regex)
	}

	// Push the kind filter down to the store; it returns canonical NodeId order.
	nodes, err := s.reader.Nodes(ctx, graphstore.Query{NodeKind: pattern.Kind})
	if err != nil {
		return Result{}, err
	}

	parentOf, err := s.definesParentMap(ctx)
	if err != nil {
		return Result{}, err
	}

	var matched []ResultNode
	for _, n := range nodes {
		if !matchName(pattern.Name, nameRe, n.QualifiedName()) {
			continue
		}
		rn := nodeToResult(n)
		if pid, ok := parentOf[n.ID()]; ok {
			parent, err := s.reader.GetNode(ctx, pid)
			switch {
			case err == nil:
				rn.ParentKind = parent.Kind()
				rn.ParentName = parent.QualifiedName()
			case errors.Is(err, graphstore.ErrNotFound):
				// Referential drift: parent edge outlived the parent node. Leave
				// parent fields empty (typed-empty) rather than fabricate them.
			default:
				return Result{}, err
			}
		}
		if pattern.ParentKind != "" && rn.ParentKind != pattern.ParentKind {
			continue
		}
		matched = append(matched, rn)
	}

	sortNodes(matched)
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	if matched == nil {
		matched = []ResultNode{}
	}

	outcome := OutcomeFound
	if len(matched) == 0 {
		outcome = OutcomeEmpty
	}
	// search_ast is a pattern query, so there is no queried Symbol. Edges are
	// never part of a structural-search result (matches are nodes, parent context
	// is folded into ParentKind/ParentName).
	return Result{
		Operation: OpSearchAst,
		Symbol:    model.NodeId(""),
		Outcome:   outcome,
		Nodes:     matched,
		Edges:     []ResultEdge{},
	}, nil
}

// definesParentMap builds the child→parent map from the "defines" edges in one
// pass. Edges are returned in canonical EdgeId order, so when a node has more
// than one inbound defines edge the first (canonically smallest) wins
// deterministically.
func (s *Service) definesParentMap(ctx context.Context) (map[model.NodeId]model.NodeId, error) {
	edges, err := s.reader.Edges(ctx, graphstore.Query{EdgeKind: EdgeKindDefines})
	if err != nil {
		return nil, err
	}
	m := make(map[model.NodeId]model.NodeId, len(edges))
	for _, e := range edges {
		if _, exists := m[e.To()]; !exists {
			m[e.To()] = e.From()
		}
	}
	return m, nil
}

// matchName applies the (already-validated) name constraint. A nil matcher or an
// all-empty matcher matches everything. The compiled regexp is passed in so it
// is built once per query, not once per node.
func matchName(m *NameMatcher, re *regexp.Regexp, name string) bool {
	if m == nil {
		return true
	}
	switch {
	case m.Eq != "":
		return name == m.Eq
	case m.Glob != "":
		ok, err := path.Match(m.Glob, name)
		return err == nil && ok
	case re != nil:
		return re.MatchString(name)
	default:
		return true
	}
}
