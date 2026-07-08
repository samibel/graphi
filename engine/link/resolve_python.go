package link

import "strings"

// pyResolver is the FU-5 registration for Python. Python binds imported symbols by
// MODULE PATH, not by a relative file path, so resolution is clause-keyed (like Go)
// rather than directory-relative (like the TypeScript family):
//
//   - import pkg            → pkg.fn()      selector base "pkg"  → crossModule("pkg", fn)
//   - import pkg.sub as p   → p.fn()        selector base "p"    → crossModule("pkg.sub", fn)
//   - from pkg import name  → name()        bare binding "name"  → crossModule("pkg", name)
//
// The clause is path.Base(importPath) — matching the cstWalk convention where a
// symbol's qualified name is "<dirBase>.<bare>", so a package DIRECTORY pkg/ (whose
// files have clause "pkg") resolves; a stdlib/3rd-party module with no committed
// node is skipped (no fabrication). A name declared under the same clause in two
// directories is ambiguous → counted, never guessed.
//
// pyImports records `import x` as {Alias:x, Path:x} and `from m import n` as
// {Alias:n, Path:m}, so the bound Alias is registered BOTH as a selector base and a
// bare binding; only a committed-node lookup ever emits an edge, so registering both
// is safe. Relative imports (`from . import x`) are recorded by the parser without
// their leading dot and resolve only when the bare module name matches a committed
// package — otherwise skip+count.
type pyResolver struct{}

// Language implements Resolver.
func (pyResolver) Language() string { return "python" }

// Resolve implements Resolver for Python via the shared clause-keyed core.
func (pyResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	b := binder{
		selBaseImportPath:  map[string]string{},
		bareNameImportPath: map[string]string{},
		externalIneligible: map[string]bool{},
		clauseOf:           pyClause,
		// WP-14: an imported module member that resolves to no committed symbol is a
		// genuine external (stdlib / 3rd-party) reference with an exact FQN
		// ("os.system", "subprocess.run") — mint an interned external node. RELATIVE
		// imports are excluded (externalIneligible below): their target is in-repo,
		// so an unresolved use is an honest skip, not a fabricated stdlib node.
		externalQN: externalMemberQN,
	}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		if imp.Alias != "" {
			b.selBaseImportPath[imp.Alias] = imp.Path
			b.bareNameImportPath[imp.Alias] = imp.Path
			if imp.Relative {
				b.externalIneligible[imp.Alias] = true
			}
		}
		b.pkgImportPaths = append(b.pkgImportPaths, imp.Path)
	}
	return resolveRefs(in, idx, st, b)
}

// pyClause derives the package-clause lookup key from a Python module path: the
// trailing DOT segment ("tax.rates" → "rates", "json" → "json"). This matches the
// cstWalk convention where a Python symbol's qualified name is "<dirBase>.<bare>",
// so a package directory pkg/ (clause "pkg") resolves while dotted module paths
// key on their last component.
func pyClause(importPath string) string {
	if i := strings.LastIndexByte(importPath, '.'); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}
