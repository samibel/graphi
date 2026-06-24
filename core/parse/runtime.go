package parse

// Runtime is the provenance marker every Parser declares: the concrete parsing
// backend that produced a ParseResult. It is the first-class hook the SW-055
// no-CGO default-tier guard inspects so purity is asserted DIRECTLY at the
// registration layer (over RegisterDefaults output) rather than re-derived from
// the import graph. The two checks are complementary defense-in-depth:
//
//   - internal/cgoconformance proves the BUILD/import-graph is CGo-free
//     (go-sitter-forest, which is wholly CGO, can never reach the default graph);
//   - parse.AssertPureGoDefaults proves every REGISTERED default parser declares a
//     pure-Go runtime — and rejects any CGO-backed parser (the negative test).
//
// A Runtime value is CGo-free iff it is one of the pure-Go runtimes below. The
// guard treats any unknown or explicitly-CGO runtime as a violation (default-deny).
type Runtime string

const (
	// RuntimeGoAST is the native Go path (go/parser, go/ast, go/token). Pure Go.
	RuntimeGoAST Runtime = "go/ast"

	// RuntimeStdlib is a stdlib-only structural parser (e.g. encoding/json).
	// Pure Go.
	RuntimeStdlib Runtime = "stdlib"

	// RuntimeGoTreeSitter is the pure-Go gotreesitter tree-sitter runtime
	// (github.com/odvcencio/gotreesitter, MIT). It interprets embedded parse-table
	// blobs in pure Go with NO cgo — this is the default tier's tree-sitter backend.
	RuntimeGoTreeSitter Runtime = "gotreesitter"

	// RuntimeCGOForest is the CGO go-sitter-forest bundle used ONLY by the opt-in
	// graphi-broad flavor (SW-056). It is NEVER pure-Go and MUST NOT appear in the
	// default tier. It is declared here so the negative/anti-vacuity guard test can
	// register a synthetic parser carrying it and prove the guard rejects it.
	RuntimeCGOForest Runtime = "go-sitter-forest-cgo"
)

// pureGoRuntimes is the closed allowlist of CGo-free runtimes the default tier
// may use. Any runtime not in this set is rejected by the guard (default-deny).
var pureGoRuntimes = map[Runtime]struct{}{
	RuntimeGoAST:        {},
	RuntimeStdlib:       {},
	RuntimeGoTreeSitter: {},
}

// IsPureGo reports whether r is one of the closed set of CGo-free runtimes the
// default tier permits. Unknown runtimes are treated as impure (default-deny).
func (r Runtime) IsPureGo() bool {
	_, ok := pureGoRuntimes[r]
	return ok
}
