package search_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/search"
)

func mustNode(t *testing.T, kind, qn, path string, line, col int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qn, path, line, col)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return n
}

func seedStore(t *testing.T, st graphstore.Graphstore) {
	t.Helper()
	ctx := context.Background()
	nodes := []model.Node{
		mustNode(t, "function", "pkg/foo.ParseGraph", "pkg/foo.go", 10, 1),
		mustNode(t, "function", "pkg/foo.ParseGraphLite", "pkg/foo.go", 20, 1),
		mustNode(t, "type", "pkg/bar.Graph", "pkg/bar.go", 5, 1),
	}
	for _, n := range nodes {
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	svc := search.New(st)
	res, err := svc.Search(ctx, "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("expected empty result for empty query, got %d", len(res.Matches))
	}
}

func TestSearch_NoMatch(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	svc := search.New(st)
	res, err := svc.Search(ctx, "DefinitelyMissing", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("expected empty result for missing token, got %d", len(res.Matches))
	}
}

func TestSearch_DeterministicTieBreak(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()

	// Two nodes with identical rank (MemStore rank is always 0) and same
	// qualified-name prefix: ordering must be deterministic on repeated runs.
	n1 := mustNode(t, "function", "pkg/z.A", "pkg/z.go", 1, 1)
	n2 := mustNode(t, "function", "pkg/z.AAlias", "pkg/z.go", 2, 1) // matches same query, different ID
	for _, n := range []model.Node{n1, n2} {
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}

	svc := search.New(st)
	res1, err := svc.Search(ctx, "pkg/z", 10)
	if err != nil {
		t.Fatalf("Search 1: %v", err)
	}
	res2, err := svc.Search(ctx, "pkg/z", 10)
	if err != nil {
		t.Fatalf("Search 2: %v", err)
	}
	if len(res1.Matches) != 2 || len(res2.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d and %d", len(res1.Matches), len(res2.Matches))
	}
	for i := range res1.Matches {
		if res1.Matches[i].NodeID != res2.Matches[i].NodeID {
			t.Fatalf("ordering non-deterministic at index %d: %s vs %s",
				i, res1.Matches[i].NodeID, res2.Matches[i].NodeID)
		}
	}
}

func TestSearch_Limit(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	svc := search.New(st)
	res, err := svc.Search(ctx, "pkg", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected limit=2 matches, got %d", len(res.Matches))
	}
}

func TestSearch_MarshalStable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	svc := search.New(st)
	res, err := svc.Search(ctx, "ParseGraph", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	b1, err := search.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal 1: %v", err)
	}
	b2, err := search.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal 2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("Marshal not stable:\n%s\n%s", b1, b2)
	}
}
