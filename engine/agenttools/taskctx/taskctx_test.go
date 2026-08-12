package taskctx

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds the shared agent-tool fixture graph plus a config file:
//
//	main.Run         --calls(confirmed)-->      util.Format
//	tests.TestFormat --calls(confirmed)-->      util.Format
//	pkg.Helper       --references(heuristic)--> util.Format
//	main.Run         --calls(derived)-->        pkg.Helper
//	util/app.yaml holds a config node (config section fixture)
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
	mk("config_key", "cfg.AppName", "util/app.yaml", 1)

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

// memReader is an in-memory snippet source keyed by path.
type memReader map[string][]string

func (m memReader) ReadSpan(path string, want enginecontext.Span) (string, enginecontext.Span, error) {
	lines, ok := m[path]
	if !ok {
		return "", enginecontext.Span{}, fmt.Errorf("not found: %s", path)
	}
	start, end := want.Start, want.End
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", enginecontext.Span{Start: start, End: start - 1}, nil
	}
	return strings.Join(lines[start-1:end], "\n"), enginecontext.Span{Start: start, End: end}, nil
}

func sources() memReader {
	return memReader{
		"util/format.go": {
			"package util",
			"// Format renders a greeting.",
			"func Format(name string) string {",
			"\treturn \"hello \" + name",
			"}",
		},
		"cmd/app/main.go": {
			"package main", "", "", "", "", "", "", "", "",
			"func Run() { util.Format(\"x\") }",
		},
	}
}

func TestTaskContextExactSymbol(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Task: "util.Format", Deps: deps, Reader: sources()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	for _, want := range []string{"1 seed(s)", MethodVersion, "weights " + WeightsHash(), "risk"} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, res.Summary)
		}
	}

	var sawPrimary, sawCaller, sawTest, sawFile, sawRead, sawRisk bool
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.Reason, "primary:") && strings.Contains(it.Reason, "util.Format"):
			sawPrimary = true
		case strings.HasPrefix(it.Reason, "caller:") && strings.Contains(it.Reason, "main.Run"):
			sawCaller = true
			if !strings.Contains(it.Reason, "calls 150") {
				t.Fatalf("caller breakdown must show calls×confirmed = 150: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "test:") && strings.Contains(it.Reason, "tests.TestFormat"):
			sawTest = true
		case strings.HasPrefix(it.Reason, "file:"):
			sawFile = true
		case strings.HasPrefix(it.Reason, "read 1:"):
			sawRead = true
			if !strings.Contains(it.Reason, "util/format.go") {
				t.Fatalf("read order must start at the seed file: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "risk:"):
			sawRisk = true
		}
	}
	if !sawPrimary || !sawCaller || !sawTest || !sawFile || !sawRead || !sawRisk {
		t.Fatalf("missing sections: primary=%v caller=%v test=%v file=%v read=%v risk=%v",
			sawPrimary, sawCaller, sawTest, sawFile, sawRead, sawRisk)
	}

	var snippets int
	for _, ev := range res.Evidence {
		if ev.Role == "snippet" {
			snippets++
			if ev.Snippet == "" || ev.Span == "" {
				t.Fatalf("snippet evidence incomplete: %+v", ev)
			}
		}
	}
	if snippets == 0 {
		t.Fatal("expected at least one token-budgeted snippet")
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestTaskContextConfigSection(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Task: "util.Format", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	var sawConfig bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "config:") && strings.Contains(it.Reason, "util/app.yaml") {
			sawConfig = true
		}
	}
	if !sawConfig {
		t.Fatalf("expected the config section to surface util/app.yaml: %+v", res.Items)
	}
}

func TestTaskContextFallbackTokenization(t *testing.T) {
	deps := fixtureDeps(t)
	// The full phrase matches no qualified name; the token fallback must find
	// "format" and seed from it instead of returning empty.
	res, err := Assemble(context.Background(), Params{Task: "format the greeting output", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found via token fallback, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "seed(s)") {
		t.Fatalf("summary must report seeds: %q", res.Summary)
	}
}

func TestTaskContextEmptyAndUnavailable(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Task: "zzzqqq nothing matches here", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty, got %s", res.Outcome)
	}

	res, err = Assemble(context.Background(), Params{Task: "x", Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}

	if _, err := Assemble(context.Background(), Params{Task: "   ", Deps: deps}); err == nil {
		t.Fatal("blank task must error")
	}
}

func TestTaskContextDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func() []byte {
		res, err := Assemble(context.Background(), Params{Task: "util.Format", Deps: deps, Reader: sources()})
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

// TestWeightsHashPinned makes any weight change an explicit, reviewed diff:
// the hash is printed in every summary, so silently retuning the model would
// change agent-visible output everywhere.
func TestWeightsHashPinned(t *testing.T) {
	const pinned = "19de62a1"
	if got := WeightsHash(); got != pinned {
		t.Fatalf("weights hash drifted: got %s, want %s — an intentional retune must update this pin AND the CHANGELOG", got, pinned)
	}
}
