package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

func TestIndex_DirectoryKeyedNoCrossDirPhantom(t *testing.T) {
	// Two directories both declare `package util` with a `Helper` function. A
	// caller in dirA must resolve to dirA's Helper, never dirB's (no cross-dir
	// phantom). Open Q1: same-package unit is the DIRECTORY.
	nodes := []model.Node{
		mustNode(t, "file", "a/util.go", "a/util.go"),
		mustNode(t, "function", "util.Helper", "a/util.go"),
		mustNode(t, "function", "util.Caller", "a/util.go"),
		mustNode(t, "file", "b/util.go", "b/util.go"),
		mustNode(t, "function", "util.Helper", "b/util.go"),
	}
	idx := BuildIndex(nodes)

	files := []FileRefs{{
		SourcePath: "a/util.go",
		Dir:        "a",
		Pending:    []parse.PendingRef{{FromQN: "util.Caller", Name: "Helper", Kind: "calls", Line: 2}},
	}}
	edges, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	wantTo := nodeID(t, nodes, "util.Helper", "a/util.go")
	if edges[0].To() != wantTo {
		t.Errorf("resolved to wrong directory's Helper: got %s want %s", edges[0].To(), wantTo)
	}
}

func TestIndex_SameDirAmbiguitySkipped(t *testing.T) {
	// Two nodes with the same bare name in one directory ⇒ ambiguous ⇒ skipped.
	nodes := []model.Node{
		mustNode(t, "function", "p.Dup", "p/a.go"),
		mustNode(t, "method", "p.T.Dup", "p/b.go"), // bare last segment also "Dup"
		mustNode(t, "function", "p.Caller", "p/a.go"),
	}
	idx := BuildIndex(nodes)
	if _, ok := idx.sameDir("p", "Dup"); ok {
		t.Errorf("ambiguous bare name should not resolve in same dir")
	}
}

func TestIndex_PosixDirAndSplitQN(t *testing.T) {
	cases := []struct{ in, dir string }{
		{"a/b/c.go", "a/b"},
		{"c.go", ""},
		{"a/c.go", "a"},
	}
	for _, c := range cases {
		if got := posixDir(c.in); got != c.dir {
			t.Errorf("posixDir(%q)=%q want %q", c.in, got, c.dir)
		}
	}
	clause, bare := splitQN("shop.Cart.Add")
	if clause != "shop" || bare != "Add" {
		t.Errorf("splitQN(shop.Cart.Add)=(%q,%q) want (shop,Add)", clause, bare)
	}
	clause, bare = splitQN("loner")
	if clause != "" || bare != "loner" {
		t.Errorf("splitQN(loner)=(%q,%q) want (,loner)", clause, bare)
	}
}

func nodeID(t *testing.T, nodes []model.Node, qn, src string) model.NodeId {
	t.Helper()
	want := mustNode(t, "function", qn, src).ID()
	for _, n := range nodes {
		if n.ID() == want {
			return n.ID()
		}
	}
	t.Fatalf("node %q@%q not in set", qn, src)
	return ""
}
