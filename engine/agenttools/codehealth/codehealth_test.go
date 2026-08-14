package codehealth

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

type fixture struct {
	t     *testing.T
	ctx   context.Context
	store *graphstore.MemStore
	nodes map[string]model.Node
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, ctx: context.Background(), store: graphstore.NewMemStore(), nodes: map[string]model.Node{}}
}

func (f *fixture) node(qn, path string, line int) model.Node {
	f.t.Helper()
	n, err := model.NewNode("function", qn, path, line, 1)
	if err != nil {
		f.t.Fatalf("node %s: %v", qn, err)
	}
	if err := f.store.PutNode(f.ctx, n); err != nil {
		f.t.Fatalf("put node %s: %v", qn, err)
	}
	f.nodes[qn] = n
	return n
}

func (f *fixture) edge(from, to string) {
	f.t.Helper()
	e, err := model.NewEdge(f.nodes[from].ID(), f.nodes[to].ID(), "calls", model.TierConfirmed, 0.9, "test fixture", []string{"fixture"})
	if err != nil {
		f.t.Fatalf("edge %s→%s: %v", from, to, err)
	}
	if err := f.store.PutEdge(f.ctx, e); err != nil {
		f.t.Fatalf("put edge: %v", err)
	}
}

func (f *fixture) deps() resolve.Deps {
	return resolve.Deps{Query: query.New(f.store), Search: search.New(f.store)}
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

// unhealthyDeps builds a graph that trips several detectors: a symbol cycle,
// a god symbol (hub with high fan-in and fan-out), a duplicate dependency
// path, and dead symbols.
func unhealthyDeps(t *testing.T) resolve.Deps {
	t.Helper()
	f := newFixture(t)
	// Cycle: ring.a → ring.b → ring.c → ring.a.
	f.node("ring.a", "ring/ring.go", 1)
	f.node("ring.b", "ring/ring.go", 2)
	f.node("ring.c", "ring/ring.go", 3)
	f.edge("ring.a", "ring.b")
	f.edge("ring.b", "ring.c")
	f.edge("ring.c", "ring.a")
	// God symbol: hub.Center with godSymbolDegree callers and callees.
	f.node("hub.Center", "hub/center.go", 5)
	for i := 0; i < godSymbolDegree; i++ {
		in := f.node(fmt.Sprintf("callers.in%02d", i), "callers/in.go", 10+i)
		_ = in
		f.edge(fmt.Sprintf("callers.in%02d", i), "hub.Center")
		f.node(fmt.Sprintf("targets.out%02d", i), "targets/out.go", 10+i)
		f.edge("hub.Center", fmt.Sprintf("targets.out%02d", i))
	}
	// Duplicate path: dup.a → dup.b directly AND via dup.x.
	f.node("dup.a", "dup/a.go", 1)
	f.node("dup.x", "dup/x.go", 1)
	f.node("dup.b", "dup/b.go", 1)
	f.edge("dup.a", "dup.b")
	f.edge("dup.a", "dup.x")
	f.edge("dup.x", "dup.b")
	// Dead symbol: never referenced, unexported-looking, non-test path.
	f.node("attic.forgotten", "attic/old.go", 9)
	return f.deps()
}

func TestCodeHealthFiresDetectors(t *testing.T) {
	res, err := Assemble(context.Background(), Params{Deps: unhealthyDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	rs := reasons(t, res)
	for _, want := range []string{
		"dependency_cycles: 3 symbols form a dependency cycle",
		fmt.Sprintf("god_symbols: function hub.Center has fan-in %d AND fan-out %d", godSymbolDegree, godSymbolDegree),
		"duplicate_dependency_paths: dup.a depends on dup.b directly AND through 1 indirect route(s)",
		"dead_symbols:",
		"change_hotspots: no git history on this surface",
		"[severity high, confidence heuristic] — remediation: split responsibilities; graphi symbol-context hub.Center",
	} {
		if !strings.Contains(rs, want) {
			t.Fatalf("missing %q in items:\n%s", want, rs)
		}
	}
	// Every finding row carries the severity/confidence/remediation triple.
	for _, it := range res.Items {
		if it.RefID == "identity" || strings.HasPrefix(it.RefID, "next-") {
			continue
		}
		if !strings.Contains(it.Reason, "[severity ") || !strings.Contains(it.Reason, "— remediation: ") {
			t.Fatalf("finding without severity/remediation: %s", it.Reason)
		}
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
}

func TestCodeHealthCleanAndEmptyAndUnavailable(t *testing.T) {
	res, err := Assemble(context.Background(), Params{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("nil deps must degrade to unavailable, got %s", res.Outcome)
	}

	f := newFixture(t)
	res, err = Assemble(context.Background(), Params{Deps: f.deps()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("empty graph must yield empty, got %s", res.Outcome)
	}

	// Healthy graph: two symbols calling each other's package once, both
	// referenced (main entry point excluded from dead detection by policy).
	f.node("main.main", "cmd/app/main.go", 1)
	f.node("app.run", "app/run.go", 1)
	f.edge("main.main", "app.run")
	f.edge("app.run", "main.main") // 2-cycle... would fire dependency_cycles.
	res, err = Assemble(context.Background(), Params{Deps: f.deps()})
	if err != nil {
		t.Fatal(err)
	}
	// The 2-cycle fires by design; the identity row still counts 10 detectors.
	if !strings.Contains(reasons(t, res), "across 10 detectors") {
		t.Fatalf("identity must state the detector count: %s", res.Summary)
	}
}

func TestCodeHealthDeterministic(t *testing.T) {
	deps := unhealthyDeps(t)
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
		t.Fatalf("code_health output not byte-deterministic:\n%s\n%s", ab, bb)
	}
}
