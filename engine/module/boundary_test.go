package module_test

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

const modulePath = "github.com/samibel/graphi"

// TestAX07_OnlyTheCompositionRootImportsTheModuleSet is the boundary this story
// adds, and it is the mechanical form of the package doc's claim that the
// builder is not a service locator.
//
// The failure mode it exists to catch is specific. A module builder is a very
// convenient thing to reach for: any package that wants "the parser registry" or
// "the analyzer service" could import engine/module and build its own. The
// moment two packages do that, there are two compositions again — which is the
// exact condition AX-07 exists to remove, reintroduced through the door AX-07
// installed. So the rule is: exactly one importer, named here, and the check
// runs in both directions so a stale entry cannot license a new one.
//
// This is deliberately narrower than what the layer guard can see.
// cmd/internal/runtime → engine/module is a perfectly legal downward edge, so
// cmd/layerguard is blind to a second, equally legal importer appearing beside
// it.
func TestAX07_OnlyTheCompositionRootImportsTheModuleSet(t *testing.T) {
	root := repoRootForTest(t)
	const modulePkg = modulePath + "/engine/module"
	selfDir := filepath.Join(root, "engine", "module")

	// The one declared importer: the composition root.
	declared := map[string]string{
		filepath.Join("cmd", "internal", "runtime", "builder.go"): "the RuntimeBuilder — the composition root itself",
	}

	importers := importersOf(t, root, selfDir, modulePkg)
	var undeclared []string
	for _, rel := range importers {
		if _, ok := declared[rel]; !ok {
			undeclared = append(undeclared, rel)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Errorf("these non-test files import the module set without being declared:\n  %s\n"+
			"engine/module composes the runtime; it is not a service locator to fetch registries from. "+
			"A second importer means a second composition, which is the condition AX-07 removed. If a "+
			"package genuinely needs a frozen registry, it should be HANDED one by the composition root "+
			"(cmd/internal/runtime.Composition) rather than building its own.",
			strings.Join(undeclared, "\n  "))
	}
	for rel, reason := range declared {
		found := false
		for _, importer := range importers {
			if importer == rel {
				found = true
			}
		}
		if !found {
			t.Errorf("declared module-set importer %q (%s) no longer imports it — a stale entry here "+
				"licenses an undeclared importer to take its place unnoticed", rel, reason)
		}
	}
	// Non-vacuity: if the walk found nothing, it is not proving a boundary.
	if len(importers) == 0 {
		t.Fatalf("the module walk found no importer of %q under %q; the scan is not looking where it thinks it is", modulePkg, root)
	}
}

// TestAX07_ModuleSet_DependsOnlyOnEngineRankCapabilityPackages keeps the builder
// at engine rank and keeps its dependency list reviewable.
//
// cmd/layerguard would catch an upward edge into surfaces or cmd. It would not
// catch this package quietly acquiring a dependency on, say, engine/ingest —
// which is legal by rank and would nonetheless make the module set unusable from
// the place ingest is constructed. Each allowed import below is one contribution
// kind or one input type, so the list doubles as the inventory of what a module
// may contribute.
func TestAX07_ModuleSet_DependsOnlyOnEngineRankCapabilityPackages(t *testing.T) {
	allowed := map[string]string{
		modulePath + "/core/parse":                 "the parser contribution kind",
		modulePath + "/core/registry":              "the shared lifecycle vocabulary",
		modulePath + "/engine/analysis":            "the analyzer contribution kind",
		modulePath + "/engine/analysis/githistory": "the git-provider input type",
		modulePath + "/engine/opcatalog":           "the operation contribution kind",
		modulePath + "/engine/query":               "the graph-reader input type and the graph.query port type",
		modulePath + "/engine/query/compound":      "the engine.compound built-in module's handler",
		modulePath + "/engine/typeresolve":         "the resolver contribution kind",
		// SW-255 (AX-15): the first handler-bearing built-in module and the
		// second typed port. engine/search is a PORT type (Inputs.GraphSearch,
		// Ports.GraphSearch), not a contribution kind; the handler packages below
		// implement the two built-in operations that execute in engine.
		modulePath + "/engine/search":              "the graph.search port type",
		modulePath + "/engine/agenttools/deadcode": "the engine.deadcode built-in module's handler",
	}

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
			if _, ok := allowed[imported]; !ok {
				t.Errorf("%s imports %q, which is not a declared contribution kind or input type. "+
					"engine/module must stay a thin composer over the capability registries; a new "+
					"dependency here is a new kind of thing modules can contribute, and it is declared "+
					"in this list in the same change.", name, imported)
			}
		}
	}
}

// importersOf returns the repo-relative paths of every non-test Go file outside
// selfDir that imports pkg.
func importersOf(t *testing.T, root, selfDir, pkg string) []string {
	t.Helper()
	var out []string
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
			if imported == pkg {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				out = append(out, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// repoRootForTest walks up from the package directory to the go.mod that roots
// this module.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %q", dir)
		}
		dir = parent
	}
}
