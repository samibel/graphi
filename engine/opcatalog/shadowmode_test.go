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

// AX-04 (SW-224) narrows AX-03's shadow-mode gate rather than deleting it.
//
// AX-03 required that ZERO non-test files import this package, and said in its
// own failure message that the story which wires the catalog in should replace
// the rule instead of exempting a file. This is that story, and this is that
// replacement: the catalog now has exactly ONE production reader, the AX-04
// executor, and the property worth keeping is that the list stays explicit.
//
// The point is unchanged from AX-03 — wiring the catalog into a new place must
// be a visible, deliberate act rather than a silent import. AX-05 (surface
// metadata projection) and AX-06 (the first dispatching canary) will each
// legitimately add a reader, and each of them widens this list on purpose, in
// the diff, where a reviewer sees it.
//
// Test files stay exempt: the parity gates in surfaces/mcp and surfaces are
// exactly the consumers the catalog is supposed to have.
//
// AX-05 (SW-225) is one of the widenings AX-04 predicted, and it widens the list
// in two directions at once so the rule ends up STRONGER, not merely larger:
//
//   - three declared readers are added, each a metadata-projection file that
//     serves no request: the MCP descriptor projection, the HTTP capability-list
//     projection, and the comparison-only CLI help generator; and
//   - a DENY list is added beside the allow list. AX-05's AC-4 says no tool-call
//     or HTTP handler dispatch may read the catalog while the projection is
//     live. An allow-list alone cannot express that: a future story could widen
//     it to include a dispatch file and this test would go green. The named
//     dispatch sites below therefore fail even if someone also adds them above,
//     which turns "dispatch stays legacy" from a convention into a gate.
func TestAX04_OnlyTheExecutorReadsTheCatalog(t *testing.T) {
	root := moduleRootForTest(t)
	const catalogPkg = "github.com/samibel/graphi/engine/opcatalog"
	selfDir := filepath.Join(root, "engine", "opcatalog")

	// The declared production readers, as relative paths. Adding one is a
	// deliberate act; the failure below explains what adding it means.
	allowedImporters := map[string]bool{
		filepath.Join("surfaces", "client", "executor.go"):           true,
		filepath.Join("surfaces", "mcp", "descriptors_projected.go"): true,
		filepath.Join("surfaces", "http", "contract_projected.go"):   true,
		filepath.Join("cmd", "graphi", "help_catalog.go"):            true,
	}

	// AX-05 AC-4: dispatch stays legacy. These files serve requests, and none of
	// them may read the catalog for as long as that AC is in force — not even by
	// being added to allowedImporters, which is why the check is separate.
	forbiddenImporters := map[string]string{
		filepath.Join("surfaces", "mcp", "toolcalls.go"): "MCP tools/call dispatch",
		filepath.Join("surfaces", "mcp", "session.go"):   "MCP session/bind handling",
		filepath.Join("surfaces", "http", "handlers.go"): "HTTP request handlers",
		filepath.Join("surfaces", "http", "routes.go"):   "HTTP routing and the SAFE-01 capability guard",
	}

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
	var undeclared []string
	for _, importer := range importers {
		if reason, forbidden := forbiddenImporters[importer]; forbidden {
			t.Errorf("%s (%s) reads the operation catalog. AX-05 AC-4 keeps DISPATCH on the legacy "+
				"path while only advertised metadata is projected; a request-serving file reading "+
				"the catalog is the boundary this story is defined by. If a later story deliberately "+
				"moves dispatch onto the catalog (AX-06's canary), remove the entry from "+
				"forbiddenImporters in that change so the crossing is reviewed.", importer, reason)
			continue
		}
		if !allowedImporters[importer] {
			undeclared = append(undeclared, importer)
		}
	}
	if len(undeclared) > 0 {
		t.Errorf("these non-test files read the operation catalog without being declared "+
			"readers:\n  %s\n"+
			"The catalog is the single source of operation identity, so every place that reads "+
			"it is part of the migration's surface area. If this is the story that adds the "+
			"reader (AX-05's projection, AX-06's canary), add it to allowedImporters in the "+
			"same change so the widening is reviewed rather than absorbed.",
			strings.Join(undeclared, "\n  "))
	}
	for declared := range allowedImporters {
		if !fileExists(filepath.Join(root, declared)) {
			t.Errorf("declared catalog reader %q does not exist — a stale entry here would let "+
				"a real one slip in unnoticed", declared)
		}
	}
	for forbidden := range forbiddenImporters {
		if !fileExists(filepath.Join(root, forbidden)) {
			t.Errorf("forbidden catalog reader %q does not exist — a stale deny entry protects "+
				"nothing and hides the file that replaced it", forbidden)
		}
	}
	if !allowedImporters[filepath.Join("surfaces", "client", "executor.go")] {
		t.Error("the AX-04 executor is no longer a declared reader; this test may not be narrowed " +
			"by dropping the reader it was written for")
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
