package jvmresolve

// Phase A, second half (slice 2): type-NAME resolution over the declaration
// table — the JLS-scoping approximation ADR 0008 specifies, with the strict
// ambiguity rule. This binds WRITTEN type names to tabled types; it does not
// touch call sites or emit edges (Phases B/C).
//
// The scope-walk order, most local first:
//
//	1. the enclosing type chain (innermost first): the enclosing type itself
//	   or a type nested directly in it,
//	2. the same file's top-level types (a compilation unit's own types are in
//	   scope throughout it),
//	3. single-type imports (JLS §6.4.1: they shadow same-package types;
//	   Kotlin aliases bind the alias name),
//	4. the declaring package,
//	5. on-demand (wildcard) imports — candidates pooled across ALL wildcard
//	   imports of the file.
//
// THE STRICT AMBIGUITY RULE: at whichever step first produces candidates, more
// than one candidate is Ambiguous — the walk stops, nothing falls through,
// nothing is ranked. A name no step binds is External (java.lang and the whole
// classpath included): external names NEVER resolve to a tabled type and can
// never carry a confirmed edge. Wrong-binding risk is what the WP-J9
// bytecode ground-truth job hunts; ambiguity and externality only cost recall.

// Resolution is the outcome of one type-name lookup.
type Resolution int

const (
	// ResolvedType: exactly one tabled type binds the name.
	ResolvedType Resolution = iota + 1
	// AmbiguousType: a step produced more than one candidate — dropped,
	// counted, never ranked (Invariant: never guess).
	AmbiguousType
	// ExternalType: no step bound the name inside the repository.
	ExternalType
)

// Index is the resolution view over a Table.
type Index struct {
	table Table
	byFQN map[string][]*Type
	// filesByPath is built lazily by fileOf (hierarchy.go).
	filesByPath map[string]*File
}

// NewIndex builds the resolution index. The Table is copied by value into the
// Index; the *Type pointers handed out by Resolve point into that copy.
func NewIndex(t Table) *Index {
	return &Index{table: t, byFQN: t.TypesByFQN()}
}

// Table returns the indexed table (for callers that walked here first).
func (ix *Index) Table() Table { return ix.table }

// lookup resolves one candidate FQN under the strict rule.
func (ix *Index) lookup(fqn string) (*Type, Resolution) {
	switch cands := ix.byFQN[fqn]; len(cands) {
	case 0:
		return nil, ExternalType
	case 1:
		return cands[0], ResolvedType
	default:
		// Duplicate declarations of one FQN across files: unresolvable by
		// name alone, deliberately (TypesByFQN keeps both).
		return nil, AmbiguousType
	}
}

// ResolveTypeName binds the written base name of a TypeRef (TypeRef.Base —
// generics/arrays/nullability already erased) from the viewpoint of file,
// inside the enclosing type (nil at top level). It implements the walk above.
func (ix *Index) ResolveTypeName(file *File, enclosing *Type, base string) (*Type, Resolution) {
	if base == "" {
		return nil, ExternalType
	}
	if first, rest, dotted := splitFirstSegment(base); dotted {
		return ix.resolveQualified(file, enclosing, first, rest, base)
	}

	// 1. Enclosing chain, innermost first: the chain type itself, then a type
	// nested directly in it.
	if enclosing != nil {
		chain := append(append([]string(nil), enclosing.Nesting...), enclosing.Name)
		for depth := len(chain); depth > 0; depth-- {
			prefix := chain[:depth]
			if prefix[len(prefix)-1] == base {
				if t, res := ix.lookup(fqnOf(file.Package, prefix[:len(prefix)-1], base)); res != ExternalType {
					return t, res
				}
			}
			if t, res := ix.lookup(fqnOf(file.Package, prefix, base)); res != ExternalType {
				return t, res
			}
		}
	}

	// 2. Same file's top-level types.
	for i := range file.Types {
		t := &file.Types[i]
		if len(t.Nesting) == 0 && t.Name == base {
			// Bind through the FQN index so a duplicate FQN elsewhere in the
			// repo still surfaces as ambiguous rather than silently local.
			return ix.lookup(t.FQN)
		}
	}

	// 3. Single-type imports. A Java static import binds a MEMBER, not a type
	// name, and stays out of type resolution.
	for _, imp := range file.Imports {
		if imp.Wildcard || imp.Static {
			continue
		}
		bound := imp.Alias
		if bound == "" {
			bound = lastSegment(imp.Path)
		}
		if bound != base {
			continue
		}
		if t, res := ix.lookup(imp.Path); res != ExternalType {
			return t, res
		}
		// Explicitly imported from outside the repository: external by
		// declaration, no fall-through to weaker scopes.
		return nil, ExternalType
	}

	// 4. The declaring package.
	if t, res := ix.lookup(fqnOf(file.Package, nil, base)); res != ExternalType {
		return t, res
	}

	// 5. On-demand imports, candidates POOLED across every wildcard import —
	// two packages both providing the name is exactly the JLS
	// ambiguous-import error, and it drops here.
	var (
		found *Type
		n     int
	)
	for _, imp := range file.Imports {
		if !imp.Wildcard || imp.Static {
			continue
		}
		for _, cand := range ix.byFQN[imp.Path+"."+base] {
			found = cand
			n++
		}
	}
	switch n {
	case 0:
		return nil, ExternalType
	case 1:
		return found, ResolvedType
	default:
		return nil, AmbiguousType
	}
}

// resolveQualified binds a dotted written name: an exact FQN, a
// package-relative chain ("Outer.Inner" from the same package), or an
// alias/import-anchored chain ("Outer.Inner" where "Outer" is imported).
func (ix *Index) resolveQualified(file *File, enclosing *Type, first, rest, whole string) (*Type, Resolution) {
	// Exact FQN as written.
	if t, res := ix.lookup(whole); res != ExternalType {
		return t, res
	}
	// Package-relative qualified name.
	if t, res := ix.lookup(fqnOf(file.Package, nil, whole)); res != ExternalType {
		return t, res
	}
	// First segment resolved as a simple type name, remainder appended:
	// covers `Outer.Inner` through an import or the enclosing scope.
	if anchor, res := ix.ResolveTypeName(file, enclosing, first); res == ResolvedType {
		return ix.lookup(anchor.FQN + "." + rest)
	} else if res == AmbiguousType {
		return nil, AmbiguousType
	}
	return nil, ExternalType
}

// splitFirstSegment splits "a.b.C" into ("a", "b.C", true); a dotless name
// reports dotted=false.
func splitFirstSegment(s string) (first, rest string, dotted bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// lastSegment returns the substring after the final dot.
func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[i+1:]
		}
	}
	return s
}
