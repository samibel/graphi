// Package gitinfo resolves the current git HEAD (branch + commit) for a repo
// root by reading the .git files directly — no `git` subprocess, no cgo, no
// network — so the local-first and CGo-free contracts hold. It exists for
// user-facing messaging only (branch banners, sync metadata, `graphi status`);
// the indexing engine never depends on git.
//
// Layering: gitinfo lives under internal/, outside the cmd→surfaces→engine→core
// graph, so cmd-rank packages may import it directly.
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// Info describes the resolved HEAD of a repository.
type Info struct {
	Branch   string // short branch name ("feature/login"); "" when detached
	Commit   string // full commit hex; "" on an unborn branch
	Detached bool   // HEAD points at a commit, not a branch
}

// Head resolves HEAD for repoRoot. ok=false when repoRoot has no .git entry
// (go.mod/go.work-only roots) or on any read/parse error — callers degrade
// their messaging instead of failing, so this never returns an error.
func Head(repoRoot string) (Info, bool) {
	gitDir, commonDir, ok := resolveGitDirs(repoRoot)
	if !ok {
		return Info{}, false
	}
	head, err := readTrimmed(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return Info{}, false
	}
	if ref, isRef := strings.CutPrefix(head, "ref: "); isRef {
		ref = strings.TrimSpace(ref)
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if branch == "" {
			return Info{}, false
		}
		commit, _ := resolveRef(commonDir, ref) // absent ref = unborn branch, not an error
		return Info{Branch: branch, Commit: commit}, true
	}
	if !isCommitHex(head) {
		return Info{}, false
	}
	return Info{Commit: head, Detached: true}, true
}

// Short renders the compact human form used in banners and status lines:
// "main @ 1a2b3c4", "detached @ 1a2b3c4", or "main (no commits yet)".
func (i Info) Short() string {
	switch {
	case i.Detached && i.Commit != "":
		return "detached @ " + shortHex(i.Commit)
	case i.Branch != "" && i.Commit != "":
		return i.Branch + " @ " + shortHex(i.Commit)
	case i.Branch != "":
		return i.Branch + " (no commits yet)"
	}
	return ""
}

// resolveGitDirs locates the per-worktree git dir (holding HEAD) and the common
// dir (holding refs/ and packed-refs). A `.git` regular file is the linked
// worktree/submodule form: it names the real gitdir via a "gitdir: <path>"
// line; a gitdir may in turn delegate shared state via a `commondir` file.
// Relative paths resolve against the file that named them.
func resolveGitDirs(repoRoot string) (gitDir, commonDir string, ok bool) {
	entry := filepath.Join(repoRoot, ".git")
	fi, err := os.Stat(entry)
	if err != nil {
		return "", "", false
	}
	gitDir = entry
	if !fi.IsDir() {
		line, err := readTrimmed(entry)
		if err != nil {
			return "", "", false
		}
		target, isGitFile := strings.CutPrefix(line, "gitdir: ")
		if !isGitFile {
			return "", "", false
		}
		gitDir = resolveAgainst(repoRoot, strings.TrimSpace(target))
	}
	commonDir = gitDir
	if common, err := readTrimmed(filepath.Join(gitDir, "commondir")); err == nil && common != "" {
		commonDir = resolveAgainst(gitDir, common)
	}
	return gitDir, commonDir, true
}

// resolveRef resolves a full ref name ("refs/heads/main") to a commit hex,
// trying the loose ref file first and falling back to packed-refs. ok=false
// means the ref does not exist (e.g. an unborn branch).
func resolveRef(commonDir, ref string) (string, bool) {
	if hex, err := readTrimmed(filepath.Join(commonDir, filepath.FromSlash(ref))); err == nil && isCommitHex(hex) {
		return hex, true
	}
	packed, err := os.ReadFile(filepath.Join(commonDir, "packed-refs"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(packed), "\n") {
		line = strings.TrimSuffix(strings.TrimSpace(line), "\r")
		// Skip header comments and "^<hex>" peel lines for annotated tags.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		hex, name, found := strings.Cut(line, " ")
		if found && name == ref && isCommitHex(hex) {
			return hex, true
		}
	}
	return "", false
}

// readTrimmed reads a small git metadata file and strips surrounding
// whitespace, tolerating CRLF line endings.
func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func resolveAgainst(base, path string) string {
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

// isCommitHex accepts full sha1 (40) and sha256 (64) object names.
func isCommitHex(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func shortHex(hex string) string {
	if len(hex) > 7 {
		return hex[:7]
	}
	return hex
}
