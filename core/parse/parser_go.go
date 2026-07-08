package parse

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// GoParser is the native, high-precision Go parser built entirely on the standard
// library (go/parser, go/ast, go/token). It is fully CGo-free and deterministic.
//
// It is registered like any other parser (see RegisterDefaults); routing ".go"
// files to it is ordinary registration, not a special case, which preserves the
// open/closed property of the Registry.
//
// Symbol extraction runs through the language-neutral SymbolExtractor seam (SW-052):
// GoParser produces the normalized *goAST handle, then delegates graph derivation to
// the Go reference SymbolExtractor. The two concerns — parsing (text→AST) and
// extraction (AST→graph) — are separated so later language workers reuse the same
// seam without touching this parser.
type GoParser struct {
	// extractor is the SymbolExtractor for the Go path. It is set once at
	// construction (the reference goSymbolExtractor) and never mutated, keeping
	// GoParser safe for concurrent use.
	extractor SymbolExtractor
}

// NewGoParser returns a ready GoParser wired to the Go reference SymbolExtractor.
// GoParser carries no mutable state and is safe for concurrent use.
func NewGoParser() *GoParser { return &GoParser{extractor: goSymbolExtractor{}} }

// Language implements Parser.
func (*GoParser) Language() string { return "go" }

// Runtime implements Parser: GoParser is the native go/ast path (pure Go).
func (*GoParser) Runtime() Runtime { return RuntimeGoAST }

// Extensions implements Parser.
func (*GoParser) Extensions() []string { return []string{".go"} }

// goAST is the typed payload placed in ParseResult.Root for Go sources. Keeping a
// dedicated struct (rather than a bare *ast.File) lets the Go precision path carry
// the FileSet needed to resolve positions without leaking it through the generic
// contract.
type goAST struct {
	FileSet *token.FileSet
	File    *ast.File
}

// Parse implements Parser. It parses src as Go source with comments retained and
// returns a normalized ParseResult whose Root is a *goAST. It honors ctx
// cancellation and recovers from any unexpected panic in the parser so a single
// malformed file can never crash the caller. This is one layer of a two-layer
// guard: this recover, plus the engine/ingest fail-closed resource bounds
// (SW-055: max file size enforced before read, parse timeout via ctx, and — for
// the gotreesitter parsers — CST nesting depth) that skip an over-bound file with
// a structured diagnostic rather than parse it.
func (g *GoParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}

	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if perr != nil {
		// Syntax errors are returned as ordinary errors (not ErrNoParser): a
		// parser WAS selected, the source was simply invalid.
		return nil, fmt.Errorf("parse: go syntax error in %q: %w", filename, perr)
	}

	root := &goAST{FileSet: fset, File: file}

	// Derive the in-file graph elements (symbol nodes + intra-file edges) plus the
	// deferred references the linker resolves later through the language-neutral
	// SymbolExtractor seam. The extractor is pure and resolves only what a single
	// file proves; cross-file/cross-package edges are left to the engine/link pass
	// (see extract_go.go / extractor.go).
	extractor := g.extractor
	if extractor == nil {
		extractor = goSymbolExtractor{}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: go extraction in %q: %w", filename, xerr)
	}

	// Collect imports (alias + path) for the linker's selector resolution and
	// populate References so ingest's reverse-dependency cascade can treat
	// imported packages as forward dependencies.
	imports := goImports(file)
	refs := make([]string, 0, len(imports))
	for _, imp := range imports {
		refs = append(refs, imp.Path)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path:        filename,
			Language:    g.Language(),
			ContentHash: contentHash(src),
			Size:        len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
		Imports:     imports,
		References:  refs,
	}, nil
}

// GoAST returns the *ast.File and *token.FileSet backing a Go ParseResult, when
// res was produced by GoParser (its Root is the internal *goAST handle). It lets
// an ENGINE-level consumer (e.g. engine/analysis/intraproctaint) run a pure
// go/ast analysis over the already-parsed file without re-parsing. It returns
// ok=false for a nil result or any non-Go backend, so callers degrade cleanly.
// This exposes only the standard-library AST types (go/ast, go/token); core/parse
// stays a pure leaf and takes on no new dependency.
func GoAST(res *ParseResult) (*ast.File, *token.FileSet, bool) {
	if res == nil {
		return nil, nil, false
	}
	root, ok := res.Root.(*goAST)
	if !ok || root == nil || root.File == nil || root.FileSet == nil {
		return nil, nil, false
	}
	return root.File, root.FileSet, true
}

// goImports extracts the import declarations of a Go file as ImportSpecs. The
// alias is the explicit local name when present ("." for dot-imports, "_" for
// blank imports, "" for the default package-name binding). Paths are unquoted.
// Order follows source order; the linker is order-independent regardless.
func goImports(file *ast.File) []ImportSpec {
	out := make([]ImportSpec, 0, len(file.Imports))
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			path = strings.Trim(imp.Path.Value, `"`)
		}
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		out = append(out, ImportSpec{Alias: alias, Path: path})
	}
	return out
}
