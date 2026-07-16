package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// scopeRepo builds a fixture with in-scope and out-of-scope files for the
// opt-in ignore configuration.
func scopeRepo(t *testing.T) string {
	t.Helper()
	root := writeRepo(t, map[string]string{
		".gitignore":         "*.log\ndist/\n!keep.log\n",
		"main.go":            "package main\n",
		"keep.log":           "kept despite *.log\n",
		"debug.log":          "ignored by *.log\n",
		"dist/bundle.go":     "package dist\n",
		"assets/big/blob.go": "package big\n",
	})
	return root
}

// TestIgnoreScope_InvalidRootGitignoreFailsClosed proves that a malformed
// privacy boundary aborts before the walk can persist any repository content.
func TestIgnoreScope_InvalidRootGitignoreFailsClosed(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	root := writeRepo(t, map[string]string{
		".gitignore": "# private files\n[secret\n",
		"main.go":    "package main\n",
	})
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	err := i.IngestAll(context.Background(), root)
	if err == nil {
		t.Fatal("invalid root .gitignore must abort ingest")
	}
	if got := err.Error(); !strings.Contains(got, "parse root .gitignore") || !strings.Contains(got, "line 2") {
		t.Fatalf("error must identify the root config and invalid line, got %q", got)
	}
	if got := pathsOf(t, store); len(got) != 0 {
		t.Fatalf("failed scope validation persisted graph content: %v", got)
	}
}

// TestIgnoreScope_UnreadableRootGitignoreFailsClosed uses a directory at the
// config path instead of chmod so the read fails even as root. Ingest must not
// reinterpret that failure as "no .gitignore".
func TestIgnoreScope_UnreadableRootGitignoreFailsClosed(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	root := writeRepo(t, map[string]string{"main.go": "package main\n"})
	if err := os.Mkdir(filepath.Join(root, ".gitignore"), 0o700); err != nil {
		t.Fatalf("create unreadable .gitignore fixture: %v", err)
	}
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	err := i.IngestAll(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "read root .gitignore") {
		t.Fatalf("unreadable root .gitignore error = %v", err)
	}
	if got := pathsOf(t, store); len(got) != 0 {
		t.Fatalf("failed scope validation persisted graph content: %v", got)
	}
}

func TestIgnoreScope_OutsideSymlinkGitignoreFailsClosedWithoutContentLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	root := writeRepo(t, map[string]string{"main.go": "package main\n"})
	const secret = "OUTSIDE_GITIGNORE_SECRET_MUST_NOT_LEAK"
	outside := filepath.Join(t.TempDir(), "external.gitignore")
	if err := os.WriteFile(outside, []byte("["+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	err := i.IngestAll(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "read root .gitignore") {
		t.Fatalf("outside symlink error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("outside .gitignore content leaked in error: %q", err)
	}
	if got := pathsOf(t, store); len(got) != 0 {
		t.Fatalf("failed scope validation persisted graph content: %v", got)
	}
}

func TestIgnoreScope_OversizeRootGitignoreFailsClosed(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	root := writeRepo(t, map[string]string{
		".gitignore": strings.Repeat("x", (1<<20)+1),
		"main.go":    "package main\n",
	})
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})

	err := i.IngestAll(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1048576-byte limit") {
		t.Fatalf("oversize .gitignore error = %v", err)
	}
	if got := pathsOf(t, store); len(got) != 0 {
		t.Fatalf("oversize scope config persisted graph content: %v", got)
	}
}

// TestIgnoreScope_ExplicitOptOutBypassesInvalidRootGitignore pins the escape
// hatch: opting out means the root file is deliberately neither read nor parsed.
func TestIgnoreScope_ExplicitOptOutBypassesInvalidRootGitignore(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "0")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	t.Setenv(ingest.EnvIndexAll, "1")
	root := writeRepo(t, map[string]string{
		".gitignore": "[secret\n",
		"main.go":    "package main\n",
	})
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("explicit opt-out must bypass invalid .gitignore: %v", err)
	}
	if !pathsOf(t, store)["main.go"] {
		t.Fatal("explicit opt-out did not ingest source file")
	}
}

func TestIgnoreScope_MissingRootGitignoreIsValid(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	t.Setenv(ingest.EnvIndexAll, "1")
	root := writeRepo(t, map[string]string{"main.go": "package main\n"})
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("missing root .gitignore must be valid: %v", err)
	}
	if !pathsOf(t, store)["main.go"] {
		t.Fatal("source file missing from repo without .gitignore")
	}
}

// pathsOf returns the set of source paths present in the store.
func pathsOf(t *testing.T, store graphstore.Graphstore) map[string]bool {
	t.Helper()
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	got := map[string]bool{}
	for _, n := range nodes {
		got[n.SourcePath()] = true
	}
	return got
}

// TestIgnoreScope_GitignoreDefaultOn pins the PRIV-01 (SW-119) privacy
// default: with NO env switches set, the root .gitignore governs what gets
// symbols — ignored files (where secrets live) never enter the graph, and
// negation still re-includes.
func TestIgnoreScope_GitignoreDefaultOn(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	t.Setenv(ingest.EnvIndexAll, "")
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	root := scopeRepo(t)
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	got := pathsOf(t, store)
	for _, want := range []string{"main.go", "keep.log", "assets/big/blob.go"} {
		if !got[want] {
			t.Errorf("in-scope file %s missing from graph", want)
		}
	}
	for _, banned := range []string{"debug.log", "dist/bundle.go"} {
		if got[banned] {
			t.Errorf("gitignored file %s was indexed under the privacy default", banned)
		}
	}
}

// TestIgnoreScope_GitignoreExplicitOptOut pins the documented escape hatch:
// GRAPHI_RESPECT_GITIGNORE=0 indexes ignored files again (with the WP-07
// denylist also opted out to isolate the gitignore axis).
func TestIgnoreScope_GitignoreExplicitOptOut(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "0")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	t.Setenv(ingest.EnvIndexAll, "1")
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	root := scopeRepo(t)
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	got := pathsOf(t, store)
	for _, want := range []string{"debug.log", "dist/bundle.go", "assets/big/blob.go"} {
		if !got[want] {
			t.Errorf("opt-out scope must index %s", want)
		}
	}
}

// TestIgnoreScope_GitignoreOptIn pins the walk-side scope: with
// GRAPHI_RESPECT_GITIGNORE the root .gitignore governs what gets symbols,
// including negation.
func TestIgnoreScope_GitignoreOptIn(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "1")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	root := scopeRepo(t)
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	got := pathsOf(t, store)
	for _, want := range []string{"main.go", "keep.log", "assets/big/blob.go"} {
		if !got[want] {
			t.Errorf("in-scope file %s missing from graph", want)
		}
	}
	for _, banned := range []string{"debug.log", "dist/bundle.go"} {
		if got[banned] {
			t.Errorf("out-of-scope file %s was indexed", banned)
		}
	}
}

// TestIgnoreScope_ExtraDirs pins GRAPHI_IGNORE: extra directory basenames
// prune at any depth, independent of .gitignore.
func TestIgnoreScope_ExtraDirs(t *testing.T) {
	// Opt out of the PRIV-01 gitignore default to isolate the extra-dirs axis
	// (the fixture .gitignore would otherwise prune debug.log/dist).
	t.Setenv(ingest.EnvRespectGitignore, "0")
	t.Setenv(ingest.EnvIgnoreDirs, "assets, Missing")
	// WP-07: opt out of the default build-output denylist so this test isolates
	// the GRAPHI_IGNORE extra-dirs contract (it asserts dist/ stays indexed).
	t.Setenv(ingest.EnvIndexAll, "1")
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	root := scopeRepo(t)
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	got := pathsOf(t, store)
	if got["assets/big/blob.go"] {
		t.Error("GRAPHI_IGNORE dir was indexed")
	}
	if !got["dist/bundle.go"] || !got["debug.log"] {
		t.Error("GRAPHI_IGNORE must not imply gitignore semantics")
	}
}

// TestIgnoreScope_WatcherAgrees pins walk/watcher agreement: ParseFile must
// ignore exactly what the walk excludes, or a watched edit would re-introduce
// an out-of-scope file (full-vs-incremental parity break).
func TestIgnoreScope_WatcherAgrees(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "1")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	root := scopeRepo(t)

	pf, err := i.ParseFile(context.Background(), root, "debug.log")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if pf != nil {
		t.Fatalf("watcher parsed out-of-scope file: %+v", pf)
	}
	kept, err := i.ParseFile(context.Background(), root, "keep.log")
	if err != nil {
		t.Fatalf("ParseFile(keep.log): %v", err)
	}
	if kept == nil {
		t.Fatal("negated (re-included) file must stay in watcher scope")
	}
}

// TestIgnoreScope_WarmStartInvalidatedOnConfigChange pins the fingerprint: a
// store certified under one scope must NOT warm-start under another — the
// graph means something different.
func TestIgnoreScope_WarmStartInvalidatedOnConfigChange(t *testing.T) {
	ctx := context.Background()
	root := scopeRepo(t)
	dir := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()

	// Pass 1: default scope (PRIV-01: gitignore ON). A fresh Ingester per phase
	// over the SAME meta sidecar simulates "same store, new process" — the env
	// is captured per (Ingester, root), matching real process lifecycles.
	metaDir := t.TempDir()
	t.Setenv(ingest.EnvRespectGitignore, "")
	i1, err := ingest.New(store, &stubParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i1.Close()
	if err := i1.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if _, ok, err := i1.CanWarmStart(ctx, root); err != nil || !ok {
		t.Fatalf("same scope must warm-start: ok=%v err=%v", ok, err)
	}

	// Same meta, new process with the gitignore scope opted OUT → stamp
	// mismatch → cold.
	t.Setenv(ingest.EnvRespectGitignore, "0")
	i2, err := ingest.New(store, &stubParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i2.Close()
	if _, ok, err := i2.CanWarmStart(ctx, root); err != nil || ok {
		t.Fatalf("changed scope must NOT warm-start: ok=%v err=%v", ok, err)
	}

	// Re-certify under the new scope, then warm-start works again.
	if err := i2.IngestAll(ctx, root); err != nil {
		t.Fatalf("re-IngestAll: %v", err)
	}
	if _, ok, err := i2.CanWarmStart(ctx, root); err != nil || !ok {
		t.Fatalf("re-certified scope must warm-start: ok=%v err=%v", ok, err)
	}
}
