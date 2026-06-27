package analysis

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// putNode is a test helper that constructs and stores a node, returning it.
func putNode(t *testing.T, store *graphstore.MemStore, kind, qn, path string) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qn, path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	return n
}

func putEdge(t *testing.T, store *graphstore.MemStore, from, to model.NodeId) {
	t.Helper()
	e, err := model.NewEdge(from, to, query.EdgeKindCalls, model.TierConfirmed, 1, "r", []string{"e"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

// branchDiffFixture builds base + head states with a KNOWN set of structural
// changes: an added node (B/Z), a removed node (A/Y), a signature/contract change
// (Svc: an interface whose outgoing dependency edge changed Svc→A ⇒ Svc→B), a
// matching edge added + edge removed, and an entity moved across files (Mover:
// old.go ⇒ new.go, a different NodeId correlated into a single move).
func branchDiffFixture(t *testing.T) (base, head *graphstore.MemStore) {
	t.Helper()
	base = graphstore.NewMemStore()
	head = graphstore.NewMemStore()

	// Svc: same identity on both sides (interface = contract node).
	svcB := putNode(t, base, "interface", "p.Svc", "svc.go")
	svcH := putNode(t, head, "interface", "p.Svc", "svc.go")
	if svcB.ID() != svcH.ID() {
		t.Fatal("Svc must share a NodeId across sides")
	}

	// A only in base; B only in head. Svc's outgoing edge flips A → B.
	a := putNode(t, base, "function", "p.A", "a.go")
	putEdge(t, base, svcB.ID(), a.ID()) // Svc → A (base)
	b := putNode(t, head, "function", "p.B", "b.go")
	putEdge(t, head, svcH.ID(), b.ID()) // Svc → B (head)

	// Y removed; Z added (plain add/remove, distinct qualified names → no move).
	putNode(t, base, "function", "p.Y", "y.go")
	putNode(t, head, "function", "p.Z", "z.go")

	// Mover: same kind+qualified-name, different path → different NodeId on each
	// side → correlated into a single moved delta.
	putNode(t, base, "function", "p.Mover", "old.go")
	putNode(t, head, "function", "p.Mover", "new.go")

	return base, head
}

// TestCompareBranches_StructuralDiff (AC-2): the diff is complete and correct with
// NO missing/spurious deltas, the signature/contract change is detected, and the
// cross-file move is correlated.
func TestCompareBranches_StructuralDiff(t *testing.T) {
	base, head := branchDiffFixture(t)
	a := newCompareBranchesAnalyzer()
	res, err := a.Analyze(context.Background(), nil, Params{CompareBase: base, CompareHead: head})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	d := res.BranchDiff
	if d == nil {
		t.Fatal("nil branch diff")
	}
	if d.Outcome != string(query.OutcomeFound) {
		t.Fatalf("outcome = %q, want found", d.Outcome)
	}

	// added: B, Z (NOT the head Mover — that is a move).
	if got := qnSet(nodeQNs(d.Added)); !setEq(got, []string{"p.B", "p.Z"}) {
		t.Fatalf("added = %v, want [p.B p.Z]", got)
	}
	// removed: A, Y (NOT the base Mover).
	if got := qnSet(nodeQNs(d.Removed)); !setEq(got, []string{"p.A", "p.Y"}) {
		t.Fatalf("removed = %v, want [p.A p.Y]", got)
	}
	// changed: exactly Svc, classified as a signature/contract change.
	if len(d.Changed) != 1 {
		t.Fatalf("changed = %+v, want exactly Svc", d.Changed)
	}
	if d.Changed[0].QualifiedName != "p.Svc" || d.Changed[0].ChangeKind != ChangeSignatureContract {
		t.Fatalf("changed[0] = %+v, want p.Svc/%s", d.Changed[0], ChangeSignatureContract)
	}
	// moved: exactly Mover, old.go → new.go, correlated by path-independent identity.
	if len(d.Moved) != 1 {
		t.Fatalf("moved = %+v, want exactly Mover", d.Moved)
	}
	mv := d.Moved[0]
	if mv.QualifiedName != "p.Mover" || mv.FromPath != "old.go" || mv.ToPath != "new.go" {
		t.Fatalf("moved[0] = %+v, want p.Mover old.go→new.go", mv)
	}
	if mv.FromID == mv.ToID {
		t.Fatalf("a move must carry distinct from/to NodeIds: %+v", mv)
	}
	// edges: exactly one added (Svc→B) and one removed (Svc→A).
	if len(d.EdgesAdded) != 1 || len(d.EdgesRemoved) != 1 {
		t.Fatalf("edges added=%v removed=%v, want exactly one each", d.EdgesAdded, d.EdgesRemoved)
	}
}

// TestCompareBranches_Determinism (AC-3): identical states → byte-identical output
// across repeated runs (no map-iteration / wall-clock reliance).
func TestCompareBranches_Determinism(t *testing.T) {
	base, head := branchDiffFixture(t)
	a := newCompareBranchesAnalyzer()
	var first []byte
	for i := 0; i < 8; i++ {
		res, err := a.Analyze(context.Background(), nil, Params{CompareBase: base, CompareHead: head})
		if err != nil {
			t.Fatal(err)
		}
		b, err := MarshalBranchDiff(*res.BranchDiff)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
			continue
		}
		if string(b) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, b)
		}
	}
}

// TestCompareBranches_FullVsIncremental (AC-4): base/head states built with
// different node/edge insertion orders over the same logical content yield
// byte-identical diffs.
func TestCompareBranches_FullVsIncremental(t *testing.T) {
	baseA, headA := branchDiffFixture(t)

	// Rebuild the SAME logical states with reversed insertion order.
	baseB := graphstore.NewMemStore()
	headB := graphstore.NewMemStore()
	moverB := putNode(t, baseB, "function", "p.Mover", "old.go")
	_ = moverB
	putNode(t, baseB, "function", "p.Y", "y.go")
	aB := putNode(t, baseB, "function", "p.A", "a.go")
	svcBB := putNode(t, baseB, "interface", "p.Svc", "svc.go")
	putEdge(t, baseB, svcBB.ID(), aB.ID())

	putNode(t, headB, "function", "p.Mover", "new.go")
	putNode(t, headB, "function", "p.Z", "z.go")
	bB := putNode(t, headB, "function", "p.B", "b.go")
	svcHB := putNode(t, headB, "interface", "p.Svc", "svc.go")
	putEdge(t, headB, svcHB.ID(), bB.ID())

	a := newCompareBranchesAnalyzer()
	r1, _ := a.Analyze(context.Background(), nil, Params{CompareBase: baseA, CompareHead: headA})
	r2, _ := a.Analyze(context.Background(), nil, Params{CompareBase: baseB, CompareHead: headB})
	b1, _ := MarshalBranchDiff(*r1.BranchDiff)
	b2, _ := MarshalBranchDiff(*r2.BranchDiff)
	if string(b1) != string(b2) {
		t.Fatalf("full vs incremental differ:\n%s\n%s", b1, b2)
	}
}

// TestCompareBranches_Degenerate (AC-7): identical states, nil states, and an
// unknown (empty) side each yield a well-defined, stable result without error.
func TestCompareBranches_Degenerate(t *testing.T) {
	ctx := context.Background()
	a := newCompareBranchesAnalyzer()

	// Identical states → empty diff, outcome empty.
	base, _ := branchDiffFixture(t)
	res, err := a.Analyze(ctx, nil, Params{CompareBase: base, CompareHead: base})
	if err != nil {
		t.Fatalf("identical analyze: %v", err)
	}
	if res.BranchDiff.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("identical states want empty outcome, got %q", res.BranchDiff.Outcome)
	}
	b, _ := MarshalBranchDiff(*res.BranchDiff)
	for _, group := range []string{`"added":[]`, `"removed":[]`, `"changed":[]`, `"moved":[]`, `"edges_added":[]`, `"edges_removed":[]`} {
		if !contains(string(b), group) {
			t.Fatalf("identical diff missing empty group %s: %s", group, b)
		}
	}

	// Both nil states → empty diff, no error, no panic.
	res, err = a.Analyze(ctx, nil, Params{})
	if err != nil {
		t.Fatalf("nil/nil analyze: %v", err)
	}
	if res.BranchDiff.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("nil/nil want empty outcome, got %q", res.BranchDiff.Outcome)
	}

	// Unknown head (empty) vs populated base → everything in base is removed,
	// stable, no error.
	base2, _ := branchDiffFixture(t)
	res, err = a.Analyze(ctx, nil, Params{CompareBase: base2, CompareHead: graphstore.NewMemStore()})
	if err != nil {
		t.Fatalf("unknown-head analyze: %v", err)
	}
	if len(res.BranchDiff.Added) != 0 {
		t.Fatalf("unknown head must add nothing, got %v", res.BranchDiff.Added)
	}
	if len(res.BranchDiff.Removed) == 0 {
		t.Fatal("unknown head must remove the base nodes")
	}
}

// TestCompareBranches_ZeroEgress (AC-5): the analyzer source imports no
// network/process/filesystem-egress package.
func TestCompareBranches_ZeroEgress(t *testing.T) {
	assertNoEgressImports(t, "compare_branches.go")
}

// --- small set helpers ---

func nodeQNs(ds []NodeDelta) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.QualifiedName)
	}
	return out
}

func qnSet(s []string) []string { return s }

func setEq(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}
