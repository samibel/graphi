package opcatalog

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// AC-4 — shadow mode is a mechanical property, not a promise.
//
// "No dispatch code path reads the catalog yet" is easy to assert in a story
// and easy to break with one import in a later PR that nobody notices, because
// nothing about the catalog's presence forces a reviewer to think about it.
// This test walks every non-test Go file in the module and requires that
// exactly zero of them — outside this package — import engine/opcatalog.
//
// It is NOT a permanent rule. AX-04 wires the executor, AX-05 projects the
// surface metadata, and both will legitimately import this package. At that
// point this test is DELETED as part of the story that makes it false, which
// is the point: the wiring becomes a visible, deliberate act instead of a
// silent one.
//
// Test files are exempt: the parity gates in surfaces/mcp and surfaces are
// exactly the consumers AX-03 is supposed to have.
func TestAX03_ShadowMode_NoProductionCodeImportsTheCatalog(t *testing.T) {
	root := moduleRootForTest(t)
	const catalogPkg = "github.com/samibel/graphi/engine/opcatalog"
	selfDir := filepath.Join(root, "engine", "opcatalog")

	var importers []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata", "web", "dist":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(path, selfDir+string(filepath.Separator)) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			// A file this walk cannot parse is not evidence of absence.
			t.Errorf("parse %s: %v", path, parseErr)
			return nil
		}
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			if imported == catalogPkg {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				importers = append(importers, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(importers)
	if len(importers) > 0 {
		t.Errorf("AX-03 is SHADOW MODE: no production code may read the operation catalog yet, "+
			"but these non-test files import it:\n  %s\n"+
			"If this is the story that wires the catalog in (AX-04 / AX-05), delete this test "+
			"as part of that change rather than exempting the file.",
			strings.Join(importers, "\n  "))
	}

	// Guard against the walk silently covering nothing.
	if !fileExists(filepath.Join(root, "surfaces", "mcp", "descriptors.go")) {
		t.Fatalf("the module walk did not find surfaces/mcp/descriptors.go under %q; "+
			"the scan is not looking where it thinks it is", root)
	}
	if _, err := os.Stat(filepath.Join(selfDir, "shadow.json")); err != nil {
		t.Fatalf("stat shadow.json: %v", err)
	}
}

// The catalog is at ENGINE rank and may depend only on the standard library and
// core/registry. cmd/layerguard catches an upward edge; it does not catch a
// sideways one into another engine package, which is what would quietly make
// this package unimportable from a lower layer later.
func TestAX03_Catalog_DependsOnlyOnStdlibAndCoreRegistry(t *testing.T) {
	const modulePath = "github.com/samibel/graphi"
	allowed := map[string]bool{modulePath + "/core/registry": true}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("%s: unquote %s: %v", name, spec.Path.Value, unquoteErr)
			}
			if !strings.HasPrefix(imported, modulePath+"/") {
				continue // stdlib or third party
			}
			if !allowed[imported] {
				t.Errorf("%s imports %q; engine/opcatalog may depend only on the standard "+
					"library and core/registry so every surface can import it", name, imported)
			}
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// moduleRootForTest walks up from the package directory to the go.mod that
// roots this module.
func moduleRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %q", dir)
		}
		dir = parent
	}
}
