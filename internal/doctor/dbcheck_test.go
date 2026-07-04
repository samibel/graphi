package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// buildFixtureDB creates a real graphstore SQLite database in a temp dir with
// nodeCount nodes and, when profile is non-empty, the "index.profile" metadata
// key the ingester writes. It returns the database path.
func buildFixtureDB(t *testing.T, nodeCount int, profile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graphi.db")
	store, err := graphstore.OpenSQLite(path)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < nodeCount; i++ {
		n, err := model.NewNode("function", "pkg.Fn"+string(rune('A'+i)), "pkg/fn.go", i+1, 1)
		if err != nil {
			t.Fatalf("new node: %v", err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node: %v", err)
		}
	}
	if profile != "" {
		if err := store.SetMetadata(ctx, "index.profile", profile); err != nil {
			t.Fatalf("set metadata: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	return path
}

func TestDBCheckRealDBReportsNodesProfileAndFTS(t *testing.T) {
	path := buildFixtureDB(t, 2, "balanced")
	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusPass {
		t.Fatalf("expected pass, got %q: %s", res.Status, res.Message)
	}
	for _, want := range []string{"2 nodes", "profile: balanced", "FTS present", "user_version="} {
		if !contains(res.Message, want) {
			t.Fatalf("message missing %q: %s", want, res.Message)
		}
	}
}

func TestDBCheckRealDBProfileUnknownStillPasses(t *testing.T) {
	path := buildFixtureDB(t, 1, "")
	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusPass {
		t.Fatalf("expected pass with unknown profile, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Message, "profile: unknown") {
		t.Fatalf("message missing unknown-profile note: %s", res.Message)
	}
}

func TestDBCheckRealDBNoNodesWarns(t *testing.T) {
	path := buildFixtureDB(t, 0, "fast")
	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for empty DB, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "graphi index") {
		t.Fatalf("expected `graphi index` action, got %q", res.Action)
	}
	if !contains(res.Message, "profile: fast") {
		t.Fatalf("expected profile in message: %s", res.Message)
	}
}

func TestDBCheckGarbageFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(path, []byte("this is definitely not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}
	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusFail {
		t.Fatalf("expected fail for garbage file, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "graphi index") {
		t.Fatalf("expected `graphi index` action, got %q", res.Action)
	}
}

func TestDBCheckMissingFileFailsAndDoesNotCreateIt(t *testing.T) {
	// Read-only guard: probing a missing DB must fail without creating the
	// file (mode=ro forbids implicit creation).
	path := filepath.Join(t.TempDir(), "does-not-exist.db")
	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusFail {
		t.Fatalf("expected fail for missing DB, got %q: %s", res.Status, res.Message)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read-only probe must not create the DB file (stat err: %v)", err)
	}
}

func TestDBCheckDoesNotWriteToFixture(t *testing.T) {
	// Read-only guard on a real DB: bytes on disk are identical before and
	// after the check runs.
	path := buildFixtureDB(t, 1, "balanced")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path}); res.Status != StatusPass {
		t.Fatalf("expected pass, got %q: %s", res.Status, res.Message)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("DBCheck modified the database file; it must be read-only")
	}
}

func TestDBCheckMissingFTSWarns(t *testing.T) {
	// A database with indexed nodes but no `search` FTS5 virtual table must be
	// reported honestly as FTS missing, with an actionable next step.
	path := filepath.Join(t.TempDir(), "nofts.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE nodes (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, qualified_name TEXT NOT NULL,
			source_path TEXT NOT NULL, line INTEGER NOT NULL, col INTEGER NOT NULL
		)`,
		`INSERT INTO nodes VALUES ('n1', 'function', 'pkg.Fn', 'pkg/fn.go', 1, 1)`,
		`CREATE TABLE kv_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO kv_meta VALUES ('index.profile', 'balanced')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	res := DBCheck().Run(context.Background(), fakeEnv{dbPath: path})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for missing FTS, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Message, "FTS missing") {
		t.Fatalf("message missing FTS-missing note: %s", res.Message)
	}
	if !contains(res.Message, "profile: balanced") {
		t.Fatalf("message missing profile: %s", res.Message)
	}
	if !contains(res.Action, "graphi index") {
		t.Fatalf("expected `graphi index` action, got %q", res.Action)
	}
}
