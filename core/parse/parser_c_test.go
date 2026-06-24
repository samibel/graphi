package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// cGoldenFixture is the committed, FROZEN C fixture (SW-054). C has no methods and no
// distinct constant kind at this tier, so `method` and `constant` are ABSENT BY DESIGN.
const cGoldenFixture = `#include <stdio.h>
#include "cart.h"

int total = 0;

struct Cart {
	int items;
};

int price(int c) {
	return total;
}

int run() {
	price(3);
	io.flush(total);
	return run();
}
`

func parseCFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseCFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseCFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewCParser().Parse(context.Background(), "shop/cart.c", []byte(cGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractC_Nodes asserts the EXACT closed node set + kinds; method/constant absent.
func TestExtractC_Nodes(t *testing.T) {
	nodes, _ := parseCFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.c": goKindFile,
		"shop.total":  goKindVariable,
		"shop.Cart":   goKindType,
		"shop.price":  goKindFunction,
		"shop.run":    goKindFunction,
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
	for _, k := range []model.NodeKind{"file", "function", "type", "variable"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"method", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("c must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "type", "variable":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractC_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractC_Edges(t *testing.T) {
	nodes, edges := parseCFixture(t)

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

	file := id("shop/cart.c")
	for _, qn := range []string{"shop.total", "shop.Cart", "shop.price", "shop.run"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.run"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge run -> price")
	}
	callEdge, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls)
	if !ok {
		t.Fatal("missing recursive calls edge run -> run")
	}
	// Use-site: run -> run recursive call on line 17 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.c:17" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.c:16")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.c:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractC_PendingRefs asserts the selector use io.flush becomes a PendingRef.
func TestExtractC_PendingRefs(t *testing.T) {
	res := parseCFixtureResult(t)
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && p.Selector && p.SelectorBase == "io" && p.Name == "flush" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for io.flush, got %+v", res.PendingRefs)
	}
}

// TestExtractC_Imports asserts #include directives are recorded + surfaced in References.
func TestExtractC_Imports(t *testing.T) {
	res := parseCFixtureResult(t)
	paths := map[string]bool{}
	for _, imp := range res.Imports {
		paths[imp.Path] = true
	}
	for _, p := range []string{"stdio.h", "cart.h"} {
		if !paths[p] {
			t.Errorf("expected include %q, got %+v", p, res.Imports)
		}
		var inRefs bool
		for _, r := range res.References {
			if r == p {
				inRefs = true
			}
		}
		if !inRefs {
			t.Errorf("expected %q in References, got %+v", p, res.References)
		}
	}
}

// TestExtractC_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractC_Deterministic(t *testing.T) {
	n1, e1 := parseCFixture(t)
	n2, e2 := parseCFixture(t)
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
	parser := NewCParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.c", []byte(cGoldenFixture))
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
