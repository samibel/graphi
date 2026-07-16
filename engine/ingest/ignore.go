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

	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/internal/gitignore"
	"github.com/samibel/graphi/internal/rootfile"
)

// Ignore-scope controls. Ignore scope changes GRAPH CONTENT (which files get
// symbols); since PRIV-01 (SW-119) gitignore respect is ON by default (opt-out
// via "0") while EnvIgnoreDirs stays an opt-in addition. Both are folded into
// the warm-start semantics stamp: flipping either forces one certified cold
// pass instead of silently serving a graph indexed under a different scope.
const (
	// EnvRespectGitignore controls whether the walk (and the watcher's
	// ParseFile) honors the repository ROOT .gitignore — the documented pattern
	// subset in internal/gitignore. Since PRIV-01 (SW-119) this is ON BY
	// DEFAULT: ignored files are exactly where secrets, local configs and
	// credentials live, and indexing them into a persistent, searchable graph
	// violated the privacy default. Set "0" to opt OUT (index ignored files);
	// any other non-empty value keeps the pre-PRIV-01 opt-in spelling working.
	EnvRespectGitignore = "GRAPHI_RESPECT_GITIGNORE"
	// EnvIgnoreDirs is a comma-separated list of extra directory basenames to
	// prune (case-insensitive), merged with the built-in defaultIgnoredDirNames.
	EnvIgnoreDirs = "GRAPHI_IGNORE"
	// EnvIndexAll, when set non-empty and != "0", DISABLES the default-on
	// build-output denylist (defaultIgnoredDirNames) so every directory is
	// indexed. Like the other scope controls it changes graph content, so it is
	// folded into the warm-start stamp.
	EnvIndexAll = "GRAPHI_INDEX_ALL"
	// maxGitignoreSize caps the root privacy policy itself. One MiB is far above
	// normal root .gitignore files while preventing a repository-controlled
	// config path from driving an unbounded allocation before source ingest.
	maxGitignoreSize int64 = 1 << 20
)

// defaultIgnoredDirNames are BUILD-OUTPUT directory basenames pruned by DEFAULT
// (WP-07). A real monorepo checks generator/build output into the tree (a Spring
// repo indexed its `build/` output; a Gradle repo its `.gradle/` cache), which
// bloats the graph with non-source files nobody queries. These names are
// ambiguous — they can occasionally be a real source directory — which is why the
// unconditional never-source list (ignoredDirNames in ingest.go: node_modules,
// .git, vendor, …) deliberately excludes them. WP-07 prunes them by default
// anyway because the monorepo case dominates, but makes it reversible: set
// GRAPHI_INDEX_ALL to index them. (node_modules et al. stay pruned regardless —
// they are never source.)
var defaultIgnoredDirNames = []string{"target", "build", ".gradle", "dist"}

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

// loadIgnoreConfig resolves the env-driven scope for root. The default-on root
// .gitignore boundary is fail-closed: an unreadable file or invalid supported
// pattern returns an error instead of silently widening the persisted index.
// A missing root .gitignore is valid, and the explicit "0" opt-out intentionally
// bypasses reading and parsing it. Nested .gitignore files remain unsupported.
func loadIgnoreConfig(root string) (ignoreConfig, error) {
	var cfg ignoreConfig
	h := fnv.New64a()
	extra := map[string]bool{}

	// WP-07: seed the default-on build-output denylist unless opted out. This
	// changes DEFAULT graph content, so the choice is hashed into the warm-start
	// stamp (and the base ingestSemanticsVersion was bumped when it landed) — a
	// store indexed with the denylist never warm-starts as an index-all store.
	if v := os.Getenv(EnvIndexAll); v != "" && v != "0" {
		_, _ = io.WriteString(h, "indexall:\n")
	} else {
		for _, n := range defaultIgnoredDirNames {
			extra[n] = true
		}
		_, _ = io.WriteString(h, "defaults:"+strings.Join(defaultIgnoredDirNames, ",")+"\n")
	}

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
			for _, n := range kept {
				extra[n] = true
			}
			_, _ = io.WriteString(h, "extra:"+strings.Join(kept, ",")+"\n")
		}
	}
	if len(extra) > 0 {
		cfg.extra = extra
	}
	// PRIV-01 (SW-119): the root .gitignore is respected BY DEFAULT — ignored
	// files are where secrets live, and they must not land in the persistent
	// graph or its search index. "0" opts out explicitly. Fingerprinting is
	// asymmetric on purpose: a repo WITHOUT a .gitignore hashes nothing here,
	// so its pre-PRIV-01 warm stamp stays valid (its scope genuinely did not
	// change); a repo WITH one hashes the pattern bytes (default) or the
	// explicit opt-out marker, so flipping either state re-certifies with one
	// cold pass instead of silently serving a differently-scoped graph.
	if v := os.Getenv(EnvRespectGitignore); v == "0" {
		_, _ = io.WriteString(h, "nogitignore:\n")
		if cfg.extra == nil {
			// Opt-out active with no other scope: still fingerprint, so the
			// opt-out itself invalidates a default-scoped warm store.
			cfg.extra = map[string]bool{}
		}
	} else {
		gitignorePath := filepath.Join(root, ".gitignore")
		data, err := rootfile.Read(root, ".gitignore", maxGitignoreSize)
		if os.IsNotExist(err) {
			// A repository is not required to have a root .gitignore.
		} else if err != nil {
			return ignoreConfig{}, fmt.Errorf("ingest: read root .gitignore %q: %w", gitignorePath, err)
		} else {
			cfg.matcher, err = gitignore.Compile(strings.Split(string(data), "\n"))
			if err != nil {
				return ignoreConfig{}, fmt.Errorf("ingest: parse root .gitignore %q: %w", gitignorePath, err)
			}
			_, _ = io.WriteString(h, "gitignore:\n")
			_, _ = h.Write(data)
		}
	}
	if cfg.matcher != nil || cfg.extra != nil {
		cfg.fingerprint = fmt.Sprintf("%016x", h.Sum64())
	}
	return cfg, nil
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

func (i *Ingester) ignoreConfigFor(root string) (ignoreConfig, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return ignoreConfig{}, fmt.Errorf("ingest: abs ignore root: %w", err)
	}
	abs = filepath.Clean(abs)
	i.ignore.mu.Lock()
	defer i.ignore.mu.Unlock()
	if i.ignore.set && i.ignore.root == abs {
		return i.ignore.cfg, nil
	}
	cfg, err := loadIgnoreConfig(abs)
	if err != nil {
		return ignoreConfig{}, err
	}
	i.ignore.cfg = cfg
	i.ignore.root = abs
	i.ignore.set = true
	return i.ignore.cfg, nil
}

// semanticsStamp is the warm-start certification value: the semantics version,
// plus the ignore-scope fingerprint when an opt-in scope is active, plus the
// per-project taint-config fingerprint when a <root>/.graphi/taint.json is
// present (WP-09). Identical sources indexed under a different scope OR a
// different taint config produce different persisted state, so the stamp must
// differ; a repo with neither an opt-in scope nor a taint config keeps the bare
// version stamp exactly as before.
func (i *Ingester) semanticsStamp(root string) (string, error) {
	stamp := ingestSemanticsVersion
	cfg, err := i.ignoreConfigFor(root)
	if err != nil {
		return "", err
	}
	if cfg.fingerprint != "" {
		stamp += "+ig:" + cfg.fingerprint
	}
	taintConfig, err := taint.LoadConfig(root)
	if err != nil {
		return "", err
	}
	if taintConfig.ContentHash != "" {
		stamp += "+taint:" + taintConfig.ContentHash
	}
	return stamp, nil
}
