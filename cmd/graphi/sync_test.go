package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const syncTestCommit = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"

// gitRepo turns dir into a minimal git checkout on branch (fabricated .git —
// no git binary in the loop, matching internal/gitinfo's contract).
func gitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	head := filepath.Join(dir, ".git", "HEAD")
	if err := os.MkdirAll(filepath.Dir(head), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(head, []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(dir, ".git", "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(syncTestCommit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunSync_LifecycleAndBranchSwitch(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	// First sync: cold store → full pass, auto-managed DB created.
	var out bytes.Buffer
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("first sync exit = %d, want 0 (output: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "graphi sync: "+repo+" (main @ 1a2b3c4)") {
		t.Fatalf("first sync header missing branch, got: %s", out.String())
	}
	if !strings.Contains(out.String(), "graphi sync: full re-index complete") {
		t.Fatalf("first sync summary != full re-index, got: %s", out.String())
	}
	matches, err := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "db.sqlite"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("auto-managed state DB matches = %v (err %v), want exactly one", matches, err)
	}

	// Unchanged repo: warm no-op.
	out.Reset()
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("second sync exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "up to date (checked") {
		t.Fatalf("second sync summary != up to date, got: %s", out.String())
	}
	if strings.Contains(out.String(), "Branch switch detected") {
		t.Fatalf("unexpected branch-switch message on same branch: %s", out.String())
	}

	// One edited file: incremental delta.
	if err := os.WriteFile(filepath.Join(repo, "cart.go"), []byte("package shop\n\nfunc checkout() int { return 9 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("delta sync exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "graphi sync: 0 added, 1 changed, 0 removed") {
		t.Fatalf("delta sync summary wrong, got: %s", out.String())
	}

	// Branch switch: rewrite HEAD, next sync announces it.
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/feature/login\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("post-switch sync exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Branch switch detected: main → feature/login") {
		t.Fatalf("branch-switch message missing, got: %s", out.String())
	}
}

func TestRunSync_NotARepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out bytes.Buffer
	if code := runSyncAt(t.TempDir(), nil, &out); code != 1 {
		t.Fatalf("sync outside a repo exit = %d, want 1", code)
	}
}

func TestRunRebuild_FullPass(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	var out bytes.Buffer
	if code := runRebuildAt(repo, nil, &out); code != 0 {
		t.Fatalf("rebuild exit = %d, want 0 (output: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "graphi rebuild: re-indexing "+repo+" (main @ 1a2b3c4)") {
		t.Fatalf("rebuild announcement missing, got: %s", out.String())
	}
	if !strings.Contains(out.String(), "graphi rebuild: done") {
		t.Fatalf("rebuild completion missing, got: %s", out.String())
	}

	// A rebuild over a warm store is still a full pass, never a warm no-op.
	out.Reset()
	if code := runRebuildAt(repo, nil, &out); code != 0 {
		t.Fatalf("second rebuild exit = %d, want 0", code)
	}
	if strings.Contains(out.String(), "up to date") {
		t.Fatalf("rebuild took the warm path, got: %s", out.String())
	}
}

func TestRunRebuild_NotARepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out bytes.Buffer
	if code := runRebuildAt(t.TempDir(), nil, &out); code != 1 {
		t.Fatalf("rebuild outside a repo exit = %d, want 1", code)
	}
}

func TestIndexHintLine(t *testing.T) {
	if got := indexHintLine(true, true); !strings.Contains(got, "graphi sync") {
		t.Fatalf("explicit-root TTY hint = %q, want sync/rebuild tip", got)
	}
	for _, c := range []struct{ explicit, tty bool }{{false, true}, {true, false}, {false, false}} {
		if got := indexHintLine(c.explicit, c.tty); got != "" {
			t.Fatalf("indexHintLine(%v, %v) = %q, want \"\"", c.explicit, c.tty, got)
		}
	}
}

// TestDetectedRootNotice pins the ancestor-root warning: an ingest verb run
// from a SUBDIRECTORY of a detected repo announces the root it is about to
// index (the nearest enclosing .git may sit far above cwd — e.g. a
// git-tracked $HOME — and the verb would otherwise silently full-index that
// entire tree), while an explicit -root or cwd == root stays silent.
func TestDetectedRootNotice(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	gitRepo(t, repo, "main")
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Detected from a subdirectory: the notice names both root and cwd.
	target, err := resolveIngestTarget(sub, "", "", "", false)
	if err != nil {
		t.Fatalf("resolveIngestTarget from subdir: %v", err)
	}
	if !target.detected {
		t.Fatal("a root found via DetectRepo must be flagged detected")
	}
	notice := detectedRootNotice(target, sub)
	if !strings.Contains(notice, target.root) || !strings.Contains(notice, sub) {
		t.Fatalf("notice %q must name the detected root %q and the cwd %q", notice, target.root, sub)
	}
	if !strings.Contains(notice, "-root") {
		t.Fatalf("notice %q must mention the -root override", notice)
	}

	// cwd IS the detected root: nothing surprising, no notice.
	atRoot, err := resolveIngestTarget(repo, "", "", "", false)
	if err != nil {
		t.Fatalf("resolveIngestTarget at root: %v", err)
	}
	if got := detectedRootNotice(atRoot, repo); got != "" {
		t.Fatalf("notice at the root itself = %q, want \"\"", got)
	}

	// Explicit -root: the user chose it, no notice — from any cwd.
	explicit, err := resolveIngestTarget(sub, repo, "", "", false)
	if err != nil {
		t.Fatalf("resolveIngestTarget explicit: %v", err)
	}
	if explicit.detected {
		t.Fatal("an explicit -root must not be flagged detected")
	}
	if got := detectedRootNotice(explicit, sub); got != "" {
		t.Fatalf("notice for explicit -root = %q, want \"\"", got)
	}
}
