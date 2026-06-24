package link

// bashResolver is the FU-5 registration for Bash/Shell. `source ./util.sh` (and
// `. ./util.sh`) pulls another script's definitions into scope relative to the
// including file's directory: the source yields a file→file `imports` edge and its
// directory becomes an ambient lookup dir, so a function call defined in the sourced
// script resolves (heuristic); a same-directory call resolves derived. A sourced
// path that resolves to no committed node skip+counts.
type bashResolver struct{}

// Language implements Resolver.
func (bashResolver) Language() string { return "bash" }

// Resolve implements Resolver for Bash.
func (bashResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, requireBinder(in, []string{".sh", ".bash"}))
}

// sqlResolver is the FU-5 registration for SQL. At this tier the SQL extractor
// records no cross-file import or selector reference (schema/table references are
// not provably resolvable to committed nodes without a schema model), so the
// resolver is an honest no-op: it emits no edge and fabricates nothing. Registering
// it keeps the per-language dispatch complete and documents the deliberate
// skip-everything decision (Invariant 2) rather than leaving SQL unhandled.
type sqlResolver struct{}

// Language implements Resolver.
func (sqlResolver) Language() string { return "sql" }

// Resolve implements Resolver for SQL: nothing is provably resolvable → no edges.
func (sqlResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, binder{})
}
