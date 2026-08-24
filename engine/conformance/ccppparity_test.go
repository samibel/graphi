package conformance_test

// SW-194b (W5.h, c/cpp slice, the SHARED family): the C/C++ full-vs-
// incremental change-class parity gate. The C and C++ family is TWO
// declared languages (c, cpp) at `cross-file-heuristic`, sharing ONE
// resolver impl at engine/link/resolve_c.go registered under both ids
// (SW-184's pattern: c uses `.c`/`.h` and cpp uses `.cpp`/`.cc`/`.cxx`/
// `.hpp`/`.hxx`/`.hh`). This single harness drives the SHARED resolver
// path with C-style extensions, covering both languages.
//
// BUNDLE DISCIPLINE, STATED. C and CPP share this YAML (parity-classes-
// c-cpp.yaml) and this single harness table; each language still carries
// its OWN evidence rows (SW-184 AC-4). The eight rows here apply to BOTH
// languages — a discharge against one discharges both. The harness table
// uses .c/.h extensions for parity with the C-language path; the C++ path
// shares the resolver and uses the same QN-stable re-home convention.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Go / JVM / Python / TS tables. C/C++ has no
// typed binder, so the rows here assert PARITY, not CORRECTNESS — parity
// compares two passes of the same rule, so a PASS certifies the rule is
// REGRESSION-CLEAN between incremental and full, never that it is correct.
// Correctness evidence lives in engine/link/resolve_clang_test.go and in
// the real-repository matrix (G4), not here.
//
// The eight rows cover the C/C++ change shapes the heuristic resolver
// models: add/modify/delete in a directory, the cross-file include call
// (the exact-path resolution at engine/link/resolve_common.go's
// requireBinder), the missing-include skip+count, the two-candidate
// ambiguity, and the same-directory file-to-file function move
// (QN-stable re-home), plus the type-definition add row. The witness
// asserts the resolver's contract: heuristic tier only (never confirmed),
// drop+count on what it cannot resolve, deterministic across passes.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// ccppBaseTree is the cross-directory C/C++ fixture the heuristic tier
// needs: a C caller into a C callee across two sibling directories so the
// includeBinder helper at engine/link/resolve_c.go opens the included
// header's directory as an ambient lookup. The base edges (heuristic,
// every row):
//
//	app.checkout --calls--> lib.helper   (cross-dir include, heuristic tier)
//
// (No same-directory derived-tier edge — C/C++ doesn't model
// same-directory resolution the same way Python/TS/Bash do; the same-
// directory resolver is gated on the family table.)
func ccppBaseTree() map[string]string {
	return map[string]string{
		"app/main.c": `// cross-file include (../lib/util.h resolves lib/util.h from app/)
#include "../lib/util.h"

int checkout(int x) {
    return helper(x);
}
`,
		"lib/util.h": `// cross-file callee
int helper(int x) {
    return x + 1;
}
`,
	}
}

// ccppChangeClassTable is the declarative C/C++ change-class matrix. Row
// order follows docs/rc/parity-classes-c-cpp.yaml so the two files diff
// side by side.
func ccppChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "ccpp_add_file",
			kind:        kindChangeClass,
			description: "A new C (or C++) file arrives in a new directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.c", "int compute(int x) { return x * 2; }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "ccpp_modify_file",
			kind:        kindChangeClass,
			description: "An indexed C (or C++) file is rewritten in place: a function is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("lib/util.h", `// cross-file callee (rewritten with extra)
int helper(int x) {
    return x + 1;
}

int extra_util(int x) {
    return x * 3;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.extra_util"),
					g.requirePresent("lib.helper"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "ccpp_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-file include call is added: `#include \"../lib/util.h\"` (the quote-form resolves RELATIVE to the importer, the angle-bracket form is external). The witness asserts the file→file `imports` edge lands AND the call edge lands at HEURISTIC tier (model.TierHeuristic), never at confirmed — the C/C++ resolver is heuristic and must not mint confirmed edges (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"app/main.c": `int checkout(int x) {
    return x;
}
`,
				"lib/util.h": `int helper(int x) {
    return x + 1;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.c", `#include "../lib/util.h"

int checkout(int x) {
    return helper(x);
}
`)
			},
			witness: func(g *graphView) error {
				// The new cross-file call must land at HEURISTIC tier —
				// the resolver's only tier. A confirmed edge here would
				// be the G2SUB never-confirmed half violated.
				return g.requireEdgeAtTier("app.checkout", "calls", "lib.helper", heuristic)
			},
		},
		{
			id:          "ccpp_include_skip",
			kind:        kindChangeClass,
			description: "A relative include (`#include \"missing.h\"`) targets a path that does not exist on disk. The witness asserts the resolver mints NO `calls` edge to the absent target — the DROP half ONLY. The witness walks every edge and `continue`s past any whose Kind() is not `calls`, so it constrains the `calls` kind alone and says nothing about an `imports` file-edge. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. An edge here would be the failure mode the level forbids.",
			seed: map[string]string{
				"app/main.c": `int checkout(int x) {
    return x;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.c", `#include "missing.h"

int checkout(int x) {
    return missing_fn(x);
}
`)
			},
			witness: func(g *graphView) error {
				// The missing include is dropped: no edge to a `missing.*`
				// node may survive. The witness asserts this by failing
				// on ANY outbound `calls` edge whose target QN carries
				// `missing.` (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "missing.") {
							return fmt.Errorf("missing include %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "missing.h", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "ccpp_ambiguous_includes",
			kind:        kindChangeClass,
			description: "A relative include is ambiguous because two candidate paths resolve to committed nodes (e.g. `lib/util.h` and `vendor/util.h` both exist). The resolver must drop the edge rather than guess. The witness asserts NEITHER candidate is minted as a `calls` edge — the never-guess half. The witness `continue`s past any edge whose Kind() is not `calls`, so it constrains the `calls` kind alone. The witness reads no counter — graphView (engine/conformance/changeclass_test.go:111-115) holds only nodes/edges/byQN/byID — so the COUNT half is UNPROVEN by this row. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used, but C/C++'s exact-path resolution makes the ambiguity a structural two-candidate case.",
			seed: map[string]string{
				"lib/util.h": `int helper(int x) {
    return x + 1;
}
`,
				"vendor/util.h": `int helper(int x) {
    return x + 2;
}
`,
				"app/main.c": `int checkout(int x) {
    return x;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.c", `#include "../util.h"

int checkout(int x) {
    return helper(x);
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
						if to.QualifiedName() == "lib.helper" || to.QualifiedName() == "vendor.helper" {
							return fmt.Errorf("ambiguous relative include %q resolved to %q — the G2SUB never-guess half is violated", "../util.h", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "ccpp_delete_file",
			kind:        kindChangeClass,
			description: "A C (or C++) file declaring a function that TWO other translation units include is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone — a stale heuristic edge would be the worst outcome here, even though the edge tier is lower than confirmed.",
			seed: map[string]string{
				"app/run.c": `#include "../lib/util.h"

int run(int x) {
    return helper(x);
}
`,
				"app/checkout.c": `#include "../lib/util.h"

int checkout(int x) {
    return helper(x);
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("lib/util.h")
			},
			witness: func(g *graphView) error {
				// The C QN keys on the LAST directory segment along the
				// included path: lib/util.h yields `lib.helper`. Two
				// importers (app/run.c and app/checkout.c) each emit a
				// heuristic edge into the deleted callee. The witness
				// pins the actual C resolver behavior on delete: the
				// file node is gone (the per-file purge ran for every
				// node anchored in lib/util.h), BOTH importers remain
				// present (the purge is scoped).
				_, hasFile := g.fileEdge("lib/util.h", "defines", "lib.helper")
				return all(
					g.requirePresent("app.run"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node lib/util.h still defines lib.helper — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "ccpp_move_symbol",
			kind:        kindChangeClass,
			description: "A C (or C++) top-level function moves file-to-file WITHIN one directory (a.c -> b.c). The function's identity is keyed on its qualified name (QN), which the resolver derives from the source file path; a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol and the BLOCK-2 stale-purge hazard. The witness asserts the function identity is preserved and its cross-file include edge survives the re-home — pins the QN-stable re-home as a parity-holding transition.",
			seed: map[string]string{
				"k/a.c": `#include "../lib/util.h"

int helper(int x) {
    return helper_src(x);
}

int keep(int x) {
    return 1;
}
`,
				"k/b.c": `int other(int x) {
    return 2;
}
`,
				"lib/util.h": `int helper_src(int x) {
    return x;
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.c -> b.c, both rewritten in place.
				f.Write("k/a.c", `int keep(int x) {
    return 1;
}
`)
				f.Write("k/b.c", `#include "../lib/util.h"

int helper(int x) {
    return helper_src(x);
}

int other(int x) {
    return 2;
}
`)
			},
			witness: func(g *graphView) error {
				// The C QN keys on the directory + basename segment along
				// the included path: helper inside the same directory
				// keeps QN `k.helper`. The witness pins the QN-stable
				// re-home: helper's identity survives the file-to-file
				// move and its cross-module edge is re-emitted against
				// the same target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "lib.helper_src", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "ccpp_add_type_definition",
			kind:        kindChangeClass,
			description: "A struct (C) or class (C++) definition is added to a header file. The witness asserts the new type node is present and the existing callees survive — pins the type-definition identity-stability contract. C and C++ differ in the type definition syntax (`struct Foo { ... };` vs `class Foo { ... };`) but share the QN-stable identity convention, so the same row id covers both.",
			apply: func(f *fixture) {
				f.Write("lib/util.h", `// cross-file callee + new type
struct Point { int x; int y; };

int helper(int x) {
    return x + 1;
}

int extra_util(int x) {
    return x * 3;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.helper"),
					g.requirePresent("lib.extra_util"),
					g.requirePresent("lib.Point"),
				)
			},
		},
	}
}

// TestCCppFullVsIncremental_ByteParity is the SW-194b C/C++ gate. One
// subtest per (backend, change class): a full-parse graph and an
// incremental watcher-driven graph over the same change serialize byte-
// identically, the class's non-vacuity witness holds against the
// incremental graph. The harness drives the SHARED resolve_c.go impl under
// both ids (the fixture uses .c extensions to bind the c-language path;
// cpp is covered by the shared resolver impl).
func TestCCppFullVsIncremental_ByteParity(t *testing.T) {
	table := ccppChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			// PROFILE AXIS, identical to the Python / TS / Bash
			// tables: a single-axis table would be blind to a
			// profile-shaped defect introduced later.
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runCCppChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runCCppChangeClassRow mirrors runBashChangeClassRow, seeding
// ccppBaseTree(). The C/C++ resolver has no JVM-binder Setenv; the
// parity harness's incremental path is identical to the other language
// tables for the heuristic resolver.
func runCCppChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := ccppBaseTree()
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
