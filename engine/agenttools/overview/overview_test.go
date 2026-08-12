package overview

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

// fixtureDeps builds a small repository-shaped graph: a Go command tree, a
// library package, a test file, a vendored file, and a YAML config.
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
	mainFn := mk("function", "main.main", "cmd/app/main.go", 5)
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	helper := mk("function", "pkg.Helper", "pkg/helper.go", 5)
	format := mk("function", "util.Format", "util/format.go", 3)
	testFn := mk("function", "tests.TestFormat", "util/format_test.go", 8)
	mk("function", "vendorlib.F", "vendor/lib/f.go", 1)
	mk("config_key", "cfg.AppName", "deploy/app.yaml", 1)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, conf float64, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, conf, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(mainFn, run, "calls", model.TierConfirmed, 0.95, "cmd/app/main.go:6")
	edge(run, format, "calls", model.TierConfirmed, 0.95, "cmd/app/main.go:12")
	edge(testFn, format, "calls", model.TierConfirmed, 0.9, "util/format_test.go:9")
	edge(helper, format, "references", model.TierHeuristic, 0.4, "pkg/helper.go:7")
	edge(run, helper, "calls", model.TierDerived, 0.8, "cmd/app/main.go:14")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func TestRepoOverviewFound(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	for _, want := range []string{"7 nodes", "5 edges", "top language Go", "1 test file(s)", MethodVersion} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, res.Summary)
		}
	}

	var sawIdentity, sawDir, sawLang, sawEntry, sawCentral, sawTests, sawGenerated, sawNext bool
	for _, it := range res.Items {
		switch {
		case it.RefID == "identity":
			sawIdentity = true
		case strings.HasPrefix(it.Reason, "dir: cmd/app"):
			sawDir = true
		case strings.HasPrefix(it.Reason, "language: Go"):
			sawLang = true
		case strings.HasPrefix(it.Reason, "entrypoint:") && strings.Contains(it.Reason, "main.main"):
			sawEntry = true
		case strings.HasPrefix(it.Reason, "central:") && strings.Contains(it.Reason, "util.Format"):
			sawCentral = true
			if !strings.Contains(it.Reason, "3 inbound edge(s)") {
				t.Fatalf("central symbol must report its degree: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "tests: util"):
			sawTests = true
		case strings.HasPrefix(it.Reason, "generated: vendor"):
			sawGenerated = true
		case strings.HasPrefix(it.Reason, "next: graphi symbol-context util.Format"):
			sawNext = true
		}
	}
	if !sawIdentity || !sawDir || !sawLang || !sawEntry || !sawCentral || !sawTests || !sawGenerated || !sawNext {
		t.Fatalf("missing sections: identity=%v dir=%v lang=%v entry=%v central=%v tests=%v generated=%v next=%v",
			sawIdentity, sawDir, sawLang, sawEntry, sawCentral, sawTests, sawGenerated, sawNext)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestRepoOverviewCommunitiesOptIn(t *testing.T) {
	deps := fixtureDeps(t)

	base, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range base.Items {
		if strings.HasPrefix(it.Reason, "community:") {
			t.Fatalf("default call must not run the community pass: %q", it.Reason)
		}
	}

	withComms, err := Assemble(context.Background(), Params{Deps: deps, Communities: true})
	if err != nil {
		t.Fatal(err)
	}
	var sawCommunity bool
	for _, it := range withComms.Items {
		if strings.HasPrefix(it.Reason, "community:") {
			sawCommunity = true
		}
	}
	if !sawCommunity {
		t.Fatal("expected community items with Communities: true")
	}
}

func TestRepoOverviewEmptyAndUnavailable(t *testing.T) {
	store := graphstore.NewMemStore()
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	res, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty for an empty graph, got %s", res.Outcome)
	}
	if !strings.Contains(res.Summary, "graphi index") {
		t.Fatalf("empty summary must point at indexing: %q", res.Summary)
	}

	res, err = Assemble(context.Background(), Params{Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}
}

func TestRepoOverviewDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func(comms bool) []byte {
		res, err := Assemble(context.Background(), Params{Deps: deps, Communities: comms})
		if err != nil {
			t.Fatal(err)
		}
		b, err := contract.Serialize(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Equal(run(false), run(false)) {
		t.Fatal("default call is non-deterministic")
	}
	if !bytes.Equal(run(true), run(true)) {
		t.Fatal("community call is non-deterministic")
	}
}
