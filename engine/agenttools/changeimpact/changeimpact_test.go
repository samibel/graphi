package changeimpact

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds a small change-shaped graph:
//
//	util.Format (exported)  <--calls(confirmed)--  tests.TestFormat
//	util.Format             <--calls(derived)--    main.Run
//	main.Run                <--calls(confirmed)--  tests.TestRun
//	pkg.helper (unexported) <--references(heuristic)-- main.Run ... plus a config file
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
	helper := mk("function", "util.helper", "util/format.go", 40)
	testFormat := mk("function", "tests.TestFormat", "util/format_test.go", 8)
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	mk("function", "tests.TestRun", "cmd/app/main_test.go", 4)
	mk("config_key", "cfg.AppName", "deploy/app.yaml", 1)

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
	edge(run, helper, "references", model.TierHeuristic, "cmd/app/main.go:15")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

const sampleDiff = `--- a/util/format.go
+++ b/util/format.go
@@ -3,1 +3,1 @@
-old
+new
--- a/deploy/app.yaml
+++ b/deploy/app.yaml
@@ -1,1 +1,1 @@
-a: 1
+a: 2
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
	// util/format.go changes Format (exported) + helper (unexported); the
	// yaml file resolves to cfg.AppName. Public API = Format + AppName = 2.
	for _, want := range []string{"3 changed symbol(s)", "2 public-API change(s)", "1 config file(s)", MethodVersion} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, res.Summary)
		}
	}

	var sawChanged, sawAPI, sawDependent, sawTest, sawConfig, sawReasonAPI, sawReasonUntested, sawRisk bool
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.Reason, "changed:") && strings.Contains(it.Reason, "util.Format"):
			sawChanged = true
		case strings.HasPrefix(it.Reason, "public_api:") && strings.Contains(it.Reason, "util.Format"):
			sawAPI = true
		case strings.HasPrefix(it.Reason, "dependent:") && strings.Contains(it.Reason, "main.Run"):
			sawDependent = true
		case strings.HasPrefix(it.Reason, "test:") && strings.Contains(it.Reason, "tests.TestFormat"):
			sawTest = true
		case strings.HasPrefix(it.Reason, "config: deploy/app.yaml"):
			sawConfig = true
		case strings.HasPrefix(it.Reason, "reason: public interface changed"):
			sawReasonAPI = true
		case strings.HasPrefix(it.Reason, "reason: no test directly covers util.helper"):
			sawReasonUntested = true
		case strings.HasPrefix(it.Reason, "risk:"):
			sawRisk = true
		}
	}
	if !sawChanged || !sawAPI || !sawDependent || !sawTest || !sawConfig || !sawReasonAPI || !sawReasonUntested || !sawRisk {
		t.Fatalf("missing sections: changed=%v api=%v dep=%v test=%v config=%v reasonAPI=%v reasonUntested=%v risk=%v",
			sawChanged, sawAPI, sawDependent, sawTest, sawConfig, sawReasonAPI, sawReasonUntested, sawRisk)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestPublicAPIEscalatesRisk(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Diff: sampleDiff, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	// Base thresholds put this graph at medium (fan-in 3 > lowMaxFanIn); the
	// exported-symbol change escalates one step to high.
	if !strings.Contains(res.Summary, "risk high") {
		t.Fatalf("expected public-API escalation to high: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "2 public-API change(s)") {
		t.Fatalf("summary must count the API changes driving the escalation: %q", res.Summary)
	}
}

func TestAssembleFromTargetAndValidation(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Target: "util.Format", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}

	if _, err := Assemble(context.Background(), Params{Deps: deps}); err == nil {
		t.Fatal("missing target and diff must error")
	}
	if _, err := Assemble(context.Background(), Params{Target: "x", Diff: "y", Deps: deps}); err == nil {
		t.Fatal("both target and diff must error")
	}
	out, err := Assemble(context.Background(), Params{Target: "x", Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", out.Outcome)
	}
}

func TestCoChangeSection(t *testing.T) {
	deps := fixtureDeps(t)
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := func(sha string, age time.Duration, files ...string) githistory.Commit {
		return githistory.Commit{SHA: sha, Author: "alice", Timestamp: now.Add(-age), FilesChanged: files}
	}
	// util/format.go historically changes together with util/format_doc.md.
	provider := &githistory.InMemoryProvider{Commits: []githistory.Commit{
		c("c3", 1*time.Hour, "util/format.go", "util/format_doc.md"),
		c("c2", 2*time.Hour, "util/format.go", "util/format_doc.md"),
		c("c1", 3*time.Hour, "util/format.go", "util/format_doc.md"),
	}}

	res, err := Assemble(context.Background(), Params{Diff: sampleDiff, Deps: deps, Provider: provider, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var sawPartner, sawReason bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "co_change: util/format_doc.md usually changes with util/format.go (3 co-commit(s)") {
			sawPartner = true
		}
		if strings.HasPrefix(it.Reason, "reason:") && strings.Contains(it.Reason, "usually change with this change") {
			sawReason = true
		}
	}
	if !sawPartner || !sawReason {
		t.Fatalf("missing co-change output: partner=%v reason=%v (%s)", sawPartner, sawReason, res.Summary)
	}
	if !strings.Contains(res.Summary, "1 co-change partner(s)") {
		t.Fatalf("summary must count partners: %q", res.Summary)
	}

	// Without a provider the section is absent (golden stability).
	res, err = Assemble(context.Background(), Params{Diff: sampleDiff, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "co_change:") {
			t.Fatalf("no provider must mean no co-change section: %q", it.Reason)
		}
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
