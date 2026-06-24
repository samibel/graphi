package link

// csharpResolver is the FU-5 registration for C#. A C# `using Shop;` brings a
// NAMESPACE into scope rather than binding a specific symbol, so a qualified call
// `Price.Of()` references a type `Price` that lives in one of the imported
// namespaces. The resolver tries each imported namespace as an ambient package
// clause (clause = the namespace's last `.` segment, matching the cstWalk
// "<dirBase>.<bare>" convention where Shop/Price.cs is keyed by clause "Shop"):
//
//   - using Shop;  Price.Of()  → crossModule(clause "Shop", Of)   [via ambientClauses]
//
// A name found under exactly one ambient namespace resolves (heuristic); a name in
// two namespaces is ambiguous and counted; an unimported / 3rd-party type
// skip+counts. The receiver-type of an instance call is unknown, so such calls are
// not over-resolved — honest per Invariant 2.
type csharpResolver struct{}

// Language implements Resolver. The C# parser's language id is "c_sharp".
func (csharpResolver) Language() string { return "c_sharp" }

// Resolve implements Resolver for C# via ambient namespace clauses.
func (csharpResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	b := binder{clauseOf: func(p string) string { return lastSegment(p, ".") }}
	seen := map[string]struct{}{}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		clause := lastSegment(imp.Path, ".")
		if clause == "" {
			continue
		}
		if _, dup := seen[clause]; dup {
			continue
		}
		seen[clause] = struct{}{}
		b.ambientClauses = append(b.ambientClauses, clause)
		b.pkgImportPaths = append(b.pkgImportPaths, imp.Path)
	}
	return resolveRefs(in, idx, st, b)
}
