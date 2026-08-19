package ingest_test

// W0.g — the persistence half of legible abstention: a full ingest pass must
// land the semantic registrants' NAMED skip counters in the generation-bound
// sidecar (schema 5, trust_language_skips), and the read ports must serve them
// beside the package rows without ever letting "cannot answer" look like
// "nothing was skipped".
//
// Two disciplines govern this file:
//
//   - NON-VACUITY. The abstention fixture is built to FORCE skips and the
//     assertions name the reasons and the counts. A control pass over the same
//     machinery with a Go-only registry proves the assertions can come back
//     empty, so a green run is not an empty map agreeing with an empty
//     expectation.
//   - RED WITHOUT THE FIX. The migration test injects a real pre-migration
//     sidecar and asserts the pre-state fails closed through a read-only
//     observer (which never migrates) BEFORE asserting the post-migration read
//     succeeds — so the assertion has a demonstrated failing side.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/semantic"
)

// jvmAbstentionFixture forces three distinct named skip reasons in one Java
// package while keeping one call that genuinely binds, so the pass produces
// both a confirmed edge and a legible abstention record:
//
//	param.value()    binds (Rate is intra-repo, param is a declared type)
//	inferred.value() java_var_inferred     — `var` local
//	param.missing()  java_lookup_not_found — closed chain lacks the member
//	ext.size()       java_receiver_external — java.util.List is outside the repo
//	u.thing()        java_receiver_external — Unknown resolves to no repo type
func jvmAbstentionFixture() map[string]string {
	return map[string]string{
		"com/tax/Rate.java": `package com.tax;
public class Rate {
    public void value() {}
}
`,
		"com/shop/Shop.java": `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        var inferred = param;
        inferred.value();
        param.missing();
        java.util.List<String> ext = null;
        ext.size();
        Unknown u = null;
        u.thing();
    }
}
`,
	}
}

// wantJavaSkips is the exact abstention record jvmAbstentionFixture forces.
var wantJavaSkips = map[string]int{
	"java_var_inferred":      1,
	"java_lookup_not_found":  1,
	"java_receiver_external": 2,
}

// TestAbstention_FullPassPersistsNamedSkips is the end-to-end persistence pin:
// a real IngestAll with the JVM registrants opted in writes the java binder's
// named skips under the pass generation, and BOTH read ports serve them — the
// whole-generation LanguageSkips roll-up and the per-package join on
// PackageEvidence that the strict-query surface gates on.
func TestAbstention_FullPassPersistsNamedSkips(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "1")
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, jvmAbstentionFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	byLang, err := ing.LanguageSkips(ctx, gen)
	if err != nil {
		t.Fatalf("LanguageSkips: %v", err)
	}
	if !reflect.DeepEqual(byLang["java"], wantJavaSkips) {
		t.Errorf("LanguageSkips[java] = %#v, want %#v", byLang["java"], wantJavaSkips)
	}
	// Keyed BY LANGUAGE, not summed into one unowned total: the registrant is
	// the only attribution these counters have and losing it would leave a
	// number nobody owns.
	if _, kotlinReported := byLang["kotlin"]; kotlinReported {
		t.Errorf("a kotlin entry was reported for a java-only repository: %#v", byLang)
	}

	// The per-package join: the row a strict query gates on carries the same
	// counters, and says the record was readable.
	pe, err := ing.PackageEvidence(ctx, gen, "com/shop")
	if err != nil {
		t.Fatalf("PackageEvidence(com/shop): %v", err)
	}
	if !pe.SkipsAvailable {
		t.Error("SkipsAvailable = false on a schema-5 sidecar that just wrote the rows")
	}
	if !reflect.DeepEqual(pe.Languages, []string{"java"}) {
		t.Errorf("PackageEvidence(com/shop).Languages = %#v, want [java] — the counters' only attribution", pe.Languages)
	}
	if !reflect.DeepEqual(pe.NamedSkips, wantJavaSkips) {
		t.Errorf("PackageEvidence(com/shop).NamedSkips = %#v, want %#v", pe.NamedSkips, wantJavaSkips)
	}

	// Non-vacuity, asserted rather than assumed.
	total := 0
	for _, n := range byLang["java"] {
		total += n
	}
	if total == 0 {
		t.Fatal("the abstention fixture recorded no skip — every assertion above would be vacuous")
	}
}

// TestAbstention_GoOnlyPassRecordsNothing is the control: the identical read
// ports over a pass whose only registrant is go/types (which has no named-skip
// vocabulary) come back AVAILABLE AND EMPTY. That pair is the whole contract —
// "the record was read and holds nothing" must stay distinguishable from "the
// record could not be read", and this is the case that proves the assertions
// in the test above are not satisfied by an unconditional empty map.
func TestAbstention_GoOnlyPassRecordsNothing(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, store)

	byLang, err := ing.LanguageSkips(ctx, gen)
	if err != nil {
		t.Fatalf("LanguageSkips on a Go-only pass must succeed, got: %v", err)
	}
	if len(byLang) != 0 {
		t.Errorf("LanguageSkips = %#v, want empty for a Go-only pass", byLang)
	}
	pe, err := ing.PackageEvidence(ctx, gen, ".")
	if err != nil {
		t.Fatalf("PackageEvidence: %v", err)
	}
	if !pe.SkipsAvailable {
		t.Error("SkipsAvailable = false on a migrated sidecar — an empty record is still an ANSWER")
	}
	if len(pe.NamedSkips) != 0 {
		t.Errorf("NamedSkips = %#v, want empty", pe.NamedSkips)
	}
}

// TestAbstention_StaleGenerationIsNeverServed pins the generation binding: the
// skip record describes ONE pass, and a mismatched generation reads empty
// rather than serving another pass's abstention as this one's. Stale evidence
// is unusable evidence — the same rule the rest of the sidecar runs under.
func TestAbstention_StaleGenerationIsNeverServed(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "1")
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, writeRepo(t, jvmAbstentionFixture())); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	byLang, err := ing.LanguageSkips(ctx, "some-other-generation")
	if err != nil {
		t.Fatalf("LanguageSkips(mismatched generation): %v", err)
	}
	if len(byLang) != 0 {
		t.Errorf("a mismatched generation served %#v — stale abstention is not this pass's abstention", byLang)
	}
	if _, err := ing.LanguageSkips(ctx, ""); !errors.Is(err, ingest.ErrTrustEvidenceNotFound) {
		t.Errorf("LanguageSkips(\"\") = %v, want ErrTrustEvidenceNotFound", err)
	}
}

// TestAbstention_Migration4To5 is the AC-8 migration proof, and it is written
// so that the failing side is demonstrated rather than asserted.
//
// A real pre-migration sidecar is injected: schema exactly 4, both evidence
// tables present and POPULATED, no trust_language_skips. A read-only observer
// — which never migrates, so it is the durable pre-state — must fail closed on
// both abstention reads. Only then does a read-write open migrate, and the
// post-migration assertions run: user_version 5, the table present, the
// pre-existing package row intact (the migration is additive), and the same
// reads now answering.
func TestAbstention_Migration4To5(t *testing.T) {
	ctx := context.Background()
	metaDir := filepath.Join(t.TempDir(), "meta")
	seedV4SidecarWithRows(t, metaDir)

	// --- the pre-state, through an observer that never migrates ---
	roStore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = roStore.Close() })
	ro, err := ingest.NewReadOnly(roStore, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("NewReadOnly on v4 sidecar: %v", err)
	}
	if _, err := ro.LanguageSkips(ctx, "g1"); !errors.Is(err, ingest.ErrTrustEvidenceUnavailable) {
		t.Errorf("LanguageSkips on a v4 sidecar = %v, want ErrTrustEvidenceUnavailable (never an empty map)", err)
	}
	// The package row is INTACT at v4 and must still be served — refusing it
	// would be a false unavailability. What must not happen is the abstention
	// field looking answered: SkipsAvailable is the visible "no answer".
	pre, err := ro.PackageEvidence(ctx, "g1", "com/shop")
	if err != nil {
		t.Fatalf("PackageEvidence on a v4 sidecar must still serve the intact row: %v", err)
	}
	if pre.SkipsAvailable {
		t.Error("SkipsAvailable = true on a sidecar with no skip table — absence would read as an all-clear")
	}
	if len(pre.NamedSkips) != 0 {
		t.Errorf("NamedSkips = %#v on a pre-migration sidecar, want none", pre.NamedSkips)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("close observer: %v", err)
	}

	// --- the migration ---
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New on v4 sidecar: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })

	var version int
	if err := ing.MetaDB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 5 {
		t.Errorf("user_version = %d after migration, want 5", version)
	}
	var present int
	if err := ing.MetaDB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='trust_language_skips'").Scan(&present); err != nil {
		t.Fatalf("probe skip table: %v", err)
	}
	if present != 1 {
		t.Fatal("trust_language_skips missing after migration")
	}

	// --- the post-migration read, and the additivity of the step ---
	post, err := ing.PackageEvidence(ctx, "g1", "com/shop")
	if err != nil {
		t.Fatalf("PackageEvidence after migration: %v", err)
	}
	if !post.SkipsAvailable {
		t.Error("SkipsAvailable = false after the migration")
	}
	if post.ConfirmedEdges != 7 || post.Language != "java" {
		t.Errorf("migration was not additive: pre-existing row came back %+v", post)
	}
	if _, err := ing.LanguageSkips(ctx, "g1"); err != nil {
		t.Errorf("LanguageSkips after migration: %v", err)
	}

	// The step is idempotent: a second open over the migrated sidecar is a
	// no-op, and the injected skip row survives it.
	if _, err := ing.MetaDB().ExecContext(ctx,
		"INSERT INTO trust_language_skips VALUES ('g1','java','java_var_inferred',9)"); err != nil {
		t.Fatalf("seed skip row: %v", err)
	}
	if _, err := ing.MetaDB().ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		t.Fatalf("roll back user_version: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	store2 := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store2.Close() })
	ing2, err := ingest.New(store2, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("re-entrant open: %v", err)
	}
	t.Cleanup(func() { _ = ing2.Close() })
	again, err := ing2.LanguageSkips(ctx, "g1")
	if err != nil {
		t.Fatalf("LanguageSkips after re-entry: %v", err)
	}
	if again["java"]["java_var_inferred"] != 9 {
		t.Errorf("re-entrant migration destroyed the seeded row: %#v", again)
	}
}

// seedV4SidecarWithRows writes a sidecar at exactly schema 4: the full ladder
// through the package-evidence language rebuild, POPULATED, and without
// trust_language_skips. This is the durable pre-state the 4 -> 5 step has to
// upgrade in place.
func seedV4SidecarWithRows(t *testing.T, metaDir string) {
	t.Helper()
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open v4 sidecar: %v", err)
	}
	defer db.Close()
	const ddl = `
CREATE TABLE file_content_cache (
	path TEXT PRIMARY KEY,
	content_hash TEXT NOT NULL,
	node_ids TEXT NOT NULL,
	last_ingested_at INTEGER NOT NULL,
	has_links INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE reverse_deps (path TEXT PRIMARY KEY, dependents TEXT NOT NULL);
CREATE TABLE dirty_units (
	path TEXT PRIMARY KEY,
	edit_id TEXT NOT NULL DEFAULT '',
	op_type TEXT NOT NULL DEFAULT '',
	recorded_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE edit_provenance (
	element_id TEXT NOT NULL, element_kind TEXT NOT NULL, edit_id TEXT NOT NULL,
	op_type TEXT NOT NULL, recorded_at INTEGER NOT NULL, PRIMARY KEY(element_id, edit_id)
);
CREATE TABLE ingest_semantics (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE trust_file_evidence (
	generation_id TEXT NOT NULL, path TEXT NOT NULL, language TEXT NOT NULL,
	parse_status TEXT NOT NULL, parse_reason TEXT NOT NULL,
	resolved_derived INTEGER NOT NULL, resolved_heuristic INTEGER NOT NULL,
	resolved_external INTEGER NOT NULL, skipped INTEGER NOT NULL, ambiguous INTEGER NOT NULL,
	PRIMARY KEY (generation_id, path)
);
CREATE TABLE trust_package_evidence (
	generation_id TEXT NOT NULL, language TEXT NOT NULL, package_key TEXT NOT NULL,
	state TEXT NOT NULL, degraded_reason TEXT NOT NULL, type_errors INTEGER NOT NULL,
	dropped_intents INTEGER NOT NULL, confirmed_edges INTEGER NOT NULL, skipped_files INTEGER NOT NULL,
	PRIMARY KEY (generation_id, language, package_key)
);
INSERT INTO trust_package_evidence VALUES ('g1','java','com/shop','CHECKED','',0,0,7,0);
PRAGMA user_version = 4;`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("seed v4 sidecar: %v", err)
	}
}
