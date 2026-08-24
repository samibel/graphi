package conformance_test

// SW-199 (W5.m, intra/parse residual, toml slice e): the TOML
// parse-determinism and intra-file parity gate, bound to
// docs/rc/parity-classes-toml.yaml by the drift guard in
// tomlparity_matrix_test.go.
//
// LEVEL, AND WHAT THAT MEANS FOR THE WITNESS SHAPE. TOML is `intra-file-only`.
// The rows prove PARSE DETERMINISM — the same input bytes produce the same AST
// and its serialization is byte-stable — plus intra-file full-vs-incremental
// parity on both stores and both profile axes. Every cross-file Go class is
// dispositioned `not_applicable` with a language-spec reason in the YAML's
// `go_class_applicability:` block.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one. TOML v1.0.0's
// specification defines exactly one document per file: keys, values, tables,
// arrays of tables, inline tables. It defines NO include, NO import and NO
// reference to another document — an `include = "other.toml"` key is an
// ordinary string-valued key to which some HOST APPLICATION may attach
// meaning, and TOML's spec says nothing about it. So TOML's own answer to
// "does the language define a cross-file reference?" is no, and that is the
// spec-level abstention this family records. Citing graphi's TOML parser
// comment instead would be the LANGHONEST-001 circular-abstention defect.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures. NOT the real-repository matrix (G4), NOT a correctness proof of the
// TOML grammar — correctness evidence lives in core/parse/parser_toml_test.go.

import (
	"testing"
)

// tomlBaseTree is the TOML fixture every row starts from: two documents in one
// directory. TOML symbol QNs are `<dir>.<table header>` for tables (kind
// `type`) and `<dir>.<key>` for top-level key/value pairs (kind `variable`).
func tomlBaseTree() map[string]string {
	return map[string]string{
		"conf/app.toml": `title = "demo"

[server]
port = 8080

[client]
retries = 3
`,
		"conf/db.toml": `dsn = "local"

[database]
host = "localhost"
`,
	}
}

// tomlParityTable is the declarative TOML matrix. Row order follows
// docs/rc/parity-classes-toml.yaml so the two files diff side by side.
func tomlParityTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "toml_add_file",
			kind:        kindChangeClass,
			description: "A new TOML document arrives in a new directory: the pure add path. The witness requires the new table and key symbols and the base-tree control symbol, so a row that indexed nothing cannot pass.",
			apply: func(f *fixture) {
				f.Write("tools/lint.toml", "profile = \"strict\"\n\n[rules]\nmax = 3\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("tools.profile", "tools.rules"),
					requireFileNode(g, "tools/lint.toml"),
					g.requirePresent("conf.title"),
				)
			},
		},
		{
			id:          "toml_modify_file",
			kind:        kindChangeClass,
			description: "An indexed TOML document is rewritten in place: a table is appended while the existing key and tables keep identity. The witness requires the new table AND all three pre-existing symbols.",
			apply: func(f *fixture) {
				f.Write("conf/app.toml", `title = "demo"

[server]
port = 8080

[client]
retries = 3

[metrics]
enabled = true
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("conf.metrics"),
					g.requirePresent("conf.title", "conf.server", "conf.client"),
				)
			},
		},
		{
			id:          "toml_delete_file",
			kind:        kindChangeClass,
			description: "An indexed TOML document is deleted, so the per-file stale-node purge runs over it. The witness requires the deleted document's key and table symbols AND its file node gone, and the sibling document's symbols still present — a stale node surviving a delete is the failure this row exists to catch.",
			apply: func(f *fixture) {
				f.Remove("conf/db.toml")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("conf.dsn", "conf.database"),
					requireNoFileNode(g, "conf/db.toml"),
					g.requirePresent("conf.title", "conf.server", "conf.client"),
				)
			},
		},
		{
			id:          "toml_rename_table",
			kind:        kindChangeClass,
			description: "A table header is renamed in place ([client] -> [worker]). The symbol's qualified name is the table header, so the rename is a delete-plus-add inside one file. The witness requires the new name present AND the old name absent.",
			apply: func(f *fixture) {
				f.Write("conf/app.toml", `title = "demo"

[server]
port = 8080

[worker]
retries = 3
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("conf.worker"),
					g.requireAbsent("conf.client"),
					g.requirePresent("conf.title", "conf.server"),
				)
			},
		},
		{
			id:          "toml_reorder_tables",
			kind:        kindChangeClass,
			description: "The two tables of one document are swapped with no textual change to either table's body. The symbol SET is unchanged and only source order and line numbers move, which is the sharpest test of the canonical-ordering discipline: a snapshot whose row order followed parse order rather than a canonical key would diverge here between the incremental and full passes.",
			apply: func(f *fixture) {
				f.Write("conf/app.toml", `title = "demo"

[client]
retries = 3

[server]
port = 8080
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("conf.title", "conf.server", "conf.client"),
					g.requireLine("conf.client", 3),
					g.requireLine("conf.server", 6),
				)
			},
		},
		{
			id:          "toml_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "An indexed TOML document is rewritten with BYTE-IDENTICAL content. This is the direct same-bytes-same-AST row: the drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes. Its witness pins the surviving symbol set rather than a new symbol, because the change is deliberately semantics-preserving — the row's force comes from the runner's two independent full passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("conf/app.toml", `title = "demo"

[server]
port = 8080

[client]
retries = 3
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("conf.title", "conf.server", "conf.client"),
					g.requirePresent("conf.dsn", "conf.database"),
					requireFileNode(g, "conf/app.toml"),
				)
			},
		},
	}
}

// TestTomlParityDeterminism_ByteStable is the SW-199 TOML gate. One subtest per
// (backend, profile, row): two independent full passes over the identical bytes
// serialize identically (parse determinism), the incremental watcher-driven
// graph equals the full graph (intra-file parity), and the row's non-vacuity
// witness holds.
func TestTomlParityDeterminism_ByteStable(t *testing.T) {
	base := tomlBaseTree()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range tomlParityTable() {
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
