package client

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/opcatalog"
)

// TestExecutor_CatalogIsServedFromTheOperationCatalog is AC-1's first half: the
// executor keeps no operation list of its own. Catalog() is the SW-223 catalog,
// re-derived here rather than compared against a copied expectation, so the two
// cannot drift.
func TestExecutor_CatalogIsServedFromTheOperationCatalog(t *testing.T) {
	direct, _ := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	got := executor.Catalog()
	want := shadow.All()
	if len(got) != len(want) {
		t.Fatalf("Catalog() has %d specs, the operation catalog has %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("Catalog()[%d] = %q, catalog says %q", i, got[i].ID, want[i].ID)
		}
		if got[i].Version != want[i].Version || got[i].Tier != want[i].Tier {
			t.Fatalf("%s: version/tier differ from the catalog: %q/%q vs %q/%q",
				got[i].ID, got[i].Version, got[i].Tier, want[i].Version, want[i].Tier)
		}
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].ID < got[j].ID }) {
		t.Fatal("Catalog() is not in canonical id order")
	}
}

// TestExecutor_AdaptedIsASubsetOfTheCatalog pins the honest half of a
// representative-set story: Adapted() says which operations Execute can actually
// run, it is a strict subset today, and nothing in it is invented.
func TestExecutor_AdaptedIsASubsetOfTheCatalog(t *testing.T) {
	direct, _ := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	adapted := executor.Adapted()
	if !sort.StringsAreSorted(adapted) {
		t.Fatalf("Adapted() is not in canonical order: %v", adapted)
	}
	for _, id := range adapted {
		if _, ok := shadow.Lookup(id); !ok {
			t.Errorf("adapted operation %q is not in the operation catalog", id)
		}
	}
	if len(adapted) >= shadow.Len() {
		t.Fatalf("Adapted() covers %d of %d catalog operations — AX-04 adapts a representative "+
			"subset, so a full set here means Adapted() stopped reporting the truth",
			len(adapted), shadow.Len())
	}
}

// TestExecutor_AdaptedCoversEveryStableOperation is the AC-4 scoping check: a
// "representative set" that skipped a frozen operation would prove nothing about
// the twelve. The expectation is read from the catalog's own tier declaration,
// never written down here, so promoting or demoting an operation moves this test
// with it.
func TestExecutor_AdaptedCoversEveryStableOperation(t *testing.T) {
	direct, _ := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	adapted := map[string]bool{}
	for _, id := range executor.Adapted() {
		adapted[id] = true
	}
	var missing []string
	for _, id := range shadow.IDsWithTier(opcatalog.TierStable) {
		if !adapted[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("Stable operations with no legacy adapter: %v", missing)
	}
}

// TestExecutor_RejectsUnknownWork is AC-5. Every rejection is typed
// (core/registry's ErrMissingDependency — this story invents no parallel error
// kind) and none of them is a silent fallback: no bytes come back with the
// error, so a surface cannot mistake a rejection for an empty answer.
func TestExecutor_RejectsUnknownWork(t *testing.T) {
	direct, ids := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	symbol := string(ids["B"])
	for _, tc := range []struct {
		name    string
		req     Request
		wantMsg string
	}{
		{
			name:    "operation absent from the catalog",
			req:     Request{Operation: "no_such_operation", Version: "1"},
			wantMsg: "is not in the operation catalog",
		},
		{
			name:    "empty operation",
			req:     Request{Operation: "", Version: "1"},
			wantMsg: "is not in the operation catalog",
		},
		{
			name:    "unsupported contract version",
			req:     Request{Operation: "callers", Version: "2", Arguments: []byte(`{"symbol":"` + symbol + `"}`)},
			wantMsg: `has no version "2"`,
		},
		{
			name:    "no declared version is not a wildcard",
			req:     Request{Operation: "callers", Version: "", Arguments: []byte(`{"symbol":"` + symbol + `"}`)},
			wantMsg: `has no version ""`,
		},
		{
			name:    "catalog operation with no legacy adapter",
			req:     Request{Operation: "refactor", Version: "1"},
			wantMsg: "has no legacy adapter",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := executor.Execute(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("Execute succeeded; want a typed rejection (returned %d bytes)", len(got))
			}
			if got != nil {
				t.Fatalf("a rejected Execute returned %d bytes — a rejection must never look like an answer", len(got))
			}
			if !errors.Is(err, registry.ErrMissingDependency) {
				t.Fatalf("error %v is not registry.ErrMissingDependency", err)
			}
			var typed *registry.Error
			if !errors.As(err, &typed) {
				t.Fatalf("error %v does not carry the registry.Error detail", err)
			}
			if typed.Registry != "executor" || typed.Op != "Execute" {
				t.Fatalf("error carries registry=%q op=%q, want executor/Execute", typed.Registry, typed.Op)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not mention %q", err, tc.wantMsg)
			}
		})
	}
}

// TestExecutor_RejectsUnknownArgumentFields keeps the argument decode fail-closed:
// a misspelled or superseded argument name is an error, not a silently dropped
// value. graphi rejects superseded wire spellings rather than aliasing them, and
// an ignored argument is the same defect with a quieter failure mode.
func TestExecutor_RejectsUnknownArgumentFields(t *testing.T) {
	direct, ids := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	req := Request{
		Operation: "callers",
		Version:   "1",
		Arguments: []byte(`{"symbol":"` + string(ids["B"]) + `","sybmol":"typo"}`),
	}
	if _, err := executor.Execute(context.Background(), req); err == nil {
		t.Fatal("Execute accepted an unknown argument field")
	}
	if _, err := executor.DecodeArguments(req); err == nil {
		t.Fatal("DecodeArguments accepted an unknown argument field")
	}
}

// TestExecutor_RefusesAnUnfrozenCatalog: an executor over a catalog that can
// still grow would be resolving a moving target, which is the
// mutation-after-composition hazard SW-222's freeze exists to close.
func TestExecutor_RefusesAnUnfrozenCatalog(t *testing.T) {
	direct, _ := executorFixture(t)
	_, err := NewExecutorWithCatalog(direct, opcatalog.New())
	if err == nil {
		t.Fatal("NewExecutorWithCatalog accepted an unfrozen catalog")
	}
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("error %v is not registry.ErrMissingDependency", err)
	}
}

// TestExecutor_RefusesAnAdapterTheCatalogDoesNotDeclare proves the catalog is
// the single source of operation identity: an adapter table that names an
// operation the catalog does not carry is a second, contradicting list, and it
// fails construction rather than shipping.
func TestExecutor_RefusesAnAdapterTheCatalogDoesNotDeclare(t *testing.T) {
	direct, _ := executorFixture(t)
	partial := opcatalog.New()
	ports := []opcatalog.Port{opcatalog.PortGraphQuery}
	if err := partial.Add(opcatalog.OperationSpec{
		ID:            "callers",
		Version:       "1",
		Tier:          opcatalog.TierStable,
		Advertisement: opcatalog.Advertisement{Description: "callers of a symbol"},
		Ports:         ports,
		Permissions:   opcatalog.PermissionsFor(ports),
		Determinism:   opcatalog.DeterminismDeterministic,
		PortsEvidence: "surfaces/client/executor_test.go fixture",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	frozen, err := partial.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = NewExecutorWithCatalog(direct, frozen)
	if err == nil {
		t.Fatal("NewExecutorWithCatalog accepted adapters the catalog does not declare")
	}
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("error %v is not registry.ErrMissingDependency", err)
	}
	if !strings.Contains(err.Error(), "the catalog does not declare") {
		t.Fatalf("error %q does not explain the contradiction", err)
	}
}

// TestExecutor_RequiresAClient closes the last construction hole.
func TestExecutor_RequiresAClient(t *testing.T) {
	if _, err := NewExecutor(nil); err == nil {
		t.Fatal("NewExecutor(nil) succeeded")
	}
	direct, _ := executorFixture(t)
	if _, err := NewExecutorWithCatalog(direct, nil); err == nil {
		t.Fatal("NewExecutorWithCatalog with a nil catalog succeeded")
	}
}

// TestExecutor_AdapterTableIsCollisionGuarded pins the SW-222 vocabulary reuse:
// the adapter table is built through registry.GuardDuplicate under
// PolicyFirstWins, so a copy-pasted id is a typed ErrDuplicate rather than a
// silent override of a frozen wire identifier.
func TestExecutor_AdapterTableIsCollisionGuarded(t *testing.T) {
	table, err := legacyAdapters()
	if err != nil {
		t.Fatalf("legacyAdapters: %v", err)
	}
	if len(table) == 0 {
		t.Fatal("the adapter table is empty")
	}
	// The guard, exercised directly on the same policy the table is built with.
	err = registry.GuardDuplicate(registry.PolicyFirstWins, executorRegistry, "adapter", "callers", true)
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("a duplicate adapter id yields %v, want registry.ErrDuplicate", err)
	}
}

// TestExecutor_ClientContractUnchanged is AC-2 as a characterization baseline.
// The story's whole premise is that operations can be attached WITHOUT widening
// the Client interface, so the interface's own width is the number to watch. A
// later story that deliberately changes it must move this number on purpose —
// which is the point; a silent drift is what this catches.
func TestExecutor_ClientContractUnchanged(t *testing.T) {
	const widthAtAX04 = 40
	iface := reflect.TypeOf((*Client)(nil)).Elem()
	if got := iface.NumMethod(); got != widthAtAX04 {
		t.Fatalf("client.Client has %d methods, %d at the AX-04 baseline. SW-224 exists so new "+
			"operations do NOT widen this interface — if the change is deliberate, move the "+
			"constant and say why in the story", got, widthAtAX04)
	}
	// Direct and HTTP still satisfy the full contract: nothing was narrowed.
	var _ Client = (*Direct)(nil)
	var _ Client = (*HTTP)(nil)
	// And the capability-narrow Stable view still composes out of it.
	var _ StableClient = AsStable((*Direct)(nil))
}

// TestExecutor_ResultPathDoesNotRoundTripThroughMaps pins the story's canonical
// bytes rule structurally. The executor transports bytes the engine produced; a
// map[string]any anywhere in the result path would mean a second serializer, and
// a second serializer is how two surfaces stop being byte-identical. The check is
// on the source because that is where the rule is either kept or broken — a
// runtime assertion cannot see a re-encode that happens to round-trip today.
func TestExecutor_ResultPathDoesNotRoundTripThroughMaps(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"executor.go", "executor_adapters.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// The AST, not the text: the package documentation in these files NAMES
		// the shape it forbids, and a grep would flag the prose that states the
		// rule as a violation of it.
		ast.Inspect(file, func(n ast.Node) bool {
			m, ok := n.(*ast.MapType)
			if !ok {
				return true
			}
			key, ok := m.Key.(*ast.Ident)
			if !ok || key.Name != "string" {
				return true
			}
			generic := false
			switch v := m.Value.(type) {
			case *ast.Ident:
				generic = v.Name == "any"
			case *ast.InterfaceType:
				generic = v.Methods == nil || len(v.Methods.List) == 0
			}
			if generic {
				t.Errorf("%s:%d declares a generic string→any map — the executor transports "+
					"engine-produced canonical bytes and must not stage a result through one",
					name, fset.Position(m.Pos()).Line)
			}
			return true
		})
	}
}

// TestExecutor_NoSurfaceDispatchesThroughIt is AC-6 and the story's out-of-scope
// line, enforced instead of promised: this story is additive plumbing, the first
// surface to dispatch through the executor is SW-226's canary, and removing
// these files must restore legacy-only operation. If nothing outside this
// package mentions the executor, that removal is a deletion.
func TestExecutor_NoSurfaceDispatchesThroughIt(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	ownPackage, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve package dir: %v", err)
	}
	markers := []string{"NewExecutor(", "NewExecutorWithCatalog(", "client.Executor", "client.Request{"}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "web", "testdata", "corpus":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || filepath.Dir(path) == ownPackage {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range markers {
			if strings.Contains(string(src), marker) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+" mentions "+marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("SW-224 is additive plumbing: no code outside surfaces/client may reach the "+
			"executor yet (the first dispatch is SW-226's canary), and rollback must stay a "+
			"deletion. Found:\n  %s", strings.Join(offenders, "\n  "))
	}
}
