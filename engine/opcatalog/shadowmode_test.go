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
//     the dispatch seam imports surfaces/client, which two dozen files
//     already do for unrelated reasons.
//
// AX-07 (SW-227) widens the allow list by two files — engine/module — and, like
// AX-05, widens the rule in two directions at once rather than merely growing it:
//
//   - the two added readers are the built-in module set and the builder's typed
//     AddOperation. Neither serves a request; both run once, at startup, inside
//     the composition root's build; and
//   - engine/module carries a boundary test of its OWN
//     (engine/module/boundary_test.go) declaring that exactly one non-test file
//     in the tree may import it — cmd/internal/runtime/builder.go. So the
//     catalog's new reader is itself reachable from exactly one place, which is
//     a stronger statement than "this file may read the catalog": it bounds who
//     can reach the reader, not only who the reader is.
//
// AX-10 (SW-230) widens it by two more — engine/extpack/conformance — and the
// direction of that reader is worth naming because it is the opposite of every
// other entry: the harness is a CONSUMER of specs it is handed, not a producer
// of specs the tree serves. It holds no catalog, registers nothing, and appears
// in no request path. See the entry's own comment.
//
// The DENY list is untouched, again. Dispatch still does not read the catalog.
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
		// AX-07 (SW-227): the built-in module set contributes the catalog's
		// specs into the runtime composition, and the builder's typed
		// AddOperation takes an OperationSpec. Both are composition, not
		// request serving — engine/module runs once at startup and is itself
		// importable only by cmd/internal/runtime (enforced by
		// engine/module/boundary_test.go). It is a reader in the ADR-0013
		// tier-B sense: first-party Go, statically compiled, no dispatch.
		filepath.Join("engine", "module", "module.go"):  true,
		filepath.Join("engine", "module", "builtin.go"): true,
		// AX-10 (SW-230): the extension conformance harness. It READS a spec it
		// is handed and never registers one — VerifyContribution takes an
		// OperationSpec as a parameter and the package holds no catalog at all,
		// so it cannot mint, mutate or advertise operation identity. It serves
		// no request either: nothing in the shipped request path imports it
		// (`graphi extension conform` is a developer verb, and everything else
		// that calls it is a _test.go file). It is here because a harness that
		// checks a spec against the catalog VOCABULARY — ports, permissions,
		// tiers, determinism classes — has to be able to name that vocabulary,
		// and the alternative is a second copy of it, which is the drift the
		// catalog exists to remove.
		filepath.Join("engine", "extpack", "conformance", "contribution.go"): true,
		filepath.Join("engine", "extpack", "conformance", "ports.go"):        true,
		// AX-11 (SW-231): the DISPOSABLE tier-C process-extension spike, whose
		// go/no-go is recorded as NO-GO in
		// docs/decisions/2026-08-process-extension-go-no-go.md.
		//
		// It reads the catalog for one thing only: the Port and Permission
		// vocabularies. A tier-C descriptor declares the host ports an extension
		// may reach and re-derives its permissions with opcatalog.PermissionsFor,
		// so the grant a user makes is expressed in the SAME closed vocabulary
		// every other reader uses. The alternative was a second port vocabulary
		// for the process tier, which is precisely the drift the catalog exists
		// to remove — and a permission list that disagreed with the catalog's
		// would be a grant nobody could audit.
		//
		// It mints nothing and serves no request: it holds no Catalog, registers
		// no spec, and — unlike every other entry here — is imported by NOTHING
		// in the tree. `go list -deps ./cmd/graphi` contains neither
		// engine/exthost nor extensions/example-analyzer, which
		// engine/exthost/isolation_test.go checks on every run, so the shipped
		// binary is byte-identical with the spike present.
		//
		// THESE TWO LINES ARE PART OF THE SPIKE'S DELETION RECIPE. If the no-go
		// is acted on — `rm -r engine/exthost extensions/example-analyzer` —
		// delete them too. Leaving them would be harmless (this check only
		// examines files that exist) but would be a stale claim about a package
		// that does not.
		filepath.Join("engine", "exthost", "descriptor.go"): true,
		filepath.Join("engine", "exthost", "host.go"):       true,
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
				"WITHOUT needing this entry removed, and AX-08 moved nine more the same way — "+
				"both call client.DispatchOperation — so a later story that thinks it needs the "+
				"entry gone should first check whether it is really solving the same problem "+
				"AX-06 and AX-08 solved.", importer, reason)
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
// crosses into the executor by CALLING the dispatch seam, and a file that does
// so imports surfaces/client — something most of the tree already does. An
// import-shaped test is blind to it.
//
// So the crossing gets its own explicit list. Only the named request-serving
// files may reach the executor-dispatch seam, and an unnamed one fails this
// test. The check runs in both directions: an undeclared caller is a failure,
// and a declared caller that no longer contains the call is also a failure,
// because a stale entry would quietly license the next one.
//
// The seam is named by its identifier rather than by a package import so the
// rule survives a rename of the file it lives in.
//
// # AX-08 (SW-228) is the widening AX-06 predicted, and it widens the rule
//
// The seam's identifier changed with its scope — DispatchCanary served one
// operation, DispatchOperation serves the migrated SET — and the file list is
// unchanged: the same three files, because AX-08 collapsed ten per-operation
// dispatch arms into ONE generic branch per surface instead of adding ten
// call sites. That is the plan's success criterion, and it is why this list did
// not have to grow to accommodate a ten-fold widening.
//
// A file-name list alone would now be weaker than it was, though, because the
// interesting question moved: it is no longer only "who calls the seam" but
// "how many places call it". Ten arms collapsing into two generic branches is
// the whole claim, and a list of file names cannot tell that apart from ten
// calls in the same two files. So this test gains a SECOND rule the AX-06
// version had no need for: the number of call sites per file is PINNED below.
//
// (The third question — WHAT the seam may carry — is not answerable from this
// package: engine may not import surfaces, and the migrated set lives in
// surfaces/client. It is gated there instead, by
// surfaces/client/migration_test.go, which checks every migrated operation
// against the catalog criteria and against argument-fidelity evidence.)
func TestAX06_OnlyTheCanaryDispatchesThroughTheExecutor(t *testing.T) {
	root := moduleRootForTest(t)
	const seam = "DispatchOperation"

	// How many times each declared file may reach the seam. AX-08's claim is
	// that a migrated operation costs a table row, not a branch: MCP resolves
	// every migrated tool through ONE generic branch, and HTTP has four —
	// its three body-based handlers (compound, search_ast, find_clones, each of
	// which owns its own route) plus the one generic branch that serves every
	// agent tool on /analyze/{name}. A number that grows here means a story
	// went back to per-operation dispatch and should say why.
	callSites := map[string]int{
		filepath.Join("surfaces", "mcp", "toolcalls.go"): 1,
		filepath.Join("surfaces", "http", "handlers.go"): 4,
		// SW-245: pinned at zero. The deferred worker is reached FROM the seam
		// and must never call back into it — a re-entrant dispatch would run
		// the comparison's own comparison, and the queue would feed itself.
		filepath.Join("surfaces", "client", "canary_shadow.go"): 0,
	}
	counted := map[string]int{}

	// The declared dispatch sites: the MCP tools/call generic branch and the
	// HTTP handlers' generic branch. Adding a fourth means a THIRD surface is
	// dispatching through the executor, which is a boundary change and owes its
	// own evidence.
	declared := map[string]string{
		filepath.Join("surfaces", "mcp", "toolcalls.go"): "MCP tools/call — the generic executor branch",
		filepath.Join("surfaces", "http", "handlers.go"): "HTTP — the generic executor branch",
		filepath.Join("surfaces", "client", "canary.go"): "the seam's own definition",
		// SW-245 split the shadow position's deferred half out of canary.go.
		// It is the seam's own definition continued, not a new crossing: it
		// contains no call to the seam at all (the callSites pin below says
		// zero), only the worker the shadow arm hands its comparison to. It is
		// declared rather than exempted because an undeclared file is how a
		// real fourth caller would arrive unnoticed.
		filepath.Join("surfaces", "client", "canary_shadow.go"): "the seam's deferred dual-run worker",
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
		// Count CALLS, not mentions: a doc comment naming the seam is not a
		// crossing, and the files below carry several of those.
		counted[rel] = strings.Count(string(src), seam+"(")
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
			"AX-06 migrated ONE operation and AX-08 migrated a bounded read-only Labs set, both "+
			"through exactly one generic branch per surface. A new caller is a new surface crossing "+
			"the seam, and it owes the same per-operation evidence the migrated set owes: catalog "+
			"criteria, byte parity, and argument fidelity. It widens this list in the same change.",
			strings.Join(undeclared, "\n  "))
	}
	for rel, reason := range declared {
		if !found[rel] {
			t.Errorf("declared canary dispatch site %q (%s) no longer reaches the seam — a stale "+
				"entry here licenses an undeclared caller to take its place unnoticed", rel, reason)
		}
	}
	for rel, want := range callSites {
		if got := counted[rel]; got != want {
			t.Errorf("%s reaches the executor dispatch seam %d time(s), want %d. AX-08's claim "+
				"is that a migrated operation costs a table row and not a dispatch arm; a "+
				"changed count is that claim changing, and it belongs in the diff with a "+
				"reason rather than in a number nobody looked at.", rel, got, want)
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
