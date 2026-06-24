package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// rubyGoldenFixture is the committed, FROZEN Ruby fixture (SW-054). Ruby has no
// local-var top-level kind at this tier, so `variable` is ABSENT BY DESIGN.
const rubyGoldenFixture = `require "logger"

TAX = 7

class Store
  def checkout
    price(3)
  end
end

def price(c)
  TAX
end

def run
  price(3)
  obj.log(1)
  run()
end
`

func parseRubyFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseRubyFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseRubyFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewRubyParser().Parse(context.Background(), "shop/cart.rb", []byte(rubyGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractRuby_Nodes asserts the EXACT closed node set + kinds; variable absent.
func TestExtractRuby_Nodes(t *testing.T) {
	nodes, _ := parseRubyFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.rb":  goKindFile,
		"shop.TAX":      goKindConstant,
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
	for _, k := range []model.NodeKind{"file", "function", "method", "type", "constant"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	if _, ok := emitted["variable"]; ok {
		t.Errorf("ruby must not emit variable (absent by design)")
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type", "constant":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractRuby_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractRuby_Edges(t *testing.T) {
	nodes, edges := parseRubyFixture(t)

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

	file := id("shop/cart.rb")
	for _, qn := range []string{"shop.TAX", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
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
	if got := callEdge.Evidence()[0]; got != "shop/cart.rb:18" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.rb:18")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.rb:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractRuby_PendingRefs asserts the selector use obj.log becomes a PendingRef.
func TestExtractRuby_PendingRefs(t *testing.T) {
	res := parseRubyFixtureResult(t)
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

// TestExtractRuby_Imports asserts require is recorded + surfaced in References.
func TestExtractRuby_Imports(t *testing.T) {
	res := parseRubyFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "logger" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected require logger, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "logger" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected logger in References, got %+v", res.References)
	}
}

// TestExtractRuby_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractRuby_Deterministic(t *testing.T) {
	n1, e1 := parseRubyFixture(t)
	n2, e2 := parseRubyFixture(t)
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
	parser := NewRubyParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.rb", []byte(rubyGoldenFixture))
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
