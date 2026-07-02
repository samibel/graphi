package typeresolve

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// This file is the package-graph plumbing (roadmap PR 4.2, still dark): pure,
// deterministic functions that turn the ingest walk's map[path][]byte of Go
// sources into an ordered list of type-checkable package units. No I/O, no
// exec, no network — internal/workspace is deliberately NOT reused (it shells
// out to `go env`, which the ingest context forbids).

// ParseModulePath extracts the module path from go.mod contents. It parses
// ONLY the `module` directive — a full go.mod grammar is not needed to map
// import paths onto repository directories. ok=false when no directive is
// found (no module path means no intra-repo import resolution; every import
// is then external and the type-check degrades to stub imports).
func ParseModulePath(gomod []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(gomod))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, found := strings.CutPrefix(line, "module")
		if !found || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue // e.g. a line starting with "modulex"
		}
		p := strings.TrimSpace(rest)
		p = strings.Trim(p, `"`) // quoted module paths are rare but legal
		if p == "" {
			continue
		}
		return p, true
	}
	return "", false
}

// Package is one type-checkable unit: all files in one directory sharing one
// package clause. Degraded is a non-empty reason when the unit must NOT be
// type-checked (its symbols keep their heuristic-tier edges — degradation
// never deletes knowledge, mirroring the engine/link discipline).
type Package struct {
	// Dir is the repo-relative, slash-separated directory ("." for the root).
	Dir string
	// Name is the package clause shared by Files.
	Name string
	// Files are the sorted repo-relative paths of the unit's sources.
	Files []string
	// Imports is the sorted, de-duplicated union of the files' import paths.
	Imports []string
	// Degraded, when non-empty, names why this unit is skipped by the
	// type-check pass (multiple package clauses in the directory, import
	// cycle, ...).
	Degraded string
}

// SkippedFile is a source the grouping could not assign to a checkable unit.
type SkippedFile struct {
	Path   string
	Reason string
}

// GroupPackages groups Go sources by (directory, package clause) into
// type-checkable units. Inputs are the ingest walk's already-read bytes; the
// clause and imports are read with parser.ImportsOnly (cheap — bodies are
// never parsed here). Deterministic: paths are visited sorted and every output
// list is sorted.
//
// Fail-open per file, never per repo:
//   - *_test.go files are skipped (v1: test files keep heuristic edges — an
//     in-package test file with test-only imports would otherwise fail the
//     whole package's check, and external _test packages double the unit
//     count for symbols the extractor treats identically).
//   - files whose package clause cannot be parsed are skipped with the reason.
//   - a directory with MULTIPLE non-test package clauses yields one unit per
//     clause, each Degraded — go/types cannot check either mixture soundly,
//     and picking a winner would silently drop the loser's symbols.
func GroupPackages(files map[string][]byte) ([]Package, []SkippedFile) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	type unitKey struct{ dir, name string }
	units := map[unitKey]*Package{}
	importsSeen := map[unitKey]map[string]struct{}{}
	clausesPerDir := map[string]map[string]struct{}{}
	var skipped []SkippedFile

	fset := token.NewFileSet()
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			continue // non-Go inputs are simply not this pass's business
		}
		if strings.HasSuffix(p, "_test.go") {
			skipped = append(skipped, SkippedFile{Path: p, Reason: "test file (heuristic-only in v1)"})
			continue
		}
		f, err := parser.ParseFile(fset, p, files[p], parser.ImportsOnly)
		if err != nil || f.Name == nil || f.Name.Name == "" {
			skipped = append(skipped, SkippedFile{Path: p, Reason: "package clause unparseable"})
			continue
		}
		dir := path.Dir(p)
		name := f.Name.Name
		key := unitKey{dir, name}
		u := units[key]
		if u == nil {
			u = &Package{Dir: dir, Name: name}
			units[key] = u
			importsSeen[key] = map[string]struct{}{}
		}
		u.Files = append(u.Files, p)
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if ip == "" {
				continue
			}
			if _, dup := importsSeen[key][ip]; !dup {
				importsSeen[key][ip] = struct{}{}
				u.Imports = append(u.Imports, ip)
			}
		}
		if clausesPerDir[dir] == nil {
			clausesPerDir[dir] = map[string]struct{}{}
		}
		clausesPerDir[dir][name] = struct{}{}
	}

	out := make([]Package, 0, len(units))
	for key, u := range units {
		sort.Strings(u.Files)
		sort.Strings(u.Imports)
		if len(clausesPerDir[key.dir]) > 1 {
			u.Degraded = "multiple package clauses in directory"
		}
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, skipped
}

// ResolveImport maps an import path onto a repo-relative directory when the
// import is INSIDE the module (modulePath itself → ".", modulePath/sub → sub).
// ok=false for stdlib and third-party imports — those degrade to stub imports
// in the type-check, they are never fetched.
func ResolveImport(modulePath, importPath string) (dir string, ok bool) {
	if modulePath == "" || importPath == "" {
		return "", false
	}
	if importPath == modulePath {
		return ".", true
	}
	rest, found := strings.CutPrefix(importPath, modulePath+"/")
	if !found || rest == "" {
		return "", false
	}
	return rest, true
}

// CheckOrder orders the units so that every intra-repo dependency is checked
// before its importers, and marks every unit that sits on an import cycle as
// Degraded (go/types cannot check a cyclic package set; degrading the whole
// strongly-connected component keeps its symbols on heuristic edges rather
// than failing the pass). The input slice is not mutated; the returned slice
// is the same units, reordered, with cycle degradations applied.
//
// Determinism: adjacency, Tarjan's SCC visit order, and the final order all
// derive from the (Dir, Name)-sorted input, so identical inputs yield an
// identical order — the property the full-vs-incremental byte-parity design
// leans on.
func CheckOrder(modulePath string, pkgs []Package) []Package {
	n := len(pkgs)
	out := make([]Package, n)
	copy(out, pkgs)

	// Map each directory to the unit indexes it hosts (multi-clause dirs host
	// several; they are already Degraded but still take part in ordering).
	byDir := map[string][]int{}
	for i, p := range out {
		byDir[p.Dir] = append(byDir[p.Dir], i)
	}

	// adj[i] = indexes of units i imports (intra-repo only), sorted.
	// hasSelfLoop tracks a unit importing its own directory — illegal Go that
	// go/types would reject anyway, but degrading it here keeps the ordering
	// pass self-consistent instead of leaning on the checker's error path.
	adj := make([][]int, n)
	hasSelfLoop := make([]bool, n)
	for i, p := range out {
		seen := map[int]struct{}{}
		for _, imp := range p.Imports {
			dir, ok := ResolveImport(modulePath, imp)
			if !ok {
				continue
			}
			if dir == p.Dir {
				hasSelfLoop[i] = true
			}
			for _, j := range byDir[dir] {
				if j == i {
					continue
				}
				if _, dup := seen[j]; !dup {
					seen[j] = struct{}{}
					adj[i] = append(adj[i], j)
				}
			}
		}
		sort.Ints(adj[i])
	}

	// Tarjan's strongly connected components over the deterministic adjacency.
	const unvisited = -1
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = unvisited
	}
	var stack []int
	next := 0
	sccOf := make([]int, n)
	sccSize := map[int]int{}
	sccCount := 0

	var strongConnect func(v int)
	strongConnect = func(v int) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if index[w] == unvisited {
				strongConnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			id := sccCount
			sccCount++
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				sccOf[w] = id
				sccSize[id]++
				if w == v {
					break
				}
			}
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == unvisited {
			strongConnect(v)
		}
	}

	// A unit on a multi-member SCC (or importing itself through its own dir)
	// is on an import cycle: degrade it, keep it in the order.
	for i := range out {
		if (sccSize[sccOf[i]] > 1 || hasSelfLoop[i]) && out[i].Degraded == "" {
			out[i].Degraded = "import cycle"
		}
	}

	// Tarjan emits SCCs in reverse topological order of the condensation, so
	// ascending SCC id is already dependencies-first. Order by (sccID, Dir,
	// Name) — deterministic and stable for same-SCC members.
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if sccOf[ia] != sccOf[ib] {
			return sccOf[ia] < sccOf[ib]
		}
		if out[ia].Dir != out[ib].Dir {
			return out[ia].Dir < out[ib].Dir
		}
		return out[ia].Name < out[ib].Name
	})
	ordered := make([]Package, n)
	for pos, i := range idx {
		ordered[pos] = out[i]
	}
	return ordered
}
