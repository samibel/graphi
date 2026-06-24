package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// tomlGoldenFixture is the committed, FROZEN TOML fixture (SW-054). TOML is config:
// `file` + `variable` (top-level pairs) + `type` (table headers) appear;
// function/method/constant are ABSENT BY DESIGN, and there is no import system.
const tomlGoldenFixture = `title = "shop"

[server]
host = "localhost"
port = 8080

[database]
name = "shopdb"
`

func parseTOMLFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseTOMLFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseTOMLFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewTOMLParser().Parse(context.Background(), "shop/config.toml", []byte(tomlGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractTOML_Nodes asserts the EXACT closed node set + kinds.
func TestExtractTOML_Nodes(t *testing.T) {
	nodes, _ := parseTOMLFixture(t)

	want := map[string]model.NodeKind{
		"shop/config.toml": goKindFile,
		"shop.title":       goKindVariable, // top-level pair
		"shop.server":      goKindType,     // [server] table
		"shop.database":    goKindType,     // [database] table
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
	for _, k := range []model.NodeKind{"file", "type", "variable"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"function", "method", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("toml must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "type", "variable":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractTOML_Edges asserts defines edges with file:line provenance.
func TestExtractTOML_Edges(t *testing.T) {
	nodes, edges := parseTOMLFixture(t)

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

	file := id("shop/config.toml")
	defEdge, ok := has(file, id("shop.server"), goEdgeDefines)
	if !ok {
		t.Fatal("missing defines edge file -> server")
	}
	// Provenance: the [server] header is on line 3 (1-based).
	if got := defEdge.Evidence()[0]; got != "shop/config.toml:3" {
		t.Errorf("file->server defines evidence = %q, want %q", got, "shop/config.toml:3")
	}

	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/config.toml:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractTOML_NoImports asserts TOML records no imports (documented absence).
func TestExtractTOML_NoImports(t *testing.T) {
	res := parseTOMLFixtureResult(t)
	if len(res.Imports) != 0 || len(res.References) != 0 {
		t.Errorf("toml has no import system; expected 0 imports/refs, got %+v / %+v", res.Imports, res.References)
	}
}

// TestExtractTOML_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractTOML_Deterministic(t *testing.T) {
	n1, e1 := parseTOMLFixture(t)
	n2, e2 := parseTOMLFixture(t)
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
	parser := NewTOMLParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/config.toml", []byte(tomlGoldenFixture))
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
