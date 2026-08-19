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

	// fileNodesByDir maps a source directory to the committed "file" nodes
	// declared in it, each paired with its normalized source PATH. Precomputed
	// once in BuildIndex so package-file-node lookups (packageFileNodes /
	// clausePackageFileNodes) cost O(files-in-dir) instead of re-scanning every
	// file node in the repo on every package import.
	//
	// THE LIST IS DELIBERATELY UNFILTERED (ADR 0011). It holds every file node in
	// the directory — `.md`, `.yml`, `_test.go` included — because its other two
	// consumers, hasPackage and DirsForImport, have an invariant that permits
	// over- but never under-approximation: under-approximating DirsForImport
	// freezes an edge permanently, which is the ADR 0009 defect class. Package
	// MEMBERSHIP is therefore decided at READ time by the asking resolver, via
	// the packageFileFilter that packageFileNodes / clausePackageFileNodes take —
	// never by narrowing what Add records here.
	fileNodesByDir map[string][]fileNodeRef

	// clauseByDir maps a directory to the package clause its symbols declare,
	// derived from node qualified names (pkg.Symbol). Used to find the directory
	// a selector base's import path resolves into.
	//
	// OPEN DEFECT LINK-002 — it holds ONE clause, and a directory can declare
	// several. Add writes it unconditionally (see the assignment below), so a
	// directory holding, say, `package shop` beside an external `package
	// shop_test` keeps only whichever clause the streaming order wrote last, and
	// every method under the losing clause becomes unreachable from the two
	// readers of this map: Build's methodDirs seed and uniqueMethodInDir. That is
	// the sole gate of receiverMethod, whose sole consumer is Go's recv.Method
	// call heuristic (resolve_go.go).
	//
	// IT IS NOT ONLY A RECALL LOSS. This comment said "no wrong edge is ever
	// emitted" in its first draft and that was FALSE. Where the WINNING clause
	// declares a method of the same bare name, the call is not dropped but
	// REDIRECTED to that unrelated method: hiding a clause manufactures FALSE
	// UNIQUENESS, which defeats receiverMethod's own frozen skip-on-ambiguity
	// rule (it is REQUIRED to abstain when both declarations are visible) and
	// turns a mandated abstention into a confident wrong edge. Reproduced through
	// the CLI — §3.2 of the record — and pinned by
	// TestLink002_RedirectsToWrongDeclaration. The wrong edge is always
	// `heuristic` tier; the stop-ship ruling is consequently REOPENED as an owner
	// question (§9 of the record), not settled here.
	//
	// It also falsifies BuildIndex's "in any order" promise below, and is
	// deterministic in production only because ForEachNode's canonical NodeId
	// order is a content hash — which is why no parity dispatch can see it and
	// why it is pinned hermetically instead (clausebydir_test.go).
	//
	// NOT FIXED HERE: the fix is a product-byte change with its own ADR,
	// candidate move and re-measurement. The direction — hold the SET of clauses
	// and degrade only on a genuine bare-name collision, NOT the plain "degrade
	// on ambiguity" of typeresolve/pkggraph.go, which measurably loses more
	// recall than it recovers — plus the measured blast radius and the verified
	// workaround are recorded in docs/rc/link-002-clause-by-dir-recall.md.
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

	// moduleMap resolves a Go import path to the ONE repo directory its module
	// path declares (PARITY-002 fix, ADR 0009). When non-Empty, Go package-file
	// resolution (packageFileNodes, crossPackage, hasPackage) goes through it
	// instead of the clause union, so an import can never fan out over an
	// unrelated directory that merely shares a package clause. When Empty (a
	// tree with no go.mod), the clause behaviour is kept unchanged — module
	// resolution is an upgrade where module information exists, never a
	// regression where it does not.
	//
	// SCOPE, precisely (review round 2 corrected an overbroad earlier claim):
	// non-Go RESOLUTION never consults it (clausePackageFileNodes and
	// packageNodeByPath are separate paths, so single-pass non-Go bytes are
	// unaffected) — but DirsForImport, which serves the language-blind
	// reverse-dep translation for ALL languages, must return the UNION of the
	// module and clause bases; see its doc comment for the frozen divergence a
	// module-only answer caused there.
	moduleMap *ModuleMap
}

// fileNodeRef is a committed `file` node paired with its normalized POSIX
// repo-relative source path. The index used to record the NodeId alone, which
// discarded the only fact that makes package membership decidable at read time:
// the file's EXTENSION. ADR 0011 (LINK-001) needs it, so fileNodesByDir carries
// it alongside the id.
type fileNodeRef struct {
	id   model.NodeId
	path string
}

// packageFileFilter decides whether a committed file node is a SOURCE file of
// the package an `imports` edge may target (ADR 0011). It is a parameter of
// READING fileNodesByDir, supplied by the resolver that asks — never a property
// of the index — because the same unfiltered list also answers "does the repo
// contain this package?" (hasPackage) and "which directories may this import
// resolve to?" (DirsForImport), where over-approximation is safe and
// under-approximation freezes an edge forever.
//
// A nil filter is NOT "admit everything": see packageFileNodes.
type packageFileFilter func(sourcePath string) bool

// extSetFilter builds a packageFileFilter admitting exactly the given file
// extensions. The extension set is STATIC per language and lives in that
// language's resolver — deliberately not
// obtained from parse.Registry, whose contents are build-tag dependent
// (graphi_broad / CGO) and would therefore make committed graph bytes depend on
// how the binary was built. Precedent: tsExts (resolve_typescript.go).
//
// An EMPTY set admits nothing. That is the fail-closed direction and it is
// intentional: an unknown source-extension set must not license an edge onto a
// README, and a lost edge is a recall defect while a wrong edge is a soundness
// defect. pkgtargets_test.go pins that every resolver populating
// binder.pkgImportPaths also declares a non-empty set, so the fail-closed branch
// cannot silently become the shipped behaviour.
//
// THE COMPARISON IS CASE-INSENSITIVE, matching parse.Registry.ParserFor, which
// lowercases an extension before selecting a parser (core/parse/registry.go
// normalizeExt). That is what decides whether a `file` node exists at all, so a
// `Main.PY` committed by a Windows-authored repository IS an indexed Python
// module; a case-sensitive filter here would have silently dropped its edges —
// a recall regression this change would otherwise have introduced. Declared sets
// are lowercase, pinned by TestExtensionSets_AreWellFormed.
func extSetFilter(exts []string) packageFileFilter {
	return func(sourcePath string) bool {
		ext := strings.ToLower(path.Ext(sourcePath))
		for _, want := range exts {
			if ext == want {
				return true
			}
		}
		return false
	}
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

// IndexBuilder incrementally constructs a SymbolIndex from a streamed node
// set, so a caller iterating the store (graphstore.ForEachNode) never has to
// materialize the whole []model.Node first. Add must see each committed node
// exactly once; Build finalizes the derived tables and returns the index.
// Feeding nodes in the store's canonical NodeId order reproduces BuildIndex
// over a full listing exactly.
type IndexBuilder struct{ idx *SymbolIndex }

// NewIndexBuilder returns an empty builder.
func NewIndexBuilder() *IndexBuilder {
	return &IndexBuilder{idx: &SymbolIndex{
		byDir:             map[string]map[string]model.NodeId{},
		dirAmbiguous:      map[string]map[string]struct{}{},
		byClause:          map[string]map[string]map[string]model.NodeId{},
		fileNodeByPath:    map[string]model.NodeId{},
		fileNodesByDir:    map[string][]fileNodeRef{},
		clauseByDir:       map[string]string{},
		packageNodeByPath: map[string]model.NodeId{},
		methodDirs:        map[string][]string{},
	}}
}

// SetModuleMap attaches the Go module resolution map (PARITY-002 fix). Callers
// that can see the tree's go.mod files (engine/ingest, at every index build
// site) set it; callers that cannot (pure-link unit tests, non-Go paths) leave
// it unset and get the historical clause behaviour. Safe to call at any point
// before the index is queried.
func (b *IndexBuilder) SetModuleMap(m *ModuleMap) { b.idx.moduleMap = m }

// Add indexes one committed node (BuildIndex's per-node step).
func (b *IndexBuilder) Add(n model.Node) {
	idx := b.idx
	sp := n.SourcePath() // already normalized POSIX repo-relative
	dir := posixDir(sp)
	if n.Kind() == packageKind {
		// Interned package node (WP-01): index by its full package path and
		// keep it OUT of the symbol tables so it can never resolve a symbol.
		idx.packageNodeByPath[n.QualifiedName()] = n.ID()
		return
	}
	if n.Kind() == externalKind {
		// Interned external node (WP-03): a linker artifact, never a resolution
		// target. Skipping it keeps drop-point 1/2 deterministic across passes.
		return
	}
	if n.Kind() == fileKind {
		idx.fileNodeByPath[sp] = n.ID()
		// Recorded UNFILTERED, on purpose — see the fileNodesByDir doc comment.
		idx.fileNodesByDir[dir] = append(idx.fileNodesByDir[dir], fileNodeRef{id: n.ID(), path: sp})
		return
	}
	clause, bare := splitQN(n.QualifiedName())
	if bare == "" {
		return
	}
	if clause != "" {
		// LINK-002 (OPEN, disclosed): last write wins. See the clauseByDir field
		// comment and docs/rc/link-002-clause-by-dir-recall.md. Deliberately left
		// as-is — fixing it is a product-byte change with its own ceremony.
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
		// LINK-003 (OPEN, disclosed): last write wins HERE TOO, and unlike byDir
		// above this map has no dirAmbiguous companion — so two methods sharing a
		// bare name in one package shadow each other and uniqueMethodInDir cannot
		// see the collision. Same false-uniqueness mechanism as LINK-002 four lines
		// up, ~5x the surface (663 of 1979 = 33.5 % unreachable-or-shadowed on this
		// tree, against 136 = 6.9 % for LINK-002 alone). See §10 of
		// docs/rc/link-002-clause-by-dir-recall.md and the backlog entry. Deliberately
		// left as-is — fixing it is a product-byte change with its own ceremony, and
		// it must be fixed TOGETHER with LINK-002: a fix that only makes clauseByDir
		// hold a set leaves this one standing (verified — TestLink003_BareNameShadowing
		// stays green under a simulated LINK-002 fix).
		idx.byClause[clause][dir][bare] = n.ID()
	}
}

// Build finalizes the derived tables and returns the index. The builder must
// not be reused after Build.
func (b *IndexBuilder) Build() *SymbolIndex {
	idx := b.idx
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

// BuildIndex constructs a SymbolIndex from a committed node set. It is pure and
// deterministic: identical input (in any order) yields an index that resolves
// identically. Resolution is O(1) per lookup (no caller×candidate scans).
//
// THE "IN ANY ORDER" CLAUSE IS CURRENTLY FALSE, and is left standing as the
// statement the fix must make true again rather than quietly weakened: OPEN
// defect LINK-002 makes clauseByDir order-dependent, so the same node set in two
// orders resolves two different recv.Method edge sets. Demonstrated by
// TestLink002_BuildIndexOrderInvariantBroken. Every other table here — byDir,
// dirAmbiguous, byClause, fileNodeByPath, fileNodesByDir, packageNodeByPath — is
// genuinely order-independent, so the exception is exactly one field wide.
func BuildIndex(nodes []model.Node) *SymbolIndex {
	b := NewIndexBuilder()
	for _, n := range nodes {
		b.Add(n)
	}
	return b.Build()
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

// hasPackage reports whether the repo contains an INTERNAL package for the
// given import path. Module-aware since ADR 0009 review round 1: with a
// moduleMap the answer comes from the ONE directory the module path declares —
// the same basis crossPackage and packageFileNodes resolve on, which is what
// keeps all three consumers of "is this import internal?" telling one story.
// Without a moduleMap (no go.mod) the historical clause basis is kept:
// byClause[path.Base(importPath)] non-empty, erring toward "internal" on a
// clause collision — the safe direction for the external-minting path
// (resolve_go.go drop-point 2), which suppresses a node rather than flooding.
//
// ADR 0011: this reads fileNodesByDir UNFILTERED, and must keep doing so. The
// question is "does the repo contain this package at all?", not "which files are
// its source?". A directory holding only a doc.go the extension filter admits
// and a directory holding a README the filter rejects are both PRESENT; answering
// "absent" for the second would mint an interned external node for an import the
// repo really does own.
func (idx *SymbolIndex) hasPackage(importPath string) bool {
	if importPath == "" {
		return false
	}
	if !idx.moduleMap.Empty() {
		dir, ok := idx.moduleMap.Dir(importPath)
		if !ok {
			return false
		}
		return len(idx.byDir[dir]) > 0 || len(idx.fileNodesByDir[dir]) > 0
	}
	return len(idx.byClause[path.Base(importPath)]) > 0
}

// crossPackage resolves a selector (importPath, name) to a NodeId.
//
// MODULE-AWARE since ADR 0009 review round 1 — this was the review's CONFIRMED
// finding 1, and the reason "fail-closed resolution" was not a good enough
// reason to leave it clause-based: on a full pass a clause collision made the
// lookup AMBIGUOUS, which dropped the intra-repo edge and minted an interned
// external node instead, while the incremental pass — whose directory-local
// cascade never re-linked the importer — kept the OLD intra-repo edge. A
// frozen full-vs-incremental divergence through `calls`, structurally
// identical to the `imports` half of PARITY-002. With the moduleMap the
// lookup consults exactly the ONE directory the import path declares, so the
// collision cannot arise. Without a moduleMap the historical clause union is
// kept: unique hit resolves, zero or ambiguous hits skip deterministically.
//
// The module branch resolves through sameDir, NOT a bare byDir read, on
// purpose: byDir keeps the FIRST-added id when one directory declares a bare
// name twice (build-tag variants make that a legitimate shape), which is
// Add-order-dependent — and the full and incremental passes add nodes in
// different orders. sameDir consults dirAmbiguous and skips those
// deterministically, which is the property BuildIndex promises.
func (idx *SymbolIndex) crossPackage(importPath, name string) (model.NodeId, bool) {
	if !idx.moduleMap.Empty() {
		dir, ok := idx.moduleMap.Dir(importPath)
		if !ok {
			return "", false
		}
		return idx.sameDir(dir, name)
	}
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
// import path resolves to, sorted for determinism. Returns nil when the package
// is not present in the repo (stdlib / 3rd-party) so no phantom imports edge
// forms.
//
// MEMBERSHIP (LINK-001 fix, ADR 0011). `keep` decides which of the resolved
// directory's file nodes are the package's SOURCE files. It is required: a nil
// filter admits nothing, because the whole defect this parameter closes was a
// caller that got the WHOLE directory — `README.md`, `.golangci.yml`,
// `Makefile`, `_test.go` — and emitted an `imports` edge onto each. Failing
// closed on a missing filter turns "someone added a caller and forgot" into a
// visible recall loss rather than a silent return of the defect.
//
// RESOLUTION BASIS (PARITY-002 fix, ADR 0009). With a moduleMap present, the
// import path resolves to exactly ONE directory — the one its module path
// declares — so two directories that merely share a package clause can never
// cross-contaminate an importer's edge set. That collision (`x/json` and
// `y/json` both declaring `package json`) was PARITY-002: the full pass fanned
// the importer out over BOTH directories while the directory-local incremental
// cascade never re-linked it. Without a moduleMap (no go.mod in the tree) the
// historical clause-union behaviour is kept unchanged.
func (idx *SymbolIndex) packageFileNodes(importPath string, keep packageFileFilter) []model.NodeId {
	if keep == nil {
		return nil
	}
	if !idx.moduleMap.Empty() {
		dir, ok := idx.moduleMap.Dir(importPath)
		if !ok {
			return nil // no module in this tree owns the path: external
		}
		refs := idx.fileNodesByDir[dir]
		out := make([]model.NodeId, 0, len(refs))
		for _, ref := range refs {
			if keep(ref.path) {
				out = append(out, ref.id)
			}
		}
		if len(out) == 0 {
			return nil
		}
		sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
		return out
	}
	clause := path.Base(importPath)
	dirs := idx.byClause[clause]
	if dirs == nil {
		return nil
	}
	seen := map[model.NodeId]struct{}{}
	var out []model.NodeId
	for dir := range dirs {
		for _, ref := range idx.fileNodesByDir[dir] {
			if !keep(ref.path) {
				continue
			}
			if _, dup := seen[ref.id]; dup {
				continue
			}
			seen[ref.id] = struct{}{}
			out = append(out, ref.id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// DirsForImport returns the source directories an import path may resolve to,
// sorted for determinism, empty when the package is not present in the repo
// (stdlib / 3rd-party / a stub file-path "import").
//
// Ingest uses this to translate import-path forward refs into the DIRECTORY key
// space so the incremental reverse-dependency cascade (dependentsOf) — which
// keys off the changed file's directory — actually finds cross-package
// importers. Without it, reverse_deps keyed by import-path string is never hit
// by a file-path/directory lookup and the import-dependent cascade is dead.
//
// THE RESULT IS THE UNION OF EVERY EMISSION BASIS, not the single module
// directory (ADR 0009 review round 2, finding 1 — a CONFIRMED blocker). This
// function serves the reverse-dep translation for ALL languages, and its
// targets carry no language: Go emission resolves through the module map, but
// Python/Ruby/JS emission stays clause-based. A module-only answer swallowed
// every non-Go target in any tree containing a go.mod (no module owns "shop"),
// so the caller's dependency record was stored verbatim instead of as a
// directory, the cascade never re-linked it, and the full pass's cross-module
// edge went PERMANENTLY missing from the incremental graph — a new frozen
// divergence of exactly the class ADR 0009 closes, pinned by
// TestLink_Python_MixedTreeWithGoMod_IncrementalParity. The invariant this
// function must uphold is records ⊇ emission dependencies, per language, all
// languages at once: the union covers Go's module basis AND the clause basis
// (the module dir is NOT always inside the clause dirs — a directory's
// declared package clause can differ from its import path's last segment).
// Over-approximation is safe by idempotence: an unnecessary re-link re-emits
// the same bytes; an under-approximation freezes an edge forever.
//
// ADR 0011: this reads fileNodesByDir UNFILTERED, and must keep doing so — the
// invariant in the paragraph above is exactly the reason LINK-001 was NOT fixed
// by filtering at SymbolIndex.Add. This function answers a re-link SCHEDULING
// question ("might a change here require re-linking an importer?"), not an
// emission question, and a directory whose only extension-admitted file is added
// LATER must still be reachable by the cascade today.
func (idx *SymbolIndex) DirsForImport(importPath string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(dir string) {
		if _, dup := seen[dir]; !dup {
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}
	// Clause basis: the emission basis for every non-Go language, and the
	// historical Go basis.
	for dir := range idx.byClause[path.Base(importPath)] {
		add(dir)
	}
	// Module basis: Go's emission basis (ADR 0009) when the tree has a go.mod.
	// The emptiness guard gives the same "not present in the repo" answer the
	// clause basis gives for an absent clause.
	if !idx.moduleMap.Empty() {
		if dir, ok := idx.moduleMap.Dir(importPath); ok {
			if len(idx.fileNodesByDir[dir]) > 0 || len(idx.byDir[dir]) > 0 {
				add(dir)
			}
		}
	}
	if len(out) == 0 {
		return nil
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
