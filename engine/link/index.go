package link

import (
	"path"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// SymbolIndex is the store-free lookup the linker resolves against. It is built
// once per link pass from a []model.Node slice (ingest passes store.Nodes) and
// holds NO graphstore reference, keeping engine/link pure.
//
// Open Q1 (frozen): the same-package unit is the DIRECTORY, not the Go package
// clause string — two `package util` directories are distinct packages, so a
// same-package bare-name lookup is scoped to the caller's own directory and can
// never produce a cross-directory phantom edge.
type SymbolIndex struct {
	// byDir maps a source directory to its declared symbols (bareName → NodeId).
	// This is the same-package (directory) resolution table. A bare name that
	// collides within a directory is dropped (ambiguous, skipped deterministically).
	byDir map[string]map[string]model.NodeId
	// dirAmbiguous records (dir,bareName) pairs seen more than once so the
	// resolver can skip them deterministically instead of picking arbitrarily.
	dirAmbiguous map[string]map[string]struct{}

	// byClause maps a package-clause name to the set of directories declaring it,
	// then each directory's bareName → NodeId table. Cross-package selector
	// resolution maps an import path to a package clause (its last path segment)
	// and looks the symbol up here.
	byClause map[string]map[string]map[string]model.NodeId

	// fileNodeByPath maps a normalized file source path to its "file" node id,
	// so the linker can emit file→file imports edges against committed file nodes.
	fileNodeByPath map[string]model.NodeId

	// fileNodesByDir maps a source directory to the committed "file" node ids
	// declared in it. Precomputed once in BuildIndex so package-file-node lookups
	// (packageFileNodes / clausePackageFileNodes) cost O(files-in-dir) instead of
	// re-scanning every file node in the repo on every package import.
	fileNodesByDir map[string][]model.NodeId

	// clauseByDir maps a directory to the package clause its symbols declare,
	// derived from node qualified names (pkg.Symbol). Used to find the directory
	// a selector base's import path resolves into.
	clauseByDir map[string]string

	// packageNodeByPath maps a full package path (e.g. "com.example.service") to
	// its interned "package" node id (WP-01). FQN/package-header languages (Java,
	// Kotlin) mint one such node per declared package; the resolver emits a single
	// file→package `imports` edge to it in place of the file→file import fan-out.
	// Package nodes are recorded here ONLY — they are deliberately kept out of
	// byDir/byClause/clauseByDir so they never pollute symbol resolution.
	packageNodeByPath map[string]model.NodeId

	// methodDirs is the receiverMethod reverse index (WP-02): a method's bare
	// name → the directories whose same-clause table declares that bare name
	// (i.e. exactly the dirs for which uniqueMethodInDir can succeed). It lets
	// receiverMethod consult only the candidate dirs for a given method instead
	// of scanning every directory in byDir per unresolved recv.method call — an
	// O(dirs)→O(candidates) win that changes NO resolution semantics.
	methodDirs map[string][]string
}

// fileKind / the qualified-name shape are mirrored from the Go extractor:
// a "file" node's QualifiedName is its source path; a symbol node's
// QualifiedName is "<pkgClause>.<name>" (methods: "<pkgClause>.<recv>.<name>").
const fileKind = "file"

// packageKind is the interned package-node kind (WP-01). Java/Kotlin parsers mint
// one package node per declared package, keyed by full package path; BuildIndex
// routes them to packageNodeByPath and the resolver links a single file→package
// `imports` edge to them.
const packageKind = "package"

// externalKind is the interned external-symbol node kind (WP-03). The Go resolver
// mints these for unresolved stdlib / 3rd-party call/reference targets. Like
// package nodes they are kept OUT of every symbol table so they can NEVER resolve
// a reference: a committed external node must not let a later pass "resolve"
// os.ReadFile to the external node itself (which would diverge from a full pass
// and make drop-point 1 non-deterministic). BuildIndex simply skips them.
const externalKind = "external"

// BuildIndex constructs a SymbolIndex from a committed node set. It is pure and
// deterministic: identical input (in any order) yields an index that resolves
// identically. Resolution is O(1) per lookup (no caller×candidate scans).
func BuildIndex(nodes []model.Node) *SymbolIndex {
	idx := &SymbolIndex{
		byDir:             map[string]map[string]model.NodeId{},
		dirAmbiguous:      map[string]map[string]struct{}{},
		byClause:          map[string]map[string]map[string]model.NodeId{},
		fileNodeByPath:    map[string]model.NodeId{},
		fileNodesByDir:    map[string][]model.NodeId{},
		clauseByDir:       map[string]string{},
		packageNodeByPath: map[string]model.NodeId{},
		methodDirs:        map[string][]string{},
	}
	for _, n := range nodes {
		sp := n.SourcePath() // already normalized POSIX repo-relative
		dir := posixDir(sp)
		if n.Kind() == packageKind {
			// Interned package node (WP-01): index by its full package path and
			// keep it OUT of the symbol tables so it can never resolve a symbol.
			idx.packageNodeByPath[n.QualifiedName()] = n.ID()
			continue
		}
		if n.Kind() == externalKind {
			// Interned external node (WP-03): a linker artifact, never a resolution
			// target. Skipping it keeps drop-point 1/2 deterministic across passes.
			continue
		}
		if n.Kind() == fileKind {
			idx.fileNodeByPath[sp] = n.ID()
			idx.fileNodesByDir[dir] = append(idx.fileNodesByDir[dir], n.ID())
			continue
		}
		clause, bare := splitQN(n.QualifiedName())
		if bare == "" {
			continue
		}
		if clause != "" {
			idx.clauseByDir[dir] = clause
		}

		// Same-package (directory) table with ambiguity tracking.
		if idx.byDir[dir] == nil {
			idx.byDir[dir] = map[string]model.NodeId{}
		}
		if existing, ok := idx.byDir[dir][bare]; ok && existing != n.ID() {
			if idx.dirAmbiguous[dir] == nil {
				idx.dirAmbiguous[dir] = map[string]struct{}{}
			}
			idx.dirAmbiguous[dir][bare] = struct{}{}
		} else if !ok {
			idx.byDir[dir][bare] = n.ID()
		}

		// Package-clause table for cross-package resolution.
		if clause != "" {
			if idx.byClause[clause] == nil {
				idx.byClause[clause] = map[string]map[string]model.NodeId{}
			}
			if idx.byClause[clause][dir] == nil {
				idx.byClause[clause][dir] = map[string]model.NodeId{}
			}
			idx.byClause[clause][dir][bare] = n.ID()
		}
	}

	// Build the receiverMethod reverse index (WP-02). A dir participates in
	// uniqueMethodInDir only through byClause[clauseByDir[dir]][dir], so index
	// exactly those (dir, bareName) pairs. This is the SAME predicate
	// uniqueMethodInDir tests, so receiverMethod's candidate set — and thus its
	// resolved edge set — is byte-identical to the old full-byDir scan.
	for dir, clause := range idx.clauseByDir {
		tbl := idx.byClause[clause][dir]
		if tbl == nil {
			continue
		}
		for bare := range tbl {
			idx.methodDirs[bare] = append(idx.methodDirs[bare], dir)
		}
	}
	return idx
}

// sameDir resolves a bare name within the caller's own directory (same-package).
// It returns ok=false on a miss OR on a deterministically-skipped ambiguity.
func (idx *SymbolIndex) sameDir(dir, name string) (model.NodeId, bool) {
	if amb := idx.dirAmbiguous[dir]; amb != nil {
		if _, bad := amb[name]; bad {
			return "", false
		}
	}
	tbl := idx.byDir[dir]
	if tbl == nil {
		return "", false
	}
	id, ok := tbl[name]
	return id, ok
}

// hasPackage reports whether the repo contains an INTERNAL package for the given
// import path, using the SAME clause (last-path-segment) basis crossPackage and
// packageFileNodes resolve on: byClause[path.Base(importPath)] is non-empty. The
// receiver-type external-minting path (resolve_go.go drop-point 2) uses this to
// avoid materializing an external node for a receiver whose type belongs to a
// package committed in the repo — an internal method call is resolved (or honestly
// skipped) through the normal paths, never minted as `external`. Erring toward
// "internal" on a clause collision (e.g. a repo package sharing a stdlib clause)
// is the SAFE direction: it suppresses an external node rather than flooding.
func (idx *SymbolIndex) hasPackage(importPath string) bool {
	if importPath == "" {
		return false
	}
	return len(idx.byClause[path.Base(importPath)]) > 0
}

// crossPackage resolves a selector (importPath, name) to a NodeId. The import
// path maps to a package clause via its last segment; the symbol is then looked
// up in every directory declaring that clause. A unique hit resolves; zero or
// ambiguous (>1 distinct NodeId) hits are skipped deterministically.
func (idx *SymbolIndex) crossPackage(importPath, name string) (model.NodeId, bool) {
	clause := path.Base(importPath)
	dirs := idx.byClause[clause]
	if dirs == nil {
		return "", false
	}
	var found model.NodeId
	count := 0
	for _, tbl := range dirs {
		id, ok := tbl[name]
		if !ok {
			continue
		}
		if count == 0 {
			found, count = id, 1
		} else if id != found {
			return "", false // ambiguous across directories declaring this clause
		}
	}
	if count == 1 {
		return found, true
	}
	return "", false
}

// receiverMethod resolves a recv.Method selector heuristically: it looks for the
// method's bare name across all directories. Open Q3 (frozen): resolve ONLY on a
// unique receiver-name match; skip deterministically on ambiguity (>1 distinct
// NodeId) or a miss. preferDir is the caller's directory, tried first so a
// same-package method wins unambiguously.
func (idx *SymbolIndex) receiverMethod(preferDir, recv, method string) (model.NodeId, bool) {
	// Method nodes carry QN "<clause>.<recv>.<method>"; the index stores them by
	// their bare LAST segment (method) AND we disambiguate by receiver via QN.
	// First try the caller's own directory.
	if id, ok := idx.uniqueMethodInDir(preferDir, recv, method); ok {
		return id, true
	}
	// Then search globally for a unique (recv, method) match. WP-02: consult the
	// methodDirs reverse index — only the dirs that actually declare this bare
	// method name — instead of scanning every directory in byDir. methodDirs is
	// built from the same predicate uniqueMethodInDir tests, so the candidate set
	// (and the collected distinct-NodeId set, and thus the resolved/ambiguous
	// outcome) is identical to the old full scan; only the cost changes.
	var found model.NodeId
	count := 0
	for _, dir := range idx.methodDirs[method] {
		if id, ok := idx.uniqueMethodInDir(dir, recv, method); ok {
			if count == 0 {
				found, count = id, 1
			} else if id != found {
				return "", false
			}
		}
	}
	if count == 1 {
		return found, true
	}
	return "", false
}

// uniqueMethodInDir finds a method node "<clause>.<recv>.<method>" in dir.
func (idx *SymbolIndex) uniqueMethodInDir(dir, recv, method string) (model.NodeId, bool) {
	clause := idx.clauseByDir[dir]
	if clause == "" {
		return "", false
	}
	tbl := idx.byClause[clause][dir]
	if tbl == nil {
		return "", false
	}
	id, ok := tbl[method]
	if !ok {
		return "", false
	}
	// Confirm the stored node really is "<clause>.<recv>.<method>" by checking
	// the recv segment is present for THIS method. The byClause table keys on the
	// bare last segment, so a free function "<clause>.<method>" would also match;
	// require the receiver to be non-empty to treat it as a method.
	if recv == "" {
		return "", false
	}
	return id, ok
}

// fileNode returns the committed "file" node id for a normalized source path.
func (idx *SymbolIndex) fileNode(sourcePath string) (model.NodeId, bool) {
	id, ok := idx.fileNodeByPath[sourcePath]
	return id, ok
}

// packageFileNodes returns the committed "file" node ids of the package an
// import path resolves to (clause = last path segment), sorted for determinism.
// A package may span multiple directories declaring the same clause; FU-1 links
// the importing file to every such file node. Returns nil when the package is
// not present in the repo (stdlib / 3rd-party) so no phantom imports edge forms.
func (idx *SymbolIndex) packageFileNodes(importPath string) []model.NodeId {
	clause := path.Base(importPath)
	dirs := idx.byClause[clause]
	if dirs == nil {
		return nil
	}
	seen := map[model.NodeId]struct{}{}
	var out []model.NodeId
	for dir := range dirs {
		for _, id := range idx.fileNodesByDir[dir] {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// DirsForImport returns the source directories that an import path resolves to:
// every directory whose package clause equals the import path's last segment
// (the same clause = path.Base(importPath) mapping crossPackage/packageFileNodes
// use). The result is sorted for determinism and is empty when the package is
// not present in the repo (stdlib / 3rd-party / a stub file-path "import").
//
// Ingest uses this to translate import-path forward refs into the DIRECTORY key
// space so the incremental reverse-dependency cascade (dependentsOf) — which
// keys off the changed file's directory — actually finds cross-package
// importers. Without it, reverse_deps keyed by import-path string is never hit
// by a file-path/directory lookup and the import-dependent cascade is dead.
func (idx *SymbolIndex) DirsForImport(importPath string) []string {
	clause := path.Base(importPath)
	dirs := idx.byClause[clause]
	if dirs == nil {
		return nil
	}
	out := make([]string, 0, len(dirs))
	for dir := range dirs {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// posixDir returns the directory portion of a normalized POSIX path. The root
// (no separator) maps to "" so files in the repo root share one directory key.
func posixDir(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

// splitQN splits a Go qualified name into its package-clause prefix and bare
// trailing name. "shop.checkout" → ("shop","checkout"); "shop.Cart.Add" →
// ("shop","Add") (the bare last segment is the lookup key; the receiver lives in
// the middle segment). A name with no dot yields ("", name).
func splitQN(qn string) (clause, bare string) {
	i := strings.IndexByte(qn, '.')
	if i < 0 {
		return "", qn
	}
	clause = qn[:i]
	rest := qn[i+1:]
	if j := strings.LastIndexByte(rest, '.'); j >= 0 {
		bare = rest[j+1:]
	} else {
		bare = rest
	}
	return clause, bare
}
