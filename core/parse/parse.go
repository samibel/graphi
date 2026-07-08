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
	// PendingRefs are deferred references this file could NOT resolve on its own
	// (same-package cross-file names, and cross-package selector calls/refs). The
	// parse leaf only RECORDS them; the engine/link linker pass resolves them
	// against the fully-committed node set. Keeping resolution out of parse
	// preserves the CI-enforced pure-leaf boundary (see purity_test.go).
	PendingRefs []PendingRef
	// Imports lists the import declarations of this file (alias + path), used by
	// the linker to map a selector alias to its package.
	Imports []ImportSpec
}

// PendingRef is a single reference (call or non-call use) that the parse leaf
// recorded but deliberately did NOT resolve. The linker pass turns it into a
// provenanced cross-file/cross-package edge once every endpoint is committed.
//
// It is an inert data record: it holds only the information the resolver needs
// and carries no NodeIds (those are minted/looked up by the linker against the
// committed node set), so the parse boundary never fabricates an endpoint.
type PendingRef struct {
	// FromQN is the qualified name of the enclosing symbol that owns the
	// reference (the edge's "from"), e.g. "shop.checkout".
	FromQN string
	// Name is the referenced bare name. For a selector (Selector=true) it is the
	// trailing selector identifier (e.g. "Fn" in "pkg.Fn"); SelectorBase carries
	// the leading qualifier.
	Name string
	// SelectorBase is the leading qualifier of a selector reference (e.g. "pkg"
	// or a receiver name "x" in "x.Method"); empty for a bare-ident reference.
	SelectorBase string
	// Kind is the edge kind to emit on resolution ("calls" or "references").
	Kind string
	// Line is the 1-based source line of the reference, used for file:line
	// evidence.
	Line int
	// Selector reports whether the reference was a selector expression
	// (alias.Name / recv.Method) versus a bare identifier.
	Selector bool
	// ReceiverType is the syntactically-resolved "<importPath>.<TypeName>" of a
	// selector call's receiver variable when its declared type is known from the
	// AST (a function/method parameter or method receiver typed as `[*]alias.T`
	// with alias in the file's imports), e.g. "database/sql.DB" for a `db *sql.DB`
	// parameter. It is empty when the receiver type is not syntactically known
	// (short-var-decls, composite types, same-package bare-ident types, chained
	// selectors). The linker uses it to mint a PRECISE external method node
	// ("database/sql.DB.Query") that config sinks can match; without it the call
	// stays an honest skip. Only populated for selector call refs (Kind=="calls").
	ReceiverType string
}

// ImportSpec is one import declaration: the local alias (empty for the default
// package-name binding) and the imported path. The linker maps a selector's
// leading qualifier to a package via these specs.
type ImportSpec struct {
	// Alias is the explicit local name ("" when none, "." for a dot-import, "_"
	// for a blank import). Dot and blank imports never produce a resolvable
	// selector and are skipped by the linker.
	Alias string
	// Path is the imported package import path (e.g. "fmt", "github.com/x/y").
	Path string
	// Wildcard marks an on-demand / star import (Java `import com.a.b.*;`, Kotlin
	// `import com.a.b.*`). For these the Path IS the package itself, so the linker
	// must not strip a trailing type segment to derive the package (WP-01).
	Wildcard bool
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

	// Runtime returns the provenance marker for this parser's parsing backend
	// (go/ast, stdlib, gotreesitter, …). It is the first-class hook the no-CGO
	// default-tier guard (AssertPureGoDefaults) inspects to assert purity directly
	// at the registration layer. Implementations declare a constant — Runtime
	// carries no per-call state.
	Runtime() Runtime

	// Parse turns src into a normalized ParseResult. It must honor ctx
	// cancellation, must never panic out to the caller (recovering from any
	// backend panic internally), and must be deterministic for identical input.
	Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error)
}
