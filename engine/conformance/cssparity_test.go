package conformance_test

// SW-199 (W5.m, intra/parse residual, css slice a): the CSS parse-determinism
// and intra-file parity gate, bound to docs/rc/parity-classes-css.yaml by the
// drift guard in cssparity_matrix_test.go.
//
// LEVEL, AND WHAT THAT MEANS FOR THE WITNESS SHAPE. CSS is `intra-file-only`.
// It has no cross-file construct graphi extracts, so there is no resolver
// contract to assert and no cross-file change class to witness: every
// cross-file Go class is dispositioned `not_applicable` with a language-spec
// reason in the YAML's `go_class_applicability:` block, which is data the
// guard reads, not prose in a note. What is left, and what these rows prove,
// is PARSE DETERMINISM — the same input bytes produce the same AST, and the
// AST's serialization is byte-stable — plus intra-file full-vs-incremental
// parity on both stores and both profile axes. The shared runner
// (intrafile_shared_test.go) states the three assertions it makes.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one. CSS's own spec
// DOES define a cross-file construct: `@import "base.css"` (CSS Cascading and
// Inheritance / CSS Syntax, the at-rule `@import`). graphi's CSS parser does
// not extract it, so the abstention here is graphi's, not CSS's, and the YAML
// says so in exactly those words. Citing graphi's parser comment as if it were
// the language's answer is the LANGHONEST-001 defect class; the YAML cites the
// CSS spec and then records what graphi does with it.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures. NOT the real-repository matrix (G4), NOT a correctness proof of the
// CSS grammar — parity and determinism compare two passes of the same rule, so
// a PASS certifies the rule is REGRESSION-CLEAN and REPRODUCIBLE, never that it
// is correct. Correctness evidence for the extractor lives in
// core/parse/parser_css_test.go.

import (
	"testing"
)

// cssBaseTree is the CSS fixture every row starts from: two stylesheets in one
// directory, so a delete has a surviving sibling and the qualified-name prefix
// (`site`, the containing directory) is shared. CSS symbol QNs are
// `<dir>.<selector>` and a selector keeps its punctuation, so `.cart` in
// `site/` is `site..cart` and `#hdr` is `site.#hdr`.
func cssBaseTree() map[string]string {
	return map[string]string{
		"site/theme.css": `.cart {
  color: red;
}

#hdr {
  margin: 0;
}
`,
		"site/layout.css": `.grid {
  display: grid;
}

.row {
  display: flex;
}
`,
	}
}

// cssParityTable is the declarative CSS matrix. Row order follows
// docs/rc/parity-classes-css.yaml so the two files diff side by side.
func cssParityTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "css_add_file",
			kind:        kindChangeClass,
			description: "A new stylesheet arrives in a new directory: the pure add path. The witness requires the new rule's selector symbol and the base-tree control symbol, so a row that indexed nothing cannot pass.",
			apply: func(f *fixture) {
				f.Write("brand/palette.css", ".accent {\n  color: blue;\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("brand..accent"),
					requireFileNode(g, "brand/palette.css"),
					g.requirePresent("site..cart"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "css_modify_file",
			kind:        kindChangeClass,
			description: "An indexed stylesheet is rewritten in place: a rule is appended while the existing rules keep identity. The witness requires the new selector AND both pre-existing selectors, so an apply that replaced the file wholesale would fail it.",
			apply: func(f *fixture) {
				f.Write("site/theme.css", `.cart {
  color: red;
}

#hdr {
  margin: 0;
}

.footer {
  padding: 1px;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("site..footer"),
					g.requirePresent("site..cart"),
					g.requirePresent("site.#hdr"),
				)
			},
		},
		{
			id:          "css_delete_file",
			kind:        kindChangeClass,
			description: "An indexed stylesheet is deleted, so the per-file stale-node purge runs over it. The witness requires the deleted file's selectors AND its file node gone, and the sibling stylesheet's selectors still present — a stale node surviving a delete is the failure this row exists to catch.",
			apply: func(f *fixture) {
				f.Remove("site/layout.css")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("site..grid", "site..row"),
					requireNoFileNode(g, "site/layout.css"),
					g.requirePresent("site..cart", "site.#hdr"),
				)
			},
		},
		{
			id:          "css_rename_selector",
			kind:        kindChangeClass,
			description: "A selector is renamed in place (.cart -> .basket). The symbol's qualified name is derived from the selector text, so the rename is a delete-plus-add inside one file. The witness requires the new name present AND the old name absent, which is what makes it fail if the apply did nothing.",
			apply: func(f *fixture) {
				f.Write("site/theme.css", `.basket {
  color: red;
}

#hdr {
  margin: 0;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("site..basket"),
					g.requireAbsent("site..cart"),
					g.requirePresent("site.#hdr"),
				)
			},
		},
		{
			id:          "css_reorder_rules",
			kind:        kindChangeClass,
			description: "The top-level rules of one stylesheet are permuted with no textual change to any rule body. The symbol SET is unchanged and only source order and line numbers move, which is the sharpest test of the canonical-ordering discipline: a snapshot whose row order followed parse order rather than a canonical key would diverge here between the incremental and full passes.",
			apply: func(f *fixture) {
				f.Write("site/layout.css", `.row {
  display: flex;
}

.grid {
  display: grid;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("site..grid", "site..row"),
					g.requireLine("site..row", 1),
					g.requireLine("site..grid", 5),
				)
			},
		},
		{
			id:          "css_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "An indexed stylesheet is rewritten with BYTE-IDENTICAL content. This is the direct same-bytes-same-AST row: the drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes. Its witness pins the surviving symbol set rather than a new symbol, because the change is deliberately semantics-preserving — the row's force comes from the runner's two independent full passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("site/theme.css", `.cart {
  color: red;
}

#hdr {
  margin: 0;
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("site..cart", "site.#hdr"),
					g.requirePresent("site..grid", "site..row"),
					requireFileNode(g, "site/theme.css"),
				)
			},
		},
	}
}

// TestCssParityDeterminism_ByteStable is the SW-199 CSS gate. One subtest per
// (backend, profile, row): two independent full passes over the identical bytes
// serialize identically (parse determinism), the incremental watcher-driven
// graph equals the full graph (intra-file parity), and the row's non-vacuity
// witness holds.
func TestCssParityDeterminism_ByteStable(t *testing.T) {
	base := cssBaseTree()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range cssParityTable() {
						row := row
						t.Run(row.id, func(t *testing.T) {
							t.Parallel()
							runIntraFileParityRow(t, b, pr, base, row)
						})
					}
				})
			}
		})
	}
}
