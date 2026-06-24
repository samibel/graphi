package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// csGoldenFixture is the committed, FROZEN C# fixture (SW-054). C# has no free
// functions and no top-level vars at this tier, so `function`, `variable`, and
// `constant` are ABSENT BY DESIGN; only `type` and `method` appear. Definitions live
// inside a namespace_declaration (the extractor must descend into it).
const csGoldenFixture = `using System;

namespace Shop {
	class Store {
		int Checkout() {
			return Price(3);
		}

		int Price(int c) {
			return Run();
		}

		int Run() {
			obj.Log(1);
			return Run();
		}
	}

	interface ICart {
		int Items();
	}
}
`

func parseCSFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseCSFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseCSFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewCSharpParser().Parse(context.Background(), "shop/Store.cs", []byte(csGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractCS_Nodes asserts the EXACT closed node set + kinds; function/variable/
// constant absent.
func TestExtractCS_Nodes(t *testing.T) {
	nodes, _ := parseCSFixture(t)

	want := map[string]model.NodeKind{
		"shop/Store.cs": goKindFile,
		"shop.Store":    goKindType,
		"shop.Checkout": goKindMethod,
		"shop.Price":    goKindMethod,
		"shop.Run":      goKindMethod,
		"shop.ICart":    goKindType,
		"shop.Items":    goKindMethod,
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
	for _, k := range []model.NodeKind{"file", "method", "type"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"function", "variable", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("c_sharp must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "method", "type":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractCS_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractCS_Edges(t *testing.T) {
	nodes, edges := parseCSFixture(t)

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

	file := id("shop/Store.cs")
	for _, qn := range []string{"shop.Store", "shop.Checkout", "shop.Price", "shop.Run", "shop.ICart", "shop.Items"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.Checkout"), id("shop.Price"), goEdgeCalls); !ok {
		t.Error("missing calls edge Checkout -> Price")
	}
	if _, ok := has(id("shop.Price"), id("shop.Run"), goEdgeCalls); !ok {
		t.Error("missing calls edge Price -> Run")
	}
	callEdge, ok := has(id("shop.Run"), id("shop.Run"), goEdgeCalls)
	if !ok {
		t.Fatal("missing recursive calls edge Run -> Run")
	}
	// Use-site: Run -> Run recursive call on line 15 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/Store.cs:15" {
		t.Errorf("Run->Run call evidence = %q, want %q (use-site file:line pin)", got, "shop/Store.cs:15")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/Store.cs:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractCS_PendingRefs asserts the selector use obj.Log becomes a PendingRef.
func TestExtractCS_PendingRefs(t *testing.T) {
	res := parseCSFixtureResult(t)
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.Run" && p.Selector && p.SelectorBase == "obj" && p.Name == "Log" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for obj.Log, got %+v", res.PendingRefs)
	}
}

// TestExtractCS_Imports asserts using is recorded + surfaced in References.
func TestExtractCS_Imports(t *testing.T) {
	res := parseCSFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "System" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected using System, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "System" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected System in References, got %+v", res.References)
	}
}

// TestExtractCS_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractCS_Deterministic(t *testing.T) {
	n1, e1 := parseCSFixture(t)
	n2, e2 := parseCSFixture(t)
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
	parser := NewCSharpParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/Store.cs", []byte(csGoldenFixture))
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
