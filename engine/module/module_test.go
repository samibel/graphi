package module_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/typeresolve"
)

// sampleSpec returns a real spec from the shadow catalog. Constructing a valid
// OperationSpec by hand means duplicating the validator's rules in the test; a
// real one keeps the fixture honest.
func sampleSpec(t *testing.T) opcatalog.OperationSpec {
	t.Helper()
	shadow, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow: %v", err)
	}
	specs := shadow.All()
	if len(specs) == 0 {
		t.Fatal("the shadow catalog is empty")
	}
	return specs[0]
}

// noopModule is a module that contributes nothing, for DAG-shape tests.
func noopModule(id string, requires ...string) module.Module {
	return module.Module{
		Manifest: module.Manifest{ID: id, Version: "1", Requires: requires},
		Register: func(*module.Builder) error { return nil },
	}
}

func mustAdd(t *testing.T, s *module.Set, mods ...module.Module) {
	t.Helper()
	for _, m := range mods {
		if err := s.Add(m); err != nil {
			t.Fatalf("Add(%q): %v", m.Manifest.ID, err)
		}
	}
}

func orderOf(t *testing.T, s *module.Set) []string {
	t.Helper()
	ordered, err := s.Order()
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	out := make([]string, 0, len(ordered))
	for _, m := range ordered {
		out = append(out, m.Manifest.ID)
	}
	return out
}

// AC-2: the composition order is a TOTAL order and a function of the manifests
// alone. Adding the same modules in a different sequence must not produce a
// different composition, because several of the registries underneath resolve
// collisions by order.
func TestAX07_Order_IsDeterministicAndIndependentOfInsertionOrder(t *testing.T) {
	forward := module.NewSet()
	mustAdd(t, forward, noopModule("a"), noopModule("b"), noopModule("c", "a"), noopModule("d", "c", "b"))

	backward := module.NewSet()
	mustAdd(t, backward, noopModule("d", "b", "c"), noopModule("c", "a"), noopModule("b"), noopModule("a"))

	want := []string{"a", "b", "c", "d"}
	if got := orderOf(t, forward); !reflect.DeepEqual(got, want) {
		t.Fatalf("forward order = %v, want %v", got, want)
	}
	if got := orderOf(t, backward); !reflect.DeepEqual(got, want) {
		t.Fatalf("backward order = %v, want %v — insertion order leaked into composition order", got, want)
	}

	// Independent modules must tie-break lexicographically, not arbitrarily:
	// repeating the sort must give the same answer every time.
	wide := module.NewSet()
	mustAdd(t, wide, noopModule("z"), noopModule("m"), noopModule("a"), noopModule("q"))
	first := orderOf(t, wide)
	for i := 0; i < 32; i++ {
		if got := orderOf(t, wide); !reflect.DeepEqual(got, first) {
			t.Fatalf("Order() is not stable: %v then %v", first, got)
		}
	}
	if !reflect.DeepEqual(first, []string{"a", "m", "q", "z"}) {
		t.Fatalf("independent modules did not tie-break lexicographically: %v", first)
	}
}

// AC-2: a duplicate module id is rejected with a typed error naming the offender.
func TestAX07_Add_RejectsDuplicateModuleID(t *testing.T) {
	s := module.NewSet()
	mustAdd(t, s, noopModule("engine.analysis"))
	err := s.Add(noopModule("engine.analysis"))
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("Add duplicate = %v, want registry.ErrDuplicate", err)
	}
	var re *registry.Error
	if !errors.As(err, &re) || re.Key != "engine.analysis" {
		t.Fatalf("error did not carry the offending id: %+v", err)
	}
	if !strings.Contains(err.Error(), "engine.analysis") {
		t.Fatalf("message does not name the offender: %v", err)
	}
}

// AC-2: an unresolvable dependency is a typed missing-dependency error naming
// BOTH the module and what it asked for.
func TestAX07_Validate_RejectsMissingDependency(t *testing.T) {
	s := module.NewSet()
	mustAdd(t, s, noopModule("engine.review", "engine.analysis"))
	err := s.Validate()
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("Validate = %v, want registry.ErrMissingDependency", err)
	}
	for _, want := range []string{"engine.review", "engine.analysis"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q does not name %q", err, want)
		}
	}
}

// AC-2: cycles are detected and reported deterministically, with every module in
// the cycle named. A cycle is its own failure kind — registry.ErrCycle — and not
// squeezed into one of SW-222's original four.
func TestAX07_Validate_RejectsCyclesDeterministically(t *testing.T) {
	for _, tc := range []struct {
		name    string
		modules []module.Module
		want    []string
	}{
		{
			name:    "self",
			modules: []module.Module{noopModule("a", "a")},
			want:    []string{"a"},
		},
		{
			name:    "two",
			modules: []module.Module{noopModule("a", "b"), noopModule("b", "a")},
			want:    []string{"a", "b"},
		},
		{
			name:    "three-plus-an-innocent-bystander",
			modules: []module.Module{noopModule("a", "c"), noopModule("b", "a"), noopModule("c", "b"), noopModule("free")},
			want:    []string{"a", "b", "c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := module.NewSet()
			mustAdd(t, s, tc.modules...)
			err := s.Validate()
			if !errors.Is(err, registry.ErrCycle) {
				t.Fatalf("Validate = %v, want registry.ErrCycle", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("cycle message %q does not name %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "free") {
				t.Errorf("cycle message %q blames a module that is not in the cycle", err)
			}
			// Deterministic: the same set reports the same message every time.
			for i := 0; i < 16; i++ {
				again := s.Validate()
				if again.Error() != err.Error() {
					t.Fatalf("cycle report is not deterministic:\n  %v\n  %v", err, again)
				}
			}
		})
	}
}

// AC-2: two modules claiming one contribution key is a composition fault, and
// the error names BOTH modules — the point of tracking contribution ownership
// rather than letting the underlying registry answer.
func TestAX07_Build_RejectsAContributionTwoModulesClaim(t *testing.T) {
	spec := sampleSpec(t)
	contribute := func(b *module.Builder) error { return b.AddOperation(spec) }

	s := module.NewSet()
	mustAdd(t, s,
		module.Module{Manifest: module.Manifest{ID: "first", Version: "1"}, Register: contribute},
		module.Module{Manifest: module.Manifest{ID: "second", Version: "1"}, Register: contribute},
	)
	_, err := s.Build(module.Inputs{})
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("Build = %v, want registry.ErrDuplicate", err)
	}
	for _, want := range []string{"first", "second", spec.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err, want)
		}
	}
}

// The builder's first-wins policy protects the LAST-WINS seams underneath it.
// core/parse takes the later registration by design; reaching that seam through
// the module set must not be able to shadow a built-in silently (ADR 0013 T5).
func TestAX07_Build_ModuleSetCannotShadowALastWinsRegistration(t *testing.T) {
	s := module.NewSet()
	mustAdd(t, s,
		module.Module{
			Manifest: module.Manifest{ID: "core.parse", Version: "1"},
			Register: func(b *module.Builder) error { return b.AddParser(parse.NewGoParser()) },
		},
		module.Module{
			Manifest: module.Manifest{ID: "third.party", Version: "1"},
			Register: func(b *module.Builder) error { return b.AddParser(parse.NewGoParser()) },
		},
	)
	_, err := s.Build(module.Inputs{})
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("a second \"go\" parser was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "core.parse") || !strings.Contains(err.Error(), "third.party") {
		t.Fatalf("message %q does not name both claimants", err)
	}
	if module.CollisionPolicy != registry.PolicyFirstWins {
		t.Fatalf("the module builder's collision policy is %s; the shadowing guard above assumes first-wins",
			module.CollisionPolicy)
	}
	// The registry it feeds keeps ITS policy — the builder narrows the door, it
	// does not redesign the room.
	if parse.CollisionPolicy != registry.PolicyLastWins {
		t.Fatalf("core/parse policy changed to %s; AX-07 must not harmonise it", parse.CollisionPolicy)
	}
}

// The resolver kind is implemented even though no built-in module contributes
// one, and the registry it feeds is pre-armed by its own constructor — so the
// ownership guard must cover those pre-registered keys too.
func TestAX07_AddResolver_IsWiredAndGuardsThePreArmedBuiltIn(t *testing.T) {
	s := module.NewSet()
	mustAdd(t, s, module.Module{
		Manifest: module.Manifest{ID: "third.party", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddResolver(fakeResolver{lang: "elixir"}) },
	})
	comp, err := s.Build(module.Inputs{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	langs := comp.Resolvers().Languages()
	found := false
	for _, l := range langs {
		if l == "elixir" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the contributed resolver is missing: %v", langs)
	}
	if !comp.Resolvers().Frozen() {
		t.Fatal("the resolver registry was not frozen by Build")
	}

	// Claiming the language typeresolve.NewRegistry already pre-armed is refused.
	shadow := module.NewSet()
	mustAdd(t, shadow, module.Module{
		Manifest: module.Manifest{ID: "third.party", Version: "1"},
		Register: func(b *module.Builder) error { return b.AddResolver(fakeResolver{lang: "go"}) },
	})
	if _, err := shadow.Build(module.Inputs{}); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("a module shadowed the built-in go resolver: %v", err)
	}
}

// fakeResolver is a synthetic registrant for the resolver contribution kind. It
// resolves nothing: the kind's WIRING is what these tests are about, and the
// resolution contract itself is engine/typeresolve's to prove.
type fakeResolver struct{ lang string }

func (f fakeResolver) Language() string     { return f.lang }
func (f fakeResolver) Subject(string) bool  { return false }
func (f fakeResolver) Input(string) bool    { return false }
func (f fakeResolver) Owns(string) bool     { return false }
func (f fakeResolver) Triggers(string) bool { return false }
func (f fakeResolver) Resolve(map[string][]byte, map[model.NodeId]struct{}) (typeresolve.Result, error) {
	return typeresolve.Result{}, nil
}

// AC-3: Build freezes everything, and the set itself refuses further additions.
func TestAX07_Build_FreezesEveryRegistry(t *testing.T) {
	store := graphstore.NewMemStore()
	s, err := module.Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	comp, err := s.Build(portedInputs(store))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !comp.Frozen() {
		t.Fatal("Frozen() = false after Build")
	}
	if err := comp.Parsers().Register(parse.NewGoParser()); !errors.Is(err, registry.ErrFrozen) {
		t.Errorf("parser registry accepted a post-build Register: %v", err)
	}
	if err := comp.Resolvers().Register(fakeResolver{lang: "elixir"}); !errors.Is(err, registry.ErrFrozen) {
		t.Errorf("resolver registry accepted a post-build Register: %v", err)
	}
	if err := comp.Operations().Add(sampleSpec(t)); !errors.Is(err, registry.ErrFrozen) {
		t.Errorf("operation catalog accepted a post-build Add: %v", err)
	}
	if !comp.Analysis().Frozen() {
		t.Error("the analysis service was not frozen by Build")
	}
	if err := s.Add(noopModule("late")); !errors.Is(err, registry.ErrFrozen) {
		t.Errorf("the module set accepted a post-build Add: %v", err)
	}
	if _, err := s.Build(portedInputs(store)); err == nil {
		t.Error("a second Build succeeded; composition must happen once")
	}
}

// AC-1/AC-4: the built-in module set composes EXACTLY what the pre-AX-07
// constructors composed. This is the equivalence the whole refactor rests on,
// asserted at the registry level rather than inferred from the surface bytes.
func TestAX07_Builtins_ComposeTheSameCapabilitiesAsTheLegacyConstructors(t *testing.T) {
	store := graphstore.NewMemStore()
	comp, err := module.BuildBuiltins(portedInputs(store))
	if err != nil {
		t.Fatalf("BuildBuiltins: %v", err)
	}

	if got, want := comp.ModuleIDs(), []string{module.IDParse, module.IDAnalysis, module.IDCompound, module.IDDeadCode, module.IDOperations}; !reflect.DeepEqual(got, want) {
		t.Fatalf("built-in composition order = %v, want %v", got, want)
	}

	if got, want := comp.Parsers().Languages(), parse.NewDefaultRegistry().Languages(); !reflect.DeepEqual(got, want) {
		t.Errorf("parser languages diverged\n  module set: %v\n  legacy:     %v", got, want)
	}
	if got, want := comp.Analysis().Names(), analysis.NewDefaultService(store).Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("analyzer names diverged\n  module set: %v\n  legacy:     %v", got, want)
	}
	shadow, serr := opcatalog.Shadow()
	if serr != nil {
		t.Fatalf("opcatalog.Shadow: %v", serr)
	}
	if got, want := comp.Operations().IDs(), shadow.IDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("operation ids diverged\n  module set: %v\n  shadow:     %v", got, want)
	}

	// Non-vacuity: the comparison is worthless if either side is empty.
	if len(comp.Parsers().Languages()) < 20 || len(comp.Analysis().Names()) < 10 || comp.Operations().Len() < 50 {
		t.Fatalf("the built-in composition looks empty: %d parsers, %d analyzers, %d operations",
			len(comp.Parsers().Languages()), len(comp.Analysis().Names()), comp.Operations().Len())
	}
}

// The Searcher-conditional analyzer is part of the legacy constructor's
// behaviour, so the module path has to reproduce it — including the case where
// the reader is NOT a Searcher and the concept analyzer must be absent.
func TestAX07_Builtins_ReproduceTheConditionalAnalyzerRegistrations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader interface {
			graphstore.Graphstore
		}
	}{
		{"memstore", graphstore.NewMemStore()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := module.BuildBuiltins(portedInputs(tc.reader))
			if err != nil {
				t.Fatalf("BuildBuiltins: %v", err)
			}
			if got, want := comp.Analysis().Names(), analysis.NewDefaultService(tc.reader).Names(); !reflect.DeepEqual(got, want) {
				t.Fatalf("analyzer set diverged for %s\n  module set: %v\n  legacy:     %v", tc.name, got, want)
			}
		})
	}

	// A reader that is not a graphstore at all still has to agree.
	comp, err := module.BuildBuiltins(portedInputs(nil))
	if err != nil {
		t.Fatalf("BuildBuiltins(nil reader): %v", err)
	}
	if got, want := comp.Analysis().Names(), analysis.NewDefaultService(nil).Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("analyzer set diverged for a nil reader\n  module set: %v\n  legacy:     %v", got, want)
	}
}

// A malformed manifest is refused at Add, before anything can depend on it.
func TestAX07_Add_RejectsMalformedManifests(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  module.Module
	}{
		{"no id", module.Module{Manifest: module.Manifest{Version: "1"}, Register: func(*module.Builder) error { return nil }}},
		{"no version", module.Module{Manifest: module.Manifest{ID: "a"}, Register: func(*module.Builder) error { return nil }}},
		{"no register", module.Module{Manifest: module.Manifest{ID: "a", Version: "1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := module.NewSet().Add(tc.mod); err == nil {
				t.Fatal("a malformed manifest was accepted")
			}
		})
	}
}

// A module whose Register fails aborts the whole composition, naming the module.
// Half a composition is worse than none: it would freeze registries that are
// missing capabilities the surfaces will advertise.
func TestAX07_Build_FailsClosedOnARegisterError(t *testing.T) {
	boom := errors.New("boom")
	s := module.NewSet()
	mustAdd(t, s, module.Module{
		Manifest: module.Manifest{ID: "broken", Version: "1"},
		Register: func(*module.Builder) error { return boom },
	})
	comp, err := s.Build(module.Inputs{})
	if err == nil {
		t.Fatal("Build succeeded over a failing module")
	}
	if comp != nil {
		t.Fatal("Build returned a composition alongside an error")
	}
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error %v does not identify the failing module", err)
	}
}
