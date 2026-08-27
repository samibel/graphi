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
//
// AX-06 (SW-226) is the other widening AX-04 predicted, and it is worth being
// precise about what it did and did not move, because the obvious reading is
// wrong.
//
//   - ONE reader is added: surfaces/client/canary.go, the canary's dual-run
//     composition. It resolves the canary's catalog contribution and drives the
//     AX-04 executor.
//   - The DENY list is NOT touched. Every entry in it is still forbidden and
//     still true. Dispatch for the dead_code canary now reaches the executor,
//     but it does so by calling one surfaces/client function; toolcalls.go and
//     handlers.go still do not import this package, so the property AX-05 gated
//     — the operation catalog stays behind surfaces/client and a request-serving
//     file never reads it directly — survives the canary intact. Deleting a deny
//     entry "because AX-06 said dispatch would cross" would have thrown away a
//     live guarantee to pay for a crossing that did not happen.
//   - A SECOND gate is added instead, TestAX06_OnlyTheCanaryDispatchesThroughTheExecutor
//     below, because the crossing that DID happen needs its own rule. The
//     executor-dispatch seam is a different boundary from the catalog-import
//     boundary, and an import-based test cannot see it at all: a file that calls
//     client.DispatchCanary imports surfaces/client, which two dozen files
//     already do for unrelated reasons.
func TestAX04_OnlyTheExecutorReadsTheCatalog(t *testing.T) {
	root := moduleRootForTest(t)
	const catalogPkg = "github.com/samibel/graphi/engine/opcatalog"
	selfDir := filepath.Join(root, "engine", "opcatalog")

	// The declared production readers, as relative paths. Adding one is a
	// deliberate act; the failure below explains what adding it means.
	allowedImporters := map[string]bool{
		filepath.Join("surfaces", "client", "executor.go"):           true,
		filepath.Join("surfaces", "client", "canary.go"):             true,
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
			t.Errorf("%s (%s) reads the operation catalog. A request-serving file must reach the "+
				"catalog through surfaces/client, never by importing it: that is what keeps the "+
				"operation catalog one seam instead of a dependency every surface grows its own "+
				"opinion about. AX-06 moved the dead_code canary's DISPATCH onto the executor "+
				"WITHOUT needing this entry removed — it calls client.DispatchCanary — so a later "+
				"story that thinks it needs the entry gone should first check whether it is really "+
				"solving the same problem AX-06 solved.", importer, reason)
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
	// The deny list may grow. It may not shrink by accident: these four are the
	// request-serving files AX-05 named, and AX-06 kept every one of them.
	// Removing one is a deliberate act that has to edit this list too.
	for _, required := range []string{
		filepath.Join("surfaces", "mcp", "toolcalls.go"),
		filepath.Join("surfaces", "mcp", "session.go"),
		filepath.Join("surfaces", "http", "handlers.go"),
		filepath.Join("surfaces", "http", "routes.go"),
	} {
		if _, denied := forbiddenImporters[required]; !denied {
			t.Errorf("%s is no longer denied. Dispatch reaching the catalog directly is the "+
				"regression this list exists to catch, and it survived AX-06 — a story that "+
				"drops it is removing a guarantee, not updating a stale entry.", required)
		}
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

// TestAX06_OnlyTheCanaryDispatchesThroughTheExecutor is the second half of the
// AX-06 boundary, and the one that watches the crossing this story actually
// made.
//
// TestAX04 above watches who IMPORTS the catalog. That rule survived AX-06
// unchanged, which is exactly why it cannot be the gate for AX-06: the canary
// crosses into the executor by CALLING client.DispatchCanary, and a file that
// does so imports surfaces/client — something most of the tree already does.
// An import-shaped test is blind to it.
//
// So the crossing gets its own explicit list. Exactly two request-serving files
// may reach the executor-dispatch seam, they are named here, and a third one
// fails this test. The check runs in both directions: an undeclared caller is a
// failure, and a declared caller that no longer contains the call is also a
// failure, because a stale entry would quietly license the next one.
//
// The seam is named by its identifier rather than by a package import so the
// rule survives a rename of the file it lives in.
func TestAX06_OnlyTheCanaryDispatchesThroughTheExecutor(t *testing.T) {
	root := moduleRootForTest(t)
	const seam = "DispatchCanary"

	// The declared dispatch sites: the MCP tools/call arm and the HTTP analyze
	// arm for the ONE canary operation. Adding a third means a second operation
	// is migrating, which is SW-228's job and needs SW-228's evidence.
	declared := map[string]string{
		filepath.Join("surfaces", "mcp", "toolcalls.go"): "MCP tools/call — the dead_code arm",
		filepath.Join("surfaces", "http", "handlers.go"): "HTTP /analyze — the dead_code arm",
		filepath.Join("surfaces", "client", "canary.go"): "the seam's own definition",
	}

	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			return nil
		}
		if !strings.Contains(string(src), seam) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		found[rel] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	var undeclared []string
	for rel := range found {
		if _, ok := declared[rel]; !ok {
			undeclared = append(undeclared, rel)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Errorf("these non-test files reach the executor dispatch seam without being declared:\n  %s\n"+
			"AX-06 migrates exactly ONE operation (dead_code) and says so in its scope: no bulk "+
			"migration, no second operation. If this is the story that migrates more (SW-228/AX-08), "+
			"it owes the same dual-run parity evidence per operation that AX-06 owed for one, and it "+
			"widens this list in the same change.", strings.Join(undeclared, "\n  "))
	}
	for rel, reason := range declared {
		if !found[rel] {
			t.Errorf("declared canary dispatch site %q (%s) no longer reaches the seam — a stale "+
				"entry here licenses an undeclared caller to take its place unnoticed", rel, reason)
		}
	}
	// Non-vacuity: if the scan found nothing at all, it is not proving a
	// boundary, it is proving that it looked in the wrong tree.
	if len(found) == 0 {
		t.Fatalf("the module walk found no reference to %q under %q; the scan is not looking "+
			"where it thinks it is", seam, root)
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
