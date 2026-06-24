package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// cppGoldenFixture is the committed, FROZEN C++ fixture (SW-054). Definitions live
// inside a namespace_definition (the extractor must descend into it). `method` and
// `constant` are ABSENT BY DESIGN at this tier.
const cppGoldenFixture = `#include <vector>

namespace shop {

int total = 0;

class Store {
public:
	int checkout();
};

int price(int c) {
	return total;
}

int run() {
	price(3);
	io.flush(total);
	return run();
}

}
`

func parseCppFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseCppFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseCppFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewCppParser().Parse(context.Background(), "shop/cart.cpp", []byte(cppGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractCpp_Nodes asserts the EXACT closed node set + kinds; method/constant absent.
func TestExtractCpp_Nodes(t *testing.T) {
	nodes, _ := parseCppFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.cpp": goKindFile,
		"shop.total":    goKindVariable,
		"shop.Store":    goKindType,
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
	for _, k := range []model.NodeKind{"file", "function", "type", "variable"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"method", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("cpp must not emit %q (absent by design)", bad)
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

// TestExtractCpp_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractCpp_Edges(t *testing.T) {
	nodes, edges := parseCppFixture(t)

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

	file := id("shop/cart.cpp")
	for _, qn := range []string{"shop.total", "shop.Store", "shop.price", "shop.run"} {
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
	// Use-site: run -> run recursive call on line 19 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.cpp:19" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.cpp:19")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.cpp:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractCpp_PendingRefs asserts the selector use io.flush becomes a PendingRef.
func TestExtractCpp_PendingRefs(t *testing.T) {
	res := parseCppFixtureResult(t)
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

// TestExtractCpp_Imports asserts #include is recorded + surfaced in References.
func TestExtractCpp_Imports(t *testing.T) {
	res := parseCppFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "vector" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected include vector, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "vector" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected vector in References, got %+v", res.References)
	}
}

// TestExtractCpp_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractCpp_Deterministic(t *testing.T) {
	n1, e1 := parseCppFixture(t)
	n2, e2 := parseCppFixture(t)
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
	parser := NewCppParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.cpp", []byte(cppGoldenFixture))
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
