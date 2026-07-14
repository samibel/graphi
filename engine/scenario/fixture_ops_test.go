package scenario

// SW-122 (EVAL-01): unit coverage for the runner operations added for the
// hero-task suite — callees, neighborhood, impact, and index — over a
// hand-built graph (file defines A/B/C; A calls B calls C), so the dispatch
// and evidence rendering are pinned independently of any corpus fixture.

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
)

// opsEngine builds a FixtureEngine over the chain graph.
func opsEngine(t *testing.T) *FixtureEngine {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	mustNode := func(kind, qn string, line int) model.Node {
		n, err := model.NewNode(kind, qn, "f.go", line, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", qn, err)
		}
		return n
	}
	mustEdge := func(from, to model.Node, kind string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, model.TierConfirmed, 1, "test", []string{"f.go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s): %v", from.QualifiedName(), to.QualifiedName(), err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	file := mustNode("file", "f.go", 1)
	a := mustNode("function", "p.A", 2)
	b := mustNode("function", "p.B", 5)
	c := mustNode("function", "p.C", 8)
	mustEdge(file, a, "defines")
	mustEdge(file, b, "defines")
	mustEdge(file, c, "defines")
	mustEdge(a, b, "calls")
	mustEdge(b, c, "calls")

	return NewFixtureEngine(resolve.Deps{Query: query.New(store)})
}

func invokeLines(t *testing.T, e *FixtureEngine, op string, args map[string]string) []string {
	t.Helper()
	lines, _, err := e.Invoke(op, args)
	if err != nil {
		t.Fatalf("Invoke(%s): %v", op, err)
	}
	return lines
}

func requireLine(t *testing.T, lines []string, needle string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return
		}
	}
	t.Fatalf("no evidence line contains %q; got %v", needle, lines)
}

func forbidLine(t *testing.T, lines []string, needle string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, needle) {
			t.Fatalf("evidence line unexpectedly contains %q: %q", needle, l)
		}
	}
}

func TestFixtureEngine_Callees(t *testing.T) {
	e := opsEngine(t)
	lines := invokeLines(t, e, OpCallees, map[string]string{"symbol": "p.A"})
	requireLine(t, lines, outcomeMarker+"found")
	requireLine(t, lines, "p.B")
	forbidLine(t, lines, "p.C") // one hop only

	lines = invokeLines(t, e, OpCallees, map[string]string{"symbol": "p.NoSuch"})
	requireLine(t, lines, outcomeMarker+"not_found")
}

func TestFixtureEngine_Neighborhood(t *testing.T) {
	e := opsEngine(t)
	lines := invokeLines(t, e, OpNeighborhood, map[string]string{"symbol": "p.C", "depth": "1"})
	requireLine(t, lines, outcomeMarker+"found")
	requireLine(t, lines, "p.B")
	forbidLine(t, lines, "p.A") // two hops away

	lines = invokeLines(t, e, OpNeighborhood, map[string]string{"symbol": "p.C", "depth": "2"})
	requireLine(t, lines, "p.A")
}

func TestFixtureEngine_Impact(t *testing.T) {
	e := opsEngine(t)
	// Reverse (default): blast radius of C climbs the chain to A at depth 2.
	lines := invokeLines(t, e, OpImpact, map[string]string{"symbol": "p.C"})
	requireLine(t, lines, outcomeMarker+"found")
	requireLine(t, lines, "p.B")
	requireLine(t, lines, "depth=2")

	// Forward: dependencies of A reach C.
	lines = invokeLines(t, e, OpImpact, map[string]string{"symbol": "p.A", "direction": "forward"})
	requireLine(t, lines, "p.C")
}

func TestFixtureEngine_Index(t *testing.T) {
	e := opsEngine(t)
	lines := invokeLines(t, e, OpIndex, nil)
	requireLine(t, lines, outcomeMarker+"found")
	requireLine(t, lines, "indexed nodes=4 edges=5 files=1")
	requireLine(t, lines, "file f.go")
}
