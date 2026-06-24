package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// kotlinGoldenFixture is the committed, FROZEN Kotlin fixture (SW-054). Kotlin
// top-level properties are out of the node set this tier, so `variable` and `constant`
// are ABSENT BY DESIGN.
const kotlinGoldenFixture = `package shop

import kotlin.collections.List

class Store {
	fun checkout(): Int {
		return price(3)
	}
}

fun price(c: Int): Int {
	return run()
}

fun run(): Int {
	obj.log(1)
	return run()
}
`

func parseKotlinFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseKotlinFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseKotlinFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewKotlinParser().Parse(context.Background(), "shop/cart.kt", []byte(kotlinGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractKotlin_Nodes asserts the EXACT closed node set + kinds; variable/constant absent.
func TestExtractKotlin_Nodes(t *testing.T) {
	nodes, _ := parseKotlinFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.kt":  goKindFile,
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
	for _, k := range []model.NodeKind{"file", "function", "method", "type"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"variable", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("kotlin must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractKotlin_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractKotlin_Edges(t *testing.T) {
	nodes, edges := parseKotlinFixture(t)

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

	file := id("shop/cart.kt")
	for _, qn := range []string{"shop.Store", "shop.checkout", "shop.price", "shop.run"} {
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
	// Use-site: run -> run recursive call on line 17 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.kt:17" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.kt:17")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.kt:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractKotlin_PendingRefs asserts the selector use obj.log becomes a PendingRef.
func TestExtractKotlin_PendingRefs(t *testing.T) {
	res := parseKotlinFixtureResult(t)
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && p.Selector && p.SelectorBase == "obj" && p.Name == "log" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for obj.log, got %+v", res.PendingRefs)
	}
}

// TestExtractKotlin_Imports asserts the import is recorded + surfaced in References.
func TestExtractKotlin_Imports(t *testing.T) {
	res := parseKotlinFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "kotlin.collections.List" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected import kotlin.collections.List, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "kotlin.collections.List" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected kotlin.collections.List in References, got %+v", res.References)
	}
}

// TestExtractKotlin_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractKotlin_Deterministic(t *testing.T) {
	n1, e1 := parseKotlinFixture(t)
	n2, e2 := parseKotlinFixture(t)
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
	parser := NewKotlinParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.kt", []byte(kotlinGoldenFixture))
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
