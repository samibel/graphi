package link

// rustResolver is the FU-5 registration for Rust. Rust paths are `::`-separated and
// a `use` path encodes BOTH the module and the imported item in one specifier:
//
//   - use crate::shop::price  → bare price()        → crossModule(clause "shop", price)
//   - mod util; util::helper() → selector util.helper → crossModule(clause "util", helper)
//
// The clause is the path's SECOND-to-last `::` segment (the module the item lives
// in), matching the cstWalk "<dirBase>.<bare>" QN convention where a Rust item in
// src/shop/… is keyed by clause "shop". A `mod`/path qualifier that names a module
// directly (util::helper) is tried as a clause via selBaseAsClause. Items with no
// committed node (std/3rd-party crates) skip+count; an item ambiguous across two
// modules of the same clause is counted, never guessed.
type rustResolver struct{}

// rustPkgExts is Rust's static package-source extension set (LINK-001, ADR 0011).
// The registered Rust parser claims exactly `.rs` (core/parse/parser_rust.go:39).
// `Cargo.toml` sits in every crate directory and is emphatically not a module, so
// it must not be an `imports` target. Rust has no separate test-file extension —
// `#[cfg(test)]` modules live inside the `.rs` file that declares them — so there
// is no Go-style `_test` exclusion to make here.
var rustPkgExts = []string{".rs"}

// Language implements Resolver.
func (rustResolver) Language() string { return "rust" }

// Resolve implements Resolver for Rust via the shared clause-keyed core.
func (rustResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	b := binder{
		selBaseImportPath:  map[string]string{},
		bareNameImportPath: map[string]string{},
		clauseOf:           func(p string) string { return packageSegment(p, "::") },
		selBaseAsClause:    true,
		pkgTargetExts:      rustPkgExts,
	}
	for _, imp := range in.Imports {
		if imp.Path == "" {
			continue
		}
		bound := imp.Alias
		if bound == "" {
			bound = lastSegment(imp.Path, "::")
		}
		if bound != "" {
			b.bareNameImportPath[bound] = imp.Path
			b.selBaseImportPath[bound] = imp.Path
		}
		b.pkgImportPaths = append(b.pkgImportPaths, imp.Path)
	}
	return resolveRefs(in, idx, st, b)
}
