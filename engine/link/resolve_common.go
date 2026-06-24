package link

import (
	"path"
	"sort"

	"github.com/samibel/graphi/core/model"
)

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

	// bareNameDirs maps a bare imported binding (a name pulled into file scope, e.g.
	// TS `import {X} from "./m"` or Python `from m import X`) to candidate target
	// directories. A bare use of such a name resolves cross-file via byDir.
	bareNameDirs map[string][]string

	// importFileTargets are candidate target file source paths (with extension) for
	// file→file `imports` edges; only those present as committed file nodes emit an
	// edge (no phantom). Resolvers expand a module specifier into its extension /
	// index-file candidates here.
	importFileTargets []string
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

		if !p.Selector {
			// Bare name: same-directory (derived) first, then an imported bare
			// binding (cross-file heuristic).
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
			if dirs, isBound := b.bareNameDirs[p.Name]; isBound {
				to, found, ambiguous := lookupInDirs(idx, dirs, p.Name)
				if found {
					out = append(out, intent{
						from: from, to: to, kind: p.Kind, class: classSelector,
						reason:   "cross-file " + p.Kind + " resolved via imported binding " + p.Name,
						evidence: ev,
					})
					st.ResolvedHeuristic++
					continue
				}
				if ambiguous {
					st.Ambiguous++
					continue
				}
			}
			st.Skipped++
			continue
		}

		// Selector base.name: a module/package alias (clause cross-module) first,
		// then a namespace/relative-dir alias (cross-file via byDir).
		if impPath, isPkg := b.selBaseImportPath[p.SelectorBase]; isPkg {
			if to, ok := idx.crossPackage(impPath, p.Name); ok {
				out = append(out, intent{
					from: from, to: to, kind: p.Kind, class: classSelector,
					reason:   "cross-module " + p.Kind + " resolved via import " + impPath + " (qualifier " + p.SelectorBase + ")",
					evidence: ev,
				})
				st.ResolvedHeuristic++
				continue
			}
			// Known module alias but symbol not in the repo (stdlib/3rd-party): skip.
			st.Skipped++
			continue
		}
		if dirs, isNS := b.selBaseDirs[p.SelectorBase]; isNS {
			to, found, ambiguous := lookupInDirs(idx, dirs, p.Name)
			if found {
				out = append(out, intent{
					from: from, to: to, kind: p.Kind, class: classSelector,
					reason:   "cross-file " + p.Kind + " resolved via namespace import " + p.SelectorBase,
					evidence: ev,
				})
				st.ResolvedHeuristic++
				continue
			}
			if ambiguous {
				st.Ambiguous++
				continue
			}
		}
		// Unresolvable selector (local-variable method, stdlib, unindexed): skip.
		st.Skipped++
	}

	// File→file imports edges: link this file node to each committed target file
	// node. Targets are language-expanded candidate paths; only committed nodes
	// emit an edge (no phantom).
	if fileID, ok := idx.fileNode(in.SourcePath); ok {
		emitted := map[model.NodeId]struct{}{}
		for _, target := range b.importFileTargets {
			tID, ok := idx.fileNode(model.NormalizePath(target))
			if !ok || tID == fileID {
				continue
			}
			if _, dup := emitted[tID]; dup {
				continue
			}
			emitted[tID] = struct{}{}
			out = append(out, intent{
				from: fileID, to: tID, kind: edgeImports, class: classSelector,
				reason:   "file imports " + target,
				evidence: evidenceFor(in.SourcePath, 1),
			})
			st.ResolvedHeuristic++
		}
	}

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
