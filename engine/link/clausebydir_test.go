package link

import (
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// LINK-002 — the clauseByDir last-write-wins recall defect.
//
// WHAT THIS FILE IS. It is NOT a test of intended behaviour. It is an executable
// PIN of the current WRONG behaviour, following the runKnownDefectRow precedent
// (engine/conformance/changeclass_test.go:1289): a known defect that leaves the
// suite green is a defect nobody re-checks, and internal/testgate carries no
// allowlist (internal/testgate/allowlist.go:2-4), so the only way to keep the
// suite honest AND green is to assert the defect as data. When LINK-002 is
// fixed, every test in this file FAILS WITH INSTRUCTIONS rather than passing
// silently — that is the whole point of the shape.
//
// THE MECHANISM, at engine/link/index.go:223. `Add` assigns
// `idx.clauseByDir[dir] = clause` UNCONDITIONALLY, so a directory whose symbols
// declare more than one package clause retains only the LAST clause the index
// build happened to see. `Build` then seeds methodDirs from that single value
// (index.go:260) and `uniqueMethodInDir` reads through it (index.go:418), so
// every method declared under a LOSING clause is unreachable from
// `receiverMethod` — the sole gate of Go's recv.Method call heuristic
// (engine/link/resolve_go.go:159, its only consumer in the tree).
//
// SHAPE: a RECALL defect (edges that should resolve do not), never a SOUNDNESS
// defect (no wrong edge is emitted). It is therefore not stop-ship under the
// zero-tolerance rule, which binds wrong edges.
//
// THE FIXTURE IS THE COMMONEST GO SHAPE THERE IS: a directory holding
// `package shop` beside an EXTERNAL test package `package shop_test`. Both
// clauses are legal in one directory and both are indexed.
//
// Record: docs/rc/link-002-clause-by-dir-recall.md. NOT fixed here — the fix is
// a product-byte change and carries its own ADR, candidate move and
// re-measurement (D7).

// link002Nodes is the fixture node set: one directory ("shop") declaring TWO
// package clauses, plus an unrelated caller directory ("app").
//
// Order matters and is the subject of TestLink002_BuildIndexOrderInvariantBroken:
// BuildIndex takes a slice, so the caller controls the streaming order exactly,
// which is what makes the defect provable rather than merely inferable.
func link002Nodes(t *testing.T) []model.Node {
	t.Helper()
	return []model.Node{
		mustNode(t, "file", "shop/cart.go", "shop/cart.go"),
		mustNode(t, "type", "shop.Cart", "shop/cart.go"),
		mustNode(t, "method", "shop.Cart.Add", "shop/cart.go"),
		mustNode(t, "method", "shop.Cart.Total", "shop/cart.go"),

		mustNode(t, "file", "shop/cart_test.go", "shop/cart_test.go"),
		mustNode(t, "type", "shop_test.Fixture", "shop/cart_test.go"),
		mustNode(t, "method", "shop_test.Fixture.Reset", "shop/cart_test.go"),

		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "main.run", "app/main.go"),
	}
}

// swapClauseOrder returns the SAME node set with the two clauses' method nodes
// exchanged in position — identical input, different order.
func swapClauseOrder(nodes []model.Node) []model.Node {
	out := make([]model.Node, len(nodes))
	copy(out, nodes)
	// Move the whole shop_test block (indices 4..6) ahead of the shop block
	// (0..3), so "shop" becomes the LAST clause written for dir "shop".
	reordered := []model.Node{out[4], out[5], out[6], out[0], out[1], out[2], out[3], out[7], out[8]}
	return reordered
}

// TestLink002_PinLastWriteWins pins the defect at its source: the directory
// "shop" declares two clauses and clauseByDir keeps exactly one of them.
func TestLink002_PinLastWriteWins(t *testing.T) {
	idx := BuildIndex(link002Nodes(t))

	if got := idx.clauseByDir["shop"]; got != "shop_test" {
		t.Fatalf("LINK-002 PIN BROKEN: clauseByDir[\"shop\"] = %q, want %q.\n"+
			"This pin asserts the CURRENT WRONG BEHAVIOUR of engine/link/index.go:223, "+
			"which keeps only the LAST package clause written for a directory. If you "+
			"just fixed LINK-002 (clauseByDir must hold a SET and uniqueMethodInDir must "+
			"degrade on ambiguity, per engine/typeresolve/pkggraph.go:132-144), this "+
			"failure is EXPECTED: delete this whole file, remove the LINK-002 entry from "+
			"internal/doctor/checks.go and its assertion in checks_test.go, remove the "+
			"readme \"Known limits\" bullet and the docs/language-support.md note, and add "+
			"a dated closing amendment to docs/rc/link-002-clause-by-dir-recall.md.", got, "shop_test")
	}

	// The consequence, stated as the index sees it: the losing clause's methods
	// are absent from the receiverMethod reverse index entirely.
	if len(idx.methodDirs["Add"]) != 0 || len(idx.methodDirs["Total"]) != 0 {
		t.Fatalf("LINK-002 PIN BROKEN: methodDirs has candidates for the LOSING clause's "+
			"methods (Add=%v Total=%v); the defect drops them. See the instructions above.",
			idx.methodDirs["Add"], idx.methodDirs["Total"])
	}
	if want := []string{"shop"}; len(idx.methodDirs["Reset"]) != 1 || idx.methodDirs["Reset"][0] != want[0] {
		t.Fatalf("LINK-002 PIN BROKEN: methodDirs[\"Reset\"] = %v, want %v — the WINNING "+
			"clause's method should still be indexed.", idx.methodDirs["Reset"], want)
	}
}

// TestLink002_ReceiverMethodRecallLoss is AC-2: a receiverMethod lookup for a
// method declared under the LOSING clause returns no candidate, while the same
// lookup under the WINNING clause succeeds.
func TestLink002_ReceiverMethodRecallLoss(t *testing.T) {
	idx := BuildIndex(link002Nodes(t))

	for _, m := range []string{"Add", "Total"} {
		if _, ok := idx.receiverMethod("app", "c", m); ok {
			t.Fatalf("LINK-002 PIN BROKEN: receiverMethod resolved %q, which is declared "+
				"under the LOSING clause %q in dir \"shop\". Under the defect it must NOT "+
				"resolve. If LINK-002 is fixed, see the instructions in "+
				"TestLink002_PinLastWriteWins.", m, "shop")
		}
	}
	if _, ok := idx.receiverMethod("app", "f", "Reset"); !ok {
		t.Fatalf("receiverMethod failed to resolve %q, declared under the WINNING clause "+
			"%q — the fixture no longer isolates LINK-002 and must be repaired before it "+
			"can pin anything.", "Reset", "shop_test")
	}
}

// TestLink002_UserVisibleEdgeLoss drives the SAME loss through the public
// Link path, which is what a user actually observes: `main.run` makes three
// recv.Method calls and exactly ONE `calls` edge survives — the one into the
// external TEST package. The two production calls are dropped silently, with no
// diagnostic and no skip the user can see.
//
// This is the hermetic twin of the CLI reproduction recorded in
// docs/rc/link-002-clause-by-dir-recall.md, which produces the same 1-of-3.
func TestLink002_UserVisibleEdgeLoss(t *testing.T) {
	nodes := link002Nodes(t)
	idx := BuildIndex(nodes)

	files := []FileRefs{{
		SourcePath: "app/main.go",
		Dir:        "app",
		Pending: []parse.PendingRef{
			{FromQN: "main.run", Name: "Add", SelectorBase: "c", Kind: "calls", Line: 9, Selector: true},
			{FromQN: "main.run", Name: "Total", SelectorBase: "c", Kind: "calls", Line: 10, Selector: true},
			{FromQN: "main.run", Name: "Reset", SelectorBase: "f", Kind: "calls", Line: 11, Selector: true},
		},
	}}
	_, edges, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("LINK-002 PIN BROKEN: got %d calls edges from three recv.Method calls, "+
			"want exactly 1 (the defect drops the two under the losing clause). If "+
			"LINK-002 is fixed this should be 3 — see the instructions in "+
			"TestLink002_PinLastWriteWins.", len(edges))
	}
	want := nodeIDOfKind(t, nodes, "method", "shop_test.Fixture.Reset", "shop/cart_test.go")
	if edges[0].To() != want {
		t.Fatalf("the surviving edge points at %s, want the WINNING clause's method %s",
			edges[0].To(), want)
	}
}

// TestLink002_BuildIndexOrderInvariantBroken is the sharpest statement of the
// defect, because it contradicts a promise the code makes about itself.
// BuildIndex's doc comment (index.go:272-274) says "identical input (in any
// order) yields an index that resolves identically". It does not: the SAME node
// set in two orders resolves two different edge sets.
//
// It is not a PARITY defect despite that, and the distinction matters: the
// streaming order in production is graphstore.ForEachNode's canonical NodeId
// order, and a NodeId is a content hash, so for a given tree the order — and
// therefore the winning clause — is FIXED. Full and incremental passes agree,
// which is exactly why no parity dispatch can ever surface this and why it needs
// a hermetic pin instead of a matrix row.
func TestLink002_BuildIndexOrderInvariantBroken(t *testing.T) {
	nodes := link002Nodes(t)
	forward := BuildIndex(nodes)
	reversed := BuildIndex(swapClauseOrder(nodes))

	if forward.clauseByDir["shop"] == reversed.clauseByDir["shop"] {
		t.Fatalf("LINK-002 PIN BROKEN: both orders produced clauseByDir[\"shop\"]=%q. The "+
			"defect is that the winner is ORDER-DEPENDENT; if that is no longer true, "+
			"LINK-002 is fixed — see the instructions in TestLink002_PinLastWriteWins.",
			forward.clauseByDir["shop"])
	}

	_, fwdAdd := forward.receiverMethod("app", "c", "Add")
	_, revAdd := reversed.receiverMethod("app", "c", "Add")
	_, fwdReset := forward.receiverMethod("app", "f", "Reset")
	_, revReset := reversed.receiverMethod("app", "f", "Reset")
	if fwdAdd || !revAdd || !fwdReset || revReset {
		t.Fatalf("LINK-002 PIN BROKEN: expected the two orders to resolve EXACTLY the "+
			"opposite methods; got forward(Add=%v Reset=%v) reversed(Add=%v Reset=%v). "+
			"See the instructions in TestLink002_PinLastWriteWins.",
			fwdAdd, fwdReset, revAdd, revReset)
	}
}

// TestLink002_Deterministic is AC-3: five consecutive runs over identical input
// give an identical result. The defect is asserted as DETERMINISTIC, and a
// flickering reproduction would not be a reproduction — it would be a different,
// worse finding (a parity defect), so this is load-bearing rather than
// decorative.
func TestLink002_Deterministic(t *testing.T) {
	const runs = 5
	var first string
	for i := 0; i < runs; i++ {
		idx := BuildIndex(link002Nodes(t))
		_, add := idx.receiverMethod("app", "c", "Add")
		_, total := idx.receiverMethod("app", "c", "Total")
		_, reset := idx.receiverMethod("app", "f", "Reset")
		got := idx.clauseByDir["shop"] + "|" +
			boolStr(add) + boolStr(total) + boolStr(reset)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d differs from run 1: %q vs %q — LINK-002 is NOT deterministic "+
				"over identical input, which upgrades it from a recall defect to a parity "+
				"defect and invalidates its published record "+
				"(docs/rc/link-002-clause-by-dir-recall.md).", i+1, got, first)
		}
	}
	if first != "shop_test|falsefalsetrue" {
		t.Fatalf("the pinned deterministic outcome changed: %q", first)
	}
}

// TestLink002_ConfinedToReceiverMethod pins the BLAST RADIUS claim the published
// record makes, so the record cannot silently go stale. clauseByDir has exactly
// two readers — Build (methodDirs) and uniqueMethodInDir — and the OTHER
// directory→package tables are keyed on byClause, not on clauseByDir, so
// cross-package selector resolution, package-file targeting and the reverse-dep
// translation are all unaffected by the collision.
//
// It is also the LINK-001 (ADR 0011) interaction, pinned: packageFileNodes now
// takes a read-time extension filter, and that filter admits `.go` for Go minus
// `_test.go` — but it reads fileNodesByDir, NOT clauseByDir, so ADR 0011's
// `_test.go` ruling does not reach this path. The test file's SYMBOLS still
// capture the directory's clause.
func TestLink002_ConfinedToReceiverMethod(t *testing.T) {
	idx := BuildIndex(link002Nodes(t))

	// crossPackage is byClause-keyed: the losing clause's symbols are still there.
	if _, ok := idx.crossPackage("shop", "Add"); !ok {
		t.Errorf("crossPackage lost \"Add\": the collision has spread beyond clauseByDir, "+
			"which would make LINK-002's published blast radius (%s) wrong",
			"docs/rc/link-002-clause-by-dir-recall.md")
	}
	// sameDir is byDir-keyed and equally unaffected.
	if _, ok := idx.sameDir("shop", "Add"); !ok {
		t.Errorf("sameDir lost \"Add\": see above")
	}
	// packageFileNodes is fileNodesByDir-keyed. ADR 0011's filter is what removes
	// cart_test.go here — and it removes it as an imports TARGET only, never from
	// the clause table that LINK-002 corrupts.
	got := idx.packageFileNodes("shop", func(p string) bool {
		return len(p) > 3 && p[len(p)-3:] == ".go" && !hasTestSuffix(p)
	})
	if len(got) != 1 {
		t.Errorf("packageFileNodes returned %d targets, want 1 (shop/cart.go): ADR 0011's "+
			"filter is independent of the clause collision", len(got))
	}
}

func hasTestSuffix(p string) bool {
	const s = "_test.go"
	return len(p) >= len(s) && p[len(p)-len(s):] == s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// nodeIDOfKind is nodeID's kind-aware twin: the fixture's method nodes are not
// "function" nodes, and a NodeId is derived from the kind as well as the name.
func nodeIDOfKind(t *testing.T, nodes []model.Node, kind, qn, src string) model.NodeId {
	t.Helper()
	want := mustNode(t, kind, qn, src).ID()
	for _, n := range nodes {
		if n.ID() == want {
			return n.ID()
		}
	}
	t.Fatalf("node %q %q@%q not in set", kind, qn, src)
	return ""
}
