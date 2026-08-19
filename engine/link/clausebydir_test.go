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
// fixed, the six pins named below FAIL WITH INSTRUCTIONS rather than passing
// silently — that is the whole point of the shape.
//
// NOT every test here is such a pin, and the difference is stated so a future
// fixer does not "fix" the wrong failure. The six LINK-002 defect pins are
// PinLastWriteWins, ReceiverMethodRecallLoss, UserVisibleEdgeLoss,
// BuildIndexOrderInvariantBroken, Deterministic and RedirectsToWrongDeclaration;
// all six were verified to go red under a simulated fix. The other four are
// deliberately different: ConfinedToReceiverMethod pins the blast RADIUS,
// RedirectIsCausedByTheCollision is the substitution's counterfactual,
// ResolverAbstainsWhenItCanSeeBoth pins the CORRECT behaviour LINK-002 defeats,
// and TestLink003_BareNameShadowing pins a DIFFERENT defect. All four stay GREEN
// through a LINK-002 fix — measured, not assumed.
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
// SHAPE: BOTH a recall and a soundness defect. This comment claimed the opposite
// in its first draft ("never a SOUNDNESS defect (no wrong edge is emitted)"),
// and that was FALSE — see TestLink002_RedirectsToWrongDeclaration below. The
// two effects are:
//
//   - DROP, when the winning clause declares no method of that bare name:
//     uniqueMethodInDir misses and no edge is emitted. This is the recall half
//     and it is what the record's 136 / 6.9 % figure counts.
//   - SUBSTITUTE, when the winning clause DOES declare that bare name on an
//     unrelated type: the edge is emitted pointing at the WRONG declaration.
//     Hiding a clause manufactures FALSE UNIQUENESS and thereby defeats
//     receiverMethod's own frozen skip-on-ambiguity rule (index.go:415-417),
//     turning a mandated abstention into a confident wrong edge.
//
// Because a wrong edge IS emitted, the stop-ship question is REOPENED and is the
// owner's to answer — D5 ("a wrong edge is stop-ship") is stated unqualified and
// has never been tested against the `heuristic` tier. §9 of the record.
//
// LINK-003, filed alongside: byClause[clause][dir][bare] is written
// unconditionally too and has no dirAmbiguous companion, so two methods sharing
// a bare name in ONE package shadow each other with no clause collision at all.
// Same mechanism, ~5x the surface (663 of 1979 = 33.5 %, against 136 = 6.9 % for
// LINK-002 alone). Pinned by TestLink003_BareNameShadowing. NOT fixed here.
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

// link002RedirectNodes is the SUBSTITUTION fixture. It differs from
// link002Nodes in exactly one way that matters: the two clauses declare the SAME
// bare method name ("Reset"). That is what turns a dropped edge into a
// redirected one, and it is the shape the record's first draft denied could
// exist.
//
// The caller's receiver is a *shop.Cart by construction, so the ONLY correct
// target is shop.Cart.Reset. The shop_test block is placed LAST so it takes the
// last write, matching the CLI reproduction in §3.2 of the record.
func link002RedirectNodes(t *testing.T) []model.Node {
	t.Helper()
	return []model.Node{
		mustNode(t, "file", "shop/cart.go", "shop/cart.go"),
		mustNode(t, "type", "shop.Cart", "shop/cart.go"),
		mustNode(t, "method", "shop.Cart.Reset", "shop/cart.go"),

		mustNode(t, "file", "shop/cart_test.go", "shop/cart_test.go"),
		mustNode(t, "type", "shop_test.Fixture", "shop/cart_test.go"),
		mustNode(t, "method", "shop_test.Fixture.Reset", "shop/cart_test.go"),

		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "main.run", "app/main.go"),
	}
}

// TestLink002_RedirectsToWrongDeclaration is the SOUNDNESS pin, added in review
// round 1 of SW-168 because the other pins in this file all pin DROPPING and
// this class would therefore have survived a fix silently.
//
// It asserts the current WRONG behaviour: a `calls` edge is emitted, and it
// points at a method on a DIFFERENT type in a DIFFERENT package in a test file.
// Reproduced end-to-end through the CLI under `-profile fast`; §3.2 of
// docs/rc/link-002-clause-by-dir-recall.md carries that output.
func TestLink002_RedirectsToWrongDeclaration(t *testing.T) {
	nodes := link002RedirectNodes(t)
	idx := BuildIndex(nodes)

	wrong := nodeIDOfKind(t, nodes, "method", "shop_test.Fixture.Reset", "shop/cart_test.go")
	right := nodeIDOfKind(t, nodes, "method", "shop.Cart.Reset", "shop/cart.go")

	files := []FileRefs{{
		SourcePath: "app/main.go",
		Dir:        "app",
		Pending: []parse.PendingRef{
			{FromQN: "main.run", Name: "Reset", SelectorBase: "c", Kind: "calls", Line: 5, Selector: true},
		},
	}}
	_, edges, _, err := New().Link("go", files, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("LINK-002 PIN BROKEN: got %d calls edges, want exactly 1. Under the "+
			"defect the call IS resolved — to the wrong declaration. If LINK-002 is "+
			"fixed, the correct behaviour here is to ABSTAIN (0 edges): two distinct "+
			"\"Reset\" nodes become visible in dir \"shop\", which is exactly the "+
			"ambiguity receiverMethod's frozen rule (index.go:415-417) mandates "+
			"skipping on. See the instructions in TestLink002_PinLastWriteWins.",
			len(edges))
	}
	if edges[0].To() != wrong {
		t.Fatalf("LINK-002 PIN BROKEN: the emitted edge points at %s; under the defect it "+
			"must point at the WRONG declaration %s (shop_test.Fixture.Reset). If it now "+
			"points at %s (shop.Cart.Reset) or the edge is gone, the substitution class is "+
			"fixed — see the instructions in TestLink002_PinLastWriteWins.",
			edges[0].To(), wrong, right)
	}
	if edges[0].To() == right {
		t.Fatalf("unreachable: wrong and right resolved to the same node id")
	}
}

// TestLink002_RedirectIsCausedByTheCollision is the counterfactual half of the
// substitution finding, and it is what makes "redirect" a defensible word rather
// than a characterisation of a loose heuristic. Remove ONLY the colliding clause
// and the SAME call site resolves to the CORRECT method.
func TestLink002_RedirectIsCausedByTheCollision(t *testing.T) {
	all := link002RedirectNodes(t)
	// Drop the shop_test block (indices 3..5) — nothing else changes.
	withoutCollision := []model.Node{all[0], all[1], all[2], all[6], all[7]}
	idx := BuildIndex(withoutCollision)

	right := nodeIDOfKind(t, all, "method", "shop.Cart.Reset", "shop/cart.go")
	got, ok := idx.receiverMethod("app", "c", "Reset")
	if !ok || got != right {
		t.Fatalf("without the collision, receiverMethod resolved (%s, ok=%v) but must "+
			"resolve to shop.Cart.Reset (%s). The fixture no longer isolates the "+
			"redirection and must be repaired before it can pin anything.", got, ok, right)
	}
}

// TestLink002_ResolverAbstainsWhenItCanSeeBoth proves the mechanism claim rather
// than asserting it: receiverMethod's frozen skip-on-ambiguity rule IS live.
// Place the two same-named methods in two DIFFERENT directories, where LINK-002
// hides neither, and the resolver abstains — which is what it is required to do
// in the fixture above too, and cannot, because LINK-002 has hidden one of the
// two candidates from it.
//
// This test does NOT pin a defect. It pins the correct behaviour that LINK-002
// defeats, so it must stay GREEN through the fix.
func TestLink002_ResolverAbstainsWhenItCanSeeBoth(t *testing.T) {
	idx := BuildIndex([]model.Node{
		mustNode(t, "file", "pkg/a.go", "pkg/a.go"),
		mustNode(t, "type", "pkg.A", "pkg/a.go"),
		mustNode(t, "method", "pkg.A.String", "pkg/a.go"),

		mustNode(t, "file", "other/b.go", "other/b.go"),
		mustNode(t, "type", "other.B", "other/b.go"),
		mustNode(t, "method", "other.B.String", "other/b.go"),

		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "main.run", "app/main.go"),
	})

	if id, ok := idx.receiverMethod("app", "a", "String"); ok {
		t.Fatalf("receiverMethod resolved to %s, but TWO distinct \"String\" methods are "+
			"visible to it and its frozen rule (index.go:415-417) requires a "+
			"deterministic SKIP on ambiguity. If this rule has changed, the mechanism "+
			"argument in §3.2 of docs/rc/link-002-clause-by-dir-recall.md — that "+
			"LINK-002 manufactures FALSE UNIQUENESS and converts a mandated abstention "+
			"into a wrong edge — no longer holds and the record must be corrected.", id)
	}
}

// TestLink003_BareNameShadowing pins LINK-003, the sibling defect surfaced by
// SW-168's review: `idx.byClause[clause][dir][bare] = n.ID()` (index.go:272) is
// ALSO written unconditionally, and unlike byDir it has NO dirAmbiguous
// companion, so uniqueMethodInDir cannot see the collision.
//
// One package, one directory, one clause — no LINK-002 involvement whatsoever —
// and two methods sharing a bare name shadow each other. Reproduced through the
// CLI under `-profile fast`; §10 of the record carries that output. Measured at
// 663 of 1979 (33.5 %) unreachable-or-shadowed on graphi's own tree, against 136
// (6.9 %) for LINK-002 alone.
//
// NOT fixed here, and deliberately NOT folded into LINK-002's fix scope by this
// story — but a fix that makes clauseByDir hold a set and stops here will leave
// this defect standing, which is why it is pinned beside its sibling.
func TestLink003_BareNameShadowing(t *testing.T) {
	nodes := []model.Node{
		mustNode(t, "file", "pkg/a.go", "pkg/a.go"),
		mustNode(t, "type", "pkg.A", "pkg/a.go"),
		mustNode(t, "method", "pkg.A.String", "pkg/a.go"),

		mustNode(t, "file", "pkg/b.go", "pkg/b.go"),
		mustNode(t, "type", "pkg.B", "pkg/b.go"),
		mustNode(t, "method", "pkg.B.String", "pkg/b.go"),

		mustNode(t, "file", "app/main.go", "app/main.go"),
		mustNode(t, "function", "main.run", "app/main.go"),
	}
	idx := BuildIndex(nodes)

	// Exactly ONE clause here: LINK-002 is not in play at all.
	if got := idx.clauseByDir["pkg"]; got != "pkg" {
		t.Fatalf("fixture defect: clauseByDir[\"pkg\"] = %q, want \"pkg\" — this test must "+
			"isolate LINK-003 from LINK-002 and no second clause may exist", got)
	}

	a := nodeIDOfKind(t, nodes, "method", "pkg.A.String", "pkg/a.go")
	b := nodeIDOfKind(t, nodes, "method", "pkg.B.String", "pkg/b.go")

	// byClause holds ONE slot for the bare name; the last write wins.
	if got := idx.byClause["pkg"]["pkg"]["String"]; got != b {
		t.Fatalf("LINK-003 PIN BROKEN: byClause[\"pkg\"][\"pkg\"][\"String\"] = %s, want the "+
			"LAST-WRITTEN node %s (pkg.B.String). If byClause now tracks ambiguity, "+
			"LINK-003 is fixed: delete this test, remove the LINK-003 entry from "+
			"internal/doctor/checks.go and its assertion in checks_test.go, remove the "+
			"readme \"Known limits\" bullet and the docs/language-support.md note, "+
			"close the backlog entry in projects/graphi/backlog.md, and add a dated "+
			"closing amendment to §10 of "+
			"docs/rc/link-002-clause-by-dir-recall.md.", got, b)
	}

	// The user-visible consequence: a call on an *A resolves to B's method.
	got, ok := idx.receiverMethod("app", "a", "String")
	if !ok {
		t.Fatalf("LINK-003 PIN BROKEN: receiverMethod abstained. Under the defect it " +
			"resolves — confidently, and to the wrong node. See the instructions above.")
	}
	if got != b {
		t.Fatalf("LINK-003 PIN BROKEN: receiverMethod resolved to %s, want the shadowing "+
			"node %s (pkg.B.String). The correct target for a receiver of type *pkg.A is "+
			"%s, which the defect makes unreachable through this path. See the "+
			"instructions above.", got, b, a)
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
