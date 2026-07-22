package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn while os.Stdout is redirected, returning what it wrote
// — needed where a delegate (runCompareBranches) writes to os.Stdout directly.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	return <-done
}

func snapshotFixture(t *testing.T) (repo, stateHome string) {
	t.Helper()
	stateHome = t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo = writeGoRepo(t)
	gitRepo(t, repo, "feature/login")
	return repo, stateHome
}

func TestRunSnapshot_CreateListRemove(t *testing.T) {
	repo, stateHome := snapshotFixture(t)

	// No args = list; an unindexed repo has nothing yet.
	var out bytes.Buffer
	if code := runSnapshotAt(repo, nil, &out); code != 0 || !strings.Contains(out.String(), "no snapshots yet") {
		t.Fatalf("empty list = %d %q, want friendly empty listing", code, out.String())
	}

	// Branch names sanitize: feature/login is stored as feature-login.
	out.Reset()
	if code := runSnapshotAt(repo, []string{"feature/login"}, &out); code != 0 {
		t.Fatalf("snapshot create exit = %d (output: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), `saved "feature-login"`) {
		t.Fatalf("create output missing saved line, got: %s", out.String())
	}
	matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "snapshots", "feature-login.sqlite"))
	if len(matches) != 1 {
		t.Fatalf("snapshot file matches = %v, want exactly one", matches)
	}
	if tmp, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "snapshots", "*.tmp*")); len(tmp) != 0 {
		t.Fatalf("leftover tmp files: %v", tmp)
	}

	// Listing shows the frozen branch context and node count.
	out.Reset()
	if code := runSnapshotAt(repo, []string{}, &out); code != 0 {
		t.Fatalf("snapshot list exit = %d", code)
	}
	if !strings.Contains(out.String(), "feature-login") || !strings.Contains(out.String(), "feature/login @ 1a2b3c4") || !strings.Contains(out.String(), "nodes") {
		t.Fatalf("listing missing name/branch/nodes, got: %s", out.String())
	}

	// Reserved name.
	out.Reset()
	if code := runSnapshotAt(repo, []string{"current"}, &out); code == 0 {
		t.Fatal("snapshot named 'current' succeeded, want rejection")
	}

	// Remove, then the listing is empty again.
	out.Reset()
	if code := runSnapshotAt(repo, []string{"-rm", "feature/login"}, &out); code != 0 {
		t.Fatalf("snapshot -rm exit = %d (output: %s)", code, out.String())
	}
	out.Reset()
	if code := runSnapshotAt(repo, nil, &out); code != 0 || !strings.Contains(out.String(), "no snapshots yet") {
		t.Fatalf("post-remove list = %d %q, want empty listing", code, out.String())
	}

	// Removing a missing snapshot errors.
	if code := runSnapshotAt(repo, []string{"-rm", "gone"}, &out); code == 0 {
		t.Fatal("snapshot -rm of a missing snapshot succeeded")
	}
}

func TestRunSnapshot_DetachedHeadWorksWithName(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(syncTestCommit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := runSnapshotAt(repo, []string{"pinned"}, &out); code != 0 {
		t.Fatalf("detached-HEAD snapshot with explicit name exit = %d (output: %s)", code, out.String())
	}
	out.Reset()
	if code := runSnapshotAt(repo, nil, &out); code != 0 || !strings.Contains(out.String(), "detached @ 1a2b3c4") {
		t.Fatalf("listing should show the frozen detached commit, got (%d): %s", code, out.String())
	}
}

func TestRunSnapshot_FailedRebuildKeepsExisting(t *testing.T) {
	repo, stateHome := snapshotFixture(t)

	var out bytes.Buffer
	if code := runSnapshotAt(repo, []string{"main"}, &out); code != 0 {
		t.Fatalf("seed snapshot exit = %d", code)
	}
	matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "snapshots", "main.sqlite"))
	if len(matches) != 1 {
		t.Fatalf("seed snapshot missing: %v", matches)
	}
	before, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}

	// Induce a build failure: occupy the .tmp path with a non-empty directory
	// so the replacement build cannot even open its staging store.
	tmpDir := matches[0] + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmpDir, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runSnapshotAt(repo, []string{"main"}, &out); code == 0 {
		t.Fatal("snapshot over a blocked tmp path succeeded, want failure")
	}
	after, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("existing snapshot gone after failed rebuild: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed rebuild corrupted the existing snapshot")
	}
}

func TestRunCompare_ResolutionAndParity(t *testing.T) {
	repo, stateHome := snapshotFixture(t)

	// current without a live graph → actionable error.
	if code := runCompareAt(repo, []string{"main", "current"}); code == 0 {
		t.Fatal("compare against missing current succeeded")
	}

	if code := runSnapshotAt(repo, []string{"main"}, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed snapshot failed")
	}
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	// Missing snapshot name → error listing the available ones.
	// (stderr carries the message; the exit code is the contract here.)
	if code := runCompareAt(repo, []string{"nope", "current"}); code == 0 {
		t.Fatal("compare with a missing snapshot succeeded")
	}

	// Byte-parity with the raw compare-branches invocation on the same paths.
	snapPath, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "snapshots", "main.sqlite"))
	dbPath, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "db.sqlite"))
	if len(snapPath) != 1 || len(dbPath) != 1 {
		t.Fatalf("fixture paths: snap=%v db=%v", snapPath, dbPath)
	}
	var friendly, raw string
	friendlyCode := -1
	friendly = captureStdout(t, func() { friendlyCode = runCompareAt(repo, []string{"main", "current"}) })
	rawCode := -1
	raw = captureStdout(t, func() { rawCode = runCompareBranches([]string{"-base", snapPath[0], "-head", dbPath[0]}) })
	if friendlyCode != 0 || rawCode != 0 {
		t.Fatalf("compare exits = (%d, %d), want (0, 0)", friendlyCode, rawCode)
	}
	if friendly != raw {
		t.Fatalf("graphi compare output diverges from compare-branches:\n--- compare ---\n%s\n--- compare-branches ---\n%s", friendly, raw)
	}
}
