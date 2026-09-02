package retrieval_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestProductionSurfaces_DoNotSelectExperimentalFusionModes proves the RRF
// modes have callers only in the evaluator and differential diagnostic. The
// production adapter separately rejects their numeric values.
func TestProductionSurfaces_DoNotSelectExperimentalFusionModes(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	allowed := map[string]bool{
		"internal/eval/retrieval": true,
		"cmd/differential":        true,
	}
	var violations []string
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(filename, ".go") || strings.HasSuffix(filename, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		if allowed[pkgDir] || pkgDir == "engine/retrieval" {
			return nil
		}
		source, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if !strings.Contains(string(source), "ModeFusionNoGraph") && !strings.Contains(string(source), "ModeFusionGraph") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filename, source, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil || importPath != "github.com/samibel/graphi/engine/retrieval" {
				continue
			}
			alias := "retrieval"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = true
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "ModeFusionNoGraph" && sel.Sel.Name != "ModeFusionGraph") {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if ok && aliases[ident.Name] {
				pos := fset.Position(sel.Pos())
				violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+": "+sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production callers: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("production code selects evaluator-only RRF mode:\n%s", strings.Join(violations, "\n"))
	}
}

func TestProductionSurfaceScanCoversCompositionRoot(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "cmd", "internal", "runtime", "builder.go")); err != nil {
		t.Fatalf("production scan root does not contain composition root: %v", err)
	}
}
