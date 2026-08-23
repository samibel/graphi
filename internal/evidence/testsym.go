package evidence

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// SymbolResolver answers "is SymbolName declared in this Go file?" for the AC-5
// rule. The mechanism is go/ast, and the choice was made in the open:
//
//   - go/ast is STDLIB. internal/evidence and cmd/evidence declare themselves
//     stdlib-only (cmd/evidence/main.go's package doc), and AC-14 binds the
//     choice: whatever resolves a symbol must not add a non-stdlib import to
//     internal/evidence. go/ast satisfies that with no trade-off to state.
//   - graphi's own index (the dogfooding alternative) would couple the honesty
//     gate to the artifact it audits: if graphi's resolver is wrong, the gate is
//     wrong in the SAME direction, and the auditor stops being independent. That
//     is the wrong direction for a gate whose entire purpose is to catch claims
//     the project made about itself.
//   - The dogfooding signal is not lost: `internal/evidencedogfood` asserts that
//     graphi's own parser agrees with go/ast on the whole governed citation set.
//     It lives in a SEPARATE package so internal/evidence — including its test
//     binary — keeps the stdlib-only property AC-14 requires.
//
// Resolution is over the COMMITTED bytes of the file, not the worktree copy, so a
// symbol that only exists in an uncommitted edit does not satisfy a citation.
type SymbolResolver struct {
	git   *Git
	cache map[string]map[string]bool
}

// NewSymbolResolver binds a resolver to a repository.
func NewSymbolResolver(g *Git) *SymbolResolver {
	return &SymbolResolver{git: g, cache: map[string]map[string]bool{}}
}

// Declares reports whether name is declared at the top level of the Go file at
// path (as committed at HEAD). Functions, methods, types, constants and variables
// all count: records cite all five shapes, and "is this name declared here" is the
// question a reader following the citation is asking.
func (r *SymbolResolver) Declares(path, name string) (bool, error) {
	if !strings.HasSuffix(path, ".go") {
		return false, fmt.Errorf("evidence: symbol citation %s::%s does not name a Go file", path, name)
	}
	decls, ok := r.cache[path]
	if !ok {
		src, err := r.git.FileAtHEAD(path)
		if err != nil {
			return false, err
		}
		decls, err = DeclaredSymbols(path, src)
		if err != nil {
			return false, err
		}
		r.cache[path] = decls
	}
	return decls[name], nil
}

// DeclaredSymbols parses Go source and returns the set of top-level declared
// names. Exported so the dogfooding cross-check can compare against it without
// re-implementing the walk.
func DeclaredSymbols(filename string, src []byte) (map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("evidence: parse %s: %w", filename, err)
	}
	out := map[string]bool{}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			out[decl.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = true
					}
				}
			}
		}
	}
	return out, nil
}
