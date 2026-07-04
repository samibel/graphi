package explain

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds an in-memory graph:
//
//	main.Run         --calls(confirmed)-->      util.Format
//	tests.TestFormat --calls(confirmed)-->      util.Format
//	pkg.Helper       --references(heuristic)--> util.Format
//	main.Run         --calls(derived)-->        pkg.Helper
//	Dup exists twice (ambiguity fixture)
func fixtureDeps(t *testing.T) resolve.Deps {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	helper := mk("function", "pkg.Helper", "pkg/helper.go", 5)
	format := mk("function", "util.Format", "util/format.go", 3)
	testFn := mk("function", "tests.TestFormat", "util/format_test.go", 8)
	mk("function", "Dup", "a/dup.go", 1)
	mk("function", "Dup", "b/dup.go", 2)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, conf float64, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, conf, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(run, format, "calls", model.TierConfirmed, 0.95, "cmd/app/main.go:12")
	edge(testFn, format, "calls", model.TierConfirmed, 0.9, "util/format_test.go:9")
	edge(helper, format, "references", model.TierHeuristic, 0.4, "pkg/helper.go:7")
	edge(run, helper, "calls", model.TierDerived, 0.8, "cmd/app/main.go:14")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func TestExplainFound(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Explain(context.Background(), deps, "util.Format", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "2 callers") || !strings.Contains(res.Summary, "1 references") {
		t.Fatalf("summary missing relation counts: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "util/format.go:3") {
		t.Fatalf("summary missing definition site: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "exact_name") {
		t.Fatalf("summary must state resolution method: %q", res.Summary)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
	// Tier distribution must reflect the incident edges (2 confirmed, 1 heuristic).
	if res.Confidence.Top != "confirmed" {
		t.Fatalf("expected confirmed top tier, got %q", res.Confidence.Top)
	}
	if res.Confidence.Distribution["heuristic"] == 0 {
		t.Fatal("expected heuristic mass in distribution")
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if _, err := contract.Serialize(res); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	// No source bodies: items are one-line reasons only.
	for _, it := range res.Items {
		if strings.Contains(it.Reason, "\n") {
			t.Fatalf("item reason contains multi-line body: %q", it.Reason)
		}
	}
}

func TestExplainAmbiguousReturnsCandidates(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Explain(context.Background(), deps, "Dup", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeAmbiguous {
		t.Fatalf("expected ambiguous, got %s (%s)", res.Outcome, res.Summary)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(res.Items))
	}
	for _, it := range res.Items {
		if !strings.HasPrefix(it.Reason, "candidate:") {
			t.Fatalf("expected candidate reason, got %q", it.Reason)
		}
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestExplainEmptyWithHints(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Explain(context.Background(), deps, "NoSuchSymbolAnywhere", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty, got %s", res.Outcome)
	}
	if !strings.Contains(res.Summary, "search") {
		t.Fatalf("empty summary must include next-step hints: %q", res.Summary)
	}
}

func TestExplainUnavailableWithoutServices(t *testing.T) {
	res, err := Explain(context.Background(), resolve.Deps{}, "anything", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}
}

func TestExplainTruncationMarksPartial(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Explain(context.Background(), deps, "util.Format", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomePartial {
		t.Fatalf("expected partial under cap, got %s", res.Outcome)
	}
	if !res.Limits.Truncated || res.Limits.Dropped == 0 {
		t.Fatalf("limits must record truncation: %+v", res.Limits)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected capped items, got %d", len(res.Items))
	}
	// Definition always survives the cap.
	if !strings.HasPrefix(res.Items[0].Reason, "definition:") {
		t.Fatalf("definition must rank first, got %q", res.Items[0].Reason)
	}
}

func TestExplainByNodeID(t *testing.T) {
	deps := fixtureDeps(t)
	byName, err := Explain(context.Background(), deps, "util.Format", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id := byName.Items[0].RefID
	byID, err := Explain(context.Background(), deps, id, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if byID.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found via node id, got %s", byID.Outcome)
	}
	if !strings.Contains(byID.Summary, "node_id") {
		t.Fatalf("summary must state node_id resolution: %q", byID.Summary)
	}
}

func TestExplainRequiresReference(t *testing.T) {
	if _, err := Explain(context.Background(), resolve.Deps{}, "", 0); err == nil {
		t.Fatal("expected error for empty reference")
	}
}

func TestExplainDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Explain(context.Background(), deps, "util.Format", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Explain(context.Background(), deps, "util.Format", 0)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := contract.Serialize(a)
	bb, _ := contract.Serialize(b)
	if string(ab) != string(bb) {
		t.Fatal("explain output is not deterministic")
	}
}
