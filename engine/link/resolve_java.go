package link

// javaResolver is the FU-5 registration for Java. A Java import is the FQN of a
// type (`import com.shop.Price;`), so the imported binding is the type name and the
// package clause is the FQN's SECOND-to-last `.` segment (the package the type lives
// in), matching the cstWalk "<dirBase>.<bare>" convention where com/shop/Price.java
// is keyed by clause "shop":
//
//   - Price.of()  (qualified static/type call) → crossModule(clause "shop", of)
//   - p.value()   (instance call via a variable) → NOT resolvable: the receiver type
//     is unknown (the QN convention drops the receiver), so it is skip+counted —
//     never guessed (conservative, honest per Invariant 2).
//
// fqnResolverBinder is shared with Kotlin (same package/FQN mechanics).
type javaResolver struct{}

// Language implements Resolver.
func (javaResolver) Language() string { return "java" }

// Resolve implements Resolver for Java via the shared FQN binder.
func (javaResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, fqnImportBinder(in))
}

// kotlinResolver is the FU-5 registration for Kotlin. Kotlin imports are FQNs like
// Java, but commonly import a top-level function or a class with an EMPTY alias
// (`import com.shop.price`), so the imported binding is the FQN's last segment and
// the clause is its second-to-last segment — exactly the Java mechanics, reused.
type kotlinResolver struct{}

// Language implements Resolver.
func (kotlinResolver) Language() string { return "kotlin" }

// Resolve implements Resolver for Kotlin via the shared FQN binder.
func (kotlinResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, fqnImportBinder(in))
}

// fqnImportBinder builds the cross-module binder for FQN, package-clause-keyed
// languages (Java, Kotlin): each import binds its last segment (alias or FQN tail)
// and resolves against the FQN's package (second-to-last segment) clause.
func fqnImportBinder(in FileRefs) binder {
	b := binder{
		selBaseImportPath:  map[string]string{},
		bareNameImportPath: map[string]string{},
		clauseOf:           func(p string) string { return packageSegment(p, ".") },
	}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		bound := imp.Alias
		if bound == "" {
			bound = lastSegment(imp.Path, ".")
		}
		if bound != "" {
			b.selBaseImportPath[bound] = imp.Path
			b.bareNameImportPath[bound] = imp.Path
		}
		b.pkgImportPaths = append(b.pkgImportPaths, imp.Path)
	}
	return b
}
