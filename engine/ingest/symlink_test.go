package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// TestIngest_FailsClosed_OnSymlinkToDirectory is a regression test: a repo
// containing a symlink whose target is a directory (the pnpm node_modules/.pnpm
// layout links whole package directories, not individual files) used to abort
// the entire ingest with "read <path>: is a directory" — fs.DirEntry.IsDir()
// reflects a symlink's OWN type (never "directory"), so the symlink reached
// os.ReadFile instead of being recognized and skipped as a directory. It must
// now be recorded as a SkipUnreadable diagnostic and ingestion of the rest of
// the repo must proceed normally.
func TestIngest_FailsClosed_OnSymlinkToDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &stubParser{}
	i := newIngester(t, store, parser)

	// "sub" (not "target"): WP-07 prunes target/ by default, which would drop
	// nested.go from the parse count this symlink test asserts.
	root := writeRepo(t, map[string]string{
		"real.go":       "package a\n",
		"sub/nested.go": "package b\n",
	})
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "linked")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll should not abort on a symlink-to-directory (fail-closed skip): %v", err)
	}

	// real.go and target/nested.go are real files and must have been parsed;
	// "linked" (the symlink) must not have been read as a file.
	if parser.parseCount.Load() != 2 {
		t.Fatalf("expected 2 real files parsed, got %d", parser.parseCount.Load())
	}

	var found bool
	for _, s := range i.SkippedDiagnostics() {
		if s.Path == "linked" {
			found = true
			if s.Reason != ingest.SkipUnreadable {
				t.Fatalf("expected SkipUnreadable for the symlink, got %q", s.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("expected a SkipUnreadable diagnostic for %q, got %v", "linked", i.SkippedDiagnostics())
	}
}

func TestIngest_NeverFollowsFileSymlinkOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{"safe.go": "package safe\n"})
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package private\nconst Token = \"must-not-enter\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "secret.go")); err != nil {
		t.Fatal(err)
	}

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if parser.parseCount.Load() != 1 {
		t.Fatalf("outside symlink target was parsed: count=%d, want only safe.go", parser.parseCount.Load())
	}
	assertUnreadableSkip(t, i.SkippedDiagnostics(), "secret.go")

	parser.parseCount.Store(0)
	if _, err := i.ParseFile(ctx, root, "secret.go"); err != nil {
		t.Fatalf("ParseFile symlink: %v", err)
	}
	if parser.parseCount.Load() != 0 {
		t.Fatal("watch parse followed an outside symlink target")
	}
}

func TestIngest_ParseFileNeverFollowsIntermediateSymlinkOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{"safe.go": "package safe\n"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	pf, err := i.ParseFile(ctx, root, "linked-dir/secret.go")
	if err != nil {
		t.Fatalf("ParseFile intermediate symlink: %v", err)
	}
	if pf == nil {
		t.Fatal("outside-root path must be represented as a fail-closed skip")
	}
	if parser.parseCount.Load() != 0 {
		t.Fatal("watch parse followed an intermediate symlink outside root")
	}
	assertUnreadableSkip(t, i.SkippedDiagnostics(), "linked-dir/secret.go")
}

func TestIngest_FileReplacedBySymlinkIsRemovedFromGraph(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)
	root := writeRepo(t, map[string]string{"owned.go": "package owned\n"})
	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "private.go")
	if err := os.WriteFile(outside, []byte("package private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(root, "owned.go")
	if err := os.Remove(owned); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, owned); err != nil {
		t.Fatal(err)
	}
	parser.parseCount.Store(0)
	if err := i.IngestChanged(ctx, root, []string{"owned.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	if parser.parseCount.Load() != 0 {
		t.Fatal("incremental ingest followed replacement symlink")
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("stale node survived file-to-symlink replacement: %v", nodes)
	}
}

func assertUnreadableSkip(t *testing.T, skips []ingest.SkipDiagnostic, path string) {
	t.Helper()
	for _, skip := range skips {
		if skip.Path == path && skip.Reason == ingest.SkipUnreadable {
			return
		}
	}
	t.Fatalf("missing SkipUnreadable for %q: %v", path, skips)
}

// TestIngest_PrunesIgnoredDirectories proves node_modules, .git, vendor, and
// friends are never descended into or read — not skipped-with-diagnostic (that
// would still cost a stat/read attempt per entry), but pruned via
// filepath.SkipDir before the walk ever enters them. This is the actual fix for
// why indexing a JS/TS repo used to crash: pnpm's node_modules/.pnpm tree is
// exactly the kind of symlink-heavy layout TestIngest_FailsClosed_OnSymlinkToDirectory
// guards against, and it's also just noise no code-intelligence query wants.
func TestIngest_PrunesIgnoredDirectories(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &stubParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"real.go":                          "package a\n",
		"node_modules/pkg/index.js":        "// not real code\n",
		".git/config":                      "[core]\n",
		"vendor/github.com/x/y/lib.go":     "package lib\n",
		".venv/lib/site-packages/mod.py":   "# not real code\n",
		"__pycache__/real.cpython-312.pyc": "\x00\x00",
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	if parser.parseCount.Load() != 1 {
		t.Fatalf("expected only real.go parsed (ignored dirs pruned), got %d parses", parser.parseCount.Load())
	}
	if skips := i.SkippedDiagnostics(); len(skips) != 0 {
		t.Fatalf("expected zero skip diagnostics (pruned dirs are never visited, not skipped), got %v", skips)
	}
}

// TestIngest_PrunesIgnoredDirectories_CaseInsensitive proves the pruning check
// matches ignored directory names regardless of case, since case-insensitive
// (but case-preserving) filesystems — the macOS and Windows defaults — don't
// guarantee node_modules/.git/vendor/... survive a checkout in their
// conventional all-lowercase casing.
func TestIngest_PrunesIgnoredDirectories_CaseInsensitive(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &stubParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"real.go":                "package a\n",
		"NODE_MODULES/pkg/a.js":  "// not real code\n",
		"Vendor/github.com/x.go": "package x\n",
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if parser.parseCount.Load() != 1 {
		t.Fatalf("expected only real.go parsed (case-insensitive prune), got %d parses", parser.parseCount.Load())
	}
}

// TestIngest_ParseFile_IgnoresPathUnderIgnoredDir proves the watcher's per-file
// entry point (ParseFile, which has no walk to prune) also ignores a path under
// node_modules/.git/vendor/... — silently, returning (nil, nil), the same
// contract used for an untracked file type. Without this check, an fsnotify
// event for a file changed inside node_modules (which churns constantly during
// a package-manager install) would still be read, parsed, and tracked by the
// live watcher, reintroducing exactly the noise walk()'s pruning exists to
// avoid, and — for an unreadable path like a pnpm symlink — would flood
// SkippedDiagnostics with routine node_modules churn instead of surfacing
// diagnostics a user would actually act on.
func TestIngest_ParseFile_IgnoresPathUnderIgnoredDir(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &stubParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"real.go": "package a\n",
		"node_modules/.pnpm/pkg@1.0.0/node_modules/pkg/index.js": "// not real code\n",
	})

	pf, err := i.ParseFile(ctx, root, "node_modules/.pnpm/pkg@1.0.0/node_modules/pkg/index.js")
	if err != nil {
		t.Fatalf("ParseFile: expected (nil, nil) for an ignored-dir path, got err %v", err)
	}
	if pf != nil {
		t.Fatalf("ParseFile: expected nil ParsedFile for an ignored-dir path, got %+v", pf)
	}
	if parser.parseCount.Load() != 0 {
		t.Fatalf("expected the file under node_modules to never reach the parser, got %d parses", parser.parseCount.Load())
	}
	if skips := i.SkippedDiagnostics(); len(skips) != 0 {
		t.Fatalf("expected zero skip diagnostics (ignored silently, not recorded), got %v", skips)
	}
}
