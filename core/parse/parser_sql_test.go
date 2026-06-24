package parse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// sqlGoldenFixture is the committed, FROZEN SQL fixture (SW-054). SQL is
// statement-oriented: only `type` (tables/views) maps onto the frozen vocabulary, so
// `function`, `method`, `variable`, and `constant` are ABSENT BY DESIGN. SQL has NO
// import system, so Imports/References are empty BY DESIGN (documented, not fabricated).
const sqlGoldenFixture = `CREATE TABLE cart (id INT);

CREATE TABLE orders (id INT);

CREATE VIEW report AS SELECT id FROM cart;
`

func parseSQLFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseSQLFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseSQLFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewSQLParser().Parse(context.Background(), "shop/schema.sql", []byte(sqlGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractSQL_Nodes asserts the EXACT closed node set + kinds; only file+type appear.
func TestExtractSQL_Nodes(t *testing.T) {
	nodes, _ := parseSQLFixture(t)

	want := map[string]model.NodeKind{
		"shop/schema.sql": goKindFile,
		"shop.cart":       goKindType,
		"shop.orders":     goKindType,
		"shop.report":     goKindType,
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
			t.Errorf("sql must not emit %q (absent by design)", bad)
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

// TestExtractSQL_Edges asserts intra-file defines + a view->table references edge with
// use-site provenance.
func TestExtractSQL_Edges(t *testing.T) {
	nodes, edges := parseSQLFixture(t)

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

	file := id("shop/schema.sql")
	for _, qn := range []string{"shop.cart", "shop.orders", "shop.report"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	refEdge, ok := has(id("shop.report"), id("shop.cart"), goEdgeReferences)
	if !ok {
		t.Fatal("missing references edge report -> cart (view FROM table)")
	}
	// Use-site: the FROM cart reference is on line 5 (1-based).
	if got := refEdge.Evidence()[0]; got != "shop/schema.sql:5" {
		t.Errorf("report->cart references evidence = %q, want %q (use-site file:line pin)", got, "shop/schema.sql:5")
	}

	for _, e := range edges {
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/schema.sql:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractSQL_NoImports asserts SQL records NO imports (documented absence, not a
// fabricated selector/import).
func TestExtractSQL_NoImports(t *testing.T) {
	res := parseSQLFixtureResult(t)
	if len(res.Imports) != 0 {
		t.Errorf("sql has no import system; expected 0 imports, got %+v", res.Imports)
	}
	if len(res.References) != 0 {
		t.Errorf("sql has no import system; expected 0 References, got %+v", res.References)
	}
	for _, p := range res.PendingRefs {
		if p.Selector {
			t.Errorf("sql must not emit selector PendingRefs: %+v", p)
		}
	}
}

// TestExtractSQL_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractSQL_Deterministic(t *testing.T) {
	n1, e1 := parseSQLFixture(t)
	n2, e2 := parseSQLFixture(t)
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
	parser := NewSQLParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/schema.sql", []byte(sqlGoldenFixture))
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
