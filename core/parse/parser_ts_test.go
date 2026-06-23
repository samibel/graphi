package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// tsGoldenFixture is the committed, FROZEN TypeScript fixture for SW-053 (the first curated
// pure-Go tree-sitter language). It exercises every extracted node kind plus the full
// intra-file edge set and a cross-module selector use, mirroring goFixture /
// extract_go_test.go.
//
// Kind mapping table (TS has MORE kinds than the frozen vocabulary; the extractor
// collapses them onto {file, function, method, type, variable, constant}):
//
//	function ← function_declaration (a named callable binding stays a function)
//	method   ← method_definition (class methods)
//	type     ← interface / type alias / enum / class declarations (collapsed to type)
//	variable ← let/var bindings (non-callable)
//	constant ← const bindings (non-callable)
//
// Absent BY DESIGN (collapsed/omitted at this tier, so the closed node-set assertion
// is unambiguous): namespaces/modules, decorators, ambient declarations. The fixture
// contains none of these; the closed `want` map below is exhaustive.
const tsGoldenFixture = `import { Logger } from "./log";
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

// parseTSFixture parses tsGoldenFixture and returns its extracted nodes/edges.
func parseTSFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseTSFixtureResult(t)
	return res.Nodes, res.Edges
}

// parseTSFixtureResult parses tsGoldenFixture and returns the full ParseResult so tests can
// assert recorded PendingRefs / Imports.
func parseTSFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewTSParser().Parse(context.Background(), "shop/cart.ts", []byte(tsGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractTS_Nodes asserts the EXACT closed node set + kinds (golden snapshot). The
// kind literals asserted are the actual emitted strings ("variable"/"constant", not
// var/const). The length check makes the set closed — any extra/missing node fails.
func TestExtractTS_Nodes(t *testing.T) {
	nodes, _ := parseTSFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.ts":  goKindFile,     // "file"
		"shop.Cart":     goKindType,     // interface -> type
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

	// Assert the actual emitted kind LITERALS are exactly the frozen vocabulary, and
	// the kinds TS lacks at this tier never appear.
	emitted := map[model.NodeKind]struct{}{}
	for _, n := range nodes {
		emitted[n.Kind()] = struct{}{}
	}
	for _, k := range []model.NodeKind{"file", "function", "method", "type", "variable", "constant"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	// var/const are NEVER emitted as literals (the impl emits variable/constant).
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type", "variable", "constant":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractTS_Edges asserts the intra-file defines/calls/references edges with full
// provenance, and that no cross-module/selector edge leaks into the graph.
func TestExtractTS_Edges(t *testing.T) {
	nodes, edges := parseTSFixture(t)

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

	file := id("shop/cart.ts")
	// Every symbol is defined by the file node.
	for _, qn := range []string{"shop.Cart", "shop.TaxRate", "shop.total", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}

	// Intra-file calls: checkout -> price, run -> price, recursive run -> run.
	if _, ok := has(id("shop.checkout"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.run"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge run -> price")
	}
	if _, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls); !ok {
		t.Error("missing recursive calls edge run -> run")
	}

	// Intra-file reference: price's signature references the in-file type Cart.
	refEdge, ok := has(id("shop.price"), id("shop.Cart"), goEdgeReferences)
	if !ok {
		t.Error("missing references edge price -> Cart")
	}

	// Provenance: at least one edge pins an exact file:line at a USE-SITE. The
	// run -> price call site is on source line 23 (1-based) in the fixture.
	callEdge, ok := has(id("shop.run"), id("shop.price"), goEdgeCalls)
	if !ok {
		t.Fatal("missing calls edge run -> price for provenance pin")
	}
	if got := callEdge.Evidence()[0]; got != "shop/cart.ts:23" {
		t.Errorf("run->price call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.ts:23")
	}
	// The reference edge cites the use-site: Cart appears as price's parameter type
	// on the signature line (18).
	if got := refEdge.Evidence()[0]; got != "shop/cart.ts:18" {
		t.Errorf("price->Cart references evidence = %q, want %q", got, "shop/cart.ts:18")
	}

	// No selector/cross-module call edge may leak (util.log is a PendingRef).
	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
	}

	// Every edge carries complete provenance.
	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance: tier=%q reason=%q evidence=%v",
				e.ID(), e.Tier(), e.Reason(), e.Evidence())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.ts:") {
				t.Errorf("edge %s evidence %q is not file:line for the fixture", e.ID(), ev)
			}
		}
	}
}

// TestExtractTS_PendingRefs asserts the extractor RECORDS (never resolves/fabricates)
// the cross-module selector use util.log: it becomes a selector PendingRef, no
// cross-module edge is emitted, and the intra-file edges remain intact.
func TestExtractTS_PendingRefs(t *testing.T) {
	res := parseTSFixtureResult(t)

	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && p.Selector && p.SelectorBase == "util" && p.Name == "log" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		// No PendingRef may carry/fabricate a NodeId — the struct has none; assert
		// FromQN/Name are always populated (an inert record).
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for util.log, got %+v", res.PendingRefs)
	}

	// The recording must not have emitted any cross-module/selector edge: every edge
	// is still an intra-file defines/calls/references edge.
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
	if !has(id("shop.run"), id("shop.price"), goEdgeCalls) {
		t.Error("intra-file calls edge run -> price regressed")
	}
	if !has(id("shop.price"), id("shop.Cart"), goEdgeReferences) {
		t.Error("intra-file references edge price -> Cart regressed")
	}
}

// TestExtractTS_Imports asserts imports are recorded (alias + path) and surfaced in
// References for the reverse-dependency cascade, mirroring the Go path.
func TestExtractTS_Imports(t *testing.T) {
	res := parseTSFixtureResult(t)

	wantPaths := map[string]string{ // path -> alias
		"./log": "Logger", // named import binds the imported name as alias
		"util":  "util",   // namespace import binds the local alias
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

// TestExtractTS_Deterministic mirrors TestExtractGo_Deterministic AND adds the
// mandatory concurrent arm (32 goroutines, runnable under -race): identical input
// must yield byte-identical node/edge IDs and ordering across repeated and concurrent
// parses, because the extractor is a pure transform over an already-parsed CST.
func TestExtractTS_Deterministic(t *testing.T) {
	n1, e1 := parseTSFixture(t)
	n2, e2 := parseTSFixture(t)
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

	// Concurrent determinism: many goroutines parsing the same fixture must all agree
	// on the full ordered ID stream (the extractor is a pure transform). Mirrors the
	// mapping_test.go 32-goroutine idStream harness.
	want := idStream(n1, e1)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]string, workers)
	parser := NewTSParser() // one shared parser handed to all goroutines (must be safe)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.ts", []byte(tsGoldenFixture))
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
