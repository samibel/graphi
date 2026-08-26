package analysis_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// SW-222 (AX-02) characterization baseline for the ANALYZER registry.
//
// Written and made green BEFORE the registry-lifecycle refactor; must pass
// UNCHANGED after it. `engine/analysis.Registry` is FIRST-WINS with an
// error on duplicate — the deliberate opposite of `core/parse.Registry`'s
// last-wins policy (ADR 0013 threat T5). Its `Replace` is a narrow, sanctioned
// override that REFUSES unknown names so it cannot become a second registration
// path. All three properties are pinned here.

type charAnalyzer struct {
	name string
	tag  string
}

func (a charAnalyzer) Name() string { return a.name }
func (a charAnalyzer) Analyze(ctx context.Context, r query.Reader, p analysis.Params) (analysis.Analysis, error) {
	return analysis.Analysis{Analyzer: a.tag, Outcome: query.OutcomeEmpty, Symbol: p.Symbol, Nodes: []analysis.ReachedNode{}}, nil
}

// TestBaseline_AnalysisRegistry_DefaultServiceNames pins the exact built-in
// analyzer set of NewDefaultService over a store that also satisfies Searcher.
// This list feeds the coverage matrix (internal/coverage) and the CLI verb
// listing, so a change here is a product change.
func TestBaseline_AnalysisRegistry_DefaultServiceNames(t *testing.T) {
	got := analysis.NewDefaultService(graphstore.NewMemStore()).Names()
	want := []string{
		"batched", "call-chain", "communities", "compare-branches", "concept",
		"conflicts-prs", "contracts", "critique-review", "git-history", "impact",
		"interproc", "metrics", "notebook-ingest", "pdg", "pr-questions",
		"pr-risk", "pr-signals", "suggest-reviewers", "taint", "taint-query",
		"triage-prs", "watcher-status",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("default analyzer set drifted:\n got: %v\nwant: %v", got, want)
	}
}

// TestBaseline_AnalysisRegistry_FirstWinsRejectsDuplicate is the T5 divergence,
// pinned from the other side: a duplicate name is REJECTED with an error, never
// silently overwritten, and the first registration keeps the slot.
func TestBaseline_AnalysisRegistry_FirstWinsRejectsDuplicate(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(charAnalyzer{name: "toy", tag: "first"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := r.Register(charAnalyzer{name: "toy", tag: "second"})
	if err == nil {
		t.Fatal("duplicate registration must be rejected (first-wins policy)")
	}
	if !strings.Contains(err.Error(), `analyzer "toy" already registered`) {
		t.Fatalf("duplicate error message drifted: %v", err)
	}
	got, ok := r.Get("toy")
	if !ok {
		t.Fatal("Get(toy) missing after rejected duplicate")
	}
	res, aerr := got.Analyze(context.Background(), nil, analysis.Params{})
	if aerr != nil {
		t.Fatalf("Analyze: %v", aerr)
	}
	if res.Analyzer != "first" {
		t.Fatalf("rejected duplicate overwrote the entry: got %q, want %q", res.Analyzer, "first")
	}
}

// TestBaseline_AnalysisRegistry_RejectsNilAndEmptyName pins the two guard paths
// on both mutation entry points.
func TestBaseline_AnalysisRegistry_RejectsNilAndEmptyName(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil) must error")
	}
	if err := r.Register(charAnalyzer{name: ""}); err == nil {
		t.Fatal("Register(empty name) must error")
	}
	if err := r.Replace(nil); err == nil {
		t.Fatal("Replace(nil) must error")
	}
	if err := r.Replace(charAnalyzer{name: ""}); err == nil {
		t.Fatal("Replace(empty name) must error")
	}
}

// TestBaseline_AnalysisRegistry_ReplaceRequiresExistingName pins the sanctioned
// override: Replace swaps a REGISTERED name and refuses an unknown one, which is
// what keeps it from becoming a second registration path.
func TestBaseline_AnalysisRegistry_ReplaceRequiresExistingName(t *testing.T) {
	r := analysis.NewRegistry()
	if err := r.Replace(charAnalyzer{name: "toy", tag: "x"}); err == nil {
		t.Fatal("Replace of an unregistered name must error")
	} else if !strings.Contains(err.Error(), `analyzer "toy" not registered`) {
		t.Fatalf("Replace error message drifted: %v", err)
	}
	if _, ok := r.Get("toy"); ok {
		t.Fatal("refused Replace must not register the analyzer")
	}

	if err := r.Register(charAnalyzer{name: "toy", tag: "first"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Replace(charAnalyzer{name: "toy", tag: "second"}); err != nil {
		t.Fatalf("Replace of a registered name must succeed: %v", err)
	}
	got, _ := r.Get("toy")
	res, _ := got.Analyze(context.Background(), nil, analysis.Params{})
	if res.Analyzer != "second" {
		t.Fatalf("Replace did not swap the entry: got %q, want %q", res.Analyzer, "second")
	}
	if names := r.Names(); len(names) != 1 {
		t.Fatalf("Replace changed the name set: %v", names)
	}
}

// TestBaseline_AnalysisRegistry_NamesAreSorted pins the deterministic ordering
// surfaces advertise tools from.
func TestBaseline_AnalysisRegistry_NamesAreSorted(t *testing.T) {
	r := analysis.NewRegistry()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(charAnalyzer{name: n}); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}
	if got := strings.Join(r.Names(), ","); got != "alpha,mid,zeta" {
		t.Fatalf("Names() = %q, want sorted", got)
	}
}
