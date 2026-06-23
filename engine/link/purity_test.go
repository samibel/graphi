package link_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImports are packages engine/link must not import: it is a PURE,
// store-free resolver. No I/O (os, net, os/exec), no graphstore, no surfaces,
// and no new module dependency — ingest owns every PutEdge / transaction
// concern. Allowed deps are the stdlib (minus the I/O set) plus core/model and
// core/parse.
var forbiddenImports = map[string]struct{}{
	"os":       {},
	"os/exec":  {},
	"net":      {},
	"net/http": {},
}

var forbiddenSubstrings = []string{
	"core/graphstore",
	"engine/ingest",
	"surfaces/",
	"cmd/",
}

func TestLinkPurity(t *testing.T) {
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
		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if _, bad := forbiddenImports[p]; bad {
				t.Errorf("%s imports forbidden I/O package %q: engine/link must stay pure & store-free", name, p)
			}
			for _, sub := range forbiddenSubstrings {
				if strings.Contains(p, sub) {
					t.Errorf("%s imports forbidden package %q (matches %q)", name, p, sub)
				}
			}
			// No third-party module dependency (anything with a dot before the
			// first slash that is not our own module) — FU-1 adds no new deps.
			if strings.Contains(p, ".") && !strings.HasPrefix(p, "github.com/samibel/graphi/") {
				t.Errorf("%s imports a non-stdlib, non-graphi dependency %q: engine/link must add no new module deps", name, p)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files found")
	}
}
