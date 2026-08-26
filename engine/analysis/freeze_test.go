package analysis_test

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/analysis"
)

// SW-222 (AX-02) AC-2: the analyzer registry DECLARES its collision policy, and
// the declaration is the OPPOSITE of core/parse's — the divergence ADR 0013
// threat T5 records and keeps.
func TestAnalysisRegistry_DeclaresItsCollisionPolicy(t *testing.T) {
	if analysis.CollisionPolicy != registry.PolicyFirstWinsWithReplace {
		t.Fatalf("analysis.CollisionPolicy = %s, want first-wins-with-replace", analysis.CollisionPolicy)
	}
	if got := analysis.NewRegistry().Policy(); got != analysis.CollisionPolicy {
		t.Fatalf("Policy() = %s, want %s", got, analysis.CollisionPolicy)
	}
	if analysis.CollisionPolicy.AllowsOverride() {
		t.Fatal("a duplicate Register must never silently override here")
	}
	if !analysis.CollisionPolicy.AllowsReplace() {
		t.Fatal("the git-provider seam needs the sanctioned Replace path")
	}
}

// AC-1: a rejected duplicate is now a TYPED error, and its message is the
// legacy one byte for byte.
func TestAnalysisRegistry_DuplicateIsTyped(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(charAnalyzer{name: "toy"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := r.Register(charAnalyzer{name: "toy"})
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate error = %v, want registry.ErrDuplicate", err)
	}
	if got, want := err.Error(), `analysis: analyzer "toy" already registered`; got != want {
		t.Fatalf("duplicate message changed:\n got: %s\nwant: %s", got, want)
	}
}

// AC-1: Replace of an unknown name is ErrMissingDependency, with the legacy
// message preserved.
func TestAnalysisRegistry_UnknownReplaceIsMissingDependency(t *testing.T) {
	err := analysis.NewRegistry().Replace(charAnalyzer{name: "toy"})
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("Replace of an unknown name = %v, want registry.ErrMissingDependency", err)
	}
	if got, want := err.Error(), `analysis: analyzer "toy" not registered`; got != want {
		t.Fatalf("Replace message changed:\n got: %s\nwant: %s", got, want)
	}
}

// AC-3: after Freeze both mutation entry points fail with the typed frozen
// error, and neither mutates.
func TestAnalysisRegistry_FreezeRefusesRegisterAndReplace(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(charAnalyzer{name: "toy", tag: "first"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	r.Freeze()
	if !r.Frozen() {
		t.Fatal("Frozen() = false after Freeze()")
	}

	if err := r.Register(charAnalyzer{name: "other"}); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register after freeze = %v, want registry.ErrFrozen", err)
	}
	if _, ok := r.Get("other"); ok {
		t.Fatal("a refused Register still registered the analyzer")
	}
	if err := r.Replace(charAnalyzer{name: "toy", tag: "second"}); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Replace after freeze = %v, want registry.ErrFrozen", err)
	}
	got, _ := r.Get("toy")
	res, _ := got.Analyze(context.Background(), nil, analysis.Params{})
	if res.Analyzer != "first" {
		t.Fatalf("a refused Replace still swapped the entry: %q", res.Analyzer)
	}
}

// AC-3 at the composition root: Service.Freeze ends the chain, and the
// git-provider seam is closed after it.
func TestAnalysisService_FreezeClosesTheGitProviderSeam(t *testing.T) {
	s := analysis.NewDefaultService(graphstore.NewMemStore())
	if s.Frozen() {
		t.Fatal("NewDefaultService must return an UNFROZEN service — WithGitProvider still has to run")
	}
	if s.Freeze() != s {
		t.Fatal("Freeze() must return the receiver so a composition root can chain it")
	}
	if !s.Frozen() {
		t.Fatal("Frozen() = false after Service.Freeze()")
	}
	// WithGitProvider panics on a Replace error by design (a programming fault
	// at the composition root); after freeze that fault is exactly what it is.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithGitProvider after Freeze silently succeeded")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, registry.ErrFrozen) {
			t.Fatalf("WithGitProvider after Freeze panicked with %v, want registry.ErrFrozen", r)
		}
	}()
	s.WithGitProvider(nil)
}

// AC-3 backstop: executing implies frozen. Dispatch is the single execution
// entry point, so a registry that has answered a request can no longer be
// mutated even if no composition root remembered to call Freeze.
func TestAnalysisService_DispatchFreezes(t *testing.T) {
	s := analysis.NewDefaultService(graphstore.NewMemStore())
	if s.Frozen() {
		t.Fatal("service frozen before any dispatch")
	}
	_, _ = s.Dispatch(context.Background(), "impact", analysis.Params{Symbol: "nope"})
	if !s.Frozen() {
		t.Fatal("Dispatch must freeze the registry — Execute is the end of the lifecycle")
	}
}

// AC-6: the rollback switch restores the legacy behaviour with no data
// migration; the registry still reports that it was frozen.
func TestAnalysisRegistry_FreezeIsDisableableForRollback(t *testing.T) {
	r := analysis.NewRegistry()
	r.Freeze()
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	if err := r.Register(charAnalyzer{name: "toy"}); err != nil {
		t.Fatalf("with freeze enforcement off, Register = %v, want nil", err)
	}
	if _, ok := r.Get("toy"); !ok {
		t.Fatal("with freeze enforcement off, the registration did not apply")
	}
	if !r.Frozen() {
		t.Fatal("the rollback switch must not erase the frozen state")
	}
}
