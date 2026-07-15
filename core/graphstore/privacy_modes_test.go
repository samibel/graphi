package graphstore

// SW-119 (PRIV-01): the graph database derives from potentially private
// source, so its files are owner-only — created 0600 and migrated to 0600 on
// open when a pre-existing store is wider.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/model"
)

func TestPrivacy_SQLiteFilesOwnerOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "graphi.db")

	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	// Write something so the WAL sidecar materializes.
	n, err := model.NewNode("function", "pkg.X", "pkg/x.go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	if err := st.PutNode(ctx, n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	assertPerm(t, dbPath, 0o600)
	// SQLite creates -wal/-shm inheriting the (now 0600) main file's mode; when
	// present they must be owner-only too.
	for _, side := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if fi, serr := os.Stat(side); serr == nil {
			if got := fi.Mode().Perm(); got&0o077 != 0 {
				t.Fatalf("%s mode = %o, want owner-only", side, got)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Migration: widen a pre-existing store, reopen, expect 0600 again.
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	st2, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	assertPerm(t, dbPath, 0o600)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
