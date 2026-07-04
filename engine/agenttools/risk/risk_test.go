package risk

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

// fixtureDeps builds the shared in-memory relation graph (see explain tests),
// plus an isolated leaf symbol for the low-risk case.
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
	mk("function", "pkg.Leaf", "pkg/leaf.go", 30)

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

func TestAssessMediumRiskWithTestHint(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assess(context.Background(), deps, "util.Format", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "risk: medium") {
		t.Fatalf("expected medium risk, got %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "resolved exactly") {
		t.Fatalf("summary must state exact resolution: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "1 dependent test file(s)") {
		t.Fatalf("summary must carry the test hint: %q", res.Summary)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("risk must be evidence-based")
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestAssessLowRiskLeaf(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assess(context.Background(), deps, "pkg.Leaf", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Summary, "risk: low") {
		t.Fatalf("expected low risk for leaf symbol, got %q", res.Summary)
	}
}

func TestAssessUnresolvedIsUnknown(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assess(context.Background(), deps, "zzz.NotThere", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty outcome, got %s", res.Outcome)
	}
	if !strings.HasPrefix(res.Summary, "risk: unknown") {
		t.Fatalf("unknown must be stated, got %q", res.Summary)
	}
}

func TestAssessAmbiguousTarget(t *testing.T) {
	deps := fixtureDeps(t)
	ctx := context.Background()
	// Two identically-named symbols in different files.
	store := graphstore.NewMemStore()
	for _, spec := range []struct{ path string }{{"a/dup.go"}, {"b/dup.go"}} {
		n, err := model.NewNode("function", "Dup", spec.path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	deps = resolve.Deps{Query: query.New(store), Search: search.New(store)}
	res, err := Assess(ctx, deps, "Dup", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeAmbiguous {
		t.Fatalf("expected ambiguous, got %s", res.Outcome)
	}
}

func TestAssessFromDiff(t *testing.T) {
	deps := fixtureDeps(t)
	diff := "diff --git a/util/format.go b/util/format.go\n--- a/util/format.go\n+++ b/util/format.go\n@@ -1 +1 @@\n-x\n+y\n"
	res, err := Assess(context.Background(), deps, "", diff, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found from diff, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "diff") {
		t.Fatalf("summary must state diff resolution: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "risk:") {
		t.Fatalf("summary must state a risk level: %q", res.Summary)
	}
}

func TestDiffPaths(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n--- a/old.go\n+++ /dev/null\n"
	got := DiffPaths(diff)
	if len(got) != 2 || got[0] != "old.go" || got[1] != "x.go" {
		t.Fatalf("unexpected diff paths: %v", got)
	}
}

func TestAssessUnavailableWithoutServices(t *testing.T) {
	res, err := Assess(context.Background(), resolve.Deps{}, "anything", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}
}

func TestAssessRequiresInput(t *testing.T) {
	if _, err := Assess(context.Background(), resolve.Deps{}, "", "", 0); err == nil {
		t.Fatal("expected error for missing target and diff")
	}
}

func TestAssessDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Assess(context.Background(), deps, "util.Format", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Assess(context.Background(), deps, "util.Format", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := contract.Serialize(a)
	bb, _ := contract.Serialize(b)
	if string(ab) != string(bb) {
		t.Fatal("change_risk output is not deterministic")
	}
}
