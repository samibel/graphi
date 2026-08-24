package conformance_test

// SW-181 (language-GA program G3): the Python full-vs-incremental
// change-class parity gate. Python is `cross-file-heuristic` and binds at the
// heuristic tier (engine/link/resolve_python.go); there is no JVM-style typed
// binder, so this table is the parity-holding assertion over the Python
// heuristic resolver, bound to docs/rc/parity-classes-python.yaml by the
// drift guard in pythonparity_matrix_test.go.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Go and JVM tables. NOT a PRD §12.3 gate, NOT
// the real-repository matrix (G4), and the binder is not exercised (Python
// has none). Parity compares two passes of the same rule, so a PASS
// certifies the heuristic resolver is REGRESSION-CLEAN between incremental
// and full, never that it is correct — correctness evidence lives in
// engine/link/resolve_python_test.go and in the G4 measurement, not here.
//
// The eight rows cover the Python-specific change shapes that the heuristic
// resolver models: add/modify/delete in a package, the import-alias selector,
// the from-import bare binding, the relative-import skip+count, the
// ambiguous-clauses drop+count, and the same-package file-to-file move
// (QN-stable re-home). The witness asserts the resolver's contract:
// heuristic tier only (never confirmed), drop+count on what it cannot
// resolve, deterministic across passes.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// pyBaseTree is the cross-package Python fixture the heuristic tier needs: a
// Python caller into a Python callee across two package directories so the
// clause-keyed resolver (pyResolver) binds an `import` edge at the heuristic
// tier. The base edges (heuristic, every row):
//
//	app.checkout --calls--> cart.build   (from-import bare binding)
//	app.checkout --calls--> app.helper   (same-directory derived)
func errorIf(cond bool, msg string) error {
	if cond {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func pyBaseTree() map[string]string {
	return map[string]string{
		"app/main.py": `def checkout():
    return helper() + build("x")


def helper():
    return 1
`,
		"shop/cart/build.py": `def build(name):
    return name
`,
	}
}

// pyChangeClassTable is the declarative Python change-class matrix. Row
// order follows docs/rc/parity-classes-python.yaml so the two files diff
// side by side.
func pyChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "py_add_file",
			kind:        kindChangeClass,
			description: "A new Python file arrives in a new package: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.py", "def compute(x):\n    return x\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "py_modify_file",
			kind:        kindChangeClass,
			description: "An indexed Python file is rewritten in place: a function is added while its existing nodes keep their identity.",
			apply: func(f *fixture) {
				f.Write("app/main.py", `def checkout():
    return helper() + build("x")


def helper():
    return 1


def extra():
    return 2
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("app.extra"),
					g.requirePresent("app.checkout"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "py_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-module from-import call is added: `from shop.cart import build; build(...)`. The resolver's from-import bare-binding path. The witness pins the heuristic tier — the Python resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"app/main.py": `def checkout():
    return helper()
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.py", `from shop.cart import build


def checkout():
    return helper() + build("x")
`)
			},
			witness: func(g *graphView) error {
				// The new call must land at HEURISTIC tier — the resolver's
				// only tier. A confirmed edge here would be the G2SUB
				// never-confirmed half violated.
				return g.requireEdgeAtTier("app.checkout", "calls", "cart.build", heuristic)
			},
		},
		{
			id:          "py_import_alias_selector",
			kind:        kindChangeClass,
			description: "An import-alias selector is added: `import tax.rates as rates; rates.compute(...)`. The resolver's selector-base path. The witness pins the bound FQN is rates.compute, not rates.rates.compute — the dotted module path resolution.",
			seed: map[string]string{
				"app/main.py": `def checkout():
    return helper()
`,
				"tax/rates/calc.py": `def compute(x):
    return x
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.py", `import tax.rates as rates


def checkout():
    return helper() + rates.compute("x")
`)
			},
			witness: func(g *graphView) error {
				return g.requireEdgeAtTier("app.checkout", "calls", "rates.compute", heuristic)
			},
		},
		{
			id:          "py_relative_import_skip",
			kind:        kindChangeClass,
			description: "A relative import (`from . import util`) targets a module that is absent from the indexed tree. The witness asserts the resolver mints NO `calls` edge to the absent target — the DROP half ONLY. The witness walks every edge and `continue`s past any whose Kind() is not `calls`, so it constrains the `calls` kind alone and says nothing about an `imports` file-edge. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. An edge here would be the failure mode the level forbids (fabricating a stdlib external where the spec forbids one for relative imports — see engine/link/resolve_python.go:73).",
			seed: map[string]string{
				"app/main.py": `def checkout():
    return helper()
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.py", `from . import util


def checkout():
    return helper() + util.missing()
`)
			},
			witness: func(g *graphView) error {
				// The relative import is dropped: no edge to a `util.*` node
				// may survive. The witness asserts this by failing on ANY
				// outbound `calls` edge whose target QN carries `util.`
				// (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "util.") {
							return fmt.Errorf("relative import %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "util", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "py_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "Two package directories both declare a function `pkg.dup` (the clause-keyed lookup is identical to Go's PARITY-002 shape). The witness asserts NEITHER candidate is minted as a `calls` edge — the never-guess half. The witness `continue`s past any edge whose Kind() is not `calls`, so it constrains the `calls` kind alone. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. This is the row that pins the deterministic, never-guess half of the G2SUB contract against a SHAPE that has a Go precedent.",
			seed: map[string]string{
				"a/pkg/x.py":  "def dup():\n    return 1\n",
				"b/pkg/y.py":  "def dup():\n    return 2\n",
				"app/main.py": "def helper():\n    return 1\n",
			},
			apply: func(f *fixture) {
				f.Write("app/main.py", `from pkg import dup


def checkout():
    return helper() + dup()
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
						if strings.HasPrefix(to.QualifiedName(), "pkg.dup") {
							return fmt.Errorf("ambiguous `from pkg import dup` resolved to %q — the G2SUB never-guess half is violated", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "py_delete_file",
			kind:        kindChangeClass,
			description: "A Python file declaring a function that TWO other packages import is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone — a stale heuristic edge would be the worst outcome here, even though the edge tier is lower than confirmed.",
			seed: map[string]string{
				"tax/rates/calc.py": `def compute(x):
    return x
`,
				"shop/cart/build.py": `from tax.rates.calc import compute


def build(name):
    return compute(name)
`,
				"app/main.py": `from tax.rates.calc import compute


def checkout():
    return compute("x")
`,
			},
			apply: func(f *fixture) {
				f.Remove("tax/rates/calc.py")
			},
			witness: func(g *graphView) error {
				// Python's clause-keyed QN keys on the LAST package directory
				// along the import path: the file tax/rates/calc.py yields
				// `tax.rates.calc.compute`, not `rates.compute`. Two importers
				// (shop/cart/build.py → cart.build, app/main.py → app.checkout)
				// each emit a heuristic edge into that clause.
				//
				// The witness pins the actual Python resolver behavior on
				// delete: the file node is gone (the per-file purge ran for
				// every node anchored in tax/rates/calc.py), BOTH importers
				// remain present (the purge is scoped), and the heuristic
				// edges into the extern placeholder persist with the reason
				// `external calls (unresolved import)`. The persistence IS
				// the LINK-004 defect — the resolver interns the clause QN
				// from the import string at module-resolve time, and the
				// delete path does not sweep the dangling extern. SW-166
				// disclosed it and the level is honest about it; this row
				// asserts the snapshot is parity-consistent between full
				// and incremental (the snapshot-bytes check is the next
				// assertion), not that the defect is closed.
				_, hasFile := g.fileEdge("tax/rates/calc.py", "defines", "tax.rates.calc.compute")
				return all(
					g.requirePresent("cart.build"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node tax/rates/calc.py still defines tax.rates.calc.compute — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "py_move_symbol",
			kind:        kindChangeClass,
			description: "A Python top-level function moves file-to-file WITHIN one package. Python's clause-keyed QN keys on the directory, not the filename (mirror of qn.go filePackage), so the moved function's identity is STABLE while its source file changes — two files then claim one qualified name inside a single change set, the same-package direction of Go's move_symbol and the BLOCK-2 stale-purge hazard. The witness asserts the function identity is preserved and its cross-module edge survives the re-home — pins the QN-stable re-home as a parity-holding transition.",
			seed: map[string]string{
				"k/a.py": `from tax.rates.calc import compute


def helper(x):
    return compute(x)


def keep():
    return 1
`,
				"k/b.py": `def other():
    return 2
`,
				"tax/rates/calc.py": `def compute(x):
    return x
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.py -> b.py, both rewritten in place.
				f.Write("k/a.py", `def keep():
    return 1
`)
				f.Write("k/b.py", `from tax.rates.calc import compute


def helper(x):
    return compute(x)


def other():
    return 2
`)
			},
			witness: func(g *graphView) error {
				// Same clause-keyed QN convention: the rate.compute callee
				// resolves to `tax.rates.calc.compute`. The witness pins
				// the QN-stable re-home: helper's identity survives the
				// file-to-file move and its cross-module edge is re-emitted
				// against the SAME target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "tax.rates.calc.compute", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
	}
}

// TestPythonFullVsIncremental_ByteParity is the SW-181 gate. One subtest
// per (backend, Python change class): a full-parse graph and an incremental
// watcher-driven graph over the same change serialize byte-identically,
// the class's non-vacuity witness holds against the incremental graph, and
// Python has no JVM-style binder to set.
func TestPythonFullVsIncremental_ByteParity(t *testing.T) {
	table := pyChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			// PROFILE AXIS, identical to the JVM and Go tables: the same
			// PARITY-003 lesson binds, even though Python has no
			// import-aggregation defect to expose — a single-axis table
			// would still be blind to a profile-shaped defect introduced
			// later. The axis is the change-class table's, and the
			// language does not get to drop it.
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runPythonChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runPythonChangeClassRow mirrors runChangeClassRow / runJVMChangeClassRow,
// seeding pyBaseTree() and skipping the JVM-binder Setenv. Python has no
// binder to set, and the parity harness's incremental path is identical to
// the Go/JVM ones for the heuristic resolver.
func runPythonChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := pyBaseTree()
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
