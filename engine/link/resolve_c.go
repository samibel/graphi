package link

// cResolver is the FU-5 registration for C (cppResolver below shares it for C++).
// C has no package system: cross-translation-unit references are wired through
// `#include` of a local header and resolved against function declarations. The
// resolver models that:
//
//   - #include "util.h"   → file→file `imports` edge to the committed header node,
//     and the header's directory becomes an ambient lookup dir;
//   - helper()            → resolved same-directory (derived) when defined in a
//     sibling translation unit, else in an included header's directory (heuristic).
//
// The parser strips the include's quotes/brackets, so a system header (<stdio.h>)
// arrives identically to a local one; it simply resolves to no committed node and
// is skipped (no phantom). D2 — NO overload resolution: a name defined more than
// once in a directory is ambiguous (byDir drops it) and across ambient dirs is
// counted, never disambiguated by signature.
type cResolver struct{}

// Language implements Resolver.
func (cResolver) Language() string { return "c" }

// Resolve implements Resolver for C via the shared include-directory binder.
func (cResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, includeBinder(in))
}

// cppResolver is the FU-5 registration for C++. C++ uses the same `#include`
// translation-unit model as C (overload resolution explicitly NOT attempted — D2),
// so it reuses the include-directory binder.
type cppResolver struct{}

// Language implements Resolver.
func (cppResolver) Language() string { return "cpp" }

// Resolve implements Resolver for C++ via the shared include-directory binder.
func (cppResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, includeBinder(in))
}

// includeBinder builds the binder for `#include`-based languages (C, C++): each
// local include contributes a file→file imports edge target (its committed header
// node) and an ambient directory in which bare call/reference names are resolved.
func includeBinder(in FileRefs) binder {
	b := binder{}
	seenDir := map[string]struct{}{}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		// The include spec is repo-relative to the including file's directory.
		target := joinRel(in.Dir, imp.Path)
		b.importFileTargets = append(b.importFileTargets, target)
		dir := posixDir(target)
		if _, dup := seenDir[dir]; !dup {
			seenDir[dir] = struct{}{}
			b.ambientDirs = append(b.ambientDirs, dir)
		}
	}
	return b
}
