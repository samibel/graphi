package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// jsGoldenFixture is the committed, FROZEN JavaScript fixture (SW-054). It exercises
// every extracted node kind plus the full intra-file edge set and a cross-module
// selector use, mirroring tsGoldenFixture / extract_go_test.go.
//
// Kind mapping (JS collapses onto {file, function, method, type, variable, constant}):
//
//	function ← function_declaration
//	method   ← method_definition (class methods)
//	type     ← class_declaration (collapsed to type)
//	variable ← let bindings
//	constant ← const bindings
//
// Absent BY DESIGN (JS lacks them at this tier, so the closed node-set is unambiguous):
// interfaces, enums, type aliases, namespaces, decorators. The fixture contains none.
const jsGoldenFixture = `import { Logger } from "./log";
import * as util from "util";

const TaxRate = 7;

let total = 0;

class Store {
	checkout() {
		return price(3);
	}
}

function price(c) {
	return TaxRate;
}

function run() {
	total = price(3);
	util.log(total);
	return run();
}
`

func parseJSFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseJSFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseJSFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewJavaScriptParser().Parse(context.Background(), "shop/cart.js", []byte(jsGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractJS_Nodes asserts the EXACT closed node set + kinds (golden snapshot).
func TestExtractJS_Nodes(t *testing.T) {
	nodes, _ := parseJSFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.js":  goKindFile,
		"shop.TaxRate":  goKindConstant, // const -> constant
		"shop.total":    goKindVariable, // let -> variable
		"shop.Store":    goKindType,     // class -> type
		"shop.checkout": goKindMethod,   // class method -> method
		"shop.price":    goKindFunction, // function
		"shop.run":      goKindFunction, // function
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
	// JS lacks an in-fixture interface/enum, so "type" present is from the class.
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

// TestExtractJS_Edges asserts intra-file defines/calls edges with full provenance and
// that no cross-module/selector edge leaks.
func TestExtractJS_Edges(t *testing.T) {
	nodes, edges := parseJSFixture(t)

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

	file := id("shop/cart.js")
	for _, qn := range []string{"shop.TaxRate", "shop.total", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
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

	// Provenance: the recursive run -> run call site is on source line 21 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.js:21" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.js:23")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
	}
	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.js:") {
				t.Errorf("edge %s evidence %q is not file:line for the fixture", e.ID(), ev)
			}
		}
	}
}

// TestExtractJS_PendingRefs asserts the cross-module selector use util.log becomes a
// selector PendingRef with no fabricated NodeId.
func TestExtractJS_PendingRefs(t *testing.T) {
	res := parseJSFixtureResult(t)

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

// TestExtractJS_Imports asserts imports are recorded and surfaced in References.
func TestExtractJS_Imports(t *testing.T) {
	res := parseJSFixtureResult(t)

	wantPaths := map[string]string{
		"./log": "Logger",
		"util":  "util",
	}
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

// TestExtractJS_Deterministic asserts repeated + concurrent (32-goroutine, -race)
// byte-identical determinism.
func TestExtractJS_Deterministic(t *testing.T) {
	n1, e1 := parseJSFixture(t)
	n2, e2 := parseJSFixture(t)
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

	want := idStream(n1, e1)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]string, workers)
	parser := NewJavaScriptParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.js", []byte(jsGoldenFixture))
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
