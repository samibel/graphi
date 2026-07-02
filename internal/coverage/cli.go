package coverage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
)

// cliMainPath is the repo-relative path of the single CLI dispatch point. Every
// user-facing `graphi <subcommand>` is a case label in main()'s switch over
// os.Args[1], so statically enumerating those labels IS the live subcommand set.
const cliMainPath = "cmd/graphi/main.go"

// enumerateCLISubcommands extracts the subcommand case labels from the dispatch
// switch over os.Args[1] in cmd/graphi/main.go via a read-only go/ast scan (the
// same static-scan posture as internal/cgoconformance). This closes the drift
// class where a subcommand is documented but never wired into the dispatch — a
// matrix row marked shipped with no matching case label now fails the build.
// Dynamic short verbs (queryVerbSet / analyzeVerbSet aliases) are intentionally
// excluded: they rewrite onto `query`/`analyze` and are covered by the analyzer
// category.
func enumerateCLISubcommands() ([]string, error) {
	return EnumerateCLISubcommands()
}

// EnumerateCLISubcommands is the exported form of the dispatch-switch scan, so
// cmd/graphi's help-coverage test can assert every dispatched subcommand has a
// help entry without duplicating the AST scan.
func EnumerateCLISubcommands() ([]string, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(cliMainPath))
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("coverage: parse %s: %w", cliMainPath, err)
	}
	var subs []string
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || !isOsArgs1(sw.Tag) {
			return true
		}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, e := range cc.List {
				lit, ok := e.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if s, uerr := strconv.Unquote(lit.Value); uerr == nil && s != "" {
					subs = append(subs, s)
				}
			}
		}
		return false
	})
	if len(subs) == 0 {
		return nil, fmt.Errorf("coverage: no `switch os.Args[1]` dispatch with string cases found in %s (dispatch moved? update internal/coverage/cli.go)", cliMainPath)
	}
	sort.Strings(subs)
	return subs, nil
}

// isOsArgs1 reports whether e is exactly the expression `os.Args[1]`.
func isOsArgs1(e ast.Expr) bool {
	ix, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := ix.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Args" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "os" {
		return false
	}
	lit, ok := ix.Index.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "1"
}
