package graphstore

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

func TestFtsNodeRowid(t *testing.T) {
	cases := []struct {
		id      model.NodeId
		want    int64
		wantErr bool
	}{
		{id: "00000000000000ff", want: 255},
		{id: "0000000000000000", want: 0},
		// High bit set: wraps to a negative rowid — legal and still bijective.
		{id: "ffffffffffffffff", want: -1},
		{id: "8000000000000000", want: -9223372036854775808},
		{id: "short", wantErr: true},
		{id: "zzzzzzzzzzzzzzzz", wantErr: true},
		{id: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := ftsNodeRowid(c.id)
		if c.wantErr {
			if err == nil {
				t.Errorf("ftsNodeRowid(%q) = %d, want error", c.id, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ftsNodeRowid(%q): %v", c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("ftsNodeRowid(%q) = %d, want %d", c.id, got, c.want)
		}
	}
}

// TestMigrateFTSRowids_RekeysPreV4Store simulates a store written before
// schema v4 — auto-assigned search rowids, user_version 3 — and asserts that
// reopening rebuilds the search table under deterministic rowids with search
// results identical to before, and that a subsequent re-put does not
// duplicate the owner's row.
func TestMigrateFTSRowids_RekeysPreV4Store(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "g.db")

	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	var nodes []model.Node
	for _, qn := range []string{"alpha.One", "alpha.Two", "beta.Three"} {
		n, err := model.NewNode("function", qn, "pkg/file.go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
		nodes = append(nodes, n)
	}
	wantSearch, err := st.SearchNodes(ctx, "alpha", 0)
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(wantSearch) != 2 {
		t.Fatalf("fixture: want 2 alpha hits, got %d", len(wantSearch))
	}

	// Mangle into the v3 shape: auto rowids and the old version stamp.
	if _, err := st.db.ExecContext(ctx, "DELETE FROM search"); err != nil {
		t.Fatalf("clear search: %v", err)
	}
	for _, n := range nodes {
		if _, err := st.db.ExecContext(ctx,
			"INSERT INTO search (owner_kind, owner_id, text) VALUES ('node', ?, ?)",
			string(n.ID()), n.QualifiedName()); err != nil {
			t.Fatalf("insert auto-rowid search row: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		t.Fatalf("stamp v3: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: migrateFTSRowids must re-key every row.
	st, err = OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	gotSearch, err := st.SearchNodes(ctx, "alpha", 0)
	if err != nil {
		t.Fatalf("SearchNodes after migration: %v", err)
	}
	if !reflect.DeepEqual(gotSearch, wantSearch) {
		t.Errorf("search results changed across migration:\n got %v\nwant %v", gotSearch, wantSearch)
	}
	for _, n := range nodes {
		wantRowid, err := ftsNodeRowid(n.ID())
		if err != nil {
			t.Fatalf("ftsNodeRowid: %v", err)
		}
		var gotRowid int64
		if err := st.db.QueryRowContext(ctx,
			"SELECT rowid FROM search WHERE owner_id = ?", string(n.ID())).Scan(&gotRowid); err != nil {
			t.Fatalf("read migrated rowid for %s: %v", n.ID(), err)
		}
		if gotRowid != wantRowid {
			t.Errorf("node %s: migrated rowid %d, want deterministic %d", n.ID(), gotRowid, wantRowid)
		}
	}

	// Re-put must replace, not duplicate, under the deterministic key — on the
	// single-write path and through a batch.
	if err := st.PutNode(ctx, nodes[0]); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	batch, err := st.BeginBatch(ctx)
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}
	if err := batch.PutNode(ctx, nodes[1]); err != nil {
		t.Fatalf("batch re-put: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("batch commit: %v", err)
	}
	var searchRows int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM search").Scan(&searchRows); err != nil {
		t.Fatalf("count search rows: %v", err)
	}
	if searchRows != len(nodes) {
		t.Errorf("search has %d rows after re-puts, want exactly %d (one per node)", searchRows, len(nodes))
	}
}
