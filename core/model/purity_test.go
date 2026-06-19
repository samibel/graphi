package model_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings are package-path fragments that core/model, as a
// PURE LEAF, must never import. Importing any of these would invert graphi's
// strict cmd -> surfaces -> engine -> core dependency direction.
var forbiddenImportSubstrings = []string{
	"core/parse",
	"core/graphstore",
	"engine/",
	"surfaces/",
	"cmd/",
}

// TestLeafPurity statically asserts that no source file in this package (test
// files excluded) imports a forbidden higher layer. This is the CI gate for
// core-leaf purity; it parses imports directly rather than trusting the build.
func TestLeafPurity(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Clean(name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenImportSubstrings {
				if strings.Contains(p, bad) {
					t.Errorf("%s imports forbidden higher layer %q (matches %q): core/model must remain a pure leaf", path, p, bad)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files found to check")
	}
}
