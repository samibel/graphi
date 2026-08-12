package gitlog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLog(t *testing.T) {
	raw := recordSep + "aaaa" + unitSep + "Alice" + unitSep + "1700000100\n" +
		"util/format.go\n" +
		"util\\format_test.go\n" + // Windows separator is normalized
		"\n" +
		recordSep + "bbbb" + unitSep + "Bob" + unitSep + "1700000000\n" +
		"cmd/app/main.go\n"
	commits, err := parseLog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	a := commits[0]
	if a.SHA != "aaaa" || a.Author != "Alice" || a.Timestamp.Unix() != 1700000100 {
		t.Fatalf("commit a = %+v", a)
	}
	if len(a.FilesChanged) != 2 || a.FilesChanged[1] != "util/format_test.go" {
		t.Fatalf("files a = %v", a.FilesChanged)
	}
	if commits[1].Author != "Bob" || len(commits[1].FilesChanged) != 1 {
		t.Fatalf("commit b = %+v", commits[1])
	}

	if _, err := parseLog(recordSep + "broken-header\n"); err == nil {
		t.Fatal("malformed header must error")
	}
	if _, err := parseLog(recordSep + "x" + unitSep + "y" + unitSep + "not-a-date\n"); err == nil {
		t.Fatal("malformed date must error")
	}
	empty, err := parseLog("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty log: %v %v", empty, err)
	}
}

// TestLogAgainstRealRepo builds a throwaway repository and reads it back
// through the real `git log` path. Skipped when git is unavailable.
func TestLogAgainstRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=a@x",
			"GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=a@x",
			"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n")
	write("b.go", "package a\n")
	run("add", "-A")
	run("commit", "-qm", "first: a and b together")
	write("a.go", "package a // v2\n")
	run("add", "-A")
	run("commit", "-qm", "second: a alone")

	commits, err := New(dir).Log(context.Background(), 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	// Newest first: the a-only commit, then the a+b commit.
	if len(commits[0].FilesChanged) != 1 || commits[0].FilesChanged[0] != "a.go" {
		t.Fatalf("newest commit files = %v", commits[0].FilesChanged)
	}
	if len(commits[1].FilesChanged) != 2 {
		t.Fatalf("oldest commit files = %v", commits[1].FilesChanged)
	}
	if commits[0].Author != "Alice" || commits[0].Timestamp.IsZero() {
		t.Fatalf("metadata = %+v", commits[0])
	}

	// maxCommits bound.
	one, err := New(dir).Log(context.Background(), 1, time.Time{})
	if err != nil || len(one) != 1 {
		t.Fatalf("bounded log = %v %v", one, err)
	}

	// A non-repository fails with an error, not a hang or empty success.
	if _, err := New(t.TempDir()).Log(context.Background(), 1, time.Time{}); err == nil {
		t.Fatal("non-repo must error")
	}
}
