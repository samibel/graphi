package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// javaGoldenFixture is the committed, FROZEN Java fixture (SW-054). Java has no
// free functions, so `function` is ABSENT BY DESIGN. This fixture declares no
// fields, so its node set is unchanged by the WP-J2 field extraction —
// TestExtractJava_FieldNodes covers fields on its own fixture.
const javaGoldenFixture = `package shop;

import java.util.List;

class Store {
	int checkout() {
		return price(3);
	}

	int price(int c) {
		return run();
	}

	int run() {
		return run();
	}
}

interface Cart {
	int items();
}
`

func parseJavaFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseJavaFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseJavaFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewJavaParser().Parse(context.Background(), "shop/Store.java", []byte(javaGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractJava_Nodes asserts the EXACT closed node set + kinds on the
// frozen field-free fixture; function is absent-by-design.
func TestExtractJava_Nodes(t *testing.T) {
	nodes, _ := parseJavaFixture(t)

	want := map[string]model.NodeKind{
		"shop/Store.java": goKindFile,
		"shop.Store":      goKindType,
		"shop.checkout":   goKindMethod,
		"shop.price":      goKindMethod,
		"shop.run":        goKindMethod,
		"shop.Cart":       goKindType,
		"shop.items":      goKindMethod,
		// WP-01: the file's `package shop;` mints one interned package node.
		"shop": KindPackage,
	}
	for qn, kind := range want {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Errorf("missing node %q", qn)
			continue
		}
		if n.Kind() != kind {
			t.Errorf("node %q kind = %q, want %q", qn, n.Kind(), kind)
		}
	}
	if len(nodes) != len(want) {
		t.Errorf("node count = %d, want %d (%v)", len(nodes), len(want), want)
	}

	emitted := map[model.NodeKind]struct{}{}
	for _, n := range nodes {
		emitted[n.Kind()] = struct{}{}
	}
	for _, k := range []model.NodeKind{"file", "method", "type", "package"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	// function is ABSENT BY DESIGN for Java (no free functions); this fixture
	// declares no fields, so variable/constant are absent HERE (not by design
	// since WP-J2 — see TestExtractJava_FieldNodes).
	for _, bad := range []model.NodeKind{"function", "variable", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("the field-free golden fixture must not emit %q", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "method", "type", "package", "variable", "constant":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractJava_FieldNodes pins the WP-J2 (ADR 0008 D3) field extraction:
// one node per variable_declarator, kind from the DECLARED form only — a
// `static final` field or an interface constant_declaration (the grammar names
// its constancy) is a constant, everything else a variable — with annotations
// and static/final flags as non-identity meta.
func TestExtractJava_FieldNodes(t *testing.T) {
	const fixture = `package shop;

class Store {
	static final int MAX = 3;
	@Deprecated private String name, alias;
	int count = 0;
	void m() {}
}

interface Cfg {
	int LIMIT = 9;
}
`
	res, err := NewJavaParser().Parse(context.Background(), "shop/Store.java", []byte(fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]model.NodeKind{
		"shop.MAX":   goKindConstant, // declared static final
		"shop.name":  goKindVariable,
		"shop.alias": goKindVariable, // second declarator of one declaration
		"shop.count": goKindVariable,
		"shop.LIMIT": goKindConstant, // interface field: the CST declares it constant_declaration
	}
	for qn, kind := range want {
		n, ok := nodeByQN(res.Nodes, qn)
		if !ok {
			t.Errorf("missing field node %q", qn)
			continue
		}
		if n.Kind() != kind {
			t.Errorf("node %q kind = %q, want %q", qn, n.Kind(), kind)
		}
	}

	metaOf := func(qn string) model.NodeMeta {
		t.Helper()
		n, ok := nodeByQN(res.Nodes, qn)
		if !ok {
			t.Fatalf("missing node %q", qn)
		}
		return n.Meta()
	}
	if m := metaOf("shop.MAX"); strings.Join(m.Flags, ",") != "final,static" {
		t.Errorf("shop.MAX flags = %v, want [final static]", m.Flags)
	}
	if m := metaOf("shop.name"); strings.Join(m.Annotations, ",") != "Deprecated" || len(m.Flags) != 0 {
		t.Errorf("shop.name meta = %+v, want annotation Deprecated and no flags", m)
	}
}

// TestExtractJava_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractJava_Edges(t *testing.T) {
	nodes, edges := parseJavaFixture(t)

	id := func(qn string) model.NodeId {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Fatalf("node %q not found", qn)
		}
		return n.ID()
	}
	has := func(from, to model.NodeId, kind string) (model.Edge, bool) {
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				return e, true
			}
		}
		return model.Edge{}, false
	}

	file := id("shop/Store.java")
	for _, qn := range []string{"shop.Store", "shop.checkout", "shop.price", "shop.run", "shop.Cart", "shop.items"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.checkout"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.price"), id("shop.run"), goEdgeCalls); !ok {
		t.Error("missing calls edge price -> run")
	}
	callEdge, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls)
	if !ok {
		t.Fatal("missing recursive calls edge run -> run")
	}
	// Use-site: run -> run recursive call on line 15 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/Store.java:15" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/Store.java:15")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/Store.java:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractJava_Imports asserts the import is recorded + surfaced in References.
func TestExtractJava_Imports(t *testing.T) {
	res := parseJavaFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "java.util.List" && imp.Alias == "List" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected import java.util.List (alias List), got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "java.util.List" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected java.util.List in References, got %+v", res.References)
	}
}

// TestExtractJava_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractJava_Deterministic(t *testing.T) {
	n1, e1 := parseJavaFixture(t)
	n2, e2 := parseJavaFixture(t)
	if len(n1) != len(n2) || len(e1) != len(e2) {
		t.Fatalf("non-deterministic counts")
	}
	for i := range n1 {
		if n1[i].ID() != n2[i].ID() {
			t.Errorf("node %d id drift", i)
		}
	}
	for i := range e1 {
		if e1[i].ID() != e2[i].ID() {
			t.Errorf("edge %d id drift", i)
		}
	}
	want := idStream(n1, e1)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]string, workers)
	parser := NewJavaParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/Store.java", []byte(javaGoldenFixture))
			if err != nil {
				t.Errorf("worker %d parse: %v", idx, err)
				return
			}
			results[idx] = idStream(res.Nodes, res.Edges)
		}(w)
	}
	wg.Wait()
	for i, got := range results {
		if got != want {
			t.Errorf("worker %d produced a divergent id stream", i)
		}
	}
}
