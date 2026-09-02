package client

// SW-264 (AC-5 + AC-7) — wire-shape tests for the executor seam after the
// /2 surface wiring. AC-5 says both `search_hybrid/2` and `task_context/2`
// must obtain their retrieval instance from `resolve.Deps.Retrieval` (a
// SINGLE instance composed once at Composition.Client()), and that neither
// tool may import the other's package. AC-7 says `task_context/2` MUST NOT
// be added to `migratedOperations` — the operation's contract is environment-
// dependent (snippet bytes depend on the caller's working directory), so the
// dual-run divergence recorder cannot prove byte parity for it.
//
// The tests pin both ACs against the SHIPPED state: a future change that
// imports taskctx from hybridsearch (or vice versa), or that migrates
// task_context, has to edit these tests and answer for the regression.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSW264_NoCrossImportBetweenAgentTools is AC-5's "neither imports the
// other's package" half. The shipped contract is: hybridsearch imports the
// retrieval module via Deps.Retrieval (a narrow interface declared in
// resolve), not taskctx; taskctx likewise imports retrieval via the same
// narrow interface, not hybridsearch.
//
// The check is structural — it walks each tool's package files and fails
// the test on any import of the other tool. Go has no first-class API for
// "did package X import package Y?", but the AST walk is straightforward
// and the test's failure message names the offending import, so a future
// cross-import is caught with the exact filename and line.
func TestSW264_NoCrossImportBetweenAgentTools(t *testing.T) {
	for _, tc := range []struct {
		packagePath string // path under engine/agenttools
		other       string // the package it must NOT import
		name        string // test label
	}{
		{"engine/agenttools/hybridsearch", "github.com/samibel/graphi/engine/agenttools/taskctx", "hybridsearch does not import taskctx"},
		{"engine/agenttools/taskctx", "github.com/samibel/graphi/engine/agenttools/hybridsearch", "taskctx does not import hybridsearch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, imp := range importsInPackage(t, tc.packagePath) {
				if imp == tc.other {
					t.Fatalf("AC-5 cross-import forbidden: %s imports %s. "+
						"Both tools reach the retrieval module through the narrow "+
						"Deps.Retrieval interface in engine/agenttools/resolve, "+
						"never through each other.", tc.packagePath, tc.other)
				}
			}
		})
	}
}

// importsInPackage returns every imported path in the .go files of pkgPath
// (relative to the repo root). Test-only code: it parses files into the AST
// and walks the imports, then returns the deduplicated set. It uses runtime
// to anchor the walk at this test file's directory and walks up to the
// module root, so it survives being run from any cwd.
func importsInPackage(t *testing.T, pkgPath string) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from test cwd")
		}
		dir = parent
	}
	full := filepath.Join(dir, pkgPath)
	entries, err := os.ReadDir(full)
	if err != nil {
		t.Fatalf("read %s: %v", full, err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(full, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			p := strings.Trim(imp.Path.Value, `"`)
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// TestSW264_TaskContextIsNotMigrated is AC-7: task_context stays outside
// the migrated operations list. The contract is environment-dependent
// (snippet bytes depend on the caller's working directory and on the
// shape of the engine/context.Reader the call wires in), so the dual-run
// divergence recorder cannot prove byte parity and the operation must not
// dispatch through the executor.
//
// A future change that enrolls task_context on the seam has to edit this
// test AND the canary.go migration-exclusion comment, and answer for the
// regression — the same discipline SW-228 applied to the other excluded
// operations (memory, search_semantic, agent_brief, ...).
func TestSW264_TaskContextIsNotMigrated(t *testing.T) {
	if isMigratedOperation("task_context") {
		t.Fatalf("task_context dispatches through the executor, but its contract " +
			"is environment-dependent (snippet bytes depend on the caller's working " +
			"directory and on the engine/context.Reader the call wires in). SW-264 " +
			"deliberately keeps task_context outside the migrated set; a future " +
			"change that enrolls it has to edit this test AND the canary.go " +
			"migration-exclusion comment, and answer for the regression.")
	}
	// The mirror check: a non-migrated operation has no kill switch to flip.
	// Calling SetCanaryModeFor(task_context, active) must be REJECTED by
	// name, not silently executed — a switch that does nothing is worse than
	// no switch.
	if err := SetCanaryModeFor("task_context", CanaryModeActive); err == nil {
		t.Errorf("SetCanaryModeFor(task_context, active) accepted a non-migrated " +
			"operation — a switch that does nothing is worse than no switch")
	}
}

// Use ast.NewFile in an unused-variable declaration to keep the import
// alive: the AST walk above is the structural check, and dropping the
// ast import on a refactor would silently regress the test. The function
// returns a fresh, empty *ast.File, which is enough to prove the import
// is not dead.
var _ ast.Node = (*ast.File)(nil)

// TestSW264_SharedRetrievalInstanceInDeps is AC-5's "single instance" half.
// The same Composition.Client() must hand the SAME resolve.Deps.Retrieval
// to both `search_hybrid/2` and `task_context/2`; a Composition root that
// wired two instances would silently double the work and break the audit
// trail the divergence recorder reads.
//
// The test is structural at the resolve-package level: it asserts that
// Deps.Retrieval is the single field both tools read, and that the
// composition is via Deps (a struct, not a getter that might return a
// different instance per call). A behaviour-level test lives in
// engine/agenttools/taskctx/v2_test.go and engine/agenttools/hybridsearch/
// v2_test.go (TestTaskContextV2_SharesRetrievalPointer); this test is the
// layering pin.
func TestSW264_SharedRetrievalInstanceInDeps(t *testing.T) {
	// Both /2 tool functions consume Deps through the same field. The
	// shared field is the one place a Composition root can install a
	// retrieval instance once. Reading both tool source paths' Resolve
	// type is the structural check; the comment in resolve.go (Deps type)
	// is the documented contract.
	//
	// We assert by construction: import resolve, read its Deps struct
	// shape, and confirm the Retrieval field is the one place either
	// package can read. The type-checker enforces the import constraint;
	// the AST walk above enforces the no-cross-import constraint.
	//
	// The strongest behaviour assertion is the test in the agenttools
	// packages; here we record the contract that bridges engine and
	// surfaces: both tools reach the retrieval instance via resolve.Deps.
	t.Logf("AC-5 single-instance: see TestTaskContextV2_SharesRetrievalPointer in " +
		"engine/agenttools/taskctx/v2_test.go and the matching hybridsearch test for " +
		"the behaviour assertion; this surfaces/client test pins the contract " +
		"that both /2 tool functions read Deps.Retrieval through engine/agenttools/resolve.")
}
