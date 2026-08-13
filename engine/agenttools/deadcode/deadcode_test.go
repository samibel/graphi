package deadcode

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds a graph with a clear dead symbol (unexported, no inbound
// edges), an exported dead symbol (lower score), a live symbol, a main
// entrypoint, and a dead-looking symbol on a test path (suppressed/entry
// point).
func fixtureDeps(t *testing.T) resolve.Deps {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}
	edge := func(from, to model.Node) {
		e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierConfirmed, 0.9, "test fixture", []string{"fixture"})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}

	caller := mk("function", "app.run", "app/run.go", 5)
	live := mk("function", "app.helper", "app/helper.go", 9)
	edge(caller, live)
	// app.run has no inbound edges either: two unexported candidates total.
	mk("function", "app.oldHelper", "app/old.go", 3)
	mk("function", "app.OldAPI", "app/old.go", 20)
	mk("function", "main.main", "cmd/app/main.go", 7)
	mk("function", "app.leftover", "app/util_test.go", 11)
	mk("function", "app.init", "app/setup.go", 2)

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func reasons(t *testing.T, res *contract.Result) string {
	t.Helper()
	var b strings.Builder
	for _, it := range res.Items {
		b.WriteString(it.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

func TestDeadCodeScoresAndExcludes(t *testing.T) {
	res, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	rs := reasons(t, res)

	// Unexported dead functions score 100.
	if !strings.Contains(rs, "dead candidate: function app.oldHelper (app/old.go:3) — score 100/100") {
		t.Fatalf("missing full-score unexported candidate:\n%s", rs)
	}
	// The exported symbol is excluded by the public-API suppression, with the
	// roadmap-style reason spelled out.
	if !strings.Contains(rs, "excluded: function app.OldAPI (app/old.go:20) — exported API with no in-graph usage evidence") {
		t.Fatalf("exported symbol must be an exclusion row with reason:\n%s", rs)
	}
	// main and the test-path symbol are excluded WITH reasons, not silently.
	if !strings.Contains(rs, "excluded: function main.main") || !strings.Contains(rs, "framework/language entry point") {
		t.Fatalf("main.main must be an exclusion row:\n%s", rs)
	}
	if !strings.Contains(rs, "app.leftover") || !strings.Contains(rs, "test fixture") {
		t.Fatalf("test-path symbol must appear as exclusion:\n%s", rs)
	}
	// Go init functions are runtime-invoked → exclusion, never a candidate.
	if strings.Contains(rs, "dead candidate: function app.init ") ||
		!strings.Contains(rs, "Go init function") {
		t.Fatalf("init must be excluded with the runtime reason:\n%s", rs)
	}
	// The live symbol never appears as a candidate.
	if strings.Contains(rs, "dead candidate: function app.helper ") {
		t.Fatalf("live symbol flagged dead:\n%s", rs)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
	if res.Confidence.Method != "integer_signal_model" {
		t.Fatalf("unexpected confidence method %q", res.Confidence.Method)
	}
}

func TestDeadCodeCleanAndEmptyAndUnavailable(t *testing.T) {
	// Unavailable.
	res, err := Assemble(context.Background(), Params{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("nil deps must degrade to unavailable, got %s", res.Outcome)
	}

	// Empty graph.
	store := graphstore.NewMemStore()
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	res, err = Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("empty graph must yield empty, got %s (%s)", res.Outcome, res.Summary)
	}

	// Clean graph: every symbol is referenced.
	ctx := context.Background()
	a, _ := model.NewNode("function", "p.a", "p/a.go", 1, 1)
	b, _ := model.NewNode("function", "p.b", "p/b.go", 1, 1)
	if err := store.PutNode(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, b); err != nil {
		t.Fatal(err)
	}
	e1, _ := model.NewEdge(a.ID(), b.ID(), "calls", model.TierConfirmed, 0.9, "t", []string{"x"})
	e2, _ := model.NewEdge(b.ID(), a.ID(), "references", model.TierConfirmed, 0.9, "t", []string{"x"})
	if err := store.PutEdge(ctx, e1); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(ctx, e2); err != nil {
		t.Fatal(err)
	}
	res, err = Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reasons(t, res), "clean: no dead-code candidates") {
		t.Fatalf("clean graph needs the cited clean item: %s", res.Summary)
	}
}

func TestDeadCodeDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	ab, err := contract.Serialize(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := contract.Serialize(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("dead_code output not byte-deterministic:\n%s\n%s", ab, bb)
	}
}
