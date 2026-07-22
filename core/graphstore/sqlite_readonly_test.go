package graphstore_test

import (
	"context"
	"path/filepath"
	"testing"

	gs "github.com/samibel/graphi/core/graphstore"
)

// TestOpenSQLiteReadOnly_ReadsAndRefusesWrites pins the read-only open: it sees
// everything a normal open wrote, and every write path fails instead of
// touching the file.
func TestOpenSQLiteReadOnly_ReadsAndRefusesWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graphi.db")
	st, err := gs.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	n := mustNode(t, "function", "pkg/x.Y", "pkg/x.go", 1, 1)
	if err := st.PutNode(ctx, n); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := st.SetMetadata(ctx, "sync.branch", "main"); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	ro, err := gs.OpenSQLiteReadOnly(path)
	if err != nil {
		t.Fatalf("OpenSQLiteReadOnly: %v", err)
	}
	defer ro.Close()

	got, err := ro.GetNode(ctx, n.ID())
	if err != nil {
		t.Fatalf("GetNode via read-only: %v", err)
	}
	if !nodesEqual(got, n) {
		t.Fatalf("read-only node mismatch")
	}
	if v, err := ro.Metadata(ctx, "sync.branch"); err != nil || v != "main" {
		t.Fatalf("Metadata via read-only = (%q, %v), want (main, nil)", v, err)
	}
	if count, err := ro.CountNodes(ctx); err != nil || count != 1 {
		t.Fatalf("CountNodes = (%d, %v), want (1, nil)", count, err)
	}

	if err := ro.SetMetadata(ctx, "k", "v"); err == nil {
		t.Fatal("SetMetadata on a read-only store succeeded, want error")
	}
	if err := ro.PutNode(ctx, mustNode(t, "function", "pkg/x.Z", "pkg/x.go", 2, 1)); err == nil {
		t.Fatal("PutNode on a read-only store succeeded, want error")
	}
}

// TestOpenSQLiteReadOnly_MissingFile pins that mode=ro never creates a file.
func TestOpenSQLiteReadOnly_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if _, err := gs.OpenSQLiteReadOnly(path); err == nil {
		t.Fatal("OpenSQLiteReadOnly on a missing file succeeded, want error")
	}
	if _, err := readFileHelper(path); err == nil {
		t.Fatal("read-only open created the missing database file")
	}
}
