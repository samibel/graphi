package community

import (
	"context"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

func mustNode(t *testing.T, store graphstore.Graphstore, name string) model.Node {
	t.Helper()
	n, err := model.NewNode("function", name, name+".go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	return n
}

func mustEdge(t *testing.T, store graphstore.Graphstore, from, to model.NodeId) {
	t.Helper()
	e, err := model.NewEdge(from, to, "calls", model.TierConfirmed, 1.0, "test", []string{"e.go:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := store.PutEdge(context.Background(), e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

func TestDetect_GroupsByPackage(t *testing.T) {
	store := graphstore.NewMemStore()
	mustNode(t, store, "pkgA.Foo")
	mustNode(t, store, "pkgA.Bar")
	mustNode(t, store, "pkgB.Qux")

	got, err := Detect(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d communities, want 2 (pkgA, pkgB): %+v", len(got), got)
	}
	if got[0].Key != "pkgA" || got[1].Key != "pkgB" {
		t.Fatalf("keys not sorted: %+v", got)
	}
	if len(got[0].Members) != 2 || len(got[1].Members) != 1 {
		t.Fatalf("member counts wrong: %+v", got)
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("IDs not 1,2: %+v", got)
	}
}

func TestDetect_NoDot_NameOwnCommunity(t *testing.T) {
	store := graphstore.NewMemStore()
	mustNode(t, store, "main") // no dot → its own package
	mustNode(t, store, "pkg.X")
	got, _ := Detect(context.Background(), store)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

func TestDetect_AllSamePackage(t *testing.T) {
	store := graphstore.NewMemStore()
	mustNode(t, store, "pkg.A")
	mustNode(t, store, "pkg.B")
	mustNode(t, store, "pkg.C")
	got, _ := Detect(context.Background(), store)
	if len(got) != 1 || len(got[0].Members) != 3 {
		t.Fatalf("expected 1 community of 3: %+v", got)
	}
	// members sorted by NodeId
	if !sorted(got[0].Members) {
		t.Fatalf("members not sorted: %v", got[0].Members)
	}
}

func TestDetect_EmptyGraph(t *testing.T) {
	got, _ := Detect(context.Background(), graphstore.NewMemStore())
	if len(got) != 0 {
		t.Fatalf("got %d, want 0", len(got))
	}
}

func TestDetect_Deterministic(t *testing.T) {
	build := func() *graphstore.MemStore {
		s := graphstore.NewMemStore()
		mustNode(t, s, "pkgA.A")
		mustNode(t, s, "pkgA.B")
		mustNode(t, s, "pkgB.C")
		mustNode(t, s, "pkgC.D")
		return s
	}
	first, _ := Detect(context.Background(), build())
	second, _ := Detect(context.Background(), build())
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestPackageKey(t *testing.T) {
	cases := map[string]string{
		"pkg.sub.Foo": "pkg.sub",
		"pkg.Foo":     "pkg",
		"main":        "main",
		"":            "",
	}
	for in, want := range cases {
		if got := packageKey(in); got != want {
			t.Errorf("packageKey(%q)=%q want %q", in, got, want)
		}
	}
}

func sorted(ids []model.NodeId) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			return false
		}
	}
	return true
}
