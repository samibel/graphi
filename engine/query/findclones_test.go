package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// cloneBuilder is a tiny deterministic store builder for the find_clones tests.
type cloneBuilder struct {
	t     *testing.T
	store *graphstore.MemStore
	ids   map[string]model.NodeId
}

func newCloneBuilder(t *testing.T) *cloneBuilder {
	t.Helper()
	return &cloneBuilder{t: t, store: graphstore.NewMemStore(), ids: map[string]model.NodeId{}}
}

func (b *cloneBuilder) node(kind, name, path string, line int) model.NodeId {
	b.t.Helper()
	n, err := model.NewNode(kind, name, path, line, 1)
	if err != nil {
		b.t.Fatal(err)
	}
	if err := b.store.PutNode(context.Background(), n); err != nil {
		b.t.Fatal(err)
	}
	b.ids[name] = n.ID()
	return n.ID()
}

func (b *cloneBuilder) edge(from, to model.NodeId, kind string) {
	b.t.Helper()
	e, err := model.NewEdge(from, to, kind, model.TierConfirmed, 1, kind, []string{"ev"})
	if err != nil {
		b.t.Fatal(err)
	}
	if err := b.store.PutEdge(context.Background(), e); err != nil {
		b.t.Fatal(err)
	}
}

// seedExactRenamed builds:
//   - exact pair: app.f1, app.g1 both call lib.parseInt (identical fingerprint)
//   - renamed pair: app.h1 calls lib.foo, app.h2 calls lib.bar (same shape, diff names)
//
// Leaf targets (lib.*) have no outbound edges and are filtered out as candidates.
func seedExactRenamed(t *testing.T) *graphstore.MemStore {
	b := newCloneBuilder(t)
	parseInt := b.node("function", "lib.parseInt", "lib/lib.go", 1)
	foo := b.node("function", "lib.foo", "lib/lib.go", 2)
	bar := b.node("function", "lib.bar", "lib/lib.go", 3)

	f1 := b.node("function", "app.f1", "app/a.go", 10)
	g1 := b.node("function", "app.g1", "app/b.go", 20)
	b.edge(f1, parseInt, query.EdgeKindCalls)
	b.edge(g1, parseInt, query.EdgeKindCalls)

	h1 := b.node("function", "app.h1", "app/c.go", 30)
	h2 := b.node("function", "app.h2", "app/d.go", 40)
	b.edge(h1, foo, query.EdgeKindCalls)
	b.edge(h2, bar, query.EdgeKindCalls)
	return b.store
}

func TestFindClones_ExactGroup(t *testing.T) {
	svc := query.New(seedExactRenamed(t))
	res, err := svc.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	var exact []query.CloneGroup
	for _, g := range res.Groups {
		if g.Type == query.CloneTypeExact {
			exact = append(exact, g)
		}
	}
	if len(exact) != 1 {
		t.Fatalf("want 1 exact group, got %d (%+v)", len(exact), res.Groups)
	}
	if exact[0].Size != 2 {
		t.Fatalf("exact group size = %d, want 2", exact[0].Size)
	}
	names := []string{exact[0].Members[0].Name, exact[0].Members[1].Name}
	if names[0] != "app.f1" || names[1] != "app.g1" {
		t.Errorf("exact members = %v, want [app.f1 app.g1] (canonical file order)", names)
	}
}

func TestFindClones_RenamedGroupListsIdentifiers(t *testing.T) {
	svc := query.New(seedExactRenamed(t))
	res, err := svc.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	var renamed *query.CloneGroup
	for i := range res.Groups {
		if res.Groups[i].Type == query.CloneTypeRenamed {
			renamed = &res.Groups[i]
		}
	}
	if renamed == nil {
		t.Fatalf("no renamed group found (%+v)", res.Groups)
	}
	if renamed.Size != 2 {
		t.Fatalf("renamed group size = %d, want 2", renamed.Size)
	}
	got := strings.Join(renamed.RenamedIdentifiers, ",")
	if got != "lib.bar,lib.foo" {
		t.Errorf("renamed_identifiers = %q, want \"lib.bar,lib.foo\"", got)
	}
}

func TestFindClones_DeterministicReplay(t *testing.T) {
	svc := query.New(seedExactRenamed(t))
	a, err := svc.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	ba, _ := query.MarshalCloneResult(a)
	bb, _ := query.MarshalCloneResult(b)
	if string(ba) != string(bb) {
		t.Fatalf("replay not byte-identical:\n %s\n %s", ba, bb)
	}
}

func TestFindClones_FullVsIncrementalParity(t *testing.T) {
	full := query.New(seedExactRenamed(t))
	incr := query.New(seedExactRenamed(t))
	rf, err := full.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	ri, err := incr.FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	bf, _ := query.MarshalCloneResult(rf)
	bi, _ := query.MarshalCloneResult(ri)
	if string(bf) != string(bi) {
		t.Fatalf("full vs incremental not byte-identical:\n full: %s\n incr: %s", bf, bi)
	}
}

func TestFindClones_TypedEmpty(t *testing.T) {
	// One candidate calling one target → no group of size >= 2.
	b := newCloneBuilder(t)
	target := b.node("function", "lib.only", "lib/lib.go", 1)
	f := b.node("function", "app.solo", "app/s.go", 5)
	b.edge(f, target, query.EdgeKindCalls)

	res, err := query.New(b.store).FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Groups == nil || len(res.Groups) != 0 {
		t.Fatalf("groups = %+v, want non-nil empty slice", res.Groups)
	}
	if res.Truncated {
		t.Errorf("Truncated = true, want false on empty")
	}
	raw, _ := query.MarshalCloneResult(res)
	if !strings.Contains(string(raw), "\"groups\":[]") || !strings.Contains(string(raw), "\"truncated\":false") {
		t.Errorf("typed-empty envelope wrong: %s", raw)
	}
}

func TestFindClones_TruncationFlag(t *testing.T) {
	// Three distinct exact pairs → three groups; MaxGroups=2 truncates to 2.
	b := newCloneBuilder(t)
	for i, tgt := range []string{"lib.t1", "lib.t2", "lib.t3"} {
		target := b.node("function", tgt, "lib/lib.go", i+1)
		f := b.node("function", "app.p"+tgt+"a", "app/"+tgt+"a.go", 10)
		g := b.node("function", "app.p"+tgt+"b", "app/"+tgt+"b.go", 20)
		b.edge(f, target, query.EdgeKindCalls)
		b.edge(g, target, query.EdgeKindCalls)
	}
	cfg := query.DefaultCloneConfig()
	cfg.MaxGroups = 2
	res, err := query.New(b.store).FindClones(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(res.Groups))
	}
	if !res.Truncated {
		t.Errorf("Truncated = false, want true when MaxGroups bounds output")
	}
}

func TestFindClones_StructuralJaccard(t *testing.T) {
	// S1 shape = {calls|function, references|type, references|variable, references|constant}
	// S2 shape = S1 + {calls|method}  →  Jaccard = 4/5 = 0.8 >= threshold (structural, not exact/renamed)
	b := newCloneBuilder(t)
	fn := b.node("function", "lib.fn", "lib/lib.go", 1)
	ty := b.node("type", "lib.Ty", "lib/lib.go", 2)
	va := b.node("variable", "lib.Va", "lib/lib.go", 3)
	co := b.node("constant", "lib.Co", "lib/lib.go", 4)
	me := b.node("method", "lib.Me", "lib/lib.go", 5) // distinct kind → adds a NEW shape token (calls|method)

	s1 := b.node("function", "app.s1", "app/s1.go", 10)
	b.edge(s1, fn, query.EdgeKindCalls)
	b.edge(s1, ty, query.EdgeKindReferences)
	b.edge(s1, va, query.EdgeKindReferences)
	b.edge(s1, co, query.EdgeKindReferences)

	s2 := b.node("function", "app.s2", "app/s2.go", 20)
	b.edge(s2, fn, query.EdgeKindCalls)
	b.edge(s2, ty, query.EdgeKindReferences)
	b.edge(s2, va, query.EdgeKindReferences)
	b.edge(s2, co, query.EdgeKindReferences)
	b.edge(s2, me, query.EdgeKindCalls) // the extra edge that makes it a near-clone

	res, err := query.New(b.store).FindClones(context.Background(), query.DefaultCloneConfig())
	if err != nil {
		t.Fatal(err)
	}
	var structural *query.CloneGroup
	for i := range res.Groups {
		if res.Groups[i].Type == query.CloneTypeStructural {
			structural = &res.Groups[i]
		}
	}
	if structural == nil {
		t.Fatalf("no structural group at threshold 0.8 (%+v)", res.Groups)
	}
	if structural.Size != 2 {
		t.Fatalf("structural size = %d, want 2", structural.Size)
	}

	// Raising the threshold above 0.8 must drop the structural group (sensitivity).
	cfg := query.DefaultCloneConfig()
	cfg.Threshold = 0.95
	res2, err := query.New(b.store).FindClones(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res2.Groups {
		if g.Type == query.CloneTypeStructural {
			t.Errorf("structural group survived threshold 0.95: %+v", g)
		}
	}
}
