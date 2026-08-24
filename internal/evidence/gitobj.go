package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git is the narrow slice of git plumbing the citation rules need. It shells out
// to the `git` binary — stdlib `os/exec` only, no new module dependency, the same
// way internal/parity already reaches git — and caches the HEAD tree listing,
// which is the one call that would otherwise run once per citation.
//
// Every method is read-only. The gate never writes to the repository.
type Git struct {
	Root string

	headPaths map[string]bool // repo-relative file paths at HEAD
	headDirs  map[string]bool // every directory prefix of those paths
	blobCache map[string]string
	typeCache map[string]string
}

// NewGit binds a Git to a repository root. It does not touch the repository until
// a method needs it, so constructing one is free.
func NewGit(root string) *Git {
	return &Git{
		Root:      root,
		blobCache: map[string]string{},
		typeCache: map[string]string{},
	}
}

func (g *Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// loadHead populates the HEAD tree listing once.
func (g *Git) loadHead() error {
	if g.headPaths != nil {
		return nil
	}
	out, err := g.run("ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return err
	}
	g.headPaths = map[string]bool{}
	g.headDirs = map[string]bool{}
	for _, p := range strings.Split(out, "\n") {
		if p == "" {
			continue
		}
		g.headPaths[p] = true
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			g.headDirs[p[:i]] = true
		}
	}
	return nil
}

// ExistsAtHEAD reports whether a repo-relative path names a file or a directory in
// the HEAD tree. Directories count: `docs/eval/runs/2026-08-20-local-grpc/` is a
// legitimate evidence citation.
func (g *Git) ExistsAtHEAD(p string) (bool, error) {
	if err := g.loadHead(); err != nil {
		return false, err
	}
	return g.headPaths[p] || g.headDirs[p], nil
}

// IsFileAtHEAD reports whether the path is a blob (not a directory) at HEAD.
func (g *Git) IsFileAtHEAD(p string) (bool, error) {
	if err := g.loadHead(); err != nil {
		return false, err
	}
	return g.headPaths[p], nil
}

// BlobSHA returns `git rev-parse HEAD:<path>` — the sha of the committed bytes.
func (g *Git) BlobSHA(p string) (string, error) {
	if s, ok := g.blobCache[p]; ok {
		return s, nil
	}
	s, err := g.run("rev-parse", "HEAD:"+p)
	if err != nil {
		return "", err
	}
	g.blobCache[p] = s
	return s, nil
}

// WorktreeSHA returns `git hash-object <path>` — the sha the worktree bytes WOULD
// have. Comparing it with BlobSHA is the AC-3 drift test.
func (g *Git) WorktreeSHA(p string) (string, error) {
	return g.run("hash-object", "--", p)
}

// ObjectType returns the git object type of a sha ("blob", "commit", "tree"), or
// an error when the sha names nothing or is ambiguous.
func (g *Git) ObjectType(sha string) (string, error) {
	if t, ok := g.typeCache[sha]; ok {
		return t, nil
	}
	t, err := g.run("cat-file", "-t", sha)
	if err != nil {
		return "", err
	}
	g.typeCache[sha] = t
	return t, nil
}

// FileAtHEAD returns the committed bytes of a path.
func (g *Git) FileAtHEAD(p string) ([]byte, error) {
	cmd := exec.Command("git", "show", "HEAD:"+p)
	cmd.Dir = g.Root
	return cmd.Output()
}

// ContentDigest is the sha256 of the committed bytes of a path — the digest form
// some rows record instead of a git blob sha (`docs/rc/go-binding-rate.md`).
func (g *Git) ContentDigest(p string) (string, error) {
	b, err := g.FileAtHEAD(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ListGoverned expands the declared governed-document globs against the worktree,
// applies the declared exclusions, and returns repo-relative paths in sorted glob
// order. Reading the worktree (not HEAD) is deliberate: a record added in the
// working tree must be swept before it is committed, or the gate would bless a
// brand-new record's citations exactly once, at the moment nobody checked them.
func ListGoverned(root string) ([]string, error) {
	excluded := map[string]bool{}
	for _, e := range GovernedDocExclusions {
		excluded[e] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, glob := range GovernedDocGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			return nil, fmt.Errorf("evidence: governed glob %q: %w", glob, err)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			rel = filepath.ToSlash(rel)
			if excluded[rel] || seen[rel] {
				continue
			}
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out, nil
}

// ReadGoverned reads one governed document from the worktree.
func ReadGoverned(root, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
