package ingest

// Internal tests for the trust_package_evidence write path: the row boundary
// that turns semantic-run evidence into persistent rows. The schema (3 -> 4)
// pins `language TEXT NOT NULL`, and a row whose registrant language is the
// empty string must NEVER reach the INSERT — it would raise the constraint and
// fail the whole pass for a row no reader can honestly claim. These tests pin
// the "skip, not a row" discipline at the one function that owns the SQL.

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestInsertPackageEvidence_SkipsEmptyLanguage pins the write-side guard: a
// row whose Language is "" is silently dropped (NOT raised as an SQLite
// constraint violation), and every row with a non-empty language still lands.
// Without the guard, the empty row raises
// "NOT NULL constraint failed: trust_package_evidence.language" and the whole
// pass aborts — the exact regression `graphi sync` hit on the bench/fixture/src
// directory.
func TestInsertPackageEvidence_SkipsEmptyLanguage(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = i.Close() })

	const gen = "gen-empty-lang"
	rows := []PackageEvidence{
		// A real registrant's row (the only kind the production code path
		// mints today) — must land unchanged.
		{Generation: gen, Language: "go", PackageKey: "alpha", State: PackageStateChecked, ConfirmedEdges: 3},
		// The regression row — Language="", the kind of row a future
		// non-language binder or an empty-lang resolver would mint. The
		// guard MUST drop it before the SQL ever runs.
		{Generation: gen, Language: "", PackageKey: "bench/fixture/src", State: PackageStateChecked, ConfirmedEdges: 16},
	}

	tx, err := i.meta.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := insertPackageEvidenceTx(ctx, tx, gen, rows); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insertPackageEvidenceTx must skip empty-language rows, got: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The empty-language row was dropped — there is no row at all for
	// "bench/fixture/src", because the row that named it carried no language
	// to attribute it under. The reader's "selective generation-checked"
	// contract says absence is information (which is why the registry seam
	// ties language to registrant in the first place), not a missing-row
	// error.
	out, err := i.meta.QueryContext(ctx, `SELECT language, package_key, confirmed_edges
		FROM trust_package_evidence WHERE generation_id = ? ORDER BY package_key`, gen)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer out.Close()
	var got []PackageEvidence
	for out.Next() {
		var pe PackageEvidence
		if err := out.Scan(&pe.Language, &pe.PackageKey, &pe.ConfirmedEdges); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, pe)
	}
	if err := out.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("persisted rows = %d, want exactly 1 (the empty-language row must skip): %+v", len(got), got)
	}
	if got[0].Language != "go" || got[0].PackageKey != "alpha" {
		t.Errorf("persisted row = %+v, want {Language:go PackageKey:alpha}", got[0])
	}

	// The whole point: an empty-language row's presence in the input must
	// not raise "NOT NULL constraint failed". A successful insertPackageEvidenceTx
	// return is the contract — the SQL guard tripped, the row was skipped, and
	// the pass could still close. This is what makes a registry seam widening
	// (or a forgotten unit-test stub) a missing row rather than an aborted
	// ingest.
	if _, err := i.meta.QueryContext(ctx,
		`SELECT 1 FROM trust_package_evidence WHERE generation_id = ? AND language = ''`, gen); err != nil {
		t.Fatalf("read empty-language guard: %v", err)
	}
}
