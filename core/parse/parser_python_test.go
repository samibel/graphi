package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// pyGoldenFixture is the committed, FROZEN Python fixture (SW-054). Python has no
// const/var distinction, so `constant` is ABSENT BY DESIGN at this tier (the closed
// node set below enumerates that absence).
//
//	function ← module-level def
//	method   ← def inside a class body
//	type     ← class definition
//	variable ← module-level assignment (TAX, total)
//
// Absent BY DESIGN: constant (no language const), interfaces/enums.
const pyGoldenFixture = `import os
from sys import path

TAX = 7

total = 0

class Store:
    def checkout(self):
        return price(3)

def price(c):
    return TAX

def run():
    total = price(3)
    util.log(total)
    return run()
`

func parsePyFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parsePyFixtureResult(t)
	return res.Nodes, res.Edges
}

func parsePyFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewPythonParser().Parse(context.Background(), "shop/cart.py", []byte(pyGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractPY_Nodes asserts the EXACT closed node set + kinds; constant is
// absent-by-design.
func TestExtractPY_Nodes(t *testing.T) {
	nodes, _ := parsePyFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.py":  goKindFile,
		"shop.TAX":      goKindVariable, // module assignment -> variable (no const in py)
		"shop.total":    goKindVariable,
		"shop.Store":    goKindType,
		"shop.checkout": goKindMethod,
		"shop.price":    goKindFunction,
		"shop.run":      goKindFunction,
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
	for _, k := range []model.NodeKind{"file", "function", "method", "type", "variable"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	// constant is ABSENT BY DESIGN for Python.
	if _, ok := emitted["constant"]; ok {
		t.Errorf("python must not emit constant (absent by design)")
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type", "variable":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractPY_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractPY_Edges(t *testing.T) {
	nodes, edges := parsePyFixture(t)

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

	file := id("shop/cart.py")
	for _, qn := range []string{"shop.TAX", "shop.total", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.checkout"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.run"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge run -> price")
	}
	callEdge, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls)
	if !ok {
		t.Fatal("missing recursive calls edge run -> run")
	}
	// Use-site: run -> run recursive call on line 18 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.py:18" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.py:18")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.py:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractPY_PendingRefs asserts the selector use util.log becomes a PendingRef.
func TestExtractPY_PendingRefs(t *testing.T) {
	res := parsePyFixtureResult(t)
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && p.Selector && p.SelectorBase == "util" && p.Name == "log" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for util.log, got %+v", res.PendingRefs)
	}
}

// TestExtractPY_Imports asserts imports recorded + surfaced in References.
func TestExtractPY_Imports(t *testing.T) {
	res := parsePyFixtureResult(t)
	got := map[string]string{}
	for _, imp := range res.Imports {
		got[imp.Alias] = imp.Path
	}
	if got["os"] != "os" {
		t.Errorf("import os = %q, want os (imports=%+v)", got["os"], res.Imports)
	}
	if got["path"] != "sys" {
		t.Errorf("from sys import path: alias path should map to module sys, got %q (imports=%+v)", got["path"], res.Imports)
	}
	for _, path := range []string{"os", "sys"} {
		var inRefs bool
		for _, r := range res.References {
			if r == path {
				inRefs = true
			}
		}
		if !inRefs {
			t.Errorf("expected %q in References, got %+v", path, res.References)
		}
	}
}

// TestExtractPY_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractPY_Deterministic(t *testing.T) {
	n1, e1 := parsePyFixture(t)
	n2, e2 := parsePyFixture(t)
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
	parser := NewPythonParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.py", []byte(pyGoldenFixture))
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
