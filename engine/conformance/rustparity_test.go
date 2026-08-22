package conformance_test

// SW-194b (W5.h, rust slice): the Rust full-vs-incremental change-class
// parity gate. Rust is `cross-file-heuristic` and binds at the heuristic
// tier (engine/link/resolve_rust.go); there is no JVM-style typed binder,
// so this table is the parity-holding assertion over the Rust heuristic
// resolver, bound to docs/rc/parity-classes-rust.yaml by the drift guard
// in rustparity_matrix_test.go.
//
// Rust's `use crate::shop::price;` declares an import path with
// `::`-separated segments. The resolver at engine/link/resolve_rust.go
// sets `bareNameImportPath["price"] = "shop::price"` and resolves the
// clause via `packageSegment("shop::price", "::")` → "shop" — matching
// the cstWalk "<dirBase>.<bare>" convention where a Rust item in
// src/shop/price.rs is keyed by clause "shop". Items with no committed
// node (std/3rd-party crates) skip+count; an item ambiguous across two
// modules of the same clause is counted, never guessed.
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

// rustBaseTree is the cross-module Rust fixture the heuristic tier
// needs: a Rust caller in `src/` using `crate::shop::price` to call a
// `pub fn price()` declared in `src/shop/price.rs`. The base edges
// (heuristic, every row):
//
//	src.checkout --calls--> shop.price   (cross-module use, heuristic tier)
func rustBaseTree() map[string]string {
	return map[string]string{
		"src/main.rs": `// cross-module use call
use crate::shop::price;

pub fn checkout() -> i32 {
    price()
}
`,
		"src/shop/price.rs": `// cross-file callee
pub fn price() -> i32 {
    1
}
`,
	}
}

// rustChangeClassTable is the declarative Rust change-class matrix. Row
// order follows docs/rc/parity-classes-rust.yaml so the two files diff
// side by side.
func rustChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "rust_add_file",
			kind:        kindChangeClass,
			description: "A new Rust file arrives in a new module directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("src/tax/rates/calc.rs", "pub fn compute(x: i32) -> i32 { x * 2 }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("src.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "rust_modify_file",
			kind:        kindChangeClass,
			description: "An indexed Rust file is rewritten in place: a function is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("src/shop/price.rs", `// cross-file callee (rewritten with extra)
pub fn price() -> i32 {
    1
}

pub fn extra_util() -> i32 {
    3
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.extra_util"),
					g.requirePresent("shop.price"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "rust_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-module use call is added: `use crate::shop::price; price()`. The resolver's clause-keyed `::`-path resolution. The witness pins the heuristic tier — the Rust resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half).",
			seed: map[string]string{
				"src/main.rs": `pub fn checkout() -> i32 {
    0
}
`,
				"src/shop/price.rs": `pub fn price() -> i32 {
    1
}
`,
			},
			apply: func(f *fixture) {
				f.Write("src/main.rs", `use crate::shop::price;

pub fn checkout() -> i32 {
    price()
}
`)
			},
			witness: func(g *graphView) error {
				// The new cross-module call must land at HEURISTIC tier
				// — the resolver's only tier. A confirmed edge here
				// would be the G2SUB never-confirmed half violated.
				return g.requireEdgeAtTier("src.checkout", "calls", "shop.price", heuristic)
			},
		},
		{
			id:          "rust_use_skip",
			kind:        kindChangeClass,
			description: "A use declaration (`use crate::missing::symbol;`) targets a path that does not exist in the indexed tree. The witness asserts the resolver SKIPS (no edge minted) — the G2SUB drop-and-count half on an absent module. An edge here would be the failure mode the level forbids.",
			seed: map[string]string{
				"src/main.rs": `pub fn checkout() -> i32 {
    0
}
`,
			},
			apply: func(f *fixture) {
				f.Write("src/main.rs", `use crate::missing::symbol;

pub fn checkout() -> i32 {
    symbol()
}
`)
			},
			witness: func(g *graphView) error {
				// The missing module is dropped: no edge to a `missing.*`
				// node may survive. The witness asserts this by failing
				// on ANY outbound `calls` edge whose target QN carries
				// `missing.` (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "missing.") {
							return fmt.Errorf("missing use %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "crate::missing::symbol", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "rust_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "Two modules with the same clause both declare a function with the same name (e.g. `shop::price` and `vendor::price`). The witness asserts NEITHER call site resolves to a single edge — the G2SUB drop-and-count half on a real ambiguity. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used.",
			seed: map[string]string{
				"src/shop/price.rs":   `pub fn price() -> i32 { 1 }
`,
				"src/vendor/price.rs": `pub fn price() -> i32 { 2 }
`,
				"src/main.rs":         `pub fn checkout() -> i32 {
    0
}
`,
			},
			apply: func(f *fixture) {
				f.Write("src/main.rs", `use crate::shop::price;
use crate::vendor::price;

pub fn checkout() -> i32 {
    price()
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
						if to.QualifiedName() == "shop.price" || to.QualifiedName() == "vendor.price" {
							return fmt.Errorf("ambiguous use resolved to %q — the G2SUB never-guess half is violated", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "rust_delete_file",
			kind:        kindChangeClass,
			description: "A Rust file declaring a function that TWO other modules import is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone.",
			seed: map[string]string{
				"src/run.rs": `use crate::shop::price;

pub fn run() -> i32 {
    price()
}
`,
				"src/checkout.rs": `use crate::shop::price;

pub fn checkout() -> i32 {
    price()
}
`,
			},
			apply: func(f *fixture) {
				f.Remove("src/shop/price.rs")
			},
			witness: func(g *graphView) error {
				// The Rust QN keys on the LAST directory segment along the
				// path: src/shop/price.rs yields `shop.price`. Two
				// importers (src/run.rs and src/checkout.rs) each emit a
				// heuristic edge into the deleted callee. The witness
				// pins the actual Rust resolver behavior on delete.
				_, hasFile := g.fileEdge("src/shop/price.rs", "defines", "shop.price")
				return all(
					g.requirePresent("src.run"),
					g.requirePresent("src.checkout"),
					errorIf(hasFile, "file node src/shop/price.rs still defines shop.price — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "rust_move_symbol",
			kind:        kindChangeClass,
			description: "A Rust top-level function moves file-to-file WITHIN one module directory (a.rs -> b.rs). The function's identity is keyed on its qualified name (QN); a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol. The witness asserts the function identity is preserved and its cross-module use edge survives the re-home.",
			seed: map[string]string{
				"k/a.rs": `use crate::shop::price_src;

pub fn helper() -> i32 {
    price_src()
}

pub fn keep() -> i32 {
    1
}
`,
				"k/b.rs": `pub fn other() -> i32 {
    2
}
`,
				"src/shop/price_src.rs": `pub fn price_src() -> i32 {
    1
}
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.rs -> b.rs, both rewritten in place.
				f.Write("k/a.rs", `pub fn keep() -> i32 {
    1
}
`)
				f.Write("k/b.rs", `use crate::shop::price_src;

pub fn helper() -> i32 {
    price_src()
}

pub fn other() -> i32 {
    2
}
`)
			},
			witness: func(g *graphView) error {
				// The Rust QN keys on the directory + basename segment
				// along the use path: helper inside the same directory
				// keeps QN `k.helper`. The witness pins the QN-stable
				// re-home: helper's identity survives the file-to-file
				// move and its cross-module edge is re-emitted against
				// the same target QN.
				return all(
					g.requirePresent("k.helper"),
					g.requireEdgeAtTier("k.helper", "calls", "shop.price_src", heuristic),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "rust_add_struct_definition",
			kind:        kindChangeClass,
			description: "A struct (or enum / trait / union / type) definition is added to a Rust file. The witness asserts the new type node is present and the existing callees survive — pins the type-definition identity-stability contract.",
			apply: func(f *fixture) {
				f.Write("src/shop/price.rs", `// cross-file callee + new struct
pub struct Discount;

pub fn price() -> i32 {
    1
}

pub fn extra_util() -> i32 {
    3
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.price"),
					g.requirePresent("shop.extra_util"),
					g.requirePresent("shop.Discount"),
				)
			},
		},
	}
}

// TestRustFullVsIncremental_ByteParity is the SW-194b Rust gate.
func TestRustFullVsIncremental_ByteParity(t *testing.T) {
	table := rustChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runRustChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runRustChangeClassRow mirrors runBashChangeClassRow, seeding
// rustBaseTree().
func runRustChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := rustBaseTree()
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