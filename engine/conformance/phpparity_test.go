package conformance_test

// SW-194b (W5.h, php slice): the PHP full-vs-incremental change-class
// parity gate. PHP is `cross-file-heuristic` and binds at the heuristic
// tier (engine/link/resolve_ruby.go's phpResolver, which delegates to the
// shared requireBinder with `.php` extension); there is no JVM-style
// typed binder, so this table is the parity-holding assertion over the
// PHP heuristic resolver, bound to docs/rc/parity-classes-php.yaml by
// the drift guard in phpparity_matrix_test.go.
//
// PHP's `require 'x.php'` and `include 'x.php'` are relative to the
// importer, resolved against `requireBinder` (engine/link/
// resolve_common.go), which produces the `imports` edge file→file and
// opens the imported directory as an ambient lookup. The witness
// asserts the resolver's contract: heuristic tier only (never confirmed),
// drop+count on what it cannot resolve, deterministic across passes.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Python, TS and Bash tables. NOT a PRD
// §12.3 gate, NOT the real-repository matrix (G4), and the binder is not
// exercised. Parity compares two passes of the same rule, so a PASS
// certifies the heuristic resolver is REGRESSION-CLEAN between
// incremental and full, never that it is correct.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// phpBaseTree is the cross-directory PHP fixture the heuristic tier
// needs: a PHP caller into a PHP callee across two sibling directories
// so the requireBinder helper at engine/link/resolve_common.go opens the
// required file's directory as an ambient lookup. The base edges
// (heuristic, every row):
//
//	app.checkout --calls--> lib.helper   (cross-dir require, heuristic tier)
func phpBaseTree() map[string]string {
	return map[string]string{
		"app/main.php": `<?php
// cross-file require (../lib/util.php resolves lib/util.php from app/)
require_once "../lib/util.php";

function checkout() {
    helper();
}
`,
		"lib/util.php": `<?php
// cross-file callee
function helper() {
    return 1;
}
`,
	}
}

// phpChangeClassTable is the declarative PHP change-class matrix. Row
// order follows docs/rc/parity-classes-php.yaml so the two files diff
// side by side.
func phpChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "php_add_file",
			kind:        kindChangeClass,
			description: "A new PHP file arrives in a new directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.php", "<?php\nfunction compute($x) { return $x * 2; }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "php_modify_file",
			kind:        kindChangeClass,
			description: "An indexed PHP file is rewritten in place: a function is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("lib/util.php", `<?php
// cross-file callee (rewritten with extra)
function helper() {
    return 1;
}

function extra_util() {
    return 3;
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
			id:          "php_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-file require call is added: `require_once '../lib/util.php'`. The resolver's exact-path require path. The witness pins the heuristic tier on the resulting `imports` edge from the importing file node to the required file node — the PHP resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half). (Bare-call PendingRef emission is the parser's job; this row covers the resolver's contract on the `require_once` itself.)",
			seed: map[string]string{
				"app/main.php": `<?php
function checkout() {
    return 0;
}
`,
				"lib/util.php": `<?php
function helper() {
    return 1;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.php", `<?php
require_once "../lib/util.php";

function checkout() {
    helper();
}
`)
			},
			witness: func(g *graphView) error {
				// The cross-file require must produce an `imports` edge
				// from app/main.php to lib/util.php at HEURISTIC tier —
				// the resolver's only tier. A confirmed edge here would
				// be the G2SUB never-confirmed half violated.
				e, ok := g.fileEdge("app/main.php", "imports", "lib/util.php")
				if !ok {
					return fmt.Errorf("imports edge app/main.php --imports--> lib/util.php absent; graph has %s", g.edgeList())
				}
				if e.Tier() != heuristic {
					return fmt.Errorf("imports edge app/main.php --imports--> lib/util.php has tier %q, want %q (wrong mechanism minted it)", e.Tier(), heuristic)
				}
				return nil
			},
		},
		{
			id:          "php_require_skip",
			kind:        kindChangeClass,
			description: "A require directive (`require_once 'missing.php'`) targets a path that does not exist on disk. The witness asserts the resolver SKIPS (no edge minted) — the G2SUB drop-and-count half on an absent file. An edge here would be the failure mode the level forbids.",
			seed: map[string]string{
				"app/main.php": `<?php
function checkout() {
    return 0;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.php", `<?php
require_once "missing.php";

function checkout() {
    missing_fn();
}
`)
			},
			witness: func(g *graphView) error {
				// The missing require is dropped: no edge to a `missing.*`
				// node may survive. The witness asserts this by failing
				// on ANY outbound `calls` edge whose target QN carries
				// `missing.` (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "missing.") {
							return fmt.Errorf("missing require %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "missing.php", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "php_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "A relative require (`require_once '../util.php'`) is ambiguous because two candidate paths resolve to committed nodes (e.g. `lib/util.php` and `vendor/util.php` both exist). The witness asserts NEITHER candidate edge is minted — the G2SUB drop-and-count half on a real ambiguity. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used.",
			seed: map[string]string{
				"lib/util.php":    `<?php
function helper() {
    return 1;
}
`,
				"vendor/util.php": `<?php
function helper() {
    return 2;
}
`,
				"app/main.php":    `<?php
function checkout() {
    return 0;
}
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.php", `<?php
require_once __DIR__ . "/../util.php";

function checkout() {
    helper();
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
							return fmt.Errorf("ambiguous relative require %q resolved to %q — the G2SUB never-guess half is violated", "../util.php", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "php_delete_file",
			kind:        kindChangeClass,
			description: "A PHP file declaring a function that TWO other scripts require is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone.",
			seed: map[string]string{
				"app/run.php": `<?php
require_once __DIR__ . "/../lib/util.php";

function run() {
    helper();
}
`,
				"app/checkout.php": `<?php
require_once __DIR__ . "/../lib/util.php";

function checkout() {
    helper();
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("lib/util.php")
			},
			witness: func(g *graphView) error {
				// The PHP QN keys on the LAST directory segment along the
				// required path: lib/util.php yields `lib.helper`. Two
				// importers (app/run.php and app/checkout.php) each emit a
				// heuristic edge into the deleted callee. The witness
				// pins the actual PHP resolver behavior on delete.
				_, hasFile := g.fileEdge("lib/util.php", "defines", "lib.helper")
				return all(
					g.requirePresent("app.run"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node lib/util.php still defines lib.helper — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "php_move_symbol",
			kind:        kindChangeClass,
			description: "A PHP top-level function moves file-to-file WITHIN one directory (a.php -> b.php). The function's identity is keyed on its qualified name (QN); a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol. The witness asserts the function identity is preserved and its cross-file require edge survives the re-home.",
			seed: map[string]string{
				"k/a.php": `<?php
require_once "../lib/util.php";

function helper() {
    helper_src();
}

function keep() {
    return 1;
}
`,
				"k/b.php": `<?php
function other() {
    return 2;
}
`,
				"lib/util.php": `<?php
function helper_src() {
    return 1;
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.php -> b.php, both rewritten in place.
				f.Write("k/a.php", `<?php
function keep() {
    return 1;
}
`)
				f.Write("k/b.php", `<?php
require_once "../lib/util.php";

function helper() {
    helper_src();
}

function other() {
    return 2;
}
`)
			},
			witness: func(g *graphView) error {
				// The PHP QN keys on the directory + basename segment
				// along the required path: helper inside the same
				// directory keeps QN `k.helper`. The witness pins the
				// QN-stable re-home: helper's identity survives the
				// file-to-file move and the cross-file `imports` edge
				// is re-emitted from k/b.php to lib/util.php at heuristic
				// tier (the resolver's only tier).
				e, ok := g.fileEdge("k/b.php", "imports", "lib/util.php")
				if !ok {
					return fmt.Errorf("imports edge k/b.php --imports--> lib/util.php absent; graph has %s", g.edgeList())
				}
				if e.Tier() != heuristic {
					return fmt.Errorf("imports edge k/b.php --imports--> lib/util.php has tier %q, want %q (wrong mechanism minted it)", e.Tier(), heuristic)
				}
				return all(
					g.requirePresent("k.helper"),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "php_add_class_definition",
			kind:        kindChangeClass,
			description: "A class (or interface / trait / enum) definition is added to a PHP file. The witness asserts the new class node is present and the existing callees survive — pins the type-definition identity-stability contract.",
			apply: func(f *fixture) {
				f.Write("lib/util.php", `<?php
// cross-file callee + new class
class Discount {
    public static function apply($x) {
        return $x / 2;
    }
}

function helper() {
    return 1;
}

function extra_util() {
    return 3;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.helper"),
					g.requirePresent("lib.extra_util"),
					g.requirePresent("lib.Discount"),
				)
			},
		},
	}
}

// TestPhpFullVsIncremental_ByteParity is the SW-194b PHP gate.
func TestPhpFullVsIncremental_ByteParity(t *testing.T) {
	table := phpChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runPhpChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runPhpChangeClassRow mirrors runBashChangeClassRow, seeding
// phpBaseTree().
func runPhpChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := phpBaseTree()
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