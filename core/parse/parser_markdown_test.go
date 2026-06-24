package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// mdGoldenFixture is the committed, FROZEN Markdown fixture (SW-054). Markdown is
// markup: only `file` + `type` (ATX headings) appear; function/method/variable/constant
// are ABSENT BY DESIGN, and there is no import system.
const mdGoldenFixture = `# Heading One

some text

## Heading Two

- list item

### Heading Three

more text
`

func parseMDFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseMDFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseMDFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewMarkdownParser().Parse(context.Background(), "shop/readme.md", []byte(mdGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractMD_Nodes asserts the EXACT closed node set + kinds; only file+type appear.
func TestExtractMD_Nodes(t *testing.T) {
	nodes, _ := parseMDFixture(t)

	want := map[string]model.NodeKind{
		"shop/readme.md":     goKindFile,
		"shop.Heading One":   goKindType,
		"shop.Heading Two":   goKindType,
		"shop.Heading Three": goKindType,
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
	for _, k := range []model.NodeKind{"file", "type"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	for _, bad := range []model.NodeKind{"function", "method", "variable", "constant"} {
		if _, ok := emitted[bad]; ok {
			t.Errorf("markdown must not emit %q (absent by design)", bad)
		}
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "type":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractMD_Edges asserts defines edges with file:line provenance.
func TestExtractMD_Edges(t *testing.T) {
	nodes, edges := parseMDFixture(t)

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

	file := id("shop/readme.md")
	defEdge, ok := has(file, id("shop.Heading Two"), goEdgeDefines)
	if !ok {
		t.Fatal("missing defines edge file -> Heading Two")
	}
	// Provenance: `## Heading Two` is on line 5 (1-based).
	if got := defEdge.Evidence()[0]; got != "shop/readme.md:5" {
		t.Errorf("file->Heading Two defines evidence = %q, want %q", got, "shop/readme.md:5")
	}

	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/readme.md:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractMD_NoImports asserts Markdown records no imports (documented absence).
func TestExtractMD_NoImports(t *testing.T) {
	res := parseMDFixtureResult(t)
	if len(res.Imports) != 0 || len(res.References) != 0 {
		t.Errorf("markdown has no import system; expected 0 imports/refs, got %+v / %+v", res.Imports, res.References)
	}
}

// TestExtractMD_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractMD_Deterministic(t *testing.T) {
	n1, e1 := parseMDFixture(t)
	n2, e2 := parseMDFixture(t)
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
	parser := NewMarkdownParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/readme.md", []byte(mdGoldenFixture))
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
