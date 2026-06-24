package link

import (
	"path"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// lastSegment returns the trailing segment of a separator-delimited module path
// ("crate::shop::price" with "::" → "price"; "com.shop.Price" with "." → "Price").
func lastSegment(p, sep string) string {
	if i := strings.LastIndex(p, sep); i >= 0 {
		return p[i+len(sep):]
	}
	return p
}

// packageSegment returns the SECOND-to-last segment of a separator-delimited path
// — the package/module a FQN's trailing symbol lives in ("com.shop.Price" with "."
// → "shop"). With fewer than two segments it returns the whole string.
func packageSegment(p, sep string) string {
	segs := strings.Split(p, sep)
	if len(segs) >= 2 {
		return segs[len(segs)-2]
	}
	return p
}

// joinRel cleans a repo-relative file path from an importing directory and a
// relative/local specifier ("app" + "util.h" → "app/util.h"; "a/b" + "../u.sh" →
// "a/u.sh"). The repo root is "".
func joinRel(dir, spec string) string {
	j := path.Clean(path.Join(dir, spec))
	if j == "." {
		return ""
	}
	return j
}

// This file is the shared, pure cross-file resolution core the FU-5 per-language
// resolvers (resolve_typescript.go, resolve_python.go, …) build on. Every non-Go
// extractor uses the SAME cstWalk conventions (parser_tswalk.go): a symbol's
// qualified name is "<dirBase>.<bare>" (langPackage = parent-directory base), and
// unresolved call/selector sites are recorded as parse.PendingRef with the same
// shape as Go. So the resolution machinery — same-directory derived resolution,
// cross-file/cross-module heuristic resolution, file→file imports edges, sorted
// evidence, and deterministic skip+count — is identical across languages; only the
// import BINDING (how a name/qualifier maps to a target directory or module path)
// differs. Each resolve_<lang>.go supplies a binder; this core does the rest.
//
// Invariants preserved here (mirrors resolve_go.go):
//   - Tier honesty: the resolution class (classSamePackage / classSelector) is the
//     SOLE tier input via tierFor; no resolver hardcodes a tier or emits confirmed.
//   - No fabrication: every emitted intent's `to` comes from a committed-node
//     lookup (sameDir / crossPackage / fileNode). Unresolved or ambiguous refs are
//     dropped and counted (st.Skipped / st.Ambiguous), never guessed.
//   - Purity / D3: cross-file lookups reuse the existing directory-keyed byDir via
//     sameDir on a COMPUTED target directory; the SymbolIndex is never edited.
//   - Determinism: candidate directories are deduped and the resolution is
//     order-independent; the Linker constructs each logical edge exactly once.

// binder is a language's cross-file binding strategy, built once per file from its
// imports. It is the only per-language input to resolveRefs.
type binder struct {
	// selBaseImportPath maps a selector base that is a module/package alias to an
	// import path resolved cross-module via SymbolIndex.crossPackage (clause =
	// path.Base(importPath)). Used by clause/FQN languages (Python `import pkg`,
	// Java imports) where the qualifier names a module, not a directory.
	selBaseImportPath map[string]string

	// selBaseDirs maps a selector base that is a namespace/relative-module alias to
	// candidate target directories, looked up via byDir (sameDir). Used by
	// relative-path languages (TS `import * as ns from "./m"`).
	selBaseDirs map[string][]string

	// bareNameDirs maps a bare imported binding (a name pulled into file scope via a
	// RELATIVE specifier, e.g. TS `import {X} from "./m"`) to candidate target
	// directories. A bare use of such a name resolves cross-file via byDir.
	bareNameDirs map[string][]string

	// bareNameImportPath maps a bare imported binding pulled in via a MODULE PATH
	// (e.g. Python `from pkg.sub import X`) to that import path, resolved
	// cross-module via SymbolIndex.crossPackage (clause = path.Base).
	bareNameImportPath map[string]string

	// importFileTargets are candidate target file source paths (with extension) for
	// file→file `imports` edges; only those present as committed file nodes emit an
	// edge (no phantom). Relative-path resolvers expand a module specifier into its
	// extension / index-file candidates here.
	importFileTargets []string

	// pkgImportPaths are module import paths whose package file nodes this file
	// imports, for clause-keyed languages (Python). Each resolves to committed file
	// nodes via the package clause → file→file `imports` edges (no phantom when the
	// package is not in the repo).
	pkgImportPaths []string

	// clauseOf derives the package-clause lookup key from an import path. The
	// SymbolIndex keys symbols by "<dirBase>.<bare>", so the clause is the import
	// path's trailing module segment. Languages differ in the separator: Go-style
	// "/"-paths default to path.Base; Python-style "."-paths set this to the last
	// dot segment; FQN languages (Java) to the SECOND-to-last (the package). nil ⇒
	// path.Base (the Go/filesystem default).
	clauseOf func(importPath string) string

	// selBaseAsClause, when true, treats an otherwise-unresolved selector base as a
	// module/clause name itself and tries crossModule(clause(base), name). Used by
	// languages whose qualifier IS a module path (Rust `mod::fn`).
	selBaseAsClause bool

	// ambientClauses are package clauses brought into scope by a wildcard/namespace
	// import (C# `using Shop`); an unresolved bare/selector name is tried against
	// each (a unique committed hit resolves; >1 distinct → ambiguous).
	ambientClauses []string

	// ambientDirs are directories brought into scope by a local include/require
	// (C `#include "h"`, PHP `require`, Bash `source ./x`); an unresolved bare name
	// is tried in each via byDir (unique hit resolves; >1 distinct → ambiguous).
	ambientDirs []string
}

// clause returns the package-clause lookup key for an import path under this binder.
func (b binder) clause(importPath string) string {
	if b.clauseOf != nil {
		return b.clauseOf(importPath)
	}
	return path.Base(importPath)
}

// resolveRefs is the shared resolution loop. It mirrors resolve_go.go's structure
// but takes a language binder for cross-file resolution.
func resolveRefs(in FileRefs, idx *SymbolIndex, st *Stats, b binder) []intent {
	var out []intent

	for _, p := range in.Pending {
		from, ok := idx.sameDir(in.Dir, lastSeg(p.FromQN))
		if !ok {
			// Owning symbol not indexed (should not happen for committed files);
			// skip without fabricating an endpoint.
			st.Skipped++
			continue
		}
		ev := evidenceFor(in.SourcePath, p.Line)

		// Bare name: same-directory (derived) wins first — a name defined in the
		// caller's own directory is the strongest, same-package claim.
		if !p.Selector {
			if to, ok := idx.sameDir(in.Dir, p.Name); ok {
				if to == from && p.Kind == edgeReferences {
					st.Skipped++ // self-reference by bare name is not a real edge
					continue
				}
				out = append(out, intent{
					from: from, to: to, kind: p.Kind, class: classSamePackage,
					reason:   "same-package cross-file " + p.Kind + " resolved by name within the directory",
					evidence: ev,
				})
				st.ResolvedDerived++
				continue
			}
		}

		// Cross-file / cross-module heuristic resolution (imported bare bindings and
		// selector qualifiers), tried in honest order. Ambiguity short-circuits to a
		// counted skip rather than guessing a single target.
		to, found, ambiguous, reason := resolveCrossFile(idx, b, p.SelectorBase, p.Name, p.Selector, p.Kind)
		if found {
			out = append(out, intent{
				from: from, to: to, kind: p.Kind, class: classSelector,
				reason: reason, evidence: ev,
			})
			st.ResolvedHeuristic++
			continue
		}
		if ambiguous {
			st.Ambiguous++
			continue
		}
		st.Skipped++
	}

	// File→file imports edges: link this file node to each committed target file
	// node. Targets are language-expanded candidate paths; only committed nodes
	// emit an edge (no phantom).
	if fileID, ok := idx.fileNode(in.SourcePath); ok {
		emitted := map[model.NodeId]struct{}{}
		emit := func(tID model.NodeId, reason string) {
			if tID == fileID {
				return
			}
			if _, dup := emitted[tID]; dup {
				return
			}
			emitted[tID] = struct{}{}
			out = append(out, intent{
				from: fileID, to: tID, kind: edgeImports, class: classSelector,
				reason:   reason,
				evidence: evidenceFor(in.SourcePath, 1),
			})
			st.ResolvedHeuristic++
		}
		// Relative-module file candidates (TS): explicit target file paths.
		for _, target := range b.importFileTargets {
			if tID, ok := idx.fileNode(model.NormalizePath(target)); ok {
				emit(tID, "file imports "+target)
			}
		}
		// Clause-keyed package imports (Python): every committed file node of the
		// imported package, keyed by the language-derived clause.
		for _, ip := range b.pkgImportPaths {
			for _, tID := range clausePackageFileNodes(idx, b.clause(ip)) {
				emit(tID, "file imports package "+ip)
			}
		}
	}

	return out
}

// requireBinder builds the binder for script languages that pull in another file by
// a `require`/`require_relative`/`source` specifier resolved relative to the
// including file's directory (Ruby, PHP, Lua, Bash). Each specifier contributes a
// file→file imports edge target (its committed node) and an ambient directory in
// which bare/selector names are resolved. A specifier without an extension is
// expanded against exts; an absolute / search-path require that resolves to no
// committed node is skipped (no phantom).
func requireBinder(in FileRefs, exts []string) binder {
	b := binder{}
	seenDir := map[string]struct{}{}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		base := joinRel(in.Dir, imp.Path)
		if path.Ext(imp.Path) != "" {
			b.importFileTargets = append(b.importFileTargets, base)
		} else {
			for _, ext := range exts {
				b.importFileTargets = append(b.importFileTargets, base+ext)
			}
		}
		dir := posixDir(base)
		if _, dup := seenDir[dir]; !dup {
			seenDir[dir] = struct{}{}
			b.ambientDirs = append(b.ambientDirs, dir)
		}
	}
	return b
}

// resolveCrossFile tries every cross-file / cross-module mechanism the binder
// declares, in honest priority order, and returns the first UNIQUE committed match
// (heuristic tier). Ambiguity at any step short-circuits to (_, false, true, _) so
// the caller counts st.Ambiguous instead of guessing; a definitive miss returns
// (_, false, false, _). It never fabricates a target and never edits the index.
func resolveCrossFile(idx *SymbolIndex, b binder, base, name string, selector bool, kind string) (model.NodeId, bool, bool, string) {
	if !selector {
		if dirs, ok := b.bareNameDirs[name]; ok {
			if id, f, a := lookupInDirs(idx, dirs, name); f {
				return id, true, false, "cross-file " + kind + " resolved via imported binding " + name
			} else if a {
				return "", false, true, ""
			}
		}
		if impPath, ok := b.bareNameImportPath[name]; ok {
			if id, f, a := crossModule(idx, b.clause(impPath), name); f {
				return id, true, false, "cross-module " + kind + " resolved via import " + impPath + " (binding " + name + ")"
			} else if a {
				return "", false, true, ""
			}
		}
	} else {
		if impPath, ok := b.selBaseImportPath[base]; ok {
			if id, f, a := crossModule(idx, b.clause(impPath), name); f {
				return id, true, false, "cross-module " + kind + " resolved via import " + impPath + " (qualifier " + base + ")"
			} else if a {
				return "", false, true, ""
			}
		}
		if dirs, ok := b.selBaseDirs[base]; ok {
			if id, f, a := lookupInDirs(idx, dirs, name); f {
				return id, true, false, "cross-file " + kind + " resolved via namespace import " + base
			} else if a {
				return "", false, true, ""
			}
		}
		if b.selBaseAsClause {
			if id, f, a := crossModule(idx, b.clause(base), name); f {
				return id, true, false, "cross-module " + kind + " resolved via module path " + base
			} else if a {
				return "", false, true, ""
			}
		}
	}
	// Ambient fallbacks apply to both bare and selector references.
	if len(b.ambientDirs) > 0 {
		if id, f, a := lookupInDirs(idx, b.ambientDirs, name); f {
			return id, true, false, "cross-file " + kind + " resolved via a local include/require directory"
		} else if a {
			return "", false, true, ""
		}
	}
	if len(b.ambientClauses) > 0 {
		if id, f, a := lookupAcrossClauses(idx, b.ambientClauses, name); f {
			return id, true, false, "cross-module " + kind + " resolved via an imported namespace"
		} else if a {
			return "", false, true, ""
		}
	}
	return "", false, false, ""
}

// lookupAcrossClauses resolves a bare name across candidate package clauses via
// crossModule. A unique committed match across all clauses resolves; zero matches
// return (_, false, false); ambiguity within one clause OR >1 distinct match across
// clauses returns (_, false, true) so the caller counts it deterministically.
func lookupAcrossClauses(idx *SymbolIndex, clauses []string, name string) (model.NodeId, bool, bool) {
	var found model.NodeId
	count := 0
	for _, c := range dedupeSorted(clauses) {
		id, f, amb := crossModule(idx, c, name)
		if amb {
			return "", false, true
		}
		if f {
			if count == 0 {
				found, count = id, 1
			} else if id != found {
				return "", false, true
			}
		}
	}
	if count == 1 {
		return found, true, false
	}
	return "", false, false
}

// crossModule resolves a (clause, name) selector against the package-clause table,
// distinguishing a MISS from an AMBIGUITY (which SymbolIndex.crossPackage collapses
// into a single false). The clause is the import path's trailing module segment
// (the caller derives it per language via binder.clause). A unique committed match
// resolves, zero matches return (_, false, false), and >1 DISTINCT matches return
// (_, false, true) so clause-keyed resolvers (Python, Java, …) can count
// st.Ambiguous honestly instead of folding it into st.Skipped. It reads the index's
// directory-keyed tables directly (same package) and never edits the index (D3) nor
// fabricates a target.
func crossModule(idx *SymbolIndex, clause, name string) (model.NodeId, bool, bool) {
	dirs := idx.byClause[clause]
	if dirs == nil {
		return "", false, false
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
			return "", false, true // ambiguous across directories declaring this clause
		}
	}
	if count == 1 {
		return found, true, false
	}
	return "", false, false
}

// clausePackageFileNodes returns the committed "file" node ids of every directory
// declaring the given package clause, sorted for determinism. It mirrors
// SymbolIndex.packageFileNodes but takes a pre-derived clause (so dot-separated
// module paths resolve correctly) and reads the index tables directly (same
// package, no index edit — D3). Returns nil when the clause is not in the repo so
// no phantom imports edge forms.
func clausePackageFileNodes(idx *SymbolIndex, clause string) []model.NodeId {
	dirs := idx.byClause[clause]
	if dirs == nil {
		return nil
	}
	seen := map[model.NodeId]struct{}{}
	var out []model.NodeId
	for dir := range dirs {
		for sp, id := range idx.fileNodeByPath {
			if posixDir(sp) != dir {
				continue
			}
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

// lookupInDirs resolves a bare name across candidate directories via the
// directory-keyed byDir table (sameDir). A unique committed NodeId resolves; zero
// matches return (_, false, false); >1 DISTINCT matches are ambiguous and return
// (_, false, true) so the caller skips and counts deterministically.
func lookupInDirs(idx *SymbolIndex, dirs []string, name string) (model.NodeId, bool, bool) {
	var found model.NodeId
	count := 0
	for _, d := range dedupeSorted(dirs) {
		id, ok := idx.sameDir(d, name)
		if !ok {
			continue
		}
		if count == 0 {
			found = id
			count = 1
		} else if id != found {
			return "", false, true // ambiguous across candidate directories
		}
	}
	if count == 1 {
		return found, true, false
	}
	return "", false, false
}

// relModuleDir resolves a relative module specifier (e.g. "./m", "../util/mod")
// against the importing file's directory and returns the two honest candidate
// directories a same-name symbol could live in WITHOUT touching the filesystem
// (D3 / no fabrication):
//
//   - fileDir: the specifier names a sibling MODULE FILE ("./m" → "<dir>/m.<ext>"),
//     whose symbols are keyed by the file's directory.
//   - pkgDir:  the specifier names a DIRECTORY module ("./m" → "<dir>/m/index.<ext>"),
//     whose symbols are keyed by that directory.
//
// Both are offered as candidates; lookupInDirs treats a match in exactly one as a
// resolution and a conflicting match in both as ambiguous. A non-relative specifier
// (bare/aliased package, e.g. "react", "@app/x") returns nil — D1: not resolved.
func relModuleDir(fromDir, spec string) []string {
	if !isRelativeSpec(spec) {
		return nil
	}
	joined := path.Clean(path.Join(fromDir, spec))
	if joined == "." {
		joined = ""
	}
	fileDir := posixDir(joined) // "<dir>/m" → "<dir>" (m is the module file)
	pkgDir := joined            // "<dir>/m" → directory module "<dir>/m"
	if fileDir == pkgDir {
		return []string{fileDir}
	}
	return []string{fileDir, pkgDir}
}

// relModuleFileCandidates expands a relative module specifier into candidate target
// FILE source paths (file-module + directory-index forms) for the given extensions,
// used to emit file→file imports edges against committed file nodes.
func relModuleFileCandidates(fromDir, spec string, exts []string) []string {
	if !isRelativeSpec(spec) {
		return nil
	}
	joined := path.Clean(path.Join(fromDir, spec))
	if joined == "." {
		joined = ""
	}
	var out []string
	for _, ext := range exts {
		out = append(out, joined+ext)                    // "<dir>/m.ts"
		out = append(out, path.Join(joined, "index")+ext) // "<dir>/m/index.ts"
	}
	return out
}

// isRelativeSpec reports whether a module specifier is repo-relative (resolvable
// without a module-resolution config). "./x", "../x" are relative; "react",
// "@scope/x", "a.b.c" are not (D1: those are external → skip+count).
func isRelativeSpec(spec string) bool {
	return spec == "." || spec == ".." ||
		hasPrefix(spec, "./") || hasPrefix(spec, "../")
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// dedupeSorted returns the input with duplicates removed, sorted for determinism.
func dedupeSorted(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
