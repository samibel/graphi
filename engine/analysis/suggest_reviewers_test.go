package analysis

import (
	"context"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/query"
)

// reviewerFixtureStore builds the small payment graph used by the golden-order
// test: Pay (touched) called by Cart and calling Charge + Refund, so Pay's
// affected subgraph is {Cart, Charge, Refund}. N=4 nodes.
func reviewerFixtureStore(t *testing.T) *graphstore.MemStore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	nodes := map[string]model.Node{}
	mk := func(key, qn, path string) {
		n, err := model.NewNode("function", qn, path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		nodes[key] = n
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	mk("Pay", "p.Pay", "pay.go")
	mk("Charge", "p.Charge", "charge.go")
	mk("Cart", "p.Cart", "cart.go")
	mk("Refund", "p.Refund", "refund.go")
	edge := func(from, to string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), query.EdgeKindCalls, model.TierConfirmed, 1, from+to, []string{from + to})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	edge("Pay", "Charge") // Pay calls Charge (callee neighbor)
	edge("Cart", "Pay")   // Cart calls Pay (caller neighbor)
	edge("Pay", "Refund") // Pay calls Refund (callee neighbor)
	return store
}

// reviewerFixtureProvider is the deterministic commit history (reverse-chronological,
// newest first). pay.go is the touched file; cart/charge/refund.go own the
// neighbors. The reference timestamp is the newest commit time (c1).
func reviewerFixtureProvider() *githistory.InMemoryProvider {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	return &githistory.InMemoryProvider{Commits: []githistory.Commit{
		{SHA: "c1", Author: "alice", Timestamp: t0, FilesChanged: []string{"pay.go"}},
		{SHA: "c2", Author: "bob", Timestamp: t0.Add(-3 * day), FilesChanged: []string{"cart.go"}},
		{SHA: "c3", Author: "alice", Timestamp: t0.Add(-5 * day), FilesChanged: []string{"pay.go"}},
		{SHA: "c4", Author: "carol", Timestamp: t0.Add(-10 * day), FilesChanged: []string{"charge.go"}},
		{SHA: "c5", Author: "dave", Timestamp: t0.Add(-15 * day), FilesChanged: []string{"refund.go"}},
		{SHA: "c6", Author: "bob", Timestamp: t0.Add(-40 * day), FilesChanged: []string{"pay.go"}},
	}}
}

// TestSuggestReviewers_GoldenOrder (AC-1): the ranked list + per-candidate signal
// breakdown matches the hand-computed golden order EXACTLY, including a tie
// (carol vs dave) resolved by reviewer identity ASC.
func TestSuggestReviewers_GoldenOrder(t *testing.T) {
	store := reviewerFixtureStore(t)
	a := suggestReviewersAnalyzer{provider: reviewerFixtureProvider(), weights: defaultReviewerWeights}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pay.go:Pay"})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	rep := res.Reviewers
	if rep == nil {
		t.Fatal("nil reviewer report")
	}
	if rep.Outcome != string(query.OutcomeFound) {
		t.Fatalf("outcome = %q, want found", rep.Outcome)
	}

	// Hand-computed golden order. Weights {ownership 10, recency 8, proximity 5}.
	//   alice: own 2 (c1,c3 on pay.go), rec 8 (4+4), prox 0      → 10*2 + 8*8        = 84
	//   bob:   own 1 (c6 on pay.go),    rec 2,        prox 3 (Cart, bucket 2)   → 10 + 16 + 15 = 41
	//   carol: own 0, rec 0, prox 3 (Charge, bucket 2)           → 15
	//   dave:  own 0, rec 0, prox 3 (Refund, bucket 2)           → 15  (tie → carol<dave)
	want := []ReviewerCandidate{
		{Reviewer: "alice", Composite: 84, Signals: ReviewerSignalBreakdown{Ownership: 2, RecencyDecayedChurn: 8, SubgraphProximity: 0}},
		{Reviewer: "bob", Composite: 41, Signals: ReviewerSignalBreakdown{Ownership: 1, RecencyDecayedChurn: 2, SubgraphProximity: 3}},
		{Reviewer: "carol", Composite: 15, Signals: ReviewerSignalBreakdown{Ownership: 0, RecencyDecayedChurn: 0, SubgraphProximity: 3}},
		{Reviewer: "dave", Composite: 15, Signals: ReviewerSignalBreakdown{Ownership: 0, RecencyDecayedChurn: 0, SubgraphProximity: 3}},
	}
	if len(rep.Candidates) != len(want) {
		t.Fatalf("got %d candidates, want %d: %+v", len(rep.Candidates), len(want), rep.Candidates)
	}
	for i, w := range want {
		got := rep.Candidates[i]
		if got != w {
			t.Fatalf("candidate[%d] = %+v, want %+v", i, got, w)
		}
	}
	// Honesty: the granularity labels must be surfaced.
	if rep.SignalGranularity["ownership"] != "file" || rep.SignalGranularity["subgraph_proximity"] != "symbol" {
		t.Fatalf("missing/incorrect granularity labels: %+v", rep.SignalGranularity)
	}
}

// TestSuggestReviewers_Determinism (AC-3): identical inputs + graph → byte-identical
// serialized output across repeated runs, with the tie-break exercised.
func TestSuggestReviewers_Determinism(t *testing.T) {
	store := reviewerFixtureStore(t)
	a := suggestReviewersAnalyzer{provider: reviewerFixtureProvider(), weights: defaultReviewerWeights}

	var first []byte
	for i := 0; i < 8; i++ {
		res, err := a.Analyze(context.Background(), store, Params{Diff: "pay.go:Pay"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := MarshalReviewers(*res.Reviewers)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
			continue
		}
		if string(b) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, b)
		}
	}
}

// TestSuggestReviewers_FullVsIncremental (AC-4): a graph built "full" (all nodes
// at once) vs "incremental" (different insertion order) over the same logical
// state yields byte-identical reviewer output.
func TestSuggestReviewers_FullVsIncremental(t *testing.T) {
	ctx := context.Background()
	full := reviewerFixtureStore(t)

	// Incremental: insert the same content in a different order.
	incr := graphstore.NewMemStore()
	type spec struct{ qn, path string }
	specs := map[string]spec{
		"Pay":    {"p.Pay", "pay.go"},
		"Charge": {"p.Charge", "charge.go"},
		"Cart":   {"p.Cart", "cart.go"},
		"Refund": {"p.Refund", "refund.go"},
	}
	nodes := map[string]model.Node{}
	for _, k := range []string{"Refund", "Cart", "Charge", "Pay"} { // reversed order
		n, err := model.NewNode("function", specs[k].qn, specs[k].path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		nodes[k] = n
		if err := incr.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	edge := func(from, to string) {
		e, _ := model.NewEdge(nodes[from].ID(), nodes[to].ID(), query.EdgeKindCalls, model.TierConfirmed, 1, from+to, []string{from + to})
		if err := incr.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	edge("Pay", "Refund")
	edge("Cart", "Pay")
	edge("Pay", "Charge")

	a := suggestReviewersAnalyzer{provider: reviewerFixtureProvider(), weights: defaultReviewerWeights}
	rf, _ := a.Analyze(ctx, full, Params{Diff: "pay.go:Pay"})
	ri, _ := a.Analyze(ctx, incr, Params{Diff: "pay.go:Pay"})
	bf, _ := MarshalReviewers(*rf.Reviewers)
	bi, _ := MarshalReviewers(*ri.Reviewers)
	if string(bf) != string(bi) {
		t.Fatalf("full vs incremental differ:\nfull: %s\nincr: %s", bf, bi)
	}
}

// TestSuggestReviewers_Degenerate (AC-7): no churn history and an empty touched set
// each yield a well-defined empty/stable result with no error and no nondeterminism.
func TestSuggestReviewers_Degenerate(t *testing.T) {
	store := reviewerFixtureStore(t)
	ctx := context.Background()

	// No git history (nil provider) → stable empty list, outcome empty, no error.
	noHist := suggestReviewersAnalyzer{provider: nil, weights: defaultReviewerWeights}
	res, err := noHist.Analyze(ctx, store, Params{Diff: "pay.go:Pay"})
	if err != nil {
		t.Fatalf("no-history analyze: %v", err)
	}
	if res.Reviewers.Outcome != string(query.OutcomeEmpty) || len(res.Reviewers.Candidates) != 0 {
		t.Fatalf("no-history want empty/0, got %q/%d", res.Reviewers.Outcome, len(res.Reviewers.Candidates))
	}

	// Empty touched set (empty diff) → empty result, no error.
	withHist := suggestReviewersAnalyzer{provider: reviewerFixtureProvider(), weights: defaultReviewerWeights}
	res, err = withHist.Analyze(ctx, store, Params{Diff: ""})
	if err != nil {
		t.Fatalf("empty-diff analyze: %v", err)
	}
	if res.Reviewers.Outcome != string(query.OutcomeEmpty) || len(res.Reviewers.Candidates) != 0 {
		t.Fatalf("empty-diff want empty/0, got %q/%d", res.Reviewers.Outcome, len(res.Reviewers.Candidates))
	}
	// Empty slices, never null (byte-shape stability).
	b, _ := MarshalReviewers(*res.Reviewers)
	if !contains(string(b), `"candidates":[]`) {
		t.Fatalf("empty candidates must serialize as [], got %s", b)
	}
}

// TestSuggestReviewers_ZeroEgress (AC-5): the analyzer source must import no
// network/process/filesystem-egress package (pure local graph + history reads).
func TestSuggestReviewers_ZeroEgress(t *testing.T) {
	assertNoEgressImports(t, "suggest_reviewers.go")
}

// assertNoEgressImports parses a source file in this package and fails if it
// imports any forbidden egress/CGo-prone package. This is the static, in-process
// proof of the zero-engine-egress contract (complements cmd/canary, which the
// sandbox may not run on macOS).
func assertNoEgressImports(t *testing.T, file string) {
	t.Helper()
	forbidden := map[string]struct{}{
		`"net"`:        {},
		`"net/http"`:   {},
		`"os"`:         {},
		`"os/exec"`:    {},
		`"syscall"`:    {},
		`"io/ioutil"`:  {},
		`"net/url"`:    {},
		`"crypto/tls"`: {},
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, imp := range f.Imports {
		if _, bad := forbidden[imp.Path.Value]; bad {
			t.Fatalf("%s imports forbidden egress package %s", file, imp.Path.Value)
		}
	}
}

// contains is a tiny substring helper (avoids importing strings just for tests).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
