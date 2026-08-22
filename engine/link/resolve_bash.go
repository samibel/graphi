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

// sqlResolver is the FU-5 registration for SQL. It supplies the EMPTY binder,
// which is the honest statement about SQL's IMPORTS: ISO/IEC 9075 defines no
// file-inclusion construct (`\i` is a psql client command, `SOURCE` a mysql
// one), so there is no import to bind and no `imports` edge is ever emitted.
//
// CORRECTION (2026-08-19, SW-183 / ADR 0012). An earlier version of this comment
// said the resolver "is an honest no-op: it emits no edge and fabricates
// nothing". THE FIRST HALF WAS FALSE, and it was the source of the same false
// claim on five downstream records — docs/language-support.md's SQL row and its
// footnote, the programme plan's §4 finding 3, its G1 wrinkle and its §7 wave 5,
// and ADR 0007's "Still open" list — each of which was written as
// "SQL's resolver deliberately proves nothing" and used to schedule a re-grade
// (the "ADR-W1" item) that measurement then showed was not needed.
//
// What an EMPTY BINDER actually disables is the IMPORT-KEYED mechanisms only.
// resolveRefs' same-directory resolution still runs, so a table declared in a
// sibling file resolves at the `derived` tier:
//
//	schema.sql: CREATE TABLE users (id INT);
//	query.sql : CREATE VIEW active_users AS SELECT id FROM users;
//	          → references query.active_users → schema.users   (derived)
//
// That is a real cross-file edge, and it is why SQL's derived
// `cross-file-heuristic` level is EARNED rather than over-claimed. Established
// by two counterfactuals, not by reading: removing this Register call makes the
// edge disappear, and replacing this Resolve body with `return nil` makes the
// capability audit report an over-claim
// (surfaces/client/capabilityaudit_test.go; docs/rc/capability-audit-2026-08-19.md §4).
//
// The BOUND, which belongs beside the capability so the correction does not
// become the next over-claim: resolution is SAME-DIRECTORY ONLY. Put the two
// files in different directories and nothing resolves — with no import
// construct there is nothing to carry the reference across a directory
// boundary. That is inside `cross-file-heuristic`'s own definition
// ("…or `derived` within a package", engine/trust/capability.go), not a defect.
type sqlResolver struct{}

// Language implements Resolver.
func (sqlResolver) Language() string { return "sql" }

// Resolve implements Resolver for SQL: no import binding (see the type comment),
// so cross-file resolution is whatever the shared core's same-directory pass can
// prove — never a fabricated target.
func (sqlResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, binder{})
}
