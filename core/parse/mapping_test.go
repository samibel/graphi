package parse

import (
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// tsFixture is a canonical set of tree-sitter query captures + specs standing in
// for what SW-053's first grammar worker will feed the mapping helper. It models a
// two-function file where one function calls the other and references a type.
func tsFixture() (string, []TSNodeSpec, []TSEdgeSpec, []TSCapture) {
	filename := "shop/cart.ts"
	nodes := []TSNodeSpec{
		{Kind: KindType, QualifiedName: "shop.Cart", Pos: TSPoint{Row: 2, Column: 0}},
		{Kind: KindFunction, QualifiedName: "shop.price", Pos: TSPoint{Row: 5, Column: 0}},
		{Kind: KindFunction, QualifiedName: "shop.checkout", Pos: TSPoint{Row: 9, Column: 0}},
	}
	edges := []TSEdgeSpec{
		{FromQN: "shop.checkout", ToQN: "shop.price", Kind: EdgeCalls, Pos: TSPoint{Row: 10, Column: 4},
			Tier: model.TierDerived, Confidence: 0.9, Reason: "call resolved by query in same file"},
		{FromQN: "shop.price", ToQN: "shop.Cart", Kind: EdgeReferences, Pos: TSPoint{Row: 6, Column: 8},
			Tier: model.TierDerived, Confidence: 0.8, Reason: "type reference resolved by query in same file"},
	}
	captures := []TSCapture{
		{Name: "type.name", Text: "Cart", Start: TSPoint{Row: 2, Column: 5}},
		{Name: "function.name", Text: "price", Start: TSPoint{Row: 5, Column: 9}},
		{Name: "function.name", Text: "checkout", Start: TSPoint{Row: 9, Column: 9}},
	}
	return filename, nodes, edges, captures
}

// TestMapTreeSitter_NodesEdgesProvenance is the dedicated mapping-helper unit test
// (AC #2): a tree-sitter node + canonical query captures fed through the Step-0
// mapping helper produce model.Node/model.Edge values with full provenance and
// file:line evidence.
func TestMapTreeSitter_NodesEdgesProvenance(t *testing.T) {
	filename, nodeSpecs, edgeSpecs, captures := tsFixture()
	nodes, edges, err := MapTreeSitter(filename, "tslang", nodeSpecs, edgeSpecs, captures)
	if err != nil {
		t.Fatalf("MapTreeSitter: %v", err)
	}

	// File node + 3 symbol nodes.
	if len(nodes) != 4 {
		t.Fatalf("node count = %d, want 4", len(nodes))
	}
	if nodes[0].Kind() != KindFile || nodes[0].QualifiedName() != filename {
		t.Errorf("nodes[0] = %q/%q, want file node for %q", nodes[0].Kind(), nodes[0].QualifiedName(), filename)
	}

	byQN := map[string]model.Node{}
	for _, n := range nodes {
		byQN[n.QualifiedName()] = n
	}
	// Node positions are mapped from 0-based TSPoint to 1-based line/column.
	if got := byQN["shop.price"]; got.Line() != 6 || got.Column() != 1 {
		t.Errorf("shop.price line/col = %d/%d, want 6/1", got.Line(), got.Column())
	}

	// 3 defines edges (one per symbol) + 2 relationship edges = 5.
	if len(edges) != 5 {
		t.Fatalf("edge count = %d, want 5", len(edges))
	}

	id := func(qn string) model.NodeId { return byQN[qn].ID() }
	has := func(from, to model.NodeId, kind string) (model.Edge, bool) {
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				return e, true
			}
		}
		return model.Edge{}, false
	}

	fileID := byQN[filename].ID()
	for _, qn := range []string{"shop.Cart", "shop.price", "shop.checkout"} {
		if _, ok := has(fileID, id(qn), EdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	callEdge, ok := has(id("shop.checkout"), id("shop.price"), EdgeCalls)
	if !ok {
		t.Fatal("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.price"), id("shop.Cart"), EdgeReferences); !ok {
		t.Error("missing references edge price -> Cart")
	}

	// Full provenance on every edge: valid tier, non-empty reason, file:line evidence.
	for _, e := range edges {
		if !e.Tier().Valid() {
			t.Errorf("edge %s has invalid tier %q", e.ID(), e.Tier())
		}
		if e.Reason() == "" {
			t.Errorf("edge %s has empty reason", e.ID())
		}
		if len(e.Evidence()) == 0 {
			t.Errorf("edge %s has no evidence", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, filename+":") {
				t.Errorf("edge %s evidence %q is not file:line for %q", e.ID(), ev, filename)
			}
		}
	}
	// The calls edge cites the reference site (row 10 -> line 11), not the def site.
	if got := callEdge.Evidence()[0]; got != filename+":11" {
		t.Errorf("calls edge evidence = %q, want %q", got, filename+":11")
	}
}

// TestMapTreeSitter_NoFabricatedEndpoint asserts the helper refuses to fabricate an
// endpoint: an edge spec whose from/to is not among the node specs is dropped (it is
// PendingRef territory), never silently creating a node.
func TestMapTreeSitter_NoFabricatedEndpoint(t *testing.T) {
	filename := "x/y.ts"
	nodeSpecs := []TSNodeSpec{
		{Kind: KindFunction, QualifiedName: "x.foo", Pos: TSPoint{Row: 0, Column: 0}},
	}
	edgeSpecs := []TSEdgeSpec{
		// Endpoint x.bar is not declared -> must be dropped.
		{FromQN: "x.foo", ToQN: "x.bar", Kind: EdgeCalls, Pos: TSPoint{Row: 1}, Tier: model.TierDerived, Confidence: 0.9, Reason: "r"},
	}
	nodes, edges, err := MapTreeSitter(filename, "tslang", nodeSpecs, edgeSpecs, nil)
	if err != nil {
		t.Fatalf("MapTreeSitter: %v", err)
	}
	// file node + foo; bar was never fabricated.
	if len(nodes) != 2 {
		t.Errorf("node count = %d, want 2 (no fabricated x.bar)", len(nodes))
	}
	// Only the defines edge survives; the unprovable calls edge is dropped.
	if len(edges) != 1 || edges[0].Kind() != EdgeDefines {
		t.Errorf("edges = %v, want a single defines edge", edges)
	}
}

// TestMapTreeSitter_DefinesKindRejected asserts a relationship spec may not steal
// the reserved EdgeDefines kind.
func TestMapTreeSitter_DefinesKindRejected(t *testing.T) {
	nodeSpecs := []TSNodeSpec{
		{Kind: KindFunction, QualifiedName: "x.foo", Pos: TSPoint{Row: 0}},
		{Kind: KindFunction, QualifiedName: "x.bar", Pos: TSPoint{Row: 1}},
	}
	edgeSpecs := []TSEdgeSpec{
		{FromQN: "x.foo", ToQN: "x.bar", Kind: EdgeDefines, Pos: TSPoint{Row: 1}, Tier: model.TierDerived, Confidence: 1, Reason: "r"},
	}
	if _, _, err := MapTreeSitter("x/y.ts", "tslang", nodeSpecs, edgeSpecs, nil); err == nil {
		t.Fatal("expected error for reserved EdgeDefines kind in a relationship spec")
	}
}

// TestMapTreeSitter_Deterministic mirrors TestExtractGo_Deterministic: repeated and
// concurrent invocations yield byte-identical node/edge IDs and ordering.
func TestMapTreeSitter_Deterministic(t *testing.T) {
	filename, nodeSpecs, edgeSpecs, captures := tsFixture()

	run := func() ([]model.Node, []model.Edge) {
		n, e, err := MapTreeSitter(filename, "tslang", nodeSpecs, edgeSpecs, captures)
		if err != nil {
			t.Fatalf("MapTreeSitter: %v", err)
		}
		return n, e
	}

	n1, e1 := run()
	n2, e2 := run()
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

	// Concurrent determinism: many goroutines mapping the same fixture must all
	// agree on the full ordered ID stream (the helper is a pure transform).
	want := idStream(n1, e1)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			n, e := run()
			results[idx] = idStream(n, e)
		}(w)
	}
	wg.Wait()
	for i, got := range results {
		if got != want {
			t.Errorf("worker %d produced a divergent id stream", i)
		}
	}
}

// idStream renders a stable joined ID stream for nodes then edges.
func idStream(nodes []model.Node, edges []model.Edge) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(string(n.ID()))
		b.WriteByte('\n')
	}
	b.WriteByte('|')
	for _, e := range edges {
		b.WriteString(string(e.ID()))
		b.WriteByte('\n')
	}
	return b.String()
}
