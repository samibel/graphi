package archmatrix

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// surfacePackages are the shipped entry adapters whose handlers must migrate off
// the broad client contract. surfaces/client itself is excluded: it is the seam
// being dismantled, not a consumer of it.
var surfacePackages = []struct {
	name    string
	relPath string
}{
	{name: "cli", relPath: "surfaces/cli"},
	{name: "mcp", relPath: "surfaces/mcp"},
	{name: "http", relPath: "surfaces/http"},
	{name: "tui", relPath: "surfaces/tui"},
	{name: "daemon", relPath: "surfaces/daemon"},
}

// Usage maps a Client method name to the surfaces that call it today.
type Usage map[string][]string

// ScanSurfaceUsage derives, per Client method, which surface packages invoke it.
//
// This column is derived rather than hand-written for the same reason the method
// set is: a hand-kept list of "who calls what" is wrong the first time a handler
// moves, and the whole point of the matrix is to track handlers moving. The scan
// matches call expressions by method name, which is a deliberate over-approximation
// — a same-named call on some other type would be counted. That trades a possible
// extra entry for never missing a real consumer, which is the safe direction for a
// migration inventory.
func ScanSurfaceUsage(moduleRoot string) (Usage, error) {
	methods := make(map[string]bool)
	for _, name := range LiveMethods() {
		methods[name] = true
	}

	found := map[string]map[string]bool{}
	for _, surface := range surfacePackages {
		dir := filepath.Join(moduleRoot, filepath.FromSlash(surface.relPath))
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			return nil, fmt.Errorf("archmatrix: parse %s: %w", surface.relPath, err)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				ast.Inspect(file, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !methods[sel.Sel.Name] {
						return true
					}
					// A method declaration on the surface's own type is an
					// implementation, not a consumption; only calls reach here.
					if found[sel.Sel.Name] == nil {
						found[sel.Sel.Name] = map[string]bool{}
					}
					found[sel.Sel.Name][surface.name] = true
					return true
				})
			}
		}
	}

	usage := Usage{}
	for method, surfaces := range found {
		names := make([]string, 0, len(surfaces))
		for name := range surfaces {
			names = append(names, name)
		}
		sort.Strings(names)
		usage[method] = names
	}
	return usage, nil
}

// For returns the surfaces calling method, or a placeholder when none do.
func (u Usage) For(method string) string {
	if len(u[method]) == 0 {
		return "—"
	}
	return strings.Join(u[method], ", ")
}
