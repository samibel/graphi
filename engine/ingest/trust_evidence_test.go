package ingest_test

// P1 WP1.2 (PRD §14.3) — persisted per-file / per-package trust evidence in
// the ingest sidecar: migration 2 -> 3, generation-bound full/incremental
// write points, selective generation-checked read ports, and full-vs-
// incremental row parity.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// dumpEvidenceTables renders both evidence tables WITHOUT the generation
// column, deterministically ordered, so two stores can be compared modulo the
// per-pass generation nonce (the same discipline as trust.FactDigest).
func dumpEvidenceTables(ctx context.Context, t *testing.T, ing *ingest.Ingester) []string {
	t.Helper()
	var out []string
	rows, err := ing.MetaDB().QueryContext(ctx, `SELECT path, language, parse_status, parse_reason,
			resolved_derived, resolved_heuristic, resolved_external, skipped, ambiguous
		FROM trust_file_evidence ORDER BY path`)
	if err != nil {
		t.Fatalf("dump trust_file_evidence: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, lang, status, reason string
		var d, h, e, s, a int
		if err := rows.Scan(&p, &lang, &status, &reason, &d, &h, &e, &s, &a); err != nil {
			t.Fatalf("scan file evidence: %v", err)
		}
		out = append(out, fmt.Sprintf("file %s lang=%s status=%s reason=%s d=%d h=%d x=%d s=%d a=%d",
			p, lang, status, reason, d, h, e, s, a))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	prows, err := ing.MetaDB().QueryContext(ctx, `SELECT package_key, state, degraded_reason,
			type_errors, dropped_intents, confirmed_edges, skipped_files
		FROM trust_package_evidence ORDER BY package_key`)
	if err != nil {
		t.Fatalf("dump trust_package_evidence: %v", err)
	}
	defer prows.Close()
	for prows.Next() {
		var key, state, reason string
		var te, di, ce, sf int
		if err := prows.Scan(&key, &state, &reason, &te, &di, &ce, &sf); err != nil {
			t.Fatalf("scan package evidence: %v", err)
		}
		out = append(out, fmt.Sprintf("pkg %s state=%s reason=%s te=%d di=%d ce=%d sf=%d",
			key, state, reason, te, di, ce, sf))
	}
	if err := prows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTrustEvidence_FullPassWritesFixtureRows pins the full-pass rows for the
// canonical typeresolve fixture against hand-computed values.
//
// Hand computation: go.mod has no registered parser (silently untracked — no
// row). main.go parses as Go and carries two heuristic-tier linker
// resolutions — the selector call util.Answer resolved cross-package via the
// import alias, and the file→file imports edge to util/util.go — plus two
// honest skips: the extractor records the unresolved bare identifiers of
// main's body (`x` and the blank `_`, coalesced per name) as same-package
// candidates, and the linker drops+counts each on the index miss instead of
// fabricating an edge. util/util.go parses with no deferred refs (all
// counters zero). The resolver checks two units — "." (main) and "util" —
// with zero swallowed type errors (the intra-module import is served the REAL
// util package), zero dropped intents, and exactly one confirmed edge
// (main.main → util.Answer), owned by the from-node's package ".".
func TestTrustEvidence_FullPassWritesFixtureRows(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	files, err := ing.ListFileEvidence(ctx, gen, 16)
	if err != nil {
		t.Fatalf("ListFileEvidence: %v", err)
	}
	want := []ingest.FileEvidence{
		{Generation: gen, Path: "main.go", Language: "go", ParseStatus: ingest.ParseStatusParsed,
			ResolvedHeuristic: 2, Skipped: 2},
		{Generation: gen, Path: "util/util.go", Language: "go", ParseStatus: ingest.ParseStatusParsed},
	}
	if len(files) != len(want) {
		t.Fatalf("ListFileEvidence returned %d rows, want %d: %+v", len(files), len(want), files)
	}
	for k := range want {
		if files[k] != want[k] {
			t.Errorf("file row %d = %+v, want %+v", k, files[k], want[k])
		}
	}

	// Point lookup equals the listed row.
	fe, err := ing.FileEvidence(ctx, gen, "main.go")
	if err != nil {
		t.Fatalf("FileEvidence: %v", err)
	}
	if fe != want[0] {
		t.Errorf("FileEvidence(main.go) = %+v, want %+v", fe, want[0])
	}

	// The bounded list is genuinely bounded and keeps canonical path order.
	one, err := ing.ListFileEvidence(ctx, gen, 1)
	if err != nil {
		t.Fatalf("ListFileEvidence(limit 1): %v", err)
	}
	if len(one) != 1 || one[0].Path != "main.go" {
		t.Errorf("ListFileEvidence(limit 1) = %+v, want exactly the first row main.go", one)
	}
	if _, err := ing.ListFileEvidence(ctx, gen, 0); err == nil {
		t.Error("ListFileEvidence accepted a non-positive limit — the list must stay bounded")
	}

	// SkipsAvailable is true and NamedSkips empty on a migrated sidecar with a
	// Go-only registry: the go/types resolver has no named-skip vocabulary, so
	// "the record was read and holds nothing" — which is exactly the state that
	// must stay distinguishable from "the record could not be read" (W0.g).
	wantPkgs := map[string]ingest.PackageEvidence{
		".":    {Generation: gen, Language: "go", PackageKey: ".", State: ingest.PackageStateChecked, ConfirmedEdges: 1, Languages: []string{"go"}, NamedSkips: map[string]int{}, SkipsAvailable: true},
		"util": {Generation: gen, Language: "go", PackageKey: "util", State: ingest.PackageStateChecked, Languages: []string{"go"}, NamedSkips: map[string]int{}, SkipsAvailable: true},
	}
	for key, w := range wantPkgs {
		pe, err := ing.PackageEvidence(ctx, gen, key)
		if err != nil {
			t.Fatalf("PackageEvidence(%s): %v", key, err)
		}
		if !reflect.DeepEqual(pe, w) {
			t.Errorf("PackageEvidence(%s) = %+v, want %+v", key, pe, w)
		}
	}
}

// TestTrustEvidence_ParseSkippedFileCarriesReason pins the fail-closed skip
// row: a file with a registered parser but invalid content (strict-JSON parse
// error) persists status=skipped with its structured reason — and stays
// visible next to the parsed rows.
func TestTrustEvidence_ParseSkippedFileCarriesReason(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	files := typeresolveFixture()
	files["bad.json"] = "{{ this is not strict JSON }}"
	root := writeRepo(t, files)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	fe, err := ing.FileEvidence(ctx, gen, "bad.json")
	if err != nil {
		t.Fatalf("FileEvidence(bad.json): %v", err)
	}
	wantRow := ingest.FileEvidence{
		Generation: gen, Path: "bad.json",
		ParseStatus: ingest.ParseStatusSkipped, ParseReason: string(ingest.SkipParseError),
	}
	if fe != wantRow {
		t.Errorf("skipped-file row = %+v, want %+v", fe, wantRow)
	}
	rows, err := ing.ListFileEvidence(ctx, gen, 16)
	if err != nil {
		t.Fatalf("ListFileEvidence: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows (bad.json + 2 Go files), got %d: %+v", len(rows), rows)
	}
}

// TestTrustEvidence_TypeErrorsAloneAreNotDegraded pins the PRD §22 semantics:
// a unit whose check swallowed type errors (stdlib stub imports cannot resolve
// fmt.Println) stays CHECKED_WITH_ERRORS with an empty degraded reason — never
// degraded from an error count. The unresolved stdlib selector also shows up
// on the file row as an external materialization, distinct from skipped.
func TestTrustEvidence_TypeErrorsAloneAreNotDegraded(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, map[string]string{
		"go.mod":  "module example.com/m\n\ngo 1.26\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(42) }\n",
	})
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	pe, err := ing.PackageEvidence(ctx, gen, ".")
	if err != nil {
		t.Fatalf("PackageEvidence(.): %v", err)
	}
	if pe.TypeErrors == 0 {
		t.Fatalf("fixture broken: expected swallowed type errors from the fmt stub import, got %+v", pe)
	}
	if pe.State != ingest.PackageStateCheckedWithErrors || pe.DegradedReason != "" {
		t.Errorf("state = %s (reason %q), want %s with an empty reason — type_errors > 0 alone is NOT degraded",
			pe.State, pe.DegradedReason, ingest.PackageStateCheckedWithErrors)
	}

	fe, err := ing.FileEvidence(ctx, gen, "main.go")
	if err != nil {
		t.Fatalf("FileEvidence(main.go): %v", err)
	}
	if fe.ResolvedExternal != 1 || fe.Skipped != 0 {
		t.Errorf("main.go row = %+v, want exactly one external materialization (fmt.Println) and no skips", fe)
	}
}

// TestTrustEvidence_DegradedPackageStateAndReason pins the degraded state on
// the read ports: an import cycle degrades both units, the incremental pass
// replaces the package rows under the SAME live generation, and the recorded
// reason is the resolver's — not invented.
func TestTrustEvidence_DegradedPackageStateAndReason(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	cycled := "package util\n\nimport _ \"example.com/m\"\n\nfunc Answer() int { return 42 }\n"
	rewrite(t, root, "util/util.go", cycled)
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged (cycle): %v", err)
	}
	if got := liveGeneration(ctx, t, store); got != gen {
		t.Fatalf("incremental pass moved the generation (%s -> %s) — it must never mint one", gen, got)
	}
	for _, key := range []string{".", "util"} {
		pe, err := ing.PackageEvidence(ctx, gen, key)
		if err != nil {
			t.Fatalf("PackageEvidence(%s): %v", key, err)
		}
		if pe.State != ingest.PackageStateDegraded || pe.DegradedReason != "import cycle" {
			t.Errorf("PackageEvidence(%s) = state %s reason %q, want degraded/import cycle", key, pe.State, pe.DegradedReason)
		}
		if pe.ConfirmedEdges != 0 {
			t.Errorf("PackageEvidence(%s) claims %d confirmed edges from a degraded pass", key, pe.ConfirmedEdges)
		}
	}
}

// TestTrustEvidence_IncrementalTouchRefreshesOnlyAffected proves the
// incremental refresh is selective and keeps the generation binding: rows of
// touched files (the edit plus its cascade) are rewritten, rows of untouched
// files are left byte-for-byte alone (observed via a planted tamper marker),
// and everything stays bound to the unchanged live generation.
func TestTrustEvidence_IncrementalTouchRefreshesOnlyAffected(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	files := typeresolveFixture()
	files["other/other.go"] = "package other\n\nfunc O() int { return 1 }\n"
	root := writeRepo(t, files)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	// Plant tamper markers on a to-be-touched row and an untouched row: the
	// refresh must overwrite the first and never rewrite the second.
	for _, p := range []string{"util/util.go", "other/other.go"} {
		if _, err := ing.MetaDB().ExecContext(ctx,
			"UPDATE trust_file_evidence SET language = 'tampered' WHERE path = ?", p); err != nil {
			t.Fatalf("tamper %s: %v", p, err)
		}
	}

	rewrite(t, root, "util/util.go", "package util\n\nfunc Answer() int { return 43 }\n")
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}

	if got := liveGeneration(ctx, t, store); got != gen {
		t.Fatalf("incremental pass moved the generation (%s -> %s)", gen, got)
	}
	touched, err := ing.FileEvidence(ctx, gen, "util/util.go")
	if err != nil {
		t.Fatalf("FileEvidence(util/util.go): %v", err)
	}
	if touched.Language != "go" || touched.ParseStatus != ingest.ParseStatusParsed {
		t.Errorf("touched row was not refreshed: %+v (tamper marker should be gone)", touched)
	}
	untouched, err := ing.FileEvidence(ctx, gen, "other/other.go")
	if err != nil {
		t.Fatalf("FileEvidence(other/other.go): %v", err)
	}
	if untouched.Language != "tampered" {
		t.Errorf("untouched row was rewritten (language %q) — the incremental refresh must only touch affected rows", untouched.Language)
	}
}

// TestTrustEvidence_FullIncrementalRowParity is the WP1.2 parity pin: a full
// pass on the initial tree followed by an incremental edit + file deletion
// produces the SAME evidence rows (modulo the per-pass generation nonce) as a
// fresh full pass over the final tree — including the deleted file's rows
// being gone.
func TestTrustEvidence_FullIncrementalRowParity(t *testing.T) {
	ctx := context.Background()
	finalMain := `package main

import "example.com/m/util"

func main() { x := util.Answer(); _ = x }

func twice() int { return util.Answer() + util.Answer() + helper() }

func helper() int { return 1 }
`

	// Path A: full pass on fixture + extra file, then edit main.go and delete
	// extra/extra.go incrementally.
	base := typeresolveFixture()
	base["extra/extra.go"] = "package extra\n\nfunc E() int { return 7 }\n"
	storeA := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeA.Close() })
	ingA := newIngester(t, storeA, parse.NewDefaultRegistry())
	rootA := writeRepo(t, base)
	if err := ingA.IngestAll(ctx, rootA); err != nil {
		t.Fatalf("IngestAll A: %v", err)
	}
	rewrite(t, rootA, "main.go", finalMain)
	if err := os.Remove(filepath.Join(rootA, "extra", "extra.go")); err != nil {
		t.Fatalf("remove extra/extra.go: %v", err)
	}
	if err := ingA.IngestChanged(ctx, rootA, []string{"main.go", "extra/extra.go"}); err != nil {
		t.Fatalf("IngestChanged A: %v", err)
	}

	// Path B: fresh full pass over the final tree.
	final := typeresolveFixture()
	final["main.go"] = finalMain
	storeB := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeB.Close() })
	ingB := newIngester(t, storeB, parse.NewDefaultRegistry())
	rootB := writeRepo(t, final)
	if err := ingB.IngestAll(ctx, rootB); err != nil {
		t.Fatalf("IngestAll B: %v", err)
	}

	a, b := dumpEvidenceTables(ctx, t, ingA), dumpEvidenceTables(ctx, t, ingB)
	if len(a) == 0 {
		t.Fatal("parity fixture produced no evidence rows — the comparison would be vacuous")
	}
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("full-vs-incremental evidence rows diverge:\n--- incremental ---\n%v\n--- full ---\n%v", a, b)
	}
	// The deleted file's rows are gone and its lookup fails closed.
	genA := liveGeneration(ctx, t, storeA)
	if _, err := ingA.FileEvidence(ctx, genA, "extra/extra.go"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("deleted file's evidence still served (err %v), want ErrTrustEvidenceNotFound", err)
	}
	if _, err := ingA.PackageEvidence(ctx, genA, "extra"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("deleted file's package evidence still served (err %v), want ErrTrustEvidenceNotFound", err)
	}
}

// TestTrustEvidence_OldGenerationRowsCleaned proves the full-pass cleanup:
// rows of any other generation are wiped by the next full pass and can never
// be read back afterwards.
func TestTrustEvidence_OldGenerationRowsCleaned(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// Plant rows under a foreign generation, as an interrupted writer might.
	if _, err := ing.MetaDB().ExecContext(ctx, `INSERT INTO trust_file_evidence
		VALUES ('old-generation', 'ghost.go', 'go', 'parsed', '', 0, 0, 0, 0, 0)`); err != nil {
		t.Fatalf("plant old file row: %v", err)
	}
	if _, err := ing.MetaDB().ExecContext(ctx, `INSERT INTO trust_package_evidence
		VALUES ('old-generation', 'go', 'ghost', 'checked', '', 0, 0, 0, 0)`); err != nil {
		t.Fatalf("plant old package row: %v", err)
	}

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll (second): %v", err)
	}
	gen := liveGeneration(ctx, t, store)
	var stray int
	if err := ing.MetaDB().QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM trust_file_evidence WHERE generation_id != ?)
		      + (SELECT COUNT(*) FROM trust_package_evidence WHERE generation_id != ?)`,
		gen, gen).Scan(&stray); err != nil {
		t.Fatalf("count stray rows: %v", err)
	}
	if stray != 0 {
		t.Errorf("%d rows of other generations survived the full pass", stray)
	}
	if _, err := ing.FileEvidence(ctx, "old-generation", "ghost.go"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("old-generation row still served (err %v), want ErrTrustEvidenceNotFound", err)
	}
}

// TestTrustEvidence_GenerationMismatchReadsNotFound pins the fail-closed read
// contract on every port: a generation other than the one the rows are bound
// to — and the empty generation of a never-certified store — reads not-found,
// never another generation's rows.
func TestTrustEvidence_GenerationMismatchReadsNotFound(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	if _, err := ing.FileEvidence(ctx, "not-the-live-generation", "main.go"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("FileEvidence under a foreign generation: err %v, want ErrTrustEvidenceNotFound", err)
	}
	if _, err := ing.PackageEvidence(ctx, "not-the-live-generation", "."); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("PackageEvidence under a foreign generation: err %v, want ErrTrustEvidenceNotFound", err)
	}
	if _, err := ing.ListFileEvidence(ctx, "not-the-live-generation", 8); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("ListFileEvidence under a foreign generation: err %v, want ErrTrustEvidenceNotFound", err)
	}
	if _, err := ing.FileEvidence(ctx, "", "main.go"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("FileEvidence under the empty generation: err %v, want ErrTrustEvidenceNotFound", err)
	}
}

// seedV2Sidecar writes an ingest-meta.db exactly as a pre-WP1.2 binary left
// it: the full version-2 schema (dirty_units WITH edit context,
// file_content_cache WITH has_links), user_version = 2, and NO trust evidence
// tables. A populated cache row proves the migration is additive.
func seedV2Sidecar(t *testing.T, metaDir string) {
	t.Helper()
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open v2 sidecar: %v", err)
	}
	defer db.Close()
	const v2DDL = `
CREATE TABLE IF NOT EXISTS file_content_cache (
	path TEXT PRIMARY KEY,
	content_hash TEXT NOT NULL,
	node_ids TEXT NOT NULL,
	last_ingested_at INTEGER NOT NULL,
	has_links INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS reverse_deps (
	path TEXT PRIMARY KEY,
	dependents TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dirty_units (
	path TEXT PRIMARY KEY,
	edit_id TEXT NOT NULL DEFAULT '',
	op_type TEXT NOT NULL DEFAULT '',
	recorded_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS edit_provenance (
	element_id TEXT NOT NULL,
	element_kind TEXT NOT NULL,
	edit_id TEXT NOT NULL,
	op_type TEXT NOT NULL,
	recorded_at INTEGER NOT NULL,
	PRIMARY KEY(element_id, edit_id)
);
CREATE TABLE IF NOT EXISTS ingest_semantics (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
PRAGMA user_version = 2;`
	if _, err := db.Exec(v2DDL); err != nil {
		t.Fatalf("create v2 schema: %v", err)
	}
	if _, err := db.Exec("INSERT INTO file_content_cache VALUES ('kept.go', 'hash', '[]', 1, 0)"); err != nil {
		t.Fatalf("seed cache row: %v", err)
	}
}

// TestTrustEvidence_Migration2To3 pins the sidecar ladder step: a version-2
// store WITHOUT the tables reads evidence-UNAVAILABLE (never empty-healthy)
// through a read-only observer, migrates additively on the next read-write
// open (existing rows intact, the ladder's current head stamped, every
// evidence table present), and serves evidence normally after the next full
// pass.
func TestTrustEvidence_Migration2To3(t *testing.T) {
	ctx := context.Background()
	metaDir := filepath.Join(t.TempDir(), "meta")
	seedV2Sidecar(t, metaDir)

	// Read-only observer over the UN-migrated store: evidence-unavailable.
	roStore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = roStore.Close() })
	ro, err := ingest.NewReadOnly(roStore, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("NewReadOnly on v2 sidecar: %v", err)
	}
	if _, err := ro.FileEvidence(ctx, "any", "main.go"); !errors.Is(err, ingest.ErrTrustEvidenceUnavailable) {
		t.Errorf("FileEvidence on a pre-migration sidecar: err %v, want ErrTrustEvidenceUnavailable", err)
	}
	if _, err := ro.ListFileEvidence(ctx, "any", 8); !errors.Is(err, ingest.ErrTrustEvidenceUnavailable) {
		t.Errorf("ListFileEvidence on a pre-migration sidecar: err %v, want ErrTrustEvidenceUnavailable", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("close read-only observer: %v", err)
	}

	// Read-write open migrates 2 -> 4 (the full remaining ladder).
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New on v2 sidecar: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	var version int
	if err := ing.MetaDB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 5 {
		t.Errorf("user_version = %d after migration, want 5", version)
	}
	for _, table := range []string{"trust_file_evidence", "trust_package_evidence", "trust_language_skips"} {
		var n int
		if err := ing.MetaDB().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n); err != nil {
			t.Fatalf("probe %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after migration", table)
		}
	}
	var kept int
	if err := ing.MetaDB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM file_content_cache WHERE path = 'kept.go'").Scan(&kept); err != nil {
		t.Fatalf("probe kept row: %v", err)
	}
	if kept != 1 {
		t.Error("migration was not additive: the pre-existing cache row is gone")
	}

	// Empty migrated tables read not-found (no invented evidence), and a full
	// pass then serves real rows through the same ports.
	if _, err := ing.FileEvidence(ctx, "any", "main.go"); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("FileEvidence on empty migrated tables: err %v, want ErrTrustEvidenceNotFound", err)
	}
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll after migration: %v", err)
	}
	gen := liveGeneration(ctx, t, store)
	if _, err := ing.FileEvidence(ctx, gen, "main.go"); err != nil {
		t.Errorf("FileEvidence after migration + full pass: %v", err)
	}
	if _, err := ing.PackageEvidence(ctx, gen, "util"); err != nil {
		t.Errorf("PackageEvidence after migration + full pass: %v", err)
	}
}

// TestTrustEvidence_MigrationIdempotentWithLeftoverV4 pins the fix for the
// migration race: a crashed or concurrent prior attempt can leave the temporary
// trust_package_evidence_v4 table behind, and the migration used to fail on the
// next open with "table trust_package_evidence_v4 already exists". Seed that
// exact debris and prove the migration now recovers — it drops the leftover and
// rebuilds inside one transaction — rather than wedging the store shut.
func TestTrustEvidence_MigrationIdempotentWithLeftoverV4(t *testing.T) {
	ctx := context.Background()
	metaDir := filepath.Join(t.TempDir(), "meta")
	seedV2Sidecar(t, metaDir)

	// Inject the debris a crashed CREATE-then-crash would leave: the v4 temp
	// table exists, but trust_package_evidence still has no language column, so
	// the migration guard will re-enter and hit the CREATE.
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open meta for debris injection: %v", err)
	}
	if _, err := raw.ExecContext(ctx, "CREATE TABLE trust_package_evidence_v4 (garbage TEXT);"); err != nil {
		t.Fatalf("inject leftover v4: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close debris injector: %v", err)
	}

	// Read-write open must migrate cleanly despite the leftover.
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New must recover from a leftover v4 table, got: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })

	// The real (migrated) table exists with the language column, and the debris
	// is gone.
	cols, err := ing.MetaDB().QueryContext(ctx, "PRAGMA table_info(trust_package_evidence)")
	if err != nil {
		t.Fatalf("read migrated columns: %v", err)
	}
	defer cols.Close()
	hasLang := false
	for cols.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == "language" {
			hasLang = true
		}
	}
	if !hasLang {
		t.Error("trust_package_evidence has no language column after recovery")
	}
	var leftover int
	if err := ing.MetaDB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='trust_package_evidence_v4'").Scan(&leftover); err != nil {
		t.Fatalf("probe leftover: %v", err)
	}
	if leftover != 0 {
		t.Error("the leftover trust_package_evidence_v4 debris survived the migration")
	}
}

// TestTrustEvidence_MigrationReentryPreservesNonGoRows pins the data-loss half
// of the migration race (ADR 0009 review round 1, finding 2): a process whose
// belief about the schema is STALE re-enters the 3→4 step against an
// already-migrated table. The step's copy backfills language='go', so without
// a guard evaluated in the SAME transaction as the copy, the re-entrant loser
// silently reset every row — including rows the jvm registrant wrote. The
// stale belief is made durable here by rolling user_version back while the
// table keeps its migrated shape and a 'kotlin' row: the reopened store MUST
// no-op the step and preserve the row.
//
// Scope, honestly stated: this pins guard EXISTENCE and re-entry idempotency.
// The sub-statement window of the original race (guard read from the DB, the
// winner commits, then the copy runs) has no black-box seam to schedule
// deterministically; it is closed structurally by evaluating the guard on the
// migration transaction itself (engine/ingest/schema.go,
// migratePackageEvidenceLanguage), and its lossy consequence was confirmed by
// a throwaway probe before the fix (the copy against a migrated table returns
// language='go' for a row seeded 'kotlin').
func TestTrustEvidence_MigrationReentryPreservesNonGoRows(t *testing.T) {
	ctx := context.Background()
	metaDir := filepath.Join(t.TempDir(), "meta")
	seedV2Sidecar(t, metaDir)

	// First open: migrate the full ladder to v4.
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New on v2 sidecar: %v", err)
	}
	if _, err := ing.MetaDB().ExecContext(ctx,
		"INSERT INTO trust_package_evidence VALUES ('g1','kotlin','com.example','ok','',0,0,3,0)"); err != nil {
		t.Fatalf("seed kotlin row: %v", err)
	}
	// The loser's worldview: user_version says the step still needs to run.
	if _, err := ing.MetaDB().ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		t.Fatalf("roll back user_version: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close first open: %v", err)
	}

	// Re-entry: the 3→4 step runs again over the already-migrated table.
	store2 := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store2.Close() })
	ing2, err := ingest.New(store2, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("re-entrant open: %v", err)
	}
	t.Cleanup(func() { _ = ing2.Close() })

	var lang string
	if err := ing2.MetaDB().QueryRowContext(ctx,
		"SELECT language FROM trust_package_evidence WHERE package_key = 'com.example'").Scan(&lang); err != nil {
		t.Fatalf("read seeded row after re-entry: %v", err)
	}
	if lang != "kotlin" {
		t.Errorf("re-entrant migration reset language to %q, want the seeded \"kotlin\" preserved", lang)
	}
}
