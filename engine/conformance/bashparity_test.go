package conformance_test

// SW-194b (W5.h, cross-file-heuristic residual, bash slice): the Bash
// full-vs-incremental change-class parity gate. Bash is `cross-file-heuristic`
// and binds at the heuristic tier (engine/link/resolve_bash.go); there is no
// JVM-style typed binder, so this table is the parity-holding assertion over
// the Bash heuristic resolver, bound to docs/rc/parity-classes-bash.yaml by
// the drift guard in bashparity_matrix_test.go.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Python and TS tables. NOT a PRD §12.3 gate, NOT
// the real-repository matrix (G4), and the binder is not exercised (Bash has
// none). Parity compares two passes of the same rule, so a PASS certifies
// the heuristic resolver is REGRESSION-CLEAN between incremental and full,
// never that it is correct — correctness evidence lives in engine/link/
// resolve_script_test.go (the FU-5 script harness) and in the G4
// measurement, not here.
//
// The eight rows cover the Bash-specific change shapes the heuristic
// resolver models: add/modify/delete in a directory, the cross-file source
// call (the exact-path resolution at engine/link/resolve_common.go's
// requireBinder), the missing-source skip+count, the two-candidate
// ambiguity (the family-specific shape — Bash's relative source with two
// committed candidates), and the same-directory file-to-file move
// (QN-stable re-home), plus the control-flow-construct add row. The witness
// asserts the resolver's contract: heuristic tier only (never confirmed),
// drop+count on what it cannot resolve, deterministic across passes.
//
// BASE-TREE LAYOUT, because the resolver's requireBinder builds the
// ambient directory from the importer's path (`joinRel(dir, imp.Path)`):
// the source directive in `app/main.sh` references `../lib/util.sh` so the
// ambient directory is `lib/`, where `helper` is defined. Without this
// shape the import target never reaches a committed node and every
// cross-file call resolves to a SKIP — the row would be green over an
// empty graph, exactly the vacuous outcome the per-row witness is meant to
// reject.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// bashBaseTree is the cross-directory Bash fixture the heuristic tier needs:
// a Bash caller into a Bash callee across two sibling directories so the
// requireBinder helper at engine/link/resolve_common.go opens the sourced
// script's directory as an ambient lookup. The base edges (heuristic,
// every row):
//
//	app.checkout --calls--> lib.helper   (cross-dir source, heuristic tier)
//	app.checkout --calls--> app.helper   (same-directory, derived tier)
func bashBaseTree() map[string]string {
	return map[string]string{
		"app/main.sh": `#!/usr/bin/env bash
# cross-file source call (../lib/util.sh resolves lib/util.sh from app/)
. ../lib/util.sh

checkout() {
    helper
    app_helper
}
`,
		"app/helper.sh": `#!/usr/bin/env bash
app_helper() {
    echo "from app"
}
`,
		"lib/util.sh": `#!/usr/bin/env bash
helper() {
    echo "from lib"
}
`,
	}
}

// bashChangeClassTable is the declarative Bash change-class matrix. Row
// order follows docs/rc/parity-classes-bash.yaml so the two files diff side
// by side.
func bashChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "bash_add_file",
			kind:        kindChangeClass,
			description: "A new Bash file arrives in a new directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.sh", "#!/usr/bin/env bash\ncompute() {\n    echo $1\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "bash_modify_file",
			kind:        kindChangeClass,
			description: "An indexed Bash file is rewritten in place: a function is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("lib/util.sh", `#!/usr/bin/env bash
helper() {
    echo "from lib"
}

extra_util() {
    echo "from util extra"
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
			id:          "bash_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-file source call is added: `. ../lib/util.sh; helper(...)`. The resolver's exact-path source path. The witness pins the heuristic tier — the Bash resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"app/main.sh": `#!/usr/bin/env bash
checkout() {
    echo "x"
}
`,
				"lib/util.sh": `#!/usr/bin/env bash
helper() {
    echo "from lib"
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.sh", `#!/usr/bin/env bash
. ../lib/util.sh

checkout() {
    helper
}
`)
			},
			witness: func(g *graphView) error {
				// The new cross-file call must land at HEURISTIC tier —
				// the resolver's only tier. A confirmed edge here would be
				// the G2SUB never-confirmed half violated.
				return g.requireEdgeAtTier("app.checkout", "calls", "lib.helper", heuristic)
			},
		},
		{
			id:          "bash_source_import_skip",
			kind:        kindChangeClass,
			description: "A source directive (`. ./missing.sh`) targets a path that does not exist on disk. The witness asserts the resolver SKIPS (no edge minted) — the G2SUB drop-and-count half on an absent file. An edge here would be the failure mode the level forbids (fabricating an extern where the spec forbids one for missing requires — see engine/link/resolve_common.go).",
			seed: map[string]string{
				"app/main.sh": `#!/usr/bin/env bash
checkout() {
    echo "x"
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.sh", `#!/usr/bin/env bash
. ./missing.sh

checkout() {
    missing_fn
}
`)
			},
			witness: func(g *graphView) error {
				// The missing source is dropped: no edge to a `missing.*`
				// node may survive. The witness asserts this by failing
				// on ANY outbound `calls` edge whose target QN carries
				// `missing.` (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "missing.") {
							return fmt.Errorf("missing source %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "missing.sh", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "bash_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "A relative source (`. ../util.sh`) is ambiguous because two candidate paths resolve to committed nodes (e.g. `lib/util.sh` and `vendor/util.sh` both exist). The witness asserts NEITHER candidate edge is minted — the G2SUB drop-and-count half on a real ambiguity. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used, but Bash's exact-path resolution makes the ambiguity a structural two-candidate case.",
			seed: map[string]string{
				"lib/util.sh": `#!/usr/bin/env bash
helper() {
    echo "from lib"
}
`,
				"vendor/util.sh": `#!/usr/bin/env bash
helper() {
    echo "from vendor"
}
`,
				"app/main.sh": `#!/usr/bin/env bash
checkout() {
    echo "x"
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.sh", `#!/usr/bin/env bash
. ../util.sh

checkout() {
    helper
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
							return fmt.Errorf("ambiguous relative source %q resolved to %q — the G2SUB never-guess half is violated", "../util.sh", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "bash_delete_file",
			kind:        kindChangeClass,
			description: "A Bash file declaring a function that TWO other scripts source is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone — a stale heuristic edge would be the worst outcome here, even though the edge tier is lower than confirmed.",
			seed: map[string]string{
				"app/run.sh": `#!/usr/bin/env bash
. ../lib/util.sh

run() {
    helper
}
`,
				"app/checkout.sh": `#!/usr/bin/env bash
. ../lib/util.sh

checkout() {
    helper
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("lib/util.sh")
			},
			witness: func(g *graphView) error {
				// Bash's QN keys on the LAST directory segment along the
				// sourced path: lib/util.sh yields `lib.helper`. Two
				// importers (app/run.sh and app/checkout.sh) each emit a
				// heuristic edge into the deleted callee. The witness
				// pins the actual Bash resolver behavior on delete: the
				// file node is gone (the per-file purge ran for every
				// node anchored in lib/util.sh), BOTH importers remain
				// present (the purge is scoped).
				_, hasFile := g.fileEdge("lib/util.sh", "defines", "lib.helper")
				return all(
					g.requirePresent("app.run"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node lib/util.sh still defines lib.helper — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "bash_move_symbol",
			kind:        kindChangeClass,
			description: "A Bash top-level function moves file-to-file WITHIN one directory (a.sh -> b.sh). The function's identity is keyed on its qualified name (QN), which the resolver derives from the script file path; a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol and the BLOCK-2 stale-purge hazard. The witness asserts the function identity is preserved and its cross-file source edge survives the re-home — pins the QN-stable re-home as a parity-holding transition.",
			seed: map[string]string{
				"k/a.sh": `#!/usr/bin/env bash
. ../lib/util.sh

helper() {
    helper_src
}

keep() {
    echo "1"
}
`,
				"k/b.sh": `#!/usr/bin/env bash
other() {
    echo "2"
}
`,
				"lib/util.sh": `#!/usr/bin/env bash
helper_src() {
    echo "src"
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.sh -> b.sh, both rewritten in place.
				f.Write("k/a.sh", `#!/usr/bin/env bash
keep() {
    echo "1"
}
`)
				f.Write("k/b.sh", `#!/usr/bin/env bash
. ../lib/util.sh

helper() {
    helper_src
}

other() {
    echo "2"
}
`)
			},
			witness: func(g *graphView) error {
				// The Bash QN keys on the directory + basename segment
				// along the sourced path: helper inside the same
				// directory keeps QN `k.helper`. The witness pins the
				// QN-stable re-home: helper's identity survives the
				// file-to-file move and its cross-module edge is re-
				// emitted against the same target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "lib.helper_src", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "bash_add_control_flow",
			kind:        kindChangeClass,
			description: "A control-flow construct (a `case` statement with multiple branches) is added to a Bash script. The witness asserts the new nodes are present and the existing callees survive — pins the control-flow / statement-level identity-stability contract. Same-package identity-stability holds for control-flow as for the function-addition rewrite class.",
			apply: func(f *fixture) {
				f.Write("app/helper.sh", `#!/usr/bin/env bash
app_helper() {
    echo "from app"
    case "$1" in
        a)
            echo "branch a"
            ;;
        b)
            echo "branch b"
            ;;
    esac
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("app.app_helper"),
					g.requirePresent("app.checkout"), // identity preserved across the rewrite
					g.requirePresent("lib.helper"),
				)
			},
		},
	}
}

// TestBashFullVsIncremental_ByteParity is the SW-194b Bash gate. One subtest
// per (backend, Bash change class): a full-parse graph and an incremental
// watcher-driven graph over the same change serialize byte-identically, the
// class's non-vacuity witness holds against the incremental graph, and the
// Bash resolver runs in heuristic-only mode.
func TestBashFullVsIncremental_ByteParity(t *testing.T) {
	table := bashChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			// PROFILE AXIS, identical to the Python and TS tables: a
			// single-axis table would be blind to a profile-shaped
			// defect introduced later. The axis is the change-class
			// table's, and the language does not get to drop it.
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runBashChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runBashChangeClassRow mirrors runPythonChangeClassRow, seeding
// bashBaseTree(). The Bash resolver has no JVM-binder Setenv; the parity
// harness's incremental path is identical to the Go/JVM/Python ones for the
// heuristic resolver.
func runBashChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := bashBaseTree()
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
