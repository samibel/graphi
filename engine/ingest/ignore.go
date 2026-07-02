package ingest

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/internal/gitignore"
)

// Opt-in scope controls. Ignore scope changes GRAPH CONTENT (which files get
// symbols), so both are off by default and both are folded into the warm-start
// semantics stamp: flipping either forces one certified cold pass instead of
// silently serving a graph indexed under a different scope.
const (
	// EnvRespectGitignore, when set non-empty and != "0", makes the walk (and
	// the watcher's ParseFile) honor the repository ROOT .gitignore — the
	// documented pattern subset in internal/gitignore.
	EnvRespectGitignore = "GRAPHI_RESPECT_GITIGNORE"
	// EnvIgnoreDirs is a comma-separated list of extra directory basenames to
	// prune (case-insensitive), merged with the built-in ignoredDirNames.
	EnvIgnoreDirs = "GRAPHI_IGNORE"
)

// ignoreConfig is the resolved opt-in ignore scope for one repo root.
type ignoreConfig struct {
	matcher *gitignore.Matcher
	extra   map[string]bool // lowercase extra dir basenames
	// fingerprint is non-empty iff any opt-in scope is active; it feeds the
	// warm-start semantics stamp.
	fingerprint string
}

func (c ignoreConfig) active() bool { return c.matcher != nil || len(c.extra) > 0 }

// ignoreDir reports whether a directory (basename name, repo-relative rel)
// falls outside the configured index scope.
func (c ignoreConfig) ignoreDir(name, rel string) bool {
	if c.extra[strings.ToLower(name)] {
		return true
	}
	return c.matcher.Match(rel, true)
}

// ignoreFile reports whether a file at repo-relative rel falls outside the
// configured index scope (matcher only — extra names are directory basenames,
// which the matcher's ancestor rule and the walk's pruning already cover).
func (c ignoreConfig) ignoreFile(rel string) bool {
	if len(c.extra) > 0 {
		for _, comp := range strings.Split(rel, "/") {
			if c.extra[strings.ToLower(comp)] {
				return true
			}
		}
	}
	return c.matcher.Match(rel, false)
}

// loadIgnoreConfig resolves the env-driven scope for root. Reading the root
// .gitignore is best-effort: an unreadable file simply means no matcher (the
// mode still fingerprints, so toggling the env always re-certifies).
func loadIgnoreConfig(root string) ignoreConfig {
	var cfg ignoreConfig
	h := fnv.New64a()
	if raw := os.Getenv(EnvIgnoreDirs); strings.TrimSpace(raw) != "" {
		names := strings.Split(raw, ",")
		var kept []string
		for _, n := range names {
			n = strings.ToLower(strings.TrimSpace(n))
			if n != "" {
				kept = append(kept, n)
			}
		}
		if len(kept) > 0 {
			sort.Strings(kept)
			cfg.extra = make(map[string]bool, len(kept))
			for _, n := range kept {
				cfg.extra[n] = true
			}
			_, _ = io.WriteString(h, "extra:"+strings.Join(kept, ",")+"\n")
		}
	}
	if v := os.Getenv(EnvRespectGitignore); v != "" && v != "0" {
		_, _ = io.WriteString(h, "gitignore:\n")
		if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil { //nolint:gosec // root is the repo being indexed
			cfg.matcher = gitignore.Compile(strings.Split(string(data), "\n"))
			_, _ = h.Write(data)
		}
		if cfg.matcher == nil && len(cfg.extra) == 0 {
			// Mode is on but there is nothing to match — still fingerprint, so
			// adding a .gitignore later invalidates the warm store.
			cfg.extra = map[string]bool{}
		}
	}
	if cfg.matcher != nil || cfg.extra != nil {
		cfg.fingerprint = fmt.Sprintf("%016x", h.Sum64())
	}
	return cfg
}

// ignoreState caches the resolved config per root on the Ingester so the walk,
// DriftSet, and the watcher's ParseFile agree within a process. The env is
// read once per (Ingester, root) — scope changes take effect on the next
// process, matching how the fingerprint re-certification works.
type ignoreState struct {
	mu   sync.Mutex
	root string
	cfg  ignoreConfig
	set  bool
}

func (i *Ingester) ignoreConfigFor(root string) ignoreConfig {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)
	i.ignore.mu.Lock()
	defer i.ignore.mu.Unlock()
	if i.ignore.set && i.ignore.root == abs {
		return i.ignore.cfg
	}
	i.ignore.cfg = loadIgnoreConfig(abs)
	i.ignore.root = abs
	i.ignore.set = true
	return i.ignore.cfg
}

// semanticsStamp is the warm-start certification value: the semantics version,
// plus the ignore-scope fingerprint when an opt-in scope is active. Identical
// sources indexed under a different scope produce a different graph, so the
// stamp must differ.
func (i *Ingester) semanticsStamp(root string) string {
	cfg := i.ignoreConfigFor(root)
	if cfg.fingerprint == "" {
		return ingestSemanticsVersion
	}
	return ingestSemanticsVersion + "+ig:" + cfg.fingerprint
}
