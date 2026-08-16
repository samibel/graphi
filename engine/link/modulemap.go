package link

import (
	"path"
	"sort"
	"strings"
)

// ModuleMap resolves a Go import path to the repository directory that declares
// it, using the module paths of every `go.mod` in the tree.
//
// WHY THIS EXISTS (PARITY-002). Import resolution used to key on the package
// CLAUSE — `path.Base(importPath)` — and union every directory in the repo
// declaring that clause. Two directories can legitimately declare `package
// json`, so an importer of `x/json` fanned out over `y/json` as well: a
// semantically wrong edge on a full pass, and, because the incremental cascade
// is directory-local and nothing imports `y/json`, an edge the incremental pass
// never re-emitted. That divergence is PARITY-002. Resolving through the module
// path makes an import a function of exactly ONE directory, which removes both
// halves at once — the wrong edge and the divergence.
//
// MULTI-MODULE REPOSITORIES ARE THE NORMAL CASE, not an edge case: the pinned
// corpus alone has grpc-go with eleven `go.mod` files and kubernetes with
// thirty-four, the latter publishing 5899 files as separate modules under
// `staging/`. So resolution picks the LONGEST matching module path — a nested
// module's import path is a prefix-extension of its parent's, and the nested
// one owns its subtree.
//
// FAIL-CLOSED: an import path matching no module in the tree resolves to
// nothing, which is exactly the existing behaviour for stdlib and third-party
// imports — they mint no intra-repo edge. This type never guesses a directory.
type ModuleMap struct {
	// mods maps a module path (e.g. "example.com/m" or
	// "google.golang.org/grpc/security/advancedtls") to the repo-relative
	// directory holding its go.mod ("" for the root module). Sorted access is
	// via order.
	mods map[string]string
	// order is the module paths sorted LONGEST FIRST, so the first match in a
	// linear scan is the most specific one. Precomputed so resolution does not
	// sort per call.
	order []string
	// moduleDirs is the set of directories that ROOT a module, used for the
	// directory-ownership check: in Go, a nested module EXCLUDES its subtree
	// from the enclosing module, so a directory resolved through module M is
	// only valid if no other module's root sits between M's root and that
	// directory. Without this, path arithmetic alone would happily resolve
	// `example.com/m/sub/pkg` into `sub/pkg` even after `sub/` became its own
	// (differently-named) module — the exact stale-edge shape the
	// add_nested_gomod conformance class pins.
	moduleDirs map[string]struct{}
}

// NewModuleMap builds the map from a set of discovered go.mod files, keyed by
// their repo-relative path ("go.mod", "sub/go.mod", …) with the file's raw
// contents as the value. A go.mod whose module directive cannot be parsed is
// skipped — it contributes no resolution rather than a wrong one.
//
// Deterministic: the same input map always yields the same resolution results,
// because ties (two go.mod files declaring the SAME module path, which is
// malformed but possible on disk) are broken by shallowest-then-lexicographic
// directory, never by map iteration order.
func NewModuleMap(gomods map[string][]byte) *ModuleMap {
	m := &ModuleMap{mods: map[string]string{}}
	paths := make([]string, 0, len(gomods))
	for p := range gomods {
		paths = append(paths, p)
	}
	// Shallowest first, then lexicographic: a deterministic winner for the
	// malformed duplicate-module-path case.
	sort.Slice(paths, func(a, b int) bool {
		da, db := strings.Count(paths[a], "/"), strings.Count(paths[b], "/")
		if da != db {
			return da < db
		}
		return paths[a] < paths[b]
	})
	for _, p := range paths {
		modPath, ok := parseModuleDirective(gomods[p])
		if !ok {
			continue
		}
		if _, dup := m.mods[modPath]; dup {
			continue // first (shallowest) declaration wins, deterministically
		}
		m.mods[modPath] = posixDir(p)
	}
	// moduleDirs comes from EVERY input go.mod path, not just the parsed
	// winners in m.mods (ADR 0009 review round 2, finding 3): a go.mod that is
	// unparseable — or that lost the duplicate-module-path tie-break — still
	// marks a module BOUNDARY even though it contributes no resolution. If it
	// did not, the enclosing module's path arithmetic would resolve into a
	// subtree the Go toolchain refuses to build, minting intra-repo edges out
	// of a guess. Shielded-and-unresolvable is the fail-closed answer.
	m.moduleDirs = make(map[string]struct{}, len(gomods))
	for p := range gomods {
		m.moduleDirs[posixDir(p)] = struct{}{}
	}
	m.order = make([]string, 0, len(m.mods))
	for mp := range m.mods {
		m.order = append(m.order, mp)
	}
	// Longest module path first so the most specific module wins; length ties
	// broken lexicographically for determinism.
	sort.Slice(m.order, func(a, b int) bool {
		if len(m.order[a]) != len(m.order[b]) {
			return len(m.order[a]) > len(m.order[b])
		}
		return m.order[a] < m.order[b]
	})
	return m
}

// Empty reports whether the map can resolve nothing — the case for a tree with
// no go.mod at all, where callers must keep their previous behaviour rather
// than silently resolving everything to nothing.
func (m *ModuleMap) Empty() bool { return m == nil || len(m.mods) == 0 }

// Dir resolves an import path to the repo-relative directory declaring it, or
// ("", false) when the path belongs to no module in this tree.
//
// The match is on MODULE-PATH BOUNDARIES, never raw string prefixes: module
// "example.com/m" matches "example.com/m/x/json" and "example.com/m" itself,
// but must NOT match "example.com/mtools" — which a naive HasPrefix would.
func (m *ModuleMap) Dir(importPath string) (string, bool) {
	if m == nil || importPath == "" {
		return "", false
	}
	for _, mp := range m.order {
		rest, ok := trimModulePrefix(importPath, mp)
		if !ok {
			continue
		}
		base := m.mods[mp]
		var dir string
		switch {
		case rest == "":
			dir = base
		case base == "":
			dir = rest
		default:
			dir = base + "/" + rest
		}
		// Ownership check: the resolved directory must still BELONG to the
		// module that resolved it. In Go, a nested module excludes its subtree
		// from the enclosing module, so if any OTHER module's root sits on the
		// path between this module's root and dir, the import does not resolve
		// here — and because module paths are disjoint prefixes, it resolves
		// nowhere: fail closed.
		if m.ownedBy(dir) != base {
			return "", false
		}
		return dir, true
	}
	return "", false
}

// ownedBy returns the module ROOT directory that owns dir: the deepest module
// root that is dir itself or an ancestor of it. The repo-root module ("") owns
// everything not claimed by a nested module.
func (m *ModuleMap) ownedBy(dir string) string {
	for d := dir; ; {
		if _, ok := m.moduleDirs[d]; ok {
			return d
		}
		if d == "" {
			return "" // fell through to the root; the root module (if any) owns it
		}
		if i := strings.LastIndexByte(d, '/'); i >= 0 {
			d = d[:i]
		} else {
			d = ""
		}
	}
}

// trimModulePrefix returns importPath relative to modPath when importPath is
// modPath itself or lies beneath it on a path-segment boundary.
func trimModulePrefix(importPath, modPath string) (string, bool) {
	if importPath == modPath {
		return "", true
	}
	if strings.HasPrefix(importPath, modPath+"/") {
		return importPath[len(modPath)+1:], true
	}
	return "", false
}

// parseModuleDirective extracts the module path from go.mod contents. It reads
// ONLY the `module` directive — the full go.mod grammar is not needed to map an
// import path to a directory, and a narrower parser cannot mis-read a require
// block as a module path.
//
// Deliberately duplicated from engine/typeresolve.ParseModulePath rather than
// imported, on the same reasoning check.go:43 records for the kind strings:
// engine/link and engine/typeresolve are independent passes over the same
// sources, and a shared helper would couple the heuristic layer to the
// type-checked one for eight lines of parsing. The two are pinned to agree by
// TestParseModuleDirective_AgreesWithTyperesolve.
func parseModuleDirective(gomod []byte) (string, bool) {
	for _, raw := range strings.Split(string(gomod), "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, ok := strings.CutPrefix(line, "module")
		if !ok {
			continue
		}
		// Require whitespace after the directive so `modulefoo` is not a match.
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		modPath := strings.TrimSpace(rest)
		modPath = strings.Trim(modPath, `"`)
		if modPath == "" {
			continue
		}
		return modPath, true
	}
	return "", false
}

// GoModPath reports whether a repo-relative path is a go.mod file, at ANY depth.
// Callers use it to decide that a change can move import resolution.
func GoModPath(relPath string) bool { return path.Base(relPath) == "go.mod" }
