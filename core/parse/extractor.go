package parse

import (
	"fmt"

	"github.com/samibel/graphi/core/model"
)

// Canonical extraction vocabulary (SW-052, STEP-0 hard gate).
//
// SymbolExtractor implementations MUST emit nodes and edges drawn from these two
// closed sets so the language-neutral graph stays consistent across grammars. Each
// language worker (SW-053..056) maps its AST/CST onto these names rather than
// inventing its own; the Go reference path (goExtractor) is the canonical example.
//
// Node kinds: file, function, method, type, variable, constant.
// Edge kinds:  defines, calls, references, imports.
//
// These are the SAME constants the Go extractor already emits (goKind*/goEdge*);
// they are surfaced here as the shared contract every SymbolExtractor honors.
const (
	// KindFile is the node kind for a source file.
	KindFile = goKindFile
	// KindFunction is the node kind for a free (non-method) function.
	KindFunction = goKindFunction
	// KindMethod is the node kind for a method (a function with a receiver).
	KindMethod = goKindMethod
	// KindType is the node kind for a type declaration.
	KindType = goKindType
	// KindVariable is the node kind for a variable declaration.
	KindVariable = goKindVariable
	// KindConstant is the node kind for a constant declaration.
	KindConstant = goKindConstant
	// KindPackage is the node kind for an INTERNED package/namespace node
	// (WP-01). Unlike the six symbol kinds above (which are per-file), a package
	// node is keyed by its full package path with an EMPTY source path, so every
	// file declaring the same package mints the byte-identical NodeId — the node
	// is interned by construction. It is emitted only by the FQN/package-header
	// languages (Java, Kotlin) so a single file→package `imports` edge replaces
	// the cross-module file→file import fan-out.
	KindPackage = "package"
)

const (
	// EdgeDefines is the edge kind from a file node to each symbol it declares.
	EdgeDefines = goEdgeDefines
	// EdgeCalls is the edge kind for a resolved intra-file call.
	EdgeCalls = goEdgeCalls
	// EdgeReferences is the edge kind for a resolved intra-file non-call use.
	EdgeReferences = goEdgeReferences
	// EdgeImports is the edge kind for an import relationship. The Go reference
	// path records imports as References/ImportSpecs rather than emitting an
	// "imports" edge at parse time; later grammar workers MAY emit it directly via
	// the mapping helper. It is part of the frozen vocabulary regardless.
	EdgeImports = "imports"
)

// SymbolExtractor is the language-neutral extraction seam (SW-052). It is a NEW,
// narrower contract layered OVER the existing Parser/ParseResult boundary — it does
// NOT rename or replace Parser. A Parser produces a normalized ParseResult whose
// Root is a backend-specific AST handle; a SymbolExtractor turns that handle into
// the shared node/edge vocabulary plus the inert PendingRefs the linker resolves.
//
// Separating the two responsibilities lets each later language worker write a
// grammar query and an Extract mapping without touching graph plumbing or any other
// language's parser. The Go path (goSymbolExtractor) is the reference implementation
// and threads the existing *goAST (which already carries the FileSet) through root.
//
// Contract:
//   - Extract MUST be deterministic: identical (filename, root) yields byte-
//     identical nodes/edges/IDs and identical ordering (mirrors
//     TestExtractGo_Deterministic).
//   - Extract MUST be a pure transform: no I/O, no network, no eval/exec/shell. It
//     operates only on the already-parsed root handle. (core/parse purity_test.go
//     enforces the package-level leaf boundary.)
//   - Extract MUST NOT fabricate cross-file/cross-package endpoints. Any reference
//     it cannot prove from a single file is RECORDED as an inert PendingRef (no
//     NodeId, no resolved edge); the engine/link linker resolves them later.
//   - Emitted nodes/edges MUST use the canonical Kind*/Edge* vocabulary above and
//     carry full provenance (file:line evidence on every edge).
//
// Implementations must be safe for concurrent use; a single SymbolExtractor value
// may be handed to many goroutines.
type SymbolExtractor interface {
	// Language returns the canonical language identifier this extractor handles
	// (e.g. "go"), matching the owning Parser's Language().
	Language() string

	// Extract maps an already-parsed, backend-specific root handle into the shared
	// graph vocabulary. filename is the originally supplied source path (used for
	// the file node and file:line provenance). root is the ParseResult.Root handle
	// for this language (e.g. *goAST for Go); an extractor type-asserts it to its
	// own backend type and returns an error on a mismatch.
	//
	// It returns the in-file nodes/edges it can prove, plus the PendingRefs it
	// recorded but deliberately did not resolve.
	Extract(filename string, root any) (nodes []model.Node, edges []model.Edge, pending []PendingRef, err error)
}

// goSymbolExtractor is the reference SymbolExtractor: it adapts the existing,
// battle-tested Go extraction (extractGo over go/ast) to the language-neutral seam.
// It carries no mutable state and is safe for concurrent use.
//
// This is an EXTRACTION, not a rewrite: the actual graph derivation still lives in
// extract_go.go (goExtractor); goSymbolExtractor only threads the *goAST handle
// through the new interface, which is why all existing TestExtractGo_* tests stay
// green unchanged.
type goSymbolExtractor struct{}

// Language implements SymbolExtractor.
func (goSymbolExtractor) Language() string { return "go" }

// Extract implements SymbolExtractor for the Go path. It expects root to be the
// *goAST produced by GoParser.Parse (which carries the FileSet and *ast.File),
// derives the package name from file.Name.Name, and delegates to extractGo. A
// nil/wrong-typed root is a programmer error and returns a descriptive error rather
// than panicking.
func (goSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	g, ok := root.(*goAST)
	if !ok || g == nil || g.File == nil || g.FileSet == nil {
		return nil, nil, nil, fmt.Errorf("parse: go extractor: expected non-nil *goAST root for %q, got %T", filename, root)
	}
	return extractGo(filename, g.File.Name.Name, g.FileSet, g.File)
}
