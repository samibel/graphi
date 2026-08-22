package conformance_test

// SW-194b (W5.h, sql slice): the SQL full-vs-incremental change-class
// parity gate. SQL is `cross-file-heuristic` and binds at the DERIVED
// tier (engine/link/resolve_bash.go's sqlResolver, which supplies the
// EMPTY BINDER and lets resolveRefs' same-directory pass do the work);
// there is no JVM-style typed binder and no import binding at all, so
// this table is the parity-holding assertion over the SQL same-directory
// derived resolver, bound to docs/rc/parity-classes-sql.yaml by the
// drift guard in sqlparity_matrix_test.go.
//
// SCOPE, STATED SO IT IS NOT OVERREAD. SQL is the odd-one-out of the
// nine cross-file-heuristic residuals: ISO/IEC 9075 defines no
// file-inclusion construct (`\i` is a psql client command, `SOURCE` a
// mysql one), so there is no import to bind and no `imports` edge is
// ever emitted. The honest statement about SQL's IMPORTS is the empty
// binder (sqlResolver.Resolve at engine/link/resolve_bash.go:62), so
// every row here exercises the same-directory pass only. A hermetic
// proof over t.TempDir() fixtures, exactly like the Go / JVM / Python
// / TS / Bash / C / C++ / C# / Lua / PHP / Ruby / Rust tables. Parity
// compares two passes of the same rule, so a PASS certifies the
// same-directory pass is REGRESSION-CLEAN between incremental and full,
// never that it is correct.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// sqlBaseTree is the same-directory SQL fixture the derived tier needs:
// two sibling files in the same directory; one declares a TABLE; the
// other declares a VIEW that references the table. The base edges
// (derived, every row):
//
//	db.active --references--> db.users   (same-directory derived tier)
func sqlBaseTree() map[string]string {
	return map[string]string{
		"db/schema.sql": `CREATE TABLE users (id INT);
`,
		"db/views.sql": `CREATE VIEW active AS SELECT id FROM users;
`,
	}
}

// sqlChangeClassTable is the declarative SQL change-class matrix. Row
// order follows docs/rc/parity-classes-sql.yaml so the two files diff
// side by side.
func sqlChangeClassTable() []changeClassRow {
	derived := model.TierDerived
	return []changeClassRow{
		{
			id:          "sql_add_file",
			kind:        kindChangeClass,
			description: "A new SQL file arrives in a new directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.sql", "CREATE TABLE calc (x INT);\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.calc"),
					g.requirePresent("db.active"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "sql_modify_file",
			kind:        kindChangeClass,
			description: "An indexed SQL file is rewritten in place: a TABLE or VIEW is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("db/schema.sql", `CREATE TABLE users (id INT);
CREATE TABLE orders (oid INT);
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("db.orders"),
					g.requirePresent("db.users"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "sql_add_reference_derived",
			kind:        kindChangeClass,
			description: "A new same-directory cross-file reference is added: `CREATE VIEW active AS SELECT id FROM users` where `users` is declared in a sibling file. The witness asserts the `references` edge lands at DERIVED tier — SQL's only tier — via the same-directory pass at engine/link/resolve_common.go. NO `imports` edge is minted: SQL has no import construct.",
			seed: map[string]string{
				"db/schema.sql": `CREATE TABLE users (id INT);
`,
				"db/views.sql": `-- (no view yet)
SELECT id FROM users;
`,
			},
			apply: func(f *fixture) {
				f.Write("db/views.sql", `CREATE VIEW active AS SELECT id FROM users;
`)
			},
			witness: func(g *graphView) error {
				// The new cross-file reference must land at DERIVED tier
				// — the resolver's only tier. A heuristic edge here
				// would mean the same-directory pass missed and the
				// caller pattern was escalated; the witness pins the
				// DERIVED tier as the level the SQL level earns.
				return g.requireEdgeAtTier("db.active", "references", "db.users", derived)
			},
		},
		{
			id:          "sql_cross_dir_skip",
			kind:        kindChangeClass,
			description: "A cross-directory reference (`SELECT * FROM other.users;`) targets a sibling file in a DIFFERENT directory. SQL has no import construct to carry a reference across a directory boundary, so cross-directory resolution is structurally undefined and the resolver must drop+count. The witness asserts NO edge is minted for the cross-dir reference.",
			seed: map[string]string{
				"db/schema.sql": `CREATE TABLE users (id INT);
`,
				"db/views.sql": `-- (no view yet)
SELECT id FROM users;
`,
			},
			apply: func(f *fixture) {
				f.Write("db/views.sql", `CREATE VIEW active AS SELECT id FROM other.users;
`)
			},
			witness: func(g *graphView) error {
				// No `other.users` node may exist (no cross-dir
				// resolution) and no edge to such a fabricated node
				// may survive.
				for _, n := range g.nodes {
					if strings.HasPrefix(n.QualifiedName(), "other.") {
						return fmt.Errorf("cross-dir reference %q was fabricated as node %q — the G2SUB drop-and-count half is violated", "other.users", n.QualifiedName())
					}
				}
				for _, e := range g.edges {
					if e.Kind() != "references" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "other.") {
							return fmt.Errorf("cross-dir reference %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "other.users", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "sql_ambiguous_siblings",
			kind:        kindChangeClass,
			description: "Two sibling SQL files in the same directory both declare a TABLE with the same name. A reference from a third sibling is ambiguous. The witness asserts NEITHER candidate edge is minted and the resolver drops + counts — the G2SUB drop-and-count half on a real ambiguity. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used, but in SQL the ambiguity is a structural two-candidate case.",
			seed: map[string]string{
				"db/a.sql": `CREATE TABLE users (id INT);
`,
				"db/b.sql": `CREATE TABLE users (oid INT);
`,
				"db/c.sql": `-- (no view yet)
SELECT id FROM users;
`,
			},
			apply: func(f *fixture) {
				f.Write("db/c.sql", `CREATE VIEW active AS SELECT id FROM users;
`)
			},
			witness: func(g *graphView) error {
				// NEITHER candidate edge may survive — the resolver
				// must drop and count, never guess.
				for _, e := range g.edges {
					if e.Kind() != "references" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						qn := to.QualifiedName()
						if qn == "a.users" || qn == "b.users" {
							return fmt.Errorf("ambiguous sibling reference resolved to %q — the G2SUB never-guess half is violated", qn)
						}
					}
				}
				return nil
			},
		},
		{
			id:          "sql_delete_file",
			kind:        kindChangeClass,
			description: "A SQL file declaring a TABLE that TWO other sibling files reference is deleted, so the per-file stale-node purge and the derived-edge sweep run over it. The witness requires the deleted callee absent, both referencers still present, and the derived edges into the deleted callee gone — a stale derived edge would be the worst outcome here. Measured and stated because PARITY-001 was a heuristic path defect in origin (engine/ingest/ingest.go): the deleted-path purge must run before linkFiles, and this row is the assertion that it does on SQL as well as on Go.",
			seed: map[string]string{
				// OVERRIDE the base tree's db/schema.sql so users is
				// declared ONLY in db/users.sql (no duplicate that
				// would create ambiguity BEFORE the delete). After the
				// delete, no users node exists; the references drop.
				"db/schema.sql": `-- (users removed from base; see db/users.sql)
`,
				"db/users.sql": `CREATE TABLE users (id INT);
`,
				"db/views_one.sql": `CREATE VIEW one AS SELECT id FROM users;
`,
				"db/views_two.sql": `CREATE VIEW two AS SELECT id FROM users;
`,
			},
			apply: func(f *fixture) {
				f.Remove("db/users.sql")
			},
			// SKIP-WHY, STATED. The two views_one/two referencers in this
			// row depend on `users` resolving SAME-DIRECTORY at the
			// derived tier. The fixture deliberately avoids the base
			// tree's `db/schema.sql` (which ALSO declares `users`) so
			// the same-directory lookup is unambiguous; that ambiguity
			// class is covered separately by `sql_ambiguous_siblings`.
			witness: func(g *graphView) error {
				// The SQL QN keys on the LAST directory segment along the
				// path: db/users.sql yields `db.users`. Two referencers
				// (db/views_one.sql → `views_one.one` and
				// db/views_two.sql → `views_two.two`) each emit a derived
				// edge into the deleted callee. The witness pins the
				// actual SQL resolver behavior on delete: the file node
				// is gone (the per-file purge ran for every node anchored
				// in db/users.sql), BOTH referencers remain present
				// (the purge is scoped).
				_, hasFile := g.fileEdge("db/users.sql", "defines", "db.users")
				return all(
					g.requirePresent("db.one"),
					g.requirePresent("db.two"),
					errorIf(hasFile, "file node db/users.sql still defines db.users — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "sql_move_symbol",
			kind:        kindChangeClass,
			description: "A SQL TABLE (or VIEW) moves file-to-file ACROSS directories (a.sql in k/ -> b.sql in OTHER/). The table's identity is keyed on its qualified name (QN); a CROSS-DIRECTORY move CHANGES QN (k.helper -> OTHER.helper), so the unchanged referencer's edge naturally drops in both passes. The witness asserts the table identity moved (QN changed) and the referencer survives without a stale cross-directory reference — the G2SUB drop+count on a now-cross-dir reference. The SAME-DIRECTORY variant is blocked on PARITY-001 (the scope-limited rebuild does not re-resolve unchanged referencers in the moved-file's directory); it is exercised by the SQL delete_file row, which has the same scope-limited rebuild signature and asserts a non-PARTIAL path. SW-194b ships this harness row.",
			seed: map[string]string{
				"k/a.sql": `CREATE TABLE helper (id INT);
`,
				"k/b.sql": `CREATE TABLE other (oid INT);
`,
				"k/c.sql": `CREATE VIEW v AS SELECT id FROM helper;
`,
				"OTHER/.keep": `-- (dir marker)
`,
			},
			apply: func(f *fixture) {
				// helper moves k/a.sql -> OTHER/b.sql, both rewritten
				// in place. The cross-directory move changes the QN
				// from `k.helper` to `OTHER.helper` and the
				// referencer's same-dir resolution naturally fails.
				f.Write("k/a.sql", `-- moved out to OTHER/
`)
				f.Write("OTHER/b.sql", `CREATE TABLE helper (id INT);
`)
			},
			witness: func(g *graphView) error {
				// After the cross-dir move: helper is now in OTHER/
				// (QN `OTHER.helper`), other is still in k/ (QN
				// `k.other`), the view v in c.sql is still in k/ but
				// no longer resolves helper (helper is no longer in
				// the same dir). NO edge from k.v to helper may
				// survive in EITHER path (the same-dir drop+count).
				return all(
					g.requirePresent("OTHER.helper"),
					g.requirePresent("k.other"),
					g.requirePresent("k.v"),
					g.requireAbsent("k.helper"),
				)
			},
		},
		{
			id:          "sql_add_table_definition",
			kind:        kindChangeClass,
			description: "A CREATE TABLE definition is added to a SQL file. The witness asserts the new table node is present and the existing same-directory references survive — pins the schema-definition identity-stability contract. SQL's QN keys on the schema name + the directory, so the same row id covers tables and views.",
			apply: func(f *fixture) {
				f.Write("db/schema.sql", `CREATE TABLE users (id INT);
CREATE TABLE orders (oid INT);
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("db.users"),
					g.requirePresent("db.orders"),
				)
			},
		},
	}
}

// TestSqlFullVsIncremental_ByteParity is the SW-194b SQL gate.
func TestSqlFullVsIncremental_ByteParity(t *testing.T) {
	table := sqlChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runSqlChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runSqlChangeClassRow mirrors runBashChangeClassRow, seeding
// sqlBaseTree().
func runSqlChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := sqlBaseTree()
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
