package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// bashGoldenFixture is the committed, FROZEN Bash fixture (SW-054). Bash has a sparse
// type system: only `function` and `variable` map onto the frozen vocabulary, so
// `method`, `type`, and `constant` are ABSENT BY DESIGN.
//
// Bash has no member/selector call syntax, so the cross-file negative case is a BARE
// unmapped command (`echo`) recorded as a non-selector PendingRef (documented absence
// of a selector form), not a fabricated edge.
const bashGoldenFixture = `#!/bin/bash
source ./lib.sh

TAX=7

price() {
	echo $TAX
}

run() {
	price
	echo done
	run
}
`

func parseBashFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseBashFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseBashFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewBashParser().Parse(context.Background(), "shop/cart.sh", []byte(bashGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractBash_Nodes asserts the EXACT closed node set + kinds; method/type/constant absent.
func TestExtractBash_Nodes(t *testing.T) {
	nodes, _ := parseBashFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.sh": goKindFile,
		"shop.TAX":     goKindVariable,
		"shop.price":   goKindFunction,
		"shop.run":     goKindFunction,
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
	for _, k := range []model.NodeKind{"file", "function", "variable"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"method", "type", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("bash must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "variable":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractBash_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractBash_Edges(t *testing.T) {
	nodes, edges := parseBashFixture(t)

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

	file := id("shop/cart.sh")
	for _, qn := range []string{"shop.TAX", "shop.price", "shop.run"} {
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
	// Use-site: run -> run recursive call on line 13 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.sh:13" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.sh:13")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.sh:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractBash_PendingRefs asserts the unmapped command `echo` becomes a bare
// (non-selector) PendingRef — Bash has no selector call syntax (documented absence).
func TestExtractBash_PendingRefs(t *testing.T) {
	res := parseBashFixtureResult(t)
	var foundBare bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && !p.Selector && p.Name == "echo" && p.Kind == goEdgeCalls {
			foundBare = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
		// Bash emits no selector PendingRefs (no member/selector syntax).
		if p.Selector {
			t.Errorf("bash must not emit selector PendingRefs (no selector syntax): %+v", p)
		}
	}
	if !foundBare {
		t.Errorf("expected a bare PendingRef for unmapped command echo, got %+v", res.PendingRefs)
	}
}

// TestExtractBash_Imports asserts `source` is recorded + surfaced in References.
func TestExtractBash_Imports(t *testing.T) {
	res := parseBashFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "./lib.sh" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected source ./lib.sh, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "./lib.sh" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected ./lib.sh in References, got %+v", res.References)
	}
}

// TestExtractBash_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractBash_Deterministic(t *testing.T) {
	n1, e1 := parseBashFixture(t)
	n2, e2 := parseBashFixture(t)
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
	parser := NewBashParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.sh", []byte(bashGoldenFixture))
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
