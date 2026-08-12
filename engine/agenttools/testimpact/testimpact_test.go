package testimpact

import (
	"bytes"
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

// fixtureDeps mirrors the testintel fixture through the agent-tool deps.
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
	format := mk("function", "util.Format", "util/format.go", 3)
	testFormat := mk("function", "tests.TestFormat", "util/format_test.go", 8)
	mk("function", "tests.TestFormat_Edge", "util/format_test.go", 30)
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	testRun := mk("function", "tests.TestRun", "cmd/app/main_test.go", 4)
	mk("function", "other.TestUnrelated", "other/other_test.go", 1)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, 0.9, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(testFormat, format, "calls", model.TierConfirmed, "util/format_test.go:9")
	edge(run, format, "calls", model.TierDerived, "cmd/app/main.go:12")
	edge(testRun, run, "calls", model.TierConfirmed, "cmd/app/main_test.go:5")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

const sampleDiff = `--- a/util/format.go
+++ b/util/format.go
@@ -3,1 +3,1 @@
-func Format(name string) string {
+func Format(name string) string { // changed
`

func TestAssembleFromDiff(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Diff: sampleDiff, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	for _, want := range []string{"1 changed symbol(s)", "1 must-run", "coverage risk low", MethodVersion} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, res.Summary)
		}
	}

	var sawMust, sawRecTransitive, sawRecNaming, sawUnaffected bool
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.Reason, "must_run:") && strings.Contains(it.Reason, "tests.TestFormat "):
			sawMust = true
			if !strings.Contains(it.Reason, "direct_call") {
				t.Fatalf("must_run must cite the direct-call signal: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "recommended:") && strings.Contains(it.Reason, "tests.TestRun"):
			sawRecTransitive = true
		case strings.HasPrefix(it.Reason, "recommended:") && strings.Contains(it.Reason, "TestFormat_Edge"):
			sawRecNaming = true
		case strings.HasPrefix(it.Reason, "probably_unaffected:") && strings.Contains(it.Reason, "other/other_test.go"):
			sawUnaffected = true
		case strings.HasPrefix(it.Reason, "must_run:") && strings.Contains(it.Reason, "TestFormat_Edge"):
			t.Fatalf("naming-only match must not be must_run: %q", it.Reason)
		}
	}
	if !sawMust || !sawRecTransitive || !sawRecNaming || !sawUnaffected {
		t.Fatalf("missing buckets: must=%v transitive=%v naming=%v unaffected=%v",
			sawMust, sawRecTransitive, sawRecNaming, sawUnaffected)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestAssembleUnknownPaths(t *testing.T) {
	deps := fixtureDeps(t)
	diff := "--- a/docs/readme.md\n+++ b/docs/readme.md\n"
	res, err := Assemble(context.Background(), Params{Diff: diff, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	var sawUnknown bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "unknown: docs/readme.md") {
			sawUnknown = true
		}
	}
	if !sawUnknown {
		t.Fatalf("expected an unknown item for the unresolvable path: %s", res.Summary)
	}
	if !strings.Contains(res.Summary, "1 unknown path(s)") {
		t.Fatalf("summary must count unknown paths: %q", res.Summary)
	}
}

func TestAssembleFromTarget(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Target: "util.Format", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "1 must-run") {
		t.Fatalf("target mode must find the direct test: %q", res.Summary)
	}
}

func TestAssembleNoTestsKnownIsACitedFinding(t *testing.T) {
	// A graph without any test files: the result must still cite the subject
	// and state the coverage gap instead of returning empty items.
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n, err := model.NewNode("function", "solo.Fn", "solo/fn.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}

	res, err := Assemble(ctx, Params{Target: "solo.Fn", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s", res.Outcome)
	}
	var sawCoverage bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "coverage: no test files known") {
			sawCoverage = true
		}
	}
	if !sawCoverage || len(res.Evidence) == 0 {
		t.Fatalf("expected a cited coverage finding: items=%+v evidence=%+v", res.Items, res.Evidence)
	}
	if !strings.Contains(res.Summary, "coverage risk high") {
		t.Fatalf("zero-signal change must grade coverage risk high: %q", res.Summary)
	}
}

func TestAssembleInputValidation(t *testing.T) {
	deps := fixtureDeps(t)
	if _, err := Assemble(context.Background(), Params{Deps: deps}); err == nil {
		t.Fatal("missing target and diff must error")
	}
	if _, err := Assemble(context.Background(), Params{Target: "x", Diff: "y", Deps: deps}); err == nil {
		t.Fatal("both target and diff must error")
	}
	res, err := Assemble(context.Background(), Params{Target: "x", Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}
}

func TestAssembleDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func() []byte {
		res, err := Assemble(context.Background(), Params{Diff: sampleDiff, Deps: deps})
		if err != nil {
			t.Fatal(err)
		}
		b, err := contract.Serialize(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic output:\n%s\n%s", a, b)
	}
}
