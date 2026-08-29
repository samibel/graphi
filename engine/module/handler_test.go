package module_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
	"github.com/samibel/graphi/engine/search"
)

// SW-255 (AX-15): the contribution form that carries a spec AND a handler.

// deadCodeSpec returns the real dead_code spec from the shadow catalog — the
// one operation whose ports the builder can supply today.
func deadCodeSpec(t *testing.T) opcatalog.OperationSpec {
	t.Helper()
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	spec, ok := shadow.Lookup(deadcode.Operation)
	if !ok {
		t.Fatalf("the catalog does not declare %q", deadcode.Operation)
	}
	return spec
}

// portedInputs returns Inputs with both graph ports over one store.
func portedInputs(store graphstore.Graphstore) module.Inputs {
	return module.Inputs{Reader: store, GraphQuery: query.New(store), GraphSearch: search.New(store)}
}

// echoHandler is a handler that records which ports it was bound with and
// answers with a fixed document, so a test can see what the builder handed in.
func echoContribution(spec opcatalog.OperationSpec, seen *module.Ports) module.OperationContribution {
	return module.OperationContribution{
		Spec: spec,
		Bind: func(p module.Ports) (module.OperationHandler, error) {
			*seen = p
			return func(context.Context, json.RawMessage) ([]byte, error) { return []byte(`{"ok":true}`), nil }, nil
		},
	}
}

// AC-1: the new form registers the spec into the catalog AND a handler into the
// composition; AC-4: the composition exposes the lookup.
func TestAX15_AddOperationContribution_RegistersSpecAndHandler(t *testing.T) {
	spec := deadCodeSpec(t)
	var seen module.Ports
	s := module.NewSet()
	mustAdd(t, s, module.Module{
		Manifest: module.Manifest{ID: "engine.deadcode.test", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddOperationContribution(echoContribution(spec, &seen)) },
	})
	store := graphstore.NewMemStore()
	comp, err := s.Build(portedInputs(store))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := comp.Operations().Lookup(spec.ID); !ok {
		t.Fatalf("the contribution did not register spec %q", spec.ID)
	}
	handler, ok := comp.Handler(spec.ID)
	if !ok || handler == nil {
		t.Fatalf("Handler(%q) = (%v, %t), want a handler", spec.ID, handler, ok)
	}
	if got := comp.Handled(); !reflect.DeepEqual(got, []string{spec.ID}) {
		t.Fatalf("Handled() = %v, want [%s]", got, spec.ID)
	}
	if _, ok := comp.Handler("no_such_operation"); ok {
		t.Fatal("Handler reported a handler for an operation nobody contributed")
	}
	out, err := handler(context.Background(), nil)
	if err != nil || string(out) != `{"ok":true}` {
		t.Fatalf("handler = (%s, %v)", out, err)
	}
	if seen.GraphQuery == nil || seen.GraphSearch == nil {
		t.Fatalf("dead_code declares graph.query and graph.search; Bind received %+v", seen)
	}
}

// AC-1: a spec-only and a handler-bearing registration of the same id claim the
// same Operation:<id> slot — whichever came first wins, the second is
// registry.ErrDuplicate naming both modules.
func TestAX15_SpecOnlyAndHandlerBearingRegistrationsCollide(t *testing.T) {
	spec := deadCodeSpec(t)
	var seen module.Ports
	specOnly := module.Module{
		Manifest: module.Manifest{ID: "spec.only", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddOperation(spec) },
	}
	withHandler := module.Module{
		Manifest: module.Manifest{ID: "with.handler", Version: "1", Requires: []string{"spec.only"}},
		Register: func(b *module.Builder) error { return b.AddOperationContribution(echoContribution(spec, &seen)) },
	}
	for _, tc := range []struct {
		name   string
		first  module.Module
		second module.Module
	}{
		{"spec-only first", specOnly, withHandler},
		{"handler-bearing first", withHandler, specOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := tc.first, tc.second
			// Order the pair explicitly so "whichever came first" is the one
			// the test names, not a lexicographic accident.
			first.Manifest.Requires = nil
			second.Manifest.Requires = []string{first.Manifest.ID}
			s := module.NewSet()
			mustAdd(t, s, first, second)
			_, err := s.Build(portedInputs(graphstore.NewMemStore()))
			if !errors.Is(err, registry.ErrDuplicate) {
				t.Fatalf("Build = %v, want registry.ErrDuplicate", err)
			}
			for _, want := range []string{first.Manifest.ID, second.Manifest.ID, spec.ID} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not name %q", err, want)
				}
			}
		})
	}
}

// AC-3: a declared port that is nil at Build fails closed with
// registry.ErrMissingDependency naming the module, the operation and the port.
func TestAX15_MissingDeclaredPortFailsClosed(t *testing.T) {
	spec := deadCodeSpec(t)
	store := graphstore.NewMemStore()
	for _, tc := range []struct {
		name   string
		inputs module.Inputs
		port   opcatalog.Port
	}{
		{"no ports at all", module.Inputs{Reader: store}, opcatalog.PortGraphQuery},
		{"query only", module.Inputs{Reader: store, GraphQuery: query.New(store)}, opcatalog.PortGraphSearch},
		{"search only", module.Inputs{Reader: store, GraphSearch: search.New(store)}, opcatalog.PortGraphQuery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen module.Ports
			bound := false
			s := module.NewSet()
			mustAdd(t, s, module.Module{
				Manifest: module.Manifest{ID: "needs.ports", Version: "1"},
				Register: func(b *module.Builder) error {
					c := echoContribution(spec, &seen)
					inner := c.Bind
					c.Bind = func(p module.Ports) (module.OperationHandler, error) { bound = true; return inner(p) }
					return b.AddOperationContribution(c)
				},
			})
			comp, err := s.Build(tc.inputs)
			if !errors.Is(err, registry.ErrMissingDependency) {
				t.Fatalf("Build = %v, want registry.ErrMissingDependency", err)
			}
			if comp != nil {
				t.Fatal("Build returned a composition alongside an error")
			}
			for _, want := range []string{"needs.ports", spec.ID, string(tc.port)} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not name %q", err, want)
				}
			}
			var re *registry.Error
			if !errors.As(err, &re) || re.Key != spec.ID {
				t.Errorf("the typed error does not carry the operation id: %+v", err)
			}
			if bound {
				t.Error("Bind ran with a missing port — the handler must never be built over a nil dependency")
			}
		})
	}
}

// AC-3: the builder hands a module ONLY the ports its spec declares. A spec
// declaring graph.query alone gets a nil graph.search port even when the
// inputs carry one, and a spec declaring a port this builder has no supply for
// fails closed rather than being handed nil.
func TestAX15_OnlyDeclaredPortsAreHandedIn(t *testing.T) {
	spec := deadCodeSpec(t)
	store := graphstore.NewMemStore()

	queryOnly := spec
	queryOnly.Ports = []opcatalog.Port{opcatalog.PortGraphQuery}
	var seen module.Ports
	s := module.NewSet()
	mustAdd(t, s, module.Module{
		Manifest: module.Manifest{ID: "query.only", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddOperationContribution(echoContribution(queryOnly, &seen)) },
	})
	if _, err := s.Build(portedInputs(store)); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if seen.GraphQuery == nil {
		t.Fatal("the declared graph.query port was not handed in")
	}
	if seen.GraphSearch != nil {
		t.Fatal("an UNDECLARED graph.search port was handed in; a module may reach only what its spec declares")
	}

	unsupplied := spec
	unsupplied.Ports = []opcatalog.Port{opcatalog.PortGraphQuery, opcatalog.PortGitHistory}
	u := module.NewSet()
	mustAdd(t, u, module.Module{
		Manifest: module.Manifest{ID: "wants.history", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddOperationContribution(echoContribution(unsupplied, &seen)) },
	})
	_, err := u.Build(portedInputs(store))
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("Build over a port the builder cannot supply = %v, want registry.ErrMissingDependency", err)
	}
	for _, want := range []string{"wants.history", spec.ID, string(opcatalog.PortGitHistory)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err, want)
		}
	}
}

// A contribution with no Bind, or a Bind that returns nil, is a malformed
// contribution and is refused — never a spec silently registered without its
// handler.
func TestAX15_MalformedContributionsAreRefused(t *testing.T) {
	spec := deadCodeSpec(t)
	for _, tc := range []struct {
		name string
		c    module.OperationContribution
	}{
		{"nil Bind", module.OperationContribution{Spec: spec}},
		{"Bind returns nil handler", module.OperationContribution{Spec: spec, Bind: func(module.Ports) (module.OperationHandler, error) { return nil, nil }}},
		{"Bind fails", module.OperationContribution{Spec: spec, Bind: func(module.Ports) (module.OperationHandler, error) { return nil, errors.New("boom") }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := module.NewSet()
			mustAdd(t, s, module.Module{
				Manifest: module.Manifest{ID: "malformed", Version: "1"},
				Register: func(b *module.Builder) error { return b.AddOperationContribution(tc.c) },
			})
			comp, err := s.Build(portedInputs(graphstore.NewMemStore()))
			if err == nil {
				t.Fatal("a malformed contribution was accepted")
			}
			if comp != nil {
				t.Fatal("Build returned a composition alongside an error")
			}
			if !strings.Contains(err.Error(), "malformed") || !strings.Contains(err.Error(), spec.ID) {
				t.Fatalf("message %q names neither the module nor the operation", err)
			}
		})
	}
}

// AC-4: after Build the handler slot is frozen like every other — a stashed
// Builder refuses a late AddOperationContribution with registry.ErrFrozen, and
// the composition's handler table is a lookup, never a mutable map.
func TestAX15_HandlerSlotIsFrozenAfterBuild(t *testing.T) {
	spec := deadCodeSpec(t)
	var stashed *module.Builder
	var seen module.Ports
	s := module.NewSet()
	mustAdd(t, s, module.Module{
		Manifest: module.Manifest{ID: "stasher", Version: "1"},
		Register: func(b *module.Builder) error { stashed = b; return nil },
	})
	comp, err := s.Build(portedInputs(graphstore.NewMemStore()))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := stashed.AddOperationContribution(echoContribution(spec, &seen)); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("a post-build AddOperationContribution was accepted: %v", err)
	}
	if err := stashed.AddOperation(spec); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("a post-build AddOperation was accepted: %v", err)
	}
	if _, ok := comp.Handler(spec.ID); ok {
		t.Fatal("the late contribution reached the composition")
	}
	if len(comp.Handled()) != 0 {
		t.Fatalf("Handled() = %v after a refused contribution", comp.Handled())
	}
}

// The built-in set carries two handler-bearing operation modules beside the
// three AX-07 modules. The catalog remains byte-for-byte the shadow catalog.
func TestBuiltinsCarryTheHandlerModules(t *testing.T) {
	store := graphstore.NewMemStore()
	comp, err := module.BuildBuiltins(portedInputs(store))
	if err != nil {
		t.Fatalf("BuildBuiltins: %v", err)
	}
	want := []string{module.IDParse, module.IDAnalysis, module.IDCompound, module.IDDeadCode, module.IDOperations}
	if got := comp.ModuleIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("built-in composition order = %v, want %v", got, want)
	}
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	if got, wantLen := comp.Operations().Len(), shadow.Len(); got != wantLen || got != 56 {
		t.Fatalf("catalog size = %d, shadow = %d, want 56", got, wantLen)
	}
	if got, wantIDs := comp.Operations().IDs(), shadow.IDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("operation ids diverged from the shadow catalog\n  module set: %v\n  shadow:     %v", got, wantIDs)
	}
	if got := comp.Handled(); !reflect.DeepEqual(got, []string{compound.Operation, deadcode.Operation}) {
		t.Fatalf("Handled() = %v, want compound and dead_code", got)
	}
	compoundHandler, ok := comp.Handler(compound.Operation)
	if !ok {
		t.Fatalf("no handler for %q", compound.Operation)
	}
	compoundRaw, err := compoundHandler(context.Background(), json.RawMessage(`{"query":"SEED no.such.symbol\nHOP out calls\n"}`))
	if err != nil || len(compoundRaw) == 0 {
		t.Fatalf("compound handler over a missing seed = %s (%v), want canonical result bytes", compoundRaw, err)
	}
	handler, ok := comp.Handler(deadcode.Operation)
	if !ok {
		t.Fatalf("no handler for %q", deadcode.Operation)
	}
	// The handler is bound to the composition's ports: on an empty graph it
	// answers the typed `empty` outcome, which only a wired graph port yields.
	raw, err := handler(context.Background(), json.RawMessage(`{"max_items":1}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var res struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Outcome != "empty" {
		t.Fatalf("handler over an empty graph = %s (%v), want outcome empty", raw, err)
	}

	// The built-in set fails closed without the ports, like any other module.
	if _, err := module.BuildBuiltins(module.Inputs{Reader: store}); !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("BuildBuiltins without ports = %v, want registry.ErrMissingDependency", err)
	}
}
