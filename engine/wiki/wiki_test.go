package wiki

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

func mustN(t *testing.T, s graphstore.Graphstore, name string) model.Node {
	t.Helper()
	n, _ := model.NewNode("function", name, name+".go", 10, 1)
	_ = s.PutNode(context.Background(), n)
	return n
}

func mustE(t *testing.T, s graphstore.Graphstore, from, to model.NodeId) {
	t.Helper()
	e, _ := model.NewEdge(from, to, "calls", model.TierConfirmed, 1.0, "test", []string{"e.go:1"})
	if err := s.PutEdge(context.Background(), e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

func twoComponentGraph(t *testing.T) *graphstore.MemStore {
	t.Helper()
	s := graphstore.NewMemStore()
	a := mustN(t, s, "pkgA.A").ID()
	b := mustN(t, s, "pkgA.B").ID()
	c := mustN(t, s, "pkgA.C").ID()
	d := mustN(t, s, "pkgB.D").ID()
	e := mustN(t, s, "pkgB.E").ID()
	mustE(t, s, a, b) // pkgA
	mustE(t, s, b, c)
	mustE(t, s, d, e) // pkgB
	return s
}

func TestGenerate_IndexPlusOnePagePerCommunity(t *testing.T) {
	w, err := Generate(context.Background(), twoComponentGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	if w.Index.ID != "index" || !strings.Contains(w.Index.Body, "community index") {
		t.Fatalf("bad index: %q", w.Index.Body)
	}
	if len(w.Pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(w.Pages))
	}
	for i, p := range w.Pages {
		wantID := "12"[i : i+1]
		if p.ID != wantID {
			t.Fatalf("page %d ID=%s want %s", i, p.ID, wantID)
		}
	}
}

func TestGenerate_IndexListsMemberCounts(t *testing.T) {
	w, _ := Generate(context.Background(), twoComponentGraph(t))
	if !strings.Contains(w.Index.Body, "3 member") || !strings.Contains(w.Index.Body, "2 member") {
		t.Fatalf("index missing member counts:\n%s", w.Index.Body)
	}
}

func TestGenerate_CommunityPageListsMembers(t *testing.T) {
	w, _ := Generate(context.Background(), twoComponentGraph(t))
	p, ok := w.PageByID("1")
	if !ok {
		t.Fatal("missing community 1")
	}
	if !strings.Contains(p.Body, "pkgA.A") || !strings.Contains(p.Body, "pkgA.B") {
		t.Fatalf("community 1 missing members:\n%s", p.Body)
	}
}

func TestGenerate_CrossLinksToNeighbors(t *testing.T) {
	// Build a graph with an inter-package edge so a cross-link appears.
	s := graphstore.NewMemStore()
	a := mustN(t, s, "pkgA.A").ID()
	b := mustN(t, s, "pkgA.B").ID() // pkgA
	c := mustN(t, s, "pkgB.C").ID()
	d := mustN(t, s, "pkgB.D").ID() // pkgB
	mustE(t, s, a, b)
	mustE(t, s, c, d)
	mustE(t, s, a, c) // inter-package edge pkgA<->pkgB

	w, _ := Generate(context.Background(), s)
	p1, _ := w.PageByID("1")
	if !strings.Contains(p1.Body, "Neighboring communities") {
		t.Fatalf("no neighbors section:\n%s", p1.Body)
	}
	// cross-link target must exist as a real page
	if !strings.Contains(p1.Body, "/wiki/c/2") {
		t.Fatalf("missing cross-link to community 2:\n%s", p1.Body)
	}
	if _, ok := w.PageByID("2"); !ok {
		t.Fatal("cross-link target community 2 does not exist")
	}
}

func TestGenerate_Deterministic_ByteIdentical(t *testing.T) {
	w1, err := Generate(context.Background(), twoComponentGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	w2, err := Generate(context.Background(), twoComponentGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	if w1.Index.Body != w2.Index.Body {
		t.Fatalf("index not deterministic")
	}
	if len(w1.Pages) != len(w2.Pages) {
		t.Fatalf("page count drift")
	}
	for i := range w1.Pages {
		if w1.Pages[i].Body != w2.Pages[i].Body {
			t.Fatalf("page %d not byte-identical:\n---1---\n%s\n---2---\n%s",
				i, w1.Pages[i].Body, w2.Pages[i].Body)
		}
	}
}

func TestPageByID_UnknownReturnsFalse(t *testing.T) {
	w, _ := Generate(context.Background(), twoComponentGraph(t))
	if _, ok := w.PageByID("999"); ok {
		t.Fatal("unknown page id should not exist")
	}
}

func TestGenerate_EmptyGraph(t *testing.T) {
	w, err := Generate(context.Background(), graphstore.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Pages) != 0 {
		t.Fatalf("empty graph should yield 0 community pages, got %d", len(w.Pages))
	}
	if !strings.Contains(w.Index.Body, "0 communities") {
		t.Fatalf("empty index should say 0 communities:\n%s", w.Index.Body)
	}
}
