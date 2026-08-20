package conformance_test

// SW-182 (language-GA program G4): the TypeScript family full-vs-incremental
// change-class parity gate. The family is `cross-file-heuristic` and binds at
// the heuristic tier (engine/link/resolve_typescript.go); there is no JVM-
// style typed binder, so this table is the parity-holding assertion over the
// TS heuristic resolver, bound to docs/rc/parity-classes-ts.yaml by the drift
// guard in typescriptparity_matrix_test.go.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Go, JVM and Python tables. NOT a PRD §12.3 gate,
// NOT the real-repository matrix (G4), and the binder is not exercised (the
// family has none). Parity compares two passes of the same rule, so a PASS
// certifies the heuristic resolver is REGRESSION-CLEAN between incremental
// and full, never that it is correct — correctness evidence lives in
// engine/link/resolve_typescript_test.go (the per-family resolver proofs, plus
// AC-4's NoDirectoryFanOut control test that pins the LINK-001 immunity) and
// in the G4 measurement, not here.
//
// The eight rows cover the TypeScript-family-specific change shapes the
// heuristic resolver models: add/modify/delete in a package, the named-import
// bare binding, the namespace selector (a binding shape Python does not have),
// the relative-import skip+count, the twin-dirs ambiguity (the family-specific
// shape — TS treats `./x/shared` as both sibling-file AND directory-module
// candidates), and the same-package file-to-file move (QN-stable re-home).
// The witness asserts the resolver's contract: heuristic tier only (never
// confirmed), drop+count on what it cannot resolve, deterministic across
// passes.

import (
	"context"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// tsBaseTree is the cross-package TypeScript fixture the heuristic tier needs:
// a TypeScript caller into a TypeScript callee across two package directories
// so the clause-keyed resolver (tsResolver) binds a `calls` edge at the
// heuristic tier. The base edges (heuristic, every row):
//
//	app.run --calls--> lib.greet   (named-import bare binding)
//	app.run --calls--> lib.add     (namespace selector)
func tsBaseTree() map[string]string {
	return map[string]string{
		"app/main.ts": `// cross-file named-import binding
import { greet } from "../lib/util"

// cross-file namespace selector
import * as mathx from "../lib/calc"

export function run(name: string): string {
  return greet(name) + mathx.add(1, 2)
}
`,
		"lib/util.ts": `export function greet(name: string): string {
  return "Hi " + name
}
`,
		"lib/calc.ts": `export function add(a: number, b: number): number {
  return a + b
}
`,
	}
}

// tsChangeClassTable is the declarative TypeScript-family change-class matrix.
// Row order follows docs/rc/parity-classes-ts.yaml so the two files diff side
// by side.
func tsChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "ts_add_file",
			kind:        kindChangeClass,
			description: "A new TypeScript file arrives in a new package: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("lib/extra.ts", "export function compute(x: number): number {\n  return x * 2\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.compute"),
					g.requirePresent("app.run"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "ts_modify_file",
			kind:        kindChangeClass,
			description: "An indexed TypeScript file is rewritten in place: a function is added while its existing nodes keep their identity.",
			apply: func(f *fixture) {
				f.Write("app/main.ts", `// cross-file named-import binding
import { greet } from "../lib/util"

export function run(name: string): string {
  return greet(name)
}

export function extra(): number {
  return 7
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("app.extra"),
					g.requirePresent("app.run"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "ts_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-module named-import call is added: `import { greet } from '../lib/util'; greet(...)`. The resolver's named-import bare-binding path. The witness pins the heuristic tier — the TS resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"app/main.ts": `export function run(name: string): string {
  return "x"
}
`,
				"lib/util.ts": `export function greet(name: string): string {
  return "Hi " + name
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.ts", `import { greet } from "../lib/util"

export function run(name: string): string {
  return greet(name)
}
`)
			},
			witness: func(g *graphView) error {
				// The new call must land at HEURISTIC tier — the resolver's
				// only tier. A confirmed edge here would be the G2SUB
				// never-confirmed half violated.
				return g.requireEdgeAtTier("app.run", "calls", "lib.greet", heuristic)
			},
		},
		{
			id:          "ts_namespace_selector",
			kind:        kindChangeClass,
			description: "A namespace-selector call is added: `import * as mathx from '../lib/calc'; mathx.add(...)`. The resolver's namespace-selector path (the selBaseDirs map). The witness pins the bound FQN is lib.add — the dotted file→module resolution into mathx's target file.",
			seed: map[string]string{
				"app/main.ts": `export function run(): number {
  return 0
}
`,
				"lib/calc.ts": `export function add(a: number, b: number): number {
  return a + b
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.ts", `import * as mathx from "../lib/calc"

export function run(): number {
  return mathx.add(1, 2)
}
`)
			},
			witness: func(g *graphView) error {
				return g.requireEdgeAtTier("app.run", "calls", "lib.add", heuristic)
			},
		},
		{
			id:          "ts_relative_import_skip",
			kind:        kindChangeClass,
			description: "A relative import (`import { x } from './missing'`) targets a file that is absent from the indexed tree. The witness asserts the resolver SKIPS (no edge minted) — the G2SUB drop-and-count half on an absent file. An edge here would be the failure mode the level forbids (fabricating an external node where the spec forbids one for relative imports — see engine/link/resolve_typescript.go:62).",
			seed: map[string]string{
				"app/main.ts": `export function run(): number {
  return 0
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.ts", `import { x } from "./missing"

export function run(): number {
  return x()
}
`)
			},
			witness: func(g *graphView) error {
				// The relative import is dropped: no edge to a node in the
				// `missing.*` namespace may survive. The witness asserts
				// this by failing on ANY outbound `calls` edge whose target
				// QN matches a fabrication shape. Only externals for
				// non-relative misses (WP-14) may exist; relative misses
				// produce NO external node.
				for _, n := range g.byQN {
					if n.Kind() != "external" {
						continue
					}
					qn := n.QualifiedName()
					if qn == "missing.x" || qn == "missing.*" {
						return fmt.Errorf("relative import %q was fabricated as external node %q — the G2SUB drop-and-count half is violated (relative misses are NEVER externals)", "./missing", qn)
					}
				}
				return nil
			},
		},
		{
			id:          "ts_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "A relative path resolves to a directory with BOTH a sibling file (`x/shared.ts`) AND a directory-module index (`x/shared/index.ts`) claiming the same `Cfg` symbol. The witness asserts NEITHER call site resolves to a single edge — the G2SUB drop-and-count half on a real ambiguity, not on an absent module. This is the family-specific ambiguity shape — TS module resolution treats `./x/shared` as BOTH candidates because tsExts includes the directory-module path.",
			seed: map[string]string{
				"x/shared.ts":       `export type Cfg = { v: number }\n`,
				"x/shared/index.ts": `export type Cfg = { v: string }\n`,
				"app/main.ts":       `export function run(): number { return 1 }\n`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.ts", `import { Cfg } from "../x/shared"

export function run(): number {
  const c: Cfg = { v: 1 }
  return 1
}
`)
			},
			witness: func(g *graphView) error {
				// NEITHER call site may resolve to a single edge — the
				// resolver must drop and count, never guess between the
				// sibling file and the directory-module index.
				for _, e := range g.edges {
					if e.Kind() != "calls" && e.Kind() != "references" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						qn := to.QualifiedName()
						if qn == "x.Cfg" || qn == "shared.Cfg" {
							return fmt.Errorf("ambiguous relative import %q resolved to %q — the G2SUB never-guess half is violated (resolver picked a winner among twin candidates)", "../x/shared", qn)
						}
					}
				}
				return nil
			},
		},
		{
			id:          "ts_delete_file",
			kind:        kindChangeClass,
			description: "A TypeScript file declaring a function that TWO other packages import is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone — a stale heuristic edge would be the worst outcome here, even though the edge tier is lower than confirmed.",
			seed: map[string]string{
				"app/serve.ts": `import { greet } from "../lib/util"

export function serve(name: string): string {
  return greet(name)
}
`,
				"app/run.ts": `import { greet } from "../lib/util"

export function run(name: string): string {
  return greet(name)
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("lib/util.ts")
			},
			witness: func(g *graphView) error {
				// The TS QN keys on the LAST package directory along the
				// file path (langPackage convention), so lib/util.ts
				// yields `lib.greet`. Two importers (app/serve.ts and
				// app/run.ts) each emit a heuristic edge into the
				// deleted callee. The witness pins the actual TS
				// resolver behavior on delete: the file node is gone
				// (the per-file purge ran for every node anchored in
				// lib/util.ts), BOTH importers remain present (the
				// purge is scoped), and the heuristic edges into the
				// deleted callee are gone — unlike Python's LINK-004
				// extern persistence, the TS family's exact-path
				// resolution means there is no extern placeholder to
				// persist (D1, no relative-path externs).
				_, hasFile := g.fileEdge("lib/util.ts", "defines", "lib.greet")
				return all(
					g.requirePresent("app.serve"),
					g.requirePresent("app.run"),
					errorIf(hasFile, "file node lib/util.ts still defines lib.greet — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "ts_move_symbol",
			kind:        kindChangeClass,
			description: "A TypeScript top-level function moves file-to-file WITHIN one package. The TS QN keys on the directory, not the filename (langPackage convention, mirror of qn.go filePackage), so the moved function's identity is STABLE while its source file changes — two files then claim one qualified name inside a single change set, the same-package direction of Go's move_symbol and the BLOCK-2 stale-purge hazard. The witness asserts the function identity is preserved and its cross-module edge survives the re-home — pins the QN-stable re-home as a parity-holding transition.",
			seed: map[string]string{
				"k/a.ts": `import { greet } from "../lib/util"

export function helper(x: string): string {
  return greet(x)
}

export function keep(): number {
  return 1
}
`,
				"k/b.ts": `export function other(): number {
  return 2
}
`,
				"lib/util.ts": `export function greet(name: string): string {
  return "Hi " + name
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.ts -> b.ts, both rewritten in place.
				f.Write("k/a.ts", `export function keep(): number {
  return 1
}
`)
				f.Write("k/b.ts", `import { greet } from "../lib/util"

export function helper(x: string): string {
  return greet(x)
}

export function other(): number {
  return 2
}
`)
			},
			witness: func(g *graphView) error {
				// Same langPackage-derived QN convention: greet inside
				// lib/util.ts resolves to `lib.greet`. The witness pins
				// the QN-stable re-home: helper's identity survives the
				// file-to-file move and its cross-module edge is re-
				// emitted against the SAME target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "lib.greet", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
	}
}

// TestTSFullVsIncremental_ByteParity is the SW-182 gate. One subtest per
// (backend, TypeScript-family change class): a full-parse graph and an
// incremental watcher-driven graph over the same change serialize byte-
// identically, the class's non-vacuity witness holds against the incremental
// graph, and the family has no JVM-style binder to set.
func TestTSFullVsIncremental_ByteParity(t *testing.T) {
	table := tsChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			// PROFILE AXIS, identical to the JVM, Go and Python tables:
			// the same PARITY-003 lesson binds, even though the TS
			// family has no import-aggregation defect to expose — a
			// single-axis table would still be blind to a profile-
			// shaped defect introduced later. The axis is the change-
			// class table's, and the language does not get to drop it.
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runTSChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runTSChangeClassRow mirrors runPythonChangeClassRow, seeding tsBaseTree()
// and skipping the JVM-binder Setenv. The family has no binder to set, and
// the parity harness's incremental path is identical to the Go/JVM/Python
// ones for the heuristic resolver.
func runTSChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := tsBaseTree()
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
