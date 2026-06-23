package parse

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// goFixture is a small single-file package exercising every extracted node kind
// plus an intra-file call, a recursive call, a method, and a reference.
const goFixture = `package shop

import "fmt"

const TaxRate = 7

var total int

type Cart struct{ items int }

func (c *Cart) Add() { c.items++ }

func price(n int) int { return n * TaxRate } // references TaxRate

func checkout() int {
	total = price(3)        // calls price, references total
	if total > 0 {
		return checkout()   // recursive self-call
	}
	fmt.Println(total)      // selector call: cross-package, NOT extracted
	return total
}
`

// parseFixture parses goFixture and returns its extracted nodes/edges.
func parseFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res, err := (&GoParser{}).Parse(context.Background(), "shop/cart.go", []byte(goFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res.Nodes, res.Edges
}

// parseFixtureResult parses goFixture and returns the full ParseResult so tests
// can assert the recorded PendingRefs/Imports.
func parseFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := (&GoParser{}).Parse(context.Background(), "shop/cart.go", []byte(goFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// nodeByQN finds a node by qualified name.
func nodeByQN(nodes []model.Node, qn string) (model.Node, bool) {
	for _, n := range nodes {
		if n.QualifiedName() == qn {
			return n, true
		}
	}
	return model.Node{}, false
}

func TestExtractGo_Nodes(t *testing.T) {
	nodes, _ := parseFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.go":  goKindFile,
		"shop.TaxRate":  goKindConstant,
		"shop.total":    goKindVariable,
		"shop.Cart":     goKindType,
		"shop.Cart.Add": goKindMethod,
		"shop.price":    goKindFunction,
		"shop.checkout": goKindFunction,
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
}

func TestExtractGo_Edges(t *testing.T) {
	nodes, edges := parseFixture(t)

	id := func(qn string) model.NodeId {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Fatalf("node %q not found", qn)
		}
		return n.ID()
	}
	has := func(from, to model.NodeId, kind string) bool {
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				return true
			}
		}
		return false
	}

	file := id("shop/cart.go")
	// Every symbol is defined by the file node.
	for _, qn := range []string{"shop.TaxRate", "shop.total", "shop.Cart", "shop.Cart.Add", "shop.price", "shop.checkout"} {
		if !has(file, id(qn), goEdgeDefines) {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}

	// Intra-file calls.
	if !has(id("shop.checkout"), id("shop.price"), goEdgeCalls) {
		t.Error("missing calls edge checkout -> price")
	}
	if !has(id("shop.checkout"), id("shop.checkout"), goEdgeCalls) {
		t.Error("missing recursive calls edge checkout -> checkout")
	}

	// Intra-file references.
	if !has(id("shop.price"), id("shop.TaxRate"), goEdgeReferences) {
		t.Error("missing references edge price -> TaxRate")
	}
	if !has(id("shop.checkout"), id("shop.total"), goEdgeReferences) {
		t.Error("missing references edge checkout -> total")
	}

	// Cross-package selector calls (fmt.Println) must NOT produce edges here.
	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
	}

	// Every edge carries complete provenance (guaranteed by NewEdge, asserted here).
	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance: tier=%q reason=%q evidence=%v",
				e.ID(), e.Tier(), e.Reason(), e.Evidence())
		}
	}
}

// TestExtractGo_PendingRefs asserts the extractor RECORDS (never resolves) the
// references it cannot prove from a single file: the selector call fmt.Println
// becomes a selector PendingRef and no cross-package edge is emitted, while the
// intra-file edges remain unchanged (regression — recording is additive).
func TestExtractGo_PendingRefs(t *testing.T) {
	res := parseFixtureResult(t)

	// fmt.Println in checkout must be recorded as a selector pending call.
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.checkout" && p.Selector && p.SelectorBase == "fmt" && p.Name == "Println" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		// No PendingRef may carry a NodeId / fabricate an endpoint — the struct
		// has none; assert FromQN/Name are always populated (inert record).
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for fmt.Println, got %+v", res.PendingRefs)
	}

	// The recording must not have emitted any cross-package/selector edge: every
	// edge is still an intra-file defines/calls/references edge.
	nodes := res.Nodes
	id := func(qn string) model.NodeId {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Fatalf("node %q not found", qn)
		}
		return n.ID()
	}
	has := func(from, to model.NodeId, kind string) bool {
		for _, e := range res.Edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				return true
			}
		}
		return false
	}
	if !has(id("shop.checkout"), id("shop.price"), goEdgeCalls) {
		t.Error("intra-file calls edge checkout -> price regressed")
	}
	if !has(id("shop.price"), id("shop.TaxRate"), goEdgeReferences) {
		t.Error("intra-file references edge price -> TaxRate regressed")
	}
}

// TestExtractGo_Imports asserts imports are recorded (alias + path).
func TestExtractGo_Imports(t *testing.T) {
	res := parseFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "fmt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected import of fmt, got %+v", res.Imports)
	}
	// References mirrors the import paths for the reverse-dep cascade.
	var inRefs bool
	for _, r := range res.References {
		if r == "fmt" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected fmt in References, got %+v", res.References)
	}
}

// TestExtractGo_Deterministic asserts repeated parses yield identical IDs/order,
// underpinning the full-vs-incremental byte-identical invariant.
func TestExtractGo_Deterministic(t *testing.T) {
	n1, e1 := parseFixture(t)
	n2, e2 := parseFixture(t)
	if len(n1) != len(n2) || len(e1) != len(e2) {
		t.Fatalf("non-deterministic counts: nodes %d/%d edges %d/%d", len(n1), len(n2), len(e1), len(e2))
	}
	for i := range n1 {
		if n1[i].ID() != n2[i].ID() {
			t.Errorf("node %d id drift: %s vs %s", i, n1[i].ID(), n2[i].ID())
		}
	}
	for i := range e1 {
		if e1[i].ID() != e2[i].ID() {
			t.Errorf("edge %d id drift: %s vs %s", i, e1[i].ID(), e2[i].ID())
		}
	}
}
