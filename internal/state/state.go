// Package state derives graphi's auto-managed, per-repo on-disk state layout so
// the default `graphi query`/`graphi search` invocation can discover a durable
// store + daemon socket for the current working directory without any flags
// (story SW-068). It is a pure path/I-O helper: every path is derived from the
// repo root and a path-only fingerprint, so the layout is deterministic and
// holds no wall-clock or random content.
//
// Layering: state lives under internal/, outside the cmd→surfaces→engine→core
// graph, so cmd may import it directly to wire default discovery.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StateDir returns the root directory under which graphi keeps its per-repo
// state. It honors $XDG_STATE_HOME when set & non-empty (joining "graphi"),
// otherwise falls back to ~/.graphi.
func StateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "graphi")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".graphi")
}

// RepoRoot resolves the repository root for cwd. It cleans+absolutizes cwd,
// then walks up looking first for a `.git` entry (dir or file) and returns that
// directory; if none is found it walks up for `go.work`, then `go.mod`, and
// returns the directory holding the first hit; otherwise it returns the
// absolute cwd. Deterministic: no environment beyond the filesystem is read.
func RepoRoot(cwd string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", fmt.Errorf("state: abs cwd: %w", err)
	}
	if root, ok := walkUpFor(abs, ".git"); ok {
		return root, nil
	}
	if root, ok := walkUpFor(abs, "go.work"); ok {
		return root, nil
	}
	if root, ok := walkUpFor(abs, "go.mod"); ok {
		return root, nil
	}
	return abs, nil
}

// DetectRepo resolves the repository root for cwd like RepoRoot, but returns
// ok=true ONLY when a real repo marker (`.git`, `go.work`, or `go.mod`) was
// actually found by walking up. Unlike RepoRoot it does NOT fall back to the
// bare cwd: when no marker is present it returns ("", false), so callers can
// distinguish "this is a code repository" from "this is just some directory".
func DetectRepo(cwd string) (root string, ok bool) {
	abs, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", false
	}
	if r, found := walkUpFor(abs, ".git"); found {
		return r, true
	}
	if r, found := walkUpFor(abs, "go.work"); found {
		return r, true
	}
	if r, found := walkUpFor(abs, "go.mod"); found {
		return r, true
	}
	return "", false
}

// walkUpFor walks from start up to the filesystem root looking for an entry
// named name (file or dir). It returns the directory containing it.
func walkUpFor(start, name string) (string, bool) {
	dir := start
	for {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Fingerprint returns a stable, path-only 16-hex-char identifier for absRoot.
// It is derived solely from the cleaned path, so it never embeds time or
// randomness and is identical across runs for the same repo.
func Fingerprint(absRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(absRoot)))
	return hex.EncodeToString(sum[:])[:16]
}

// Paths is the resolved per-repo state layout. All fields are absolute paths
// (plus the repo Root and its Fingerprint); none of this implies the paths
// exist on disk — see Ensure / DiscoverDB.
type Paths struct {
	Root        string
	Dir         string
	DB          string
	Socket      string
	Meta        string
	RepoFile    string
	Fingerprint string
}

// Resolve computes the per-repo Paths for cwd. It performs no I/O beyond the
// repo-root walk: it derives the fingerprint from the path and lays out the
// per-repo directory under StateDir().
func Resolve(cwd string) (Paths, error) {
	root, err := RepoRoot(cwd)
	if err != nil {
		return Paths{}, err
	}
	fp := Fingerprint(root)
	dir := filepath.Join(StateDir(), fp)
	return Paths{
		Root:        root,
		Dir:         dir,
		DB:          filepath.Join(dir, "db.sqlite"),
		Socket:      filepath.Join(dir, "daemon.sock"),
		Meta:        filepath.Join(dir, "meta"),
		RepoFile:    filepath.Join(dir, "repo.json"),
		Fingerprint: fp,
	}, nil
}

// repoRecord is the deterministic descriptor written to repo.json. The created
// field is a static placeholder ("-"), NOT a timestamp, so the file content is
// reproducible across runs.
type repoRecord struct {
	AbsRoot     string `json:"abs_root"`
	Fingerprint string `json:"fingerprint"`
	Created     string `json:"created"`
}

// Ensure creates the per-repo state directories with owner-only permissions and
// writes repo.json (0600) if it is absent. Existing state from an older release
// is migrated to those owner-only modes on every call, including the idempotent
// repo.json-present path. It never rewrites an existing repo.json, so its
// deterministic content is preserved.
func Ensure(p Paths) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("state: mkdir dir: %w", err)
	}
	// MkdirAll intentionally leaves an existing directory's mode untouched.
	// Tighten it explicitly so a state tree created by an older version (or a
	// permissive umask/manual setup) does not remain group/world-readable.
	if err := os.Chmod(p.Dir, 0o700); err != nil {
		return fmt.Errorf("state: chmod dir: %w", err)
	}
	if err := os.MkdirAll(p.Meta, 0o700); err != nil {
		return fmt.Errorf("state: mkdir meta: %w", err)
	}
	if err := os.Chmod(p.Meta, 0o700); err != nil {
		return fmt.Errorf("state: chmod meta: %w", err)
	}
	if _, err := os.Stat(p.RepoFile); err == nil {
		if err := os.Chmod(p.RepoFile, 0o600); err != nil {
			return fmt.Errorf("state: chmod repo.json: %w", err)
		}
		return nil // already present; leave deterministic content untouched
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("state: stat repo.json: %w", err)
	}
	rec := repoRecord{AbsRoot: p.Root, Fingerprint: p.Fingerprint, Created: "-"}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("state: marshal repo.json: %w", err)
	}
	if err := os.WriteFile(p.RepoFile, data, 0o600); err != nil {
		return fmt.Errorf("state: write repo.json: %w", err)
	}
	// WriteFile preserves the mode if another process won the create race.
	// Enforce the privacy contract after the write as well.
	if err := os.Chmod(p.RepoFile, 0o600); err != nil {
		return fmt.Errorf("state: chmod repo.json: %w", err)
	}
	return nil
}

// DiscoverDB returns the durable store path to use. An explicit override wins
// unchanged. Otherwise it resolves the per-repo layout and returns the DB path
// ONLY IF that file already exists; if no state DB exists for cwd it returns
// "" so callers fall back to today's in-memory behavior.
func DiscoverDB(cwd, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	p, err := Resolve(cwd)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p.DB); err != nil {
		return "", nil //nolint:nilerr // absent state DB → fall back, not an error
	}
	return p.DB, nil
}

// DiscoverSocket returns the daemon socket path to use. An explicit override
// wins unchanged; otherwise it returns the per-repo socket path (whether or not
// a daemon is currently listening on it).
func DiscoverSocket(cwd, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	p, err := Resolve(cwd)
	if err != nil {
		return "", err
	}
	return p.Socket, nil
}
