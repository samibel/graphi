package conformance_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/semantic"
)

// WP-J5: the JVM full-vs-incremental change-class parity gate, driven with the
// experimental JVM binder LIVE (GRAPHI_JVM_TYPERESOLVE=1). It reuses every leaf
// helper of the Go conformance harness — the fixture recorder, the graphView
// witness vocabulary, buildIncrementalParallel, snapshot, parityBackends — with
// a JVM base tree, and it is bound to docs/rc/parity-classes-jvm.yaml by the
// drift guard in jvmparity_matrix_test.go.
//
// The env is set once in the parent and the subtests are NOT parallel:
// testing.T.Setenv forbids parallel ancestors, and determinism (this is a proof
// gate) matters more than the wall clock a hermetic table costs. The ingesters
// buildIncrementalParallel/newIngester construct read the env at construction
// (engine/semantic.NewRegistry), so setting it here reaches the whole pass.
//
// SCOPE, stated so it is not overread: a hermetic proof over t.TempDir()
// fixtures, exactly like TestFullVsIncremental_ByteParity. NOT a PRD §12.3
// gate, NOT the real-repository matrix, and it proves parity for the
// wave-1 SUBSET of JVM change classes the YAML marks required — the rest are
// declared deferred there, not silently skipped.

// jvmBaseTree is the cross-package JVM fixture the confirmed tier needs: a Java
// caller into a Java callee, plus a Kotlin caller into the same Java callee, so
// both binders' edges appear. No go.mod — the go/types pass finds no Go subject
// and skips, leaving the JVM registrants the sole semantic producers.
//
// The base confirmed edges (binder live):
//
//	shop.checkout --calls--> tax.apply   (arity-1, the overload row drops this)
//	shop.checkout --calls--> tax.rate    (arity-0 control)
//	k.run         --calls--> tax.rate    (Kotlin, declared-typed local)
func jvmBaseTree() map[string]string {
	return map[string]string{
		"tax/Rate.java": `package tax;
public class Rate {
    public int apply(int x) { return x; }
    public int rate() { return 7; }
}
`,
		"shop/Cart.java": `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.apply(1) + r.rate(); }
}
`,
		"k/App.kt": `package k
import tax.Rate
class App {
    fun run(r: Rate): Int {
        val typed: Rate = r
        return typed.rate()
    }
}
`,
	}
}

// jvmChangeClassTable is the declarative JVM change-class matrix. Row order
// follows docs/rc/parity-classes-jvm.yaml so the two files diff side by side.
func jvmChangeClassTable() []changeClassRow {
	confirmed := model.TierConfirmed
	return []changeClassRow{
		{
			id:          "jvm_add_file",
			kind:        kindChangeClass,
			description: "A new Java file arrives in a new package: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("util/Util.java", "package util;\npublic class Util { public int help() { return 1; } }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("util.help"),
					g.requirePresent("shop.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "jvm_modify_file",
			kind:        kindChangeClass,
			description: "An indexed Java file is rewritten in place: a method is added while its existing nodes keep their identity.",
			apply: func(f *fixture) {
				f.Write("shop/Cart.java", `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.apply(1) + r.rate(); }
    public int extra(Rate r) { return r.rate(); }
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.extra"),
					g.requirePresent("shop.checkout"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "jvm_add_call",
			kind:        kindChangeClass,
			description: "A new call is added whose declared-typed receiver binds a confirmed edge: the confirmed-tier add path.",
			seed: map[string]string{
				"shop/Cart.java": `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.rate(); }
}
`,
			},
			apply: func(f *fixture) {
				f.Write("shop/Cart.java", `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.rate() + r.apply(1); }
}
`)
			},
			witness: func(g *graphView) error {
				// The new call must land as a CONFIRMED edge — the binder's
				// output, not the heuristic linker (which skips variable-receiver
				// instance calls).
				return g.requireEdgeAtTier("shop.checkout", "calls", "tax.apply", confirmed)
			},
		},
		{
			id:   "jvm_change_overload",
			kind: kindChangeClass,
			description: "A same-arity overload is added to the receiver type, so a previously (name,arity)-unique confirmed call becomes ambiguous and the confirmed edge must DROP (ADR 0008 D6). " +
				"The inverse of jvm_add_call: this is the class that proves the binder never RANKS an overload set.",
			apply: func(f *fixture) {
				// tax.Rate.apply gains a String overload: (apply,1) now has two
				// differing signatures, so shop.checkout's confirmed edge to
				// tax.apply must vanish. tax.rate stays uniquely bound.
				f.Write("tax/Rate.java", `package tax;
public class Rate {
    public int apply(int x) { return x; }
    public int apply(String s) { return 0; }
    public int rate() { return 7; }
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					// The ambiguous call dropped: no edge survives to tax.apply
					// (neither confirmed nor heuristic — the FQN linker skips
					// variable-receiver instance calls too).
					g.requireNoEdge("shop.checkout", "calls", "tax.apply"),
					// Control: the still-unique call stays confirmed, so the drop
					// is the overload's doing, not a wholesale loss.
					g.requireEdgeAtTier("shop.checkout", "calls", "tax.rate", confirmed),
				)
			},
		},
		{
			id:   "kotlin_infer_declared_flip",
			kind: kindChangeClass,
			description: "A Kotlin local flips from declared (`val x: Rate = …`) to inferred (`val x = …`). The binder types the declared form and drops the inferred one (no inference, ADR 0008 D2), " +
				"so the confirmed edge through the local must vanish — the class that makes the D2 recall boundary a proven behaviour, not a claim.",
			apply: func(f *fixture) {
				f.Write("k/App.kt", `package k
import tax.Rate
class App {
    fun run(r: Rate): Int {
        val typed = r
        return typed.rate()
    }
}
`)
			},
			witness: func(g *graphView) error {
				// Declared → inferred: the confirmed edge through the local
				// vanishes. Non-vacuous because the base tree's declared form
				// produced it.
				return g.requireNoEdge("k.run", "calls", "tax.rate")
			},
		},
		{
			id:   "jvm_rename_package",
			kind: kindChangeClass,
			description: "A package is renamed: its directory, its package clause AND its importer all move in one change set. Because a JVM node's QN keys on the file's directory (qn.go filePackage), the callee's " +
				"node identity changes, so this exercises the full rename cascade — stale-node purge, re-link, and the confirmed re-emission at the new identity.",
			seed: map[string]string{
				"iso/Iso.java": "package iso;\npublic class Iso { public int val() { return 1; } }\n",
				"use/Use.java": "package use;\nimport iso.Iso;\npublic class Use { public int f(Iso i) { return i.val(); } }\n",
			},
			apply: func(f *fixture) {
				// iso → moved: the file moves AND the importer's import clause
				// is rewritten, so use.f's confirmed edge re-points from
				// iso.val to moved.val.
				f.Move("iso/Iso.java", "moved/Iso.java", "package moved;\npublic class Iso { public int val() { return 1; } }\n")
				f.Write("use/Use.java", "package use;\nimport moved.Iso;\npublic class Use { public int f(Iso i) { return i.val(); } }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("iso.val"), // old identity gone
					g.requireEdgeAtTier("use.f", "calls", "moved.val", confirmed),
				)
			},
		},
		{
			id:   "jvm_change_type_hierarchy",
			kind: kindChangeClass,
			description: "A class's `extends` clause is re-pointed to a different intra-repo supertype. The inherited-member lookup must converge on the NEW supertype's method, so the confirmed call through the " +
				"receiver re-points — the class that proves supertype-chain re-resolution (hierarchy.go), not just member add/remove.",
			seed: map[string]string{
				"base/Base.java": "package base;\npublic class Base { public void ship() {} }\n",
				"mid/Mid.java":   "package mid;\npublic class Mid { public void ship() {} }\n",
				"h/Sub.java":     "package h;\nimport base.Base;\npublic class Sub extends Base {}\n",
				"h/User.java":    "package h;\npublic class User { public void use(Sub s) { s.ship(); } }\n",
			},
			apply: func(f *fixture) {
				// Sub now extends mid.Mid: s.ship() binds mid.Mid.ship instead
				// of base.Base.ship, and the confirmed edge re-points.
				f.Write("h/Sub.java", "package h;\nimport mid.Mid;\npublic class Sub extends Mid {}\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireNoEdge("h.use", "calls", "base.ship"), // the old inherited target
					g.requireEdgeAtTier("h.use", "calls", "mid.ship", confirmed),
				)
			},
		},
		{
			id:   "jvm_move_nested_class",
			kind: kindChangeClass,
			description: "A nested Java class is promoted to top level. Nested types mint NO node (qn.go), so promotion makes the type AND its method appear as nodes for the first time — the class that pins the " +
				"nested/top-level node boundary as a parity-holding transition.",
			seed: map[string]string{
				"nest/Outer.java": "package nest;\npublic class Outer {\n    class Inner { public int deep() { return 1; } }\n}\n",
			},
			apply: func(f *fixture) {
				// Inner promoted to top level: it and its method gain nodes.
				f.Write("nest/Outer.java", "package nest;\npublic class Outer {}\nclass Inner { public int deep() { return 1; } }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("nest.Inner"), // the promoted type now has a node
					g.requirePresent("nest.deep"),  // and so does its method
				)
			},
		},
		{
			id:   "jvm_change_import_shadowing",
			kind: kindChangeClass,
			description: "A single-type import is added to a caller, shadowing an on-demand (`b.*`) import of the same simple name (JLS §6.4.1). The receiver type `Model` re-resolves from b.Model to c.Model, so the " +
				"confirmed call re-points — the class that proves the resolver's import-precedence ladder (resolve.go), not just presence/absence of a member.",
			seed: map[string]string{
				"b/Model.java": "package b;\npublic class Model { public void run() {} }\n",
				"c/Model.java": "package c;\npublic class Model { public void run() {} }\n",
				"u/User.java":  "package u;\nimport b.*;\npublic class User { public void use(Model m) { m.run(); } }\n",
			},
			apply: func(f *fixture) {
				// The explicit `import c.Model` shadows the on-demand `import
				// b.*`, so Model now binds c.Model and use's confirmed edge
				// re-points from b.run to c.run.
				f.Write("u/User.java", "package u;\nimport b.*;\nimport c.Model;\npublic class User { public void use(Model m) { m.run(); } }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireNoEdge("u.use", "calls", "b.run"), // the shadowed binding
					g.requireEdgeAtTier("u.use", "calls", "c.run", confirmed),
				)
			},
		},
		{
			id:   "jvm_move_symbol",
			kind: kindChangeClass,
			description: "A Kotlin top-level function moves file-to-file WITHIN one package. A JVM node's QN keys on the directory, not the filename (qn.go filePackage), so the moved function's identity k.helper is STABLE while its " +
				"source file changes — two files then claim one NodeId inside a single change set, the same-package direction of Go's move_symbol and the BLOCK-2 stale-purge hazard. The class that proves a QN-stable re-home carries the node AND its confirmed edge to the new file with no stale copy left behind.",
			seed: map[string]string{
				"k/a.kt": `package k
import tax.Rate
fun helper(r: Rate): Int {
    val typed: Rate = r
    return typed.rate()
}
fun keep(): Int = 1
`,
				"k/b.kt": `package k
fun other(): Int = 2
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.kt → b.kt, both rewritten in place (no file
				// add/delete, so the deferred delete path is untouched). k.helper's
				// QN is stable, so the incremental reconcile must re-home the node
				// and its confirmed edge onto b.kt without leaving a stale a.kt copy
				// or dropping the edge — the parity risk this class exists to pin.
				f.Write("k/a.kt", `package k
fun keep(): Int = 1
`)
				f.Write("k/b.kt", `package k
import tax.Rate
fun helper(r: Rate): Int {
    val typed: Rate = r
    return typed.rate()
}
fun other(): Int = 2
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("k.helper"),                                    // identity preserved across the cross-file move
					g.requireEdgeAtTier("k.helper", "calls", "tax.rate", confirmed), // and its confirmed edge survives the re-home
					g.requirePresent("k.keep"),                                      // control: stayed in a.kt
					g.requirePresent("k.other"),                                     // control: stayed in b.kt
				)
			},
		},
		{
			id:   "jvm_delete_file",
			kind: kindChangeClass,
			description: "A Java file declaring a type that TWO other packages call through imports is deleted, so the per-file stale-node purge, the confirmed-edge sweep and the re-link all run over it. " +
				"This class was DEFERRED behind PARITY-001 (ADR 0008 D8) as a PRECAUTION — the Go delete path diverged permanently, and a JVM row failing for that language-independent reason would have " +
				"pinned a defect that was not JVM's. MEASURED WHEN ADDING IT, and stated because a row that would have passed all along must not be sold as newly enabled: this class converges byte-exactly " +
				"BOTH WITH AND WITHOUT the PARITY-001 fix (verified by reverting engine/ingest/ingest.go to its pre-fix revision and re-running — still green on both stores). So the JVM delete path never " +
				"carried PARITY-001's divergence in this shape. Consistent with the witness: PARITY-001's Go divergence was ENTIRELY about the full side minting an interned external node for the now-unresolvable " +
				"import, and here NEITHER side mints one (tax.Rate is absent from both graphs), so there is nothing for the two passes to disagree about. The row is worth having on its own terms — the JVM " +
				"delete path was assumed to converge and is now proven to, and it closes the last deferred row in this matrix.",
			apply: func(f *fixture) {
				// tax/Rate.java declares the type BOTH shop.Cart (Java) and k.App
				// (Kotlin) import and call. Deleting it is the JVM instance of the
				// exact shape PARITY-001 governed: the callee's nodes must go, the
				// confirmed edges into them must go with them, and the incremental
				// pass must land on the same bytes as a full parse — including
				// whatever the heuristic linker interns for the now-unresolvable
				// imports, which is the half the old ordering silently skipped.
				f.Remove("tax/Rate.java")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("tax.Rate"), // the deleted type
					g.requireAbsent("tax.rate"), // and its members
					g.requireAbsent("tax.apply"),
					// Controls: only the deleted file's nodes went. Both importers
					// survive, in both languages, so the purge is scoped and the
					// row is not vacuously green on an empty graph.
					g.requirePresent("shop.checkout"),
					g.requirePresent("k.run"),
					// The confirmed edges into the deleted callee must be gone —
					// a stale confirmed edge would be the worst outcome here.
					g.requireNoEdge("shop.checkout", "calls", "tax.rate"),
					g.requireNoEdge("k.run", "calls", "tax.rate"),
				)
			},
		},
	}
}

// TestJVMFullVsIncremental_ByteParity is the WP-J5 gate. One subtest per
// (backend, JVM change class): a full-parse graph and an incremental
// watcher-driven graph over the same change serialize byte-identically, the
// class's non-vacuity witness holds against the incremental graph, and the JVM
// binder is live throughout.
func TestJVMFullVsIncremental_ByteParity(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")
	table := jvmChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, row := range table {
				row := row
				t.Run(row.id, func(t *testing.T) {
					runJVMChangeClassRow(t, b, row)
				})
			}
		})
	}
}

// runJVMChangeClassRow mirrors runChangeClassRow but seeds jvmBaseTree(). The
// JVM wave-1 subset carries no known-defect rows (delete_file's PARITY-001 is
// deferred per ADR 0008 D8), so this driver is the clean add/modify path:
// seed+reconcile, apply+reconcile, full parse, byte-parity, non-vacuity,
// idempotency.
func runJVMChangeClassRow(t *testing.T, b parityBackend, row changeClassRow) {
	t.Helper()
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := jvmBaseTree()
	for rel, content := range row.seed {
		seed[rel] = content
	}

	incStore := newBackendStore(t, b)
	buildIncrementalParallel(t, root, incStore, []func(){
		func() { writeTree(t, root, seed) },
		func() { row.apply(f) },
	})

	fullStore := newBackendStore(t, b)
	fullIng := newIngester(t, fullStore)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] full IngestAll: %v", b.name, row.id, err)
	}

	incSnap := snapshot(t, incStore)
	fullSnap := snapshot(t, fullStore)

	// Non-vacuity first and unconditionally, against the incremental graph.
	g, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", b.name, row.id, err)
	}
	if err := row.witness(g); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: witness did not hold, so `apply` did not produce the claimed shape: %v", b.name, row.id, err)
	}

	// The assertion: snapshot bytes, nothing weaker.
	if string(incSnap) != string(fullSnap) {
		t.Errorf("[%s/%s] PARITY FAIL: incremental != full snapshot bytes.\nclass: %s\nchange set: %v\n%s",
			b.name, row.id, row.description, f.changeSet(),
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}
}
