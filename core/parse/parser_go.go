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
type GoParser struct{}

// NewGoParser returns a ready GoParser. GoParser carries no mutable state and is
// safe for concurrent use.
func NewGoParser() *GoParser { return &GoParser{} }

// Language implements Parser.
func (*GoParser) Language() string { return "go" }

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
// malformed file can never crash the caller (two-layer guard: this recover plus
// the engine-side timeout/size guard).
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

	// Derive the in-file graph elements (symbol nodes + intra-file edges) plus the
	// deferred references the linker resolves later. The extractor is pure and
	// resolves only what a single file proves; cross-file/cross-package edges are
	// left to the engine/link pass (see extract_go.go).
	nodes, edges, pending, xerr := extractGo(filename, file.Name.Name, fset, file)
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
		Root:        &goAST{FileSet: fset, File: file},
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
		Imports:     imports,
		References:  refs,
	}, nil
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
