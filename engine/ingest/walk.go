package ingest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ignoredDirNames are directory basenames pruned from every walk (never
// descended into, regardless of depth). These hold dependency trees or VCS
// metadata, not a repo's own code: indexing them is slow, drowns query
// results in third-party noise, and — for pnpm's node_modules/.pnpm layout in
// particular — walks through symlinked package directories that used to abort
// the whole ingest (see SkipUnreadable). The set is deliberately small and
// unambiguous; anything with a plausible legitimate use as real source (e.g.
// "dist", "build", "target") is left alone rather than guessed at.
var ignoredDirNames = map[string]bool{
	"node_modules":     true,
	".git":             true,
	"vendor":           true,
	".venv":            true,
	"venv":             true,
	"__pycache__":      true,
	"bower_components": true,
}

// isIgnoredDirName reports whether name matches ignoredDirNames, case-insensitively
// — node_modules et al. are created by well-known tooling in a fixed case, but
// case-insensitive-but-preserving filesystems (macOS/Windows defaults) don't
// guarantee a checkout can't surface a different casing.
func isIgnoredDirName(name string) bool {
	return ignoredDirNames[strings.ToLower(name)]
}

// pathHasIgnoredDir reports whether any path segment of the slash-separated,
// repo-relative rel is an ignored directory name. walk() prunes ignoredDirNames
// at the directory level via filepath.SkipDir and never descends into them in
// the first place, so a full index never reaches this; ParseFile has no walk to
// prune, so it must check the whole path instead — otherwise the filesystem
// watcher would still read, parse, and record skip diagnostics for every
// changed file under node_modules/.git/vendor/... (which churns constantly
// during a package-manager install), reintroducing exactly the noise and cost
// the walk-time pruning exists to avoid.
func pathHasIgnoredDir(rel string) bool {
	for _, comp := range strings.Split(rel, "/") {
		if isIgnoredDirName(comp) {
			return true
		}
	}
	return false
}

// walk returns all source files under root, sorted deterministically.
// onFile, when non-nil, is invoked with the running count after each file is
// read into the unit list (progress reporting for the full-ingest path only;
// other callers pass nil).
func (i *Ingester) walk(root string, onFile func(discovered int)) ([]fileUnit, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: abs root: %w", err)
	}
	root = filepath.Clean(root)
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: open root: %w", err)
	}
	defer rootHandle.Close()
	// Configured index scope (default-on root .gitignore / GRAPHI_IGNORE): pruned
	// silently, exactly like the built-in ignoredDirNames — scope hygiene is
	// configuration, not a diagnostic.
	scope, err := i.ignoreConfigFor(root)
	if err != nil {
		return nil, err
	}

	var units []fileUnit
	err = fs.WalkDir(rootHandle.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if isIgnoredDirName(d.Name()) {
				return filepath.SkipDir
			}
			if scope.active() {
				if scope.ignoreDir(d.Name(), rel) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return fmt.Errorf("ingest: escaped path %q", rel)
		}
		if scope.active() && scope.ignoreFile(rel) {
			return nil
		}
		read := readRootedRegularFile(rootHandle, rel, i.bounds.MaxFileSize)
		if read.reason != "" {
			i.recordSkip(SkipDiagnostic{Path: rel, Reason: read.reason, Size: read.size})
			return nil
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		units = append(units, fileUnit{
			path:    path,
			relPath: rel,
			src:     read.src,
			hash:    hashBytes(read.src),
		})
		if onFile != nil {
			onFile(len(units))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(units, func(i, j int) bool { return units[i].relPath < units[j].relPath })
	return units, nil
}

// sanitizePath ensures p is inside root and returns a repo-relative POSIX path.
func (i *Ingester) sanitizePath(root, p string) (string, error) {
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", fmt.Errorf("ingest: path outside root: %w", err)
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(p, "..") || strings.Contains(p, "../") {
		return "", fmt.Errorf("ingest: escaped path %q", p)
	}
	return p, nil
}
