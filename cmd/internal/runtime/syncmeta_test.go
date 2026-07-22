package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

const testCommitHex = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"

// gitify turns a repository() fixture into a git checkout on branch with the
// fixed test commit, so stamping has real HEAD state to resolve.
func gitify(t *testing.T, root, branch string) {
	t.Helper()
	head := filepath.Join(root, ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(root, ".git", "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(testCommitHex+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStampAndLastSync_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	if _, _, _, ok := LastSync(ctx, store); ok {
		t.Fatal("LastSync on a fresh store reported ok")
	}

	repo := repository(t, "repo", "package repo\n")
	gitify(t, repo, "feature/login")
	now := time.Date(2026, 7, 21, 9, 14, 0, 0, time.UTC)
	if err := StampSyncMetadata(ctx, store, repo, now); err != nil {
		t.Fatalf("StampSyncMetadata: %v", err)
	}
	ts, branch, commit, ok := LastSync(ctx, store)
	if !ok || !ts.Equal(now) || branch != "feature/login" || commit != testCommitHex {
		t.Fatalf("LastSync = (%v, %q, %q, %v), want (%v, feature/login, %s, true)", ts, branch, commit, ok, now, testCommitHex)
	}
}

func TestStampSyncMetadata_NoGitRepo(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// repository() creates a bare .git dir without HEAD — gitinfo degrades, the
	// timestamp still lands.
	repo := repository(t, "nogit", "package nogit\n")
	now := time.Date(2026, 7, 21, 9, 14, 0, 0, time.UTC)
	if err := StampSyncMetadata(ctx, store, repo, now); err != nil {
		t.Fatalf("StampSyncMetadata: %v", err)
	}
	ts, branch, commit, ok := LastSync(ctx, store)
	if !ok || !ts.Equal(now) || branch != "" || commit != "" {
		t.Fatalf("LastSync = (%v, %q, %q, %v), want (%v, \"\", \"\", true)", ts, branch, commit, ok, now)
	}
}

func TestSyncRepo_StampsOnFullAndWarmPaths(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	repo := repository(t, "repo", "package repo\nfunc A() {}\n")
	gitify(t, repo, "main")
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })

	// Cold store → full pass, stamped.
	stats, err := SyncRepo(ctx, ing, store, repo, nil)
	if err != nil {
		t.Fatalf("SyncRepo (cold): %v", err)
	}
	if !stats.Full {
		t.Fatalf("cold SyncRepo stats = %+v, want Full", stats)
	}
	t1, branch, _, ok := LastSync(ctx, store)
	if !ok || branch != "main" {
		t.Fatalf("after cold sync: LastSync = (%v, %q, ok=%v)", t1, branch, ok)
	}

	// Unchanged repo → warm no-op with a checked count, restamped.
	stats, err = SyncRepo(ctx, ing, store, repo, nil)
	if err != nil {
		t.Fatalf("SyncRepo (warm): %v", err)
	}
	if stats.Full || stats.Checked == 0 || stats.Added+stats.Changed+stats.Removed != 0 {
		t.Fatalf("warm no-op stats = %+v, want warm zero-delta", stats)
	}

	// One edited file → warm delta with Changed=1.
	if err := os.WriteFile(filepath.Join(repo, "sample.go"), []byte("package repo\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err = SyncRepo(ctx, ing, store, repo, nil)
	if err != nil {
		t.Fatalf("SyncRepo (delta): %v", err)
	}
	if stats.Full || stats.Changed != 1 || stats.Added != 0 || stats.Removed != 0 {
		t.Fatalf("delta stats = %+v, want Changed=1", stats)
	}
}

func TestSyncRepo_NoStampOnIngestError(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := SyncRepo(ctx, ing, store, missing, nil); err == nil {
		t.Fatal("SyncRepo over a missing root succeeded, want error")
	}
	if _, _, _, ok := LastSync(ctx, store); ok {
		t.Fatal("failed sync still stamped metadata")
	}
}
