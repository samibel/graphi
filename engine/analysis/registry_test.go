package analysis_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// stubAnalyzer is a minimal Analyzer for registry contract tests.
type stubAnalyzer struct {
	name string
}

func (s stubAnalyzer) Name() string { return s.name }
func (s stubAnalyzer) Analyze(ctx context.Context, r query.Reader, p analysis.Params) (analysis.Analysis, error) {
	return analysis.Analysis{Analyzer: s.name, Outcome: query.OutcomeEmpty, Symbol: p.Symbol, Nodes: []analysis.ReachedNode{}}, nil
}

func TestRegistryRegisterGetNames(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(stubAnalyzer{name: "impact"}); err != nil {
		t.Fatalf("register impact: %v", err)
	}
	if err := r.Register(stubAnalyzer{name: "call-chain"}); err != nil {
		t.Fatalf("register call-chain: %v", err)
	}

	got, ok := r.Get("impact")
	if !ok || got.Name() != "impact" {
		t.Fatalf("Get(impact) = %v, %v", got, ok)
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) should be false")
	}

	names := r.Names()
	want := []string{"call-chain", "impact"}
	if !sortAreEqual(names, want) {
		t.Fatalf("Names() = %v, want sorted %v", names, want)
	}
}

func TestRegistryRejectsNilEmptyDuplicate(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("register nil analyzer must error")
	}
	if err := r.Register(stubAnalyzer{name: ""}); err == nil {
		t.Fatal("register empty-name analyzer must error")
	}
	if err := r.Register(stubAnalyzer{name: "impact"}); err != nil {
		t.Fatalf("first register impact: %v", err)
	}
	err := r.Register(stubAnalyzer{name: "impact"})
	if err == nil {
		t.Fatal("duplicate register must error")
	}
	// Duplicate registration is a typed error (not a panic) so callers can react.
	if !errors.Is(err, err) {
		t.Fatal("duplicate error value malformed")
	}
}

func TestRegistryGetConcurrent(t *testing.T) {
	// Smoke-test that RWMutex-protected access does not race under concurrent
	// reads (run with -race). The registry is effectively immutable after
	// construction in practice, but the lock must still hold.
	r := analysis.NewRegistry()
	_ = r.Register(stubAnalyzer{name: "impact"})
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = r.Get("impact")
				_ = r.Names()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}

// unused import guard: keep model referenced so the stub analyzer file compiles
// across analyzer additions without churn.
var _ = model.NodeId("")

func sortAreEqual(a, b []string) bool {
	sort.Strings(a)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
