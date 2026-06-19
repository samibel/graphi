// Package parse is the pure-leaf parsing boundary of graphi's core layer.
//
// It defines the canonical contract that turns source files into a normalized,
// language-tagged AST handle (ParseResult) plus a concurrency-safe Registry that
// maps file extensions / languages to Parser implementations.
//
// Layering rule (CI-enforced): core/parse is a PURE LEAF. It MUST NOT import any
// engine/ or surfaces/ package. It depends only on the Go standard library so the
// default build stays CGO_ENABLED=0 with zero outbound network and no eval/exec/shell.
//
// The registry is open/closed: new Parser implementations register against the
// stable Parser interface and become selectable for their languages without
// editing any existing parser code. Tree-sitter tier-1 grammars (and the opt-in
// CGO graphi-broad bundle) are expected to plug in through this same seam.
package parse

import (
	"context"
	"errors"

	"github.com/samibel/graphi/core/model"
)

// ErrNoParser is the typed sentinel returned when no parser is registered for a
// given file extension or language. Callers use errors.Is(err, ErrNoParser) to
// detect the miss path. Lookups that miss perform NO shared-state mutation and
// are idempotent on repeat calls — there is no panic and no partial state.
var ErrNoParser = errors.New("parse: no parser registered for file type")

// SourceMeta is the normalized source metadata attached to every ParseResult so
// downstream extract/link passes are decoupled from the grammar backend.
type SourceMeta struct {
	// Path is the originally supplied filename/path of the source.
	Path string
	// Language is the canonical language identifier the parser declares (e.g. "go").
	Language string
	// ContentHash is a deterministic hash of the source bytes. Identical input
	// always yields an identical hash, anchoring provenance determinism.
	ContentHash string
	// Size is the byte length of the parsed source.
	Size int
}

// ParseResult is the normalized, language-tagged parse output returned across the
// parse boundary. Root is an opaque, backend-specific AST handle (e.g. an
// *ast.File for the Go path); downstream consumers obtain it via type assertion
// keyed on Meta.Language, keeping this contract grammar-agnostic.
type ParseResult struct {
	// Meta carries path, language, and content-hash provenance.
	Meta SourceMeta
	// Root is the backend-specific AST root/handle. May be nil for parsers that
	// only expose structural metadata.
	Root any
	// Nodes and Edges are optional graph elements produced by parsers that also
	// perform extraction. When populated, ingest pipelines can commit them
	// directly without a separate extraction pass.
	Nodes []model.Node
	Edges []model.Edge
	// References lists paths/files this source file depends on (imports, includes,
	// etc.). Ingest uses this to compute the reverse-dependency cascade.
	References []string
}

// Parser is the stable contract every language backend implements. Implementations
// must be safe for concurrent use: ParserFor may hand the same Parser to many
// goroutines simultaneously.
type Parser interface {
	// Language returns the canonical language identifier this parser produces.
	Language() string

	// Extensions returns the lowercase file extensions (with leading dot, e.g.
	// ".go") this parser handles. Used by the registry for selection.
	Extensions() []string

	// Parse turns src into a normalized ParseResult. It must honor ctx
	// cancellation, must never panic out to the caller (recovering from any
	// backend panic internally), and must be deterministic for identical input.
	Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error)
}
