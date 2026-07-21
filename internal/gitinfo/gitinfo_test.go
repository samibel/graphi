package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sha1Hex   = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	sha256Hex = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1a2b3c4d5e6f7a8b9c0d1e2f"
)

// writeFile creates path (and parents) with content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitFixture lays out a minimal .git dir with the given HEAD content.
func gitFixture(t *testing.T, head string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), head)
	return root
}

func TestHeadLooseRef(t *testing.T) {
	root := gitFixture(t, "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "main"), sha1Hex+"\n")
	info, ok := Head(root)
	if !ok {
		t.Fatal("Head not ok")
	}
	want := Info{Branch: "main", Commit: sha1Hex}
	if info != want {
		t.Fatalf("got %+v, want %+v", info, want)
	}
}

func TestHeadBranchWithSlash(t *testing.T) {
	root := gitFixture(t, "ref: refs/heads/feature/login\n")
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "feature", "login"), sha1Hex+"\n")
	info, ok := Head(root)
	if !ok || info.Branch != "feature/login" || info.Commit != sha1Hex {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestHeadPackedRefsOnly(t *testing.T) {
	root := gitFixture(t, "ref: refs/heads/main\n")
	packed := "# pack-refs with: peeled fully-peeled sorted\n" +
		"9999999999999999999999999999999999999999 refs/tags/v1\n" +
		"^8888888888888888888888888888888888888888\n" +
		sha1Hex + " refs/heads/main\n"
	writeFile(t, filepath.Join(root, ".git", "packed-refs"), packed)
	info, ok := Head(root)
	if !ok || info.Branch != "main" || info.Commit != sha1Hex {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestHeadDetached(t *testing.T) {
	for _, hex := range []string{sha1Hex, sha256Hex} {
		info, ok := Head(gitFixture(t, hex+"\n"))
		if !ok || !info.Detached || info.Commit != hex || info.Branch != "" {
			t.Fatalf("hex len %d: got %+v ok=%v", len(hex), info, ok)
		}
	}
}

func TestHeadUnbornBranch(t *testing.T) {
	info, ok := Head(gitFixture(t, "ref: refs/heads/main\n"))
	if !ok || info.Branch != "main" || info.Commit != "" || info.Detached {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestHeadCRLF(t *testing.T) {
	root := gitFixture(t, "ref: refs/heads/main\r\n")
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "main"), sha1Hex+"\r\n")
	info, ok := Head(root)
	if !ok || info.Branch != "main" || info.Commit != sha1Hex {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

// TestHeadLinkedWorktree covers the `.git` regular-file form: HEAD lives in the
// per-worktree gitdir while refs/packed-refs live in the shared common dir.
func TestHeadLinkedWorktree(t *testing.T) {
	shared := t.TempDir()
	mainGit := filepath.Join(shared, "main", ".git")
	writeFile(t, filepath.Join(mainGit, "refs", "heads", "feature"), sha1Hex+"\n")
	wtGit := filepath.Join(mainGit, "worktrees", "wt")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feature\n")
	// Relative commondir, as git writes it for linked worktrees.
	writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")

	worktree := filepath.Join(shared, "wt")
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+wtGit+"\n")

	info, ok := Head(worktree)
	if !ok || info.Branch != "feature" || info.Commit != sha1Hex {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestHeadRelativeGitdirFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real-git", "HEAD"), sha1Hex+"\n")
	writeFile(t, filepath.Join(root, "repo", ".git"), "gitdir: ../real-git\n")
	info, ok := Head(filepath.Join(root, "repo"))
	if !ok || info.Commit != sha1Hex || !info.Detached {
		t.Fatalf("got %+v ok=%v", info, ok)
	}
}

func TestHeadNoGit(t *testing.T) {
	if info, ok := Head(t.TempDir()); ok {
		t.Fatalf("expected ok=false, got %+v", info)
	}
}

func TestHeadGarbage(t *testing.T) {
	for _, head := range []string{"", "not a ref", "ref: ", "abc123"} {
		if info, ok := Head(gitFixture(t, head)); ok {
			t.Fatalf("HEAD %q: expected ok=false, got %+v", head, info)
		}
	}
}

func TestShort(t *testing.T) {
	cases := []struct {
		info Info
		want string
	}{
		{Info{Branch: "main", Commit: sha1Hex}, "main @ 1a2b3c4"},
		{Info{Commit: sha1Hex, Detached: true}, "detached @ 1a2b3c4"},
		{Info{Branch: "main"}, "main (no commits yet)"},
		{Info{}, ""},
	}
	for _, c := range cases {
		if got := c.info.Short(); got != c.want {
			t.Errorf("Short(%+v) = %q, want %q", c.info, got, c.want)
		}
	}
}

func TestIsCommitHexRejects(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("g", 40),  // non-hex rune
		strings.ToUpper(sha1Hex), // git object names are lowercase
		sha1Hex[:39],             // wrong length
		strings.Repeat("a", 41),  // wrong length
	} {
		if isCommitHex(s) {
			t.Errorf("isCommitHex(%q) = true, want false", s)
		}
	}
}
