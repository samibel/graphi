package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// tsxGoldenFixture is the committed, FROZEN TSX fixture (SW-054). It mirrors
// tsGoldenFixture (TSX is TypeScript + JSX) and exercises every extracted node kind,
// the full intra-file edge set, an in-file type reference (price's param type Cart),
// and a cross-module selector use.
//
//	function ← function_declaration
//	method   ← method_definition
//	type     ← interface / class declarations (collapsed)
//	variable ← let bindings
//	constant ← const bindings
//
// Absent BY DESIGN: namespaces, decorators, ambient declarations, JSX elements.
const tsxGoldenFixture = `import { Logger } from "./log";
import * as util from "util";

interface Cart {
	items: number;
}

const TaxRate = 7;

let total = 0;

class Store {
	checkout(): number {
		return price(3);
	}
}

function price(c: Cart): number {
	return TaxRate;
}

function run(): number {
	total = price(3);
	util.log(total);
	return run();
}
`

func parseTSXFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseTSXFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseTSXFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewTSXParser().Parse(context.Background(), "shop/cart.tsx", []byte(tsxGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractTSX_Nodes asserts the EXACT closed node set + kinds.
func TestExtractTSX_Nodes(t *testing.T) {
	nodes, _ := parseTSXFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.tsx": goKindFile,
		"shop.Cart":     goKindType,
		"shop.TaxRate":  goKindConstant,
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
	for _, k := range []model.NodeKind{"file", "function", "method", "type", "variable", "constant"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type", "variable", "constant":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractTSX_Edges asserts intra-file defines/calls/references edges with full
// provenance including a use-site file:line pin.
func TestExtractTSX_Edges(t *testing.T) {
	nodes, edges := parseTSXFixture(t)

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

	file := id("shop/cart.tsx")
	for _, qn := range []string{"shop.Cart", "shop.TaxRate", "shop.total", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.checkout"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls); !ok {
		t.Error("missing recursive calls edge run -> run")
	}
	refEdge, ok := has(id("shop.price"), id("shop.Cart"), goEdgeReferences)
	if !ok {
		t.Fatal("missing references edge price -> Cart")
	}
	// Use-site provenance: Cart appears as price's parameter type on signature line 18.
	if got := refEdge.Evidence()[0]; got != "shop/cart.tsx:18" {
		t.Errorf("price->Cart references evidence = %q, want %q", got, "shop/cart.tsx:18")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.tsx:") {
				t.Errorf("edge %s evidence %q is not file:line for the fixture", e.ID(), ev)
			}
		}
	}
}

// TestExtractTSX_PendingRefs asserts the selector use util.log becomes a PendingRef.
func TestExtractTSX_PendingRefs(t *testing.T) {
	res := parseTSXFixtureResult(t)
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

// TestExtractTSX_Imports asserts imports recorded + surfaced in References.
func TestExtractTSX_Imports(t *testing.T) {
	res := parseTSXFixtureResult(t)
	wantPaths := map[string]string{"./log": "Logger", "util": "util"}
	got := map[string]string{}
	for _, imp := range res.Imports {
		got[imp.Path] = imp.Alias
	}
	for path, alias := range wantPaths {
		if got[path] != alias {
			t.Errorf("import %q alias = %q, want %q (imports=%+v)", path, got[path], alias, res.Imports)
		}
	}
	for _, path := range []string{"./log", "util"} {
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

// TestExtractTSX_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractTSX_Deterministic(t *testing.T) {
	n1, e1 := parseTSXFixture(t)
	n2, e2 := parseTSXFixture(t)
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
	parser := NewTSXParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.tsx", []byte(tsxGoldenFixture))
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
