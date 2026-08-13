package archintel

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureBuilder assembles a small layered application graph on a MemStore.
type fixtureBuilder struct {
	t     *testing.T
	ctx   context.Context
	store *graphstore.MemStore
	nodes map[string]model.Node
}

func newFixture(t *testing.T) *fixtureBuilder {
	t.Helper()
	return &fixtureBuilder{t: t, ctx: context.Background(), store: graphstore.NewMemStore(), nodes: map[string]model.Node{}}
}

func (f *fixtureBuilder) node(qn, path string) {
	f.t.Helper()
	n, err := model.NewNode("function", qn, path, 1, 1)
	if err != nil {
		f.t.Fatalf("node %s: %v", qn, err)
	}
	if err := f.store.PutNode(f.ctx, n); err != nil {
		f.t.Fatalf("put node %s: %v", qn, err)
	}
	f.nodes[qn] = n
}

// group creates three symbols <pkg>.A/<pkg>.B/<pkg>.C in <pkg>/<pkg>.go and
// densely wires them (3 intra edges) so Louvain keeps them together.
func (f *fixtureBuilder) group(pkg string) {
	f.t.Helper()
	for _, s := range []string{"A", "B", "C"} {
		f.node(pkg+"."+s, pkg+"/"+pkg+".go")
	}
	f.edge(pkg+".A", pkg+".B", 0.9)
	f.edge(pkg+".B", pkg+".C", 0.9)
	f.edge(pkg+".A", pkg+".C", 0.9)
}

func (f *fixtureBuilder) edge(from, to string, conf float64) {
	f.t.Helper()
	e, err := model.NewEdge(f.nodes[from].ID(), f.nodes[to].ID(), "calls", model.TierConfirmed, conf, "test fixture", []string{"fixture"})
	if err != nil {
		f.t.Fatalf("edge %s→%s: %v", from, to, err)
	}
	if err := f.store.PutEdge(f.ctx, e); err != nil {
		f.t.Fatalf("put edge %s→%s: %v", from, to, err)
	}
}

func (f *fixtureBuilder) deps() resolve.Deps {
	return resolve.Deps{Query: query.New(f.store), Search: search.New(f.store)}
}

// layeredDeps builds web → domain → storage with one back-edge
// storage.C → domain.C (against the dominant domain → storage direction).
func layeredDeps(t *testing.T) resolve.Deps {
	t.Helper()
	f := newFixture(t)
	f.group("web")
	f.group("domain")
	f.group("storage")
	// Dominant directions: web depends on domain (2), domain on storage (2).
	f.edge("web.A", "domain.A", 0.5)
	f.edge("web.B", "domain.B", 0.5)
	f.edge("domain.A", "storage.A", 0.5)
	f.edge("domain.B", "storage.B", 0.5)
	// The violation: one edge against domain → storage.
	f.edge("storage.C", "domain.C", 0.5)
	return f.deps()
}

// cycleDeps closes the loop: storage also depends on web dominantly.
func cycleDeps(t *testing.T) resolve.Deps {
	t.Helper()
	f := newFixture(t)
	f.group("web")
	f.group("domain")
	f.group("storage")
	f.edge("web.A", "domain.A", 0.5)
	f.edge("web.B", "domain.B", 0.5)
	f.edge("domain.A", "storage.A", 0.5)
	f.edge("domain.B", "storage.B", 0.5)
	f.edge("storage.A", "web.A", 0.5)
	f.edge("storage.B", "web.B", 0.5)
	return f.deps()
}

func allReasons(t *testing.T, res *contract.Result) string {
	t.Helper()
	var b strings.Builder
	for _, it := range res.Items {
		b.WriteString(it.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

func TestArchitectureLayersByDependencyDirection(t *testing.T) {
	res, err := Assemble(context.Background(), Params{Deps: layeredDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "3 communities") {
		t.Fatalf("expected 3 communities in summary: %q", res.Summary)
	}
	reasons := allReasons(t, res)
	for _, want := range []string{
		"layer 3: web (community",
		"layer 2: domain (community",
		"layer 1: storage (community",
	} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("missing %q in items:\n%s", want, reasons)
		}
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`dependency: web \(community \d+\) → domain \(community \d+\) — 2 edge\(s\)`),
		regexp.MustCompile(`dependency: domain \(community \d+\) → storage \(community \d+\) — 2 edge\(s\) \(reverse 1 — see architecture-violations\)`),
	} {
		if !want.MatchString(reasons) {
			t.Fatalf("missing %s in items:\n%s", want, reasons)
		}
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
}

func TestArchitectureFlagsCyclicCommunities(t *testing.T) {
	res, err := Assemble(context.Background(), Params{Deps: cycleDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	reasons := allReasons(t, res)
	if !strings.Contains(reasons, "in a dependency cycle — run architecture-violations") {
		t.Fatalf("identity row must flag the cycle:\n%s", reasons)
	}
	if !strings.Contains(reasons, "layer ? [cyclic]") {
		t.Fatalf("cyclic communities must not get a layer:\n%s", reasons)
	}
}

func TestArchitectureEmptyAndUnavailable(t *testing.T) {
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
		t.Fatalf("empty graph must yield empty, got %s (%s)", res.Outcome, res.Summary)
	}
}

func TestArchitectureDeterministic(t *testing.T) {
	deps := layeredDeps(t)
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
		t.Fatalf("architecture output not byte-deterministic:\n%s\n%s", ab, bb)
	}
}
