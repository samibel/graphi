package query_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// seedAst builds a deterministic store for the search_ast tests:
//
//	pkg.Outer (function) --defines--> pkg.handle_a (function)
//	pkg.Outer (function) --defines--> pkg.handle_b (function)
//	pkg.lonely (function)              (no parent)
//	pkg.Thing  (type)                  (different kind)
//
// Returns the store and a name→NodeId map.
func seedAst(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}

	type spec struct{ kind, name, path string }
	specs := []spec{
		{"function", "pkg.Outer", "pkg/outer.go"},
		{"function", "pkg.handle_a", "pkg/a.go"},
		{"function", "pkg.handle_b", "pkg/b.go"},
		{"function", "pkg.lonely", "pkg/l.go"},
		{"type", "pkg.Thing", "pkg/t.go"},
	}
	for _, s := range specs {
		n, err := model.NewNode(s.kind, s.name, s.path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[s.name] = n.ID()
	}
	mkDefines := func(parent, child string) {
		e, err := model.NewEdge(ids[parent], ids[child], query.EdgeKindDefines, model.TierConfirmed, 1, "defines", []string{"ev"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	mkDefines("pkg.Outer", "pkg.handle_a")
	mkDefines("pkg.Outer", "pkg.handle_b")
	return store, ids
}

func TestSearchAst_MatchesWithParentNoBody(t *testing.T) {
	store, _ := seedAst(t)
	svc := query.New(store)

	pat := query.AstPattern{
		Kind: "function",
		Name: &query.NameMatcher{Regex: `^pkg\.handle_`},
	}
	res, err := svc.SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatalf("SearchAst: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %q, want found", res.Outcome)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("matched %d nodes, want 2 (%+v)", len(res.Nodes), res.Nodes)
	}
	for _, n := range res.Nodes {
		if !strings.HasPrefix(n.QualifiedName, "pkg.handle_") {
			t.Errorf("unexpected match %q", n.QualifiedName)
		}
		if n.ParentKind != "function" || n.ParentName != "pkg.Outer" {
			t.Errorf("node %q parent = (%q,%q), want (function, pkg.Outer)", n.QualifiedName, n.ParentKind, n.ParentName)
		}
	}

	// Structural guarantee: the serialized envelope carries identity + parent
	// context only — never a file body / line-window blob.
	raw, err := query.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"\"body\"", "\"snippet\"", "\"source\"", "\"content\""} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("result envelope leaked %s: %s", banned, raw)
		}
	}
	if !strings.Contains(string(raw), "\"parent_name\":\"pkg.Outer\"") {
		t.Errorf("result envelope missing parent context: %s", raw)
	}
}

func TestSearchAst_DeterministicReplay(t *testing.T) {
	store, _ := seedAst(t)
	svc := query.New(store)
	pat := query.AstPattern{Kind: "function"}

	first, err := svc.SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := query.Marshal(first)
	b2, _ := query.Marshal(second)
	if string(b1) != string(b2) {
		t.Fatalf("replay not byte-identical:\n  %s\n  %s", b1, b2)
	}
}

func TestSearchAst_FullVsIncrementalParity(t *testing.T) {
	// Two independently-built stores with the SAME logical content stand in for a
	// full index and a caught-up incremental index. The canonical NodeId ordering
	// makes their search_ast bytes identical regardless of insertion order.
	full, _ := seedAst(t)
	incr, _ := seedAst(t)

	pat := query.AstPattern{Kind: "function", Name: &query.NameMatcher{Glob: "pkg.handle_*"}}
	rFull, err := query.New(full).SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatal(err)
	}
	rIncr, err := query.New(incr).SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatal(err)
	}
	bFull, _ := query.Marshal(rFull)
	bIncr, _ := query.Marshal(rIncr)
	if string(bFull) != string(bIncr) {
		t.Fatalf("full vs incremental not byte-identical:\n  full: %s\n  incr: %s", bFull, bIncr)
	}
}

func TestSearchAst_TypedEmpty(t *testing.T) {
	store, _ := seedAst(t)
	svc := query.New(store)

	res, err := svc.SearchAst(context.Background(), query.AstPattern{Name: &query.NameMatcher{Eq: "pkg.does_not_exist"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("outcome = %q, want empty", res.Outcome)
	}
	if res.Nodes == nil || len(res.Nodes) != 0 {
		t.Fatalf("nodes = %+v, want non-nil empty slice", res.Nodes)
	}
	raw, _ := query.Marshal(res)
	if !strings.Contains(string(raw), "\"outcome\":\"empty\"") {
		t.Errorf("typed-empty envelope wrong: %s", raw)
	}
	if !strings.Contains(string(raw), "\"nodes\":[]") {
		t.Errorf("empty nodes must serialize as [] not null: %s", raw)
	}
}

func TestSearchAst_ParentKindFilter(t *testing.T) {
	store, _ := seedAst(t)
	svc := query.New(store)

	// parent_kind=type excludes the handle_* functions (their parent is a function).
	res, err := svc.SearchAst(context.Background(), query.AstPattern{Kind: "function", ParentKind: "type"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("outcome = %q, want empty (no function has a type parent)", res.Outcome)
	}
}

func TestParseAstPattern_UnknownFieldTyped(t *testing.T) {
	_, err := query.ParseAstPattern([]byte(`{"kind":"function","callee":"foo"}`))
	if err == nil {
		t.Fatal("expected InvalidPattern, got nil")
	}
	var ip *query.InvalidPattern
	if !errors.As(err, &ip) {
		t.Fatalf("error type = %T, want *query.InvalidPattern", err)
	}
	if ip.FieldPath != "callee" {
		t.Errorf("FieldPath = %q, want %q", ip.FieldPath, "callee")
	}
}

func TestParseAstPattern_ConflictingNameMatchers(t *testing.T) {
	_, err := query.ParseAstPattern([]byte(`{"name":{"eq":"a","glob":"b*"}}`))
	var ip *query.InvalidPattern
	if !errors.As(err, &ip) {
		t.Fatalf("error type = %T, want *query.InvalidPattern", err)
	}
	if ip.FieldPath != "name" {
		t.Errorf("FieldPath = %q, want name", ip.FieldPath)
	}
}

func TestParseAstPattern_BadRegexTyped(t *testing.T) {
	_, err := query.ParseAstPattern([]byte(`{"name":{"regex":"("}}`))
	var ip *query.InvalidPattern
	if !errors.As(err, &ip) {
		t.Fatalf("error type = %T, want *query.InvalidPattern", err)
	}
	if ip.FieldPath != "name.regex" {
		t.Errorf("FieldPath = %q, want name.regex", ip.FieldPath)
	}
}

func TestSearchAst_LimitDeterministic(t *testing.T) {
	store, _ := seedAst(t)
	svc := query.New(store)
	pat := query.AstPattern{Kind: "function"}

	full, err := svc.SearchAst(context.Background(), pat, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Nodes) < 3 {
		t.Fatalf("setup: want >=3 functions, got %d", len(full.Nodes))
	}
	limited, err := svc.SearchAst(context.Background(), pat, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Nodes) != 2 {
		t.Fatalf("limit=2 returned %d nodes", len(limited.Nodes))
	}
	// Truncation is applied AFTER the canonical sort, so the limited set is the
	// canonical prefix of the full set.
	for i := range limited.Nodes {
		if limited.Nodes[i].ID != full.Nodes[i].ID {
			t.Errorf("limited[%d]=%s not canonical prefix of full=%s", i, limited.Nodes[i].ID, full.Nodes[i].ID)
		}
	}
}
