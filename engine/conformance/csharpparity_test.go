package conformance_test

// SW-194b (W5.h, c_sharp slice): the C# full-vs-incremental change-class
// parity gate. C# is `cross-file-heuristic` and binds at the heuristic
// tier (engine/link/resolve_csharp.go); there is no JVM-style typed
// binder (the JVM binder is Java/Kotlin-only), so this table is the
// parity-holding assertion over the C# heuristic resolver, bound to
// docs/rc/parity-classes-c_sharp.yaml by the drift guard in
// csharpparity_matrix_test.go.
//
// C# uses `using Namespace;` to bring a NAMESPACE into scope. The
// resolver tries each imported namespace's LAST segment as a clause (the
// cstWalk "<dirBase>.<bare>" convention where Shop/Price.cs is keyed by
// clause "Shop"); a name found under exactly one ambient namespace
// resolves at the heuristic tier. The witness asserts the resolver's
// contract: heuristic tier only (never confirmed), drop+count on what it
// cannot resolve, deterministic across passes.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Python and TS tables. NOT a PRD §12.3 gate,
// NOT the real-repository matrix (G4), and the binder is not exercised.
// Parity compares two passes of the same rule, so a PASS certifies the
// heuristic resolver is REGRESSION-CLEAN between incremental and full,
// never that it is correct.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// csharpBaseTree is the cross-namespace C# fixture the heuristic tier
// needs: a C# caller in `app/` using the `Shop` namespace, calling
// `Price.Of()`. The base edges (heuristic):
//
//	app.checkout --calls--> Shop.Of   (selector via Shop ambient clause)
//	app.checkout --calls--> Shop.Price (bare Price via Shop ambient clause)
func csharpBaseTree() map[string]string {
	return map[string]string{
		"app/checkout.cs": `using Shop;

class Checkout {
    int checkout() {
        return Price.Of();
    }
}
`,
		"Shop/Price.cs": `namespace Shop {
    class Price {
        static int Of() { return 1; }
    }
}
`,
	}
}

// csharpChangeClassTable is the declarative C# change-class matrix. Row
// order follows docs/rc/parity-classes-c_sharp.yaml so the two files
// diff side by side.
func csharpChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "csharp_add_file",
			kind:        kindChangeClass,
			description: "A new C# file arrives in a new namespace: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("Tax/Rates/Calc.cs", `namespace Tax.Rates {
    class Calc {
        static int Compute(int x) { return x * 2; }
    }
}
`)
			},
			witness: func(g *graphView) error {
				// QN keys on the LAST directory segment (langPackage:
				// parent dir base = "Rates") plus the bare name, so the
				// method node QN is `Rates.Compute` and the class node
				// QN is `Rates.Calc`.
				return all(
					g.requirePresent("Rates.Compute"),
					g.requirePresent("Rates.Calc"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "csharp_modify_file",
			kind:        kindChangeClass,
			description: "An indexed C# file is rewritten in place: a method is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("Shop/Price.cs", `namespace Shop {
    class Price {
        static int Of() { return 1; }
        static int Extra() { return 7; }
    }
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("Shop.Extra"),
					g.requirePresent("Shop.Of"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "csharp_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-namespace call is added: `using Shop; Price.Of()`. The resolver's namespace-ambient clause path. The witness pins the heuristic tier — the C# resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"app/checkout.cs": `class Checkout {
    int checkout() {
        return 0;
    }
}
`,
				"Shop/Price.cs": `namespace Shop {
    class Price {
        static int Of() { return 1; }
    }
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/checkout.cs", `using Shop;

class Checkout {
    int checkout() {
        return Price.Of();
    }
}
`)
			},
			witness: func(g *graphView) error {
				// The new cross-namespace call must land at HEURISTIC
				// tier — the resolver's only tier. A confirmed edge
				// here would be the G2SUB never-confirmed half
				// violated.
				return g.requireEdgeAtTier("app.checkout", "calls", "Shop.Of", heuristic)
			},
		},
		{
			id:          "csharp_using_skip",
			kind:        kindChangeClass,
			description: "A using directive (`using Missing;`) targets a namespace that is absent from the indexed tree. The witness asserts the resolver mints NO `calls` edge to the absent target — the DROP half ONLY. The witness walks every edge and `continue`s past any whose Kind() is not `calls`, so it constrains the `calls` kind alone and says nothing about an `imports` file-edge. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. An edge here would be the failure mode the level forbids.",
			seed: map[string]string{
				"app/checkout.cs": `class Checkout {
    int checkout() {
        return 0;
    }
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/checkout.cs", `using Missing;

class Checkout {
    int checkout() {
        return Something.Run();
    }
}
`)
			},
			witness: func(g *graphView) error {
				// The missing namespace is dropped: no edge to a
				// `missing.*` or external node may survive. The
				// witness asserts this by failing on ANY outbound
				// `calls` edge whose target QN carries `missing.`
				// (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						qn := to.QualifiedName()
						if strings.HasPrefix(qn, "missing.") {
							return fmt.Errorf("missing namespace %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "Missing", qn)
						}
					}
				}
				return nil
			},
		},
		{
			id:          "csharp_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "Two namespaces both declare a method with the same name (e.g. `Shop.Of` and `Market.Of`). The witness asserts NEITHER candidate is minted as a `calls` edge — the never-guess half. The witness `continue`s past any edge whose Kind() is not `calls`, so it constrains the `calls` kind alone. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used.",
			seed: map[string]string{
				"Shop/Price.cs": `namespace Shop { class Price { static int Of() { return 1; } } }
`,
				"Market/Price.cs": `namespace Market { class Price { static int Of() { return 2; } } }
`,
				"app/checkout.cs": `class Checkout {
    int checkout() {
        return 0;
    }
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/checkout.cs", `using Shop;
using Market;

class Checkout {
    int checkout() {
        return Price.Of();
    }
}
`)
			},
			witness: func(g *graphView) error {
				// NEITHER call site may resolve to a single edge — the
				// resolver must drop and count, never guess.
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						qn := to.QualifiedName()
						if qn == "Shop.Of" || qn == "Market.Of" {
							return fmt.Errorf("ambiguous using resolved to %q — the G2SUB never-guess half is violated", qn)
						}
					}
				}
				return nil
			},
		},
		{
			id:          "csharp_delete_file",
			kind:        kindChangeClass,
			description: "A C# file declaring a method that TWO other namespaces' callers invoke is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone.",
			seed: map[string]string{
				"app/run.cs": `using Shop;

class Run {
    int run() {
        return Price.Of();
    }
}
`,
				"app/checkout.cs": `using Shop;

class Checkout {
    int checkout() {
        return Price.Of();
    }
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("Shop/Price.cs")
			},
			witness: func(g *graphView) error {
				// The C# QN keys on the LAST directory segment along
				// the path: Shop/Price.cs yields `Shop.Price` and
				// `Shop.Of`. Two importers (app/run.cs and
				// app/checkout.cs) each emit heuristic edges into the
				// deleted callee. The witness pins the actual C#
				// resolver behavior on delete: the file node is gone
				// (the per-file purge ran for every node anchored in
				// Shop/Price.cs), BOTH importers remain present (the
				// purge is scoped).
				_, hasFile := g.fileEdge("Shop/Price.cs", "defines", "Shop.Of")
				return all(
					g.requirePresent("app.run"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node Shop/Price.cs still defines Shop.Of — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "csharp_move_symbol",
			kind:        kindChangeClass,
			description: "A C# top-level method moves file-to-file WITHIN one namespace directory (Price.cs -> Moved.cs). The method's identity is keyed on its qualified name (QN); a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol. The witness asserts the method identity is preserved and its cross-namespace call edge survives the re-home.",
			seed: map[string]string{
				"k/A.cs": `using Shop;

class A {
    int helper() {
        return Of();
    }
    int keep() { return 1; }
}
`,
				"k/B.cs": `class B {
    int other() { return 2; }
}
`,
				"Shop/Price.cs": `namespace Shop {
    class Price {
        static int Of() { return 1; }
    }
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves A.cs -> B.cs, both rewritten in
				// place.
				f.Write("k/A.cs", `class A {
    int keep() { return 1; }
}
`)
				f.Write("k/B.cs", `using Shop;

class B {
    int helper() {
        return Of();
    }
    int other() { return 2; }
}
`)
			},
			witness: func(g *graphView) error {
				// The C# QN keys on the directory + basename segment:
				// helper inside the same directory keeps QN `k.helper`.
				// The witness pins the QN-stable re-home: helper's
				// identity survives the file-to-file move and its
				// cross-namespace call edge is re-emitted against the
				// same target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "Shop.Of", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "csharp_add_type_definition",
			kind:        kindChangeClass,
			description: "A class (or interface / struct / enum / record) definition is added to a namespace file. The witness asserts the new type node is present and the existing callees survive — pins the type-definition identity-stability contract.",
			apply: func(f *fixture) {
				f.Write("Shop/Price.cs", `namespace Shop {
    class Price {
        static int Of() { return 1; }
    }
    class Discount {
        static int Apply(int x) { return x / 2; }
    }
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("Shop.Price"),
					g.requirePresent("Shop.Discount"),
					g.requirePresent("Shop.Of"),
				)
			},
		},
	}
}

// TestCSharpFullVsIncremental_ByteParity is the SW-194b C# gate. One
// subtest per (backend, C# change class): a full-parse graph and an
// incremental watcher-driven graph over the same change serialize byte-
// identically, the class's non-vacuity witness holds against the
// incremental graph.
func TestCSharpFullVsIncremental_ByteParity(t *testing.T) {
	table := csharpChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			// PROFILE AXIS, identical to the other family tables.
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runCSharpChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runCSharpChangeClassRow mirrors runBashChangeClassRow, seeding
// csharpBaseTree().
func runCSharpChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := csharpBaseTree()
	for rel, content := range row.seed {
		seed[rel] = content
	}

	incStore := newBackendStore(t, b)
	buildIncrementalParallel(t, root, incStore, pr.p, []func(){
		func() { writeTree(t, root, seed) },
		func() { row.apply(f) },
	})

	fullStore := newBackendStore(t, b)
	fullIng := newIngester(t, fullStore, pr.p)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] full IngestAll: %v", axis, row.id, err)
	}

	incSnap := snapshot(t, incStore)
	fullSnap := snapshot(t, fullStore)

	// Non-vacuity first and unconditionally, against the incremental graph.
	g, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", axis, row.id, err)
	}
	if err := row.witness(g); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: witness did not hold, so `apply` did not produce the claimed shape: %v", axis, row.id, err)
	}

	// The assertion: snapshot bytes, nothing weaker.
	if string(incSnap) != string(fullSnap) {
		t.Errorf("[%s/%s] PARITY FAIL: incremental != full snapshot bytes.\nclass: %s\nchange set: %v\n%s",
			axis, row.id, row.description, f.changeSet(),
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}
}
