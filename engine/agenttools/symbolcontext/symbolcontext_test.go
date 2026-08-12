package symbolcontext

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

// fixtureDeps builds the shared agent-tool fixture graph plus a hierarchy pair
// and a second-hop test caller:
//
//	main.Run          --calls(confirmed)-->      util.Format
//	tests.TestFormat  --calls(confirmed)-->      util.Format
//	pkg.Helper        --references(heuristic)--> util.Format
//	main.Run          --calls(derived)-->        pkg.Helper
//	tests.TestRun     --calls(confirmed)-->      main.Run      (depth-2 test)
//	impl.Concrete     --implements(confirmed)--> util.Formatter
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
	testRun := mk("function", "tests.TestRun", "cmd/app/main_test.go", 4)
	iface := mk("interface", "util.Formatter", "util/formatter.go", 2)
	concrete := mk("type", "impl.Concrete", "impl/concrete.go", 6)
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
	edge(testRun, run, "calls", model.TierConfirmed, 0.9, "cmd/app/main_test.go:5")
	edge(concrete, iface, "implements", model.TierConfirmed, 0.9, "impl/concrete.go:6")

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

func formatSource() memReader {
	return memReader{
		"util/format.go": {
			"package util",
			"// Format renders a greeting.",
			"func Format(name string) string {",
			"\treturn \"hello \" + name",
			"}",
		},
	}
}

func TestSymbolContextFound(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Context(context.Background(), Params{Ref: "util.Format", Deps: deps, Reader: formatSource()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	// Default depth is 2: the direct test (TestFormat) plus the indirect one
	// (TestRun via main.Run) are both in reach.
	for _, want := range []string{"2 callers", "0 callees", "1 references", "util/format.go:3", "risk medium", "2 test file(s)", MethodVersion} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, res.Summary)
		}
	}

	var sawDefinition, sawSnippet, sawTest, sawRisk bool
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.Reason, "definition:"):
			sawDefinition = true
		case strings.HasPrefix(it.Reason, "snippet:"):
			sawSnippet = true
		case strings.HasPrefix(it.Reason, "test:") && strings.Contains(it.Reason, "tests.TestFormat"):
			sawTest = true
			if !strings.Contains(it.Reason, "[depth 1]") {
				t.Fatalf("direct test must be depth 1: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "risk:"):
			sawRisk = true
			if !strings.Contains(it.Reason, "medium") {
				t.Fatalf("unexpected risk item: %q", it.Reason)
			}
		}
	}
	if !sawDefinition || !sawSnippet || !sawTest || !sawRisk {
		t.Fatalf("missing sections: def=%v snippet=%v test=%v risk=%v", sawDefinition, sawSnippet, sawTest, sawRisk)
	}

	// The snippet evidence carries the cited source text including the doc line.
	var snip string
	for _, ev := range res.Evidence {
		if ev.Role == "snippet" {
			snip = ev.Snippet
			if ev.Span == "" {
				t.Fatal("snippet evidence must carry its span")
			}
		}
	}
	if !strings.Contains(snip, "// Format renders a greeting.") || !strings.Contains(snip, "func Format(name string) string {") {
		t.Fatalf("snippet must include doc comment and declaration: %q", snip)
	}

	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestSymbolContextDepthTwoFindsIndirectTest(t *testing.T) {
	deps := fixtureDeps(t)

	shallow, err := Context(context.Background(), Params{Ref: "util.Format", Depth: 1, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	deep, err := Context(context.Background(), Params{Ref: "util.Format", Depth: 2, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}

	countTests := func(r *contract.Result) (n int, sawDeep bool) {
		for _, it := range r.Items {
			if strings.HasPrefix(it.Reason, "test:") {
				n++
				if strings.Contains(it.Reason, "tests.TestRun") && strings.Contains(it.Reason, "[depth 2]") {
					sawDeep = true
				}
			}
		}
		return
	}
	if n, _ := countTests(shallow); n != 1 {
		t.Fatalf("depth 1: expected exactly the direct test, got %d test items", n)
	}
	if n, sawDeep := countTests(deep); n != 2 || !sawDeep {
		t.Fatalf("depth 2: expected direct + indirect test (got %d, indirect=%v)", n, sawDeep)
	}
}

func TestSymbolContextHierarchy(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Context(context.Background(), Params{Ref: "util.Formatter", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	var sawImplementer, sawSubtype bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "implementer:") && strings.Contains(it.Reason, "impl.Concrete") {
			sawImplementer = true
		}
		if strings.HasPrefix(it.Reason, "subtype:") && strings.Contains(it.Reason, "impl.Concrete") {
			sawSubtype = true
		}
	}
	if !sawImplementer || !sawSubtype {
		t.Fatalf("expected implementer and subtype items for util.Formatter: %+v", res.Items)
	}
}

func TestSymbolContextAmbiguous(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Context(context.Background(), Params{Ref: "Dup", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeAmbiguous {
		t.Fatalf("expected ambiguous, got %s (%s)", res.Outcome, res.Summary)
	}
}

func TestSymbolContextEmptyAndUnavailable(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Context(context.Background(), Params{Ref: "no.Such.Symbol.Anywhere", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty, got %s", res.Outcome)
	}

	res, err = Context(context.Background(), Params{Ref: "x", Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}

	if _, err := Context(context.Background(), Params{Deps: deps}); err == nil {
		t.Fatal("empty ref must error")
	}
}

func TestSymbolContextNoReaderHintsInLimits(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Context(context.Background(), Params{Ref: "util.Format", Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "snippet:") {
			t.Fatal("no reader: snippet item must be absent")
		}
	}
	if !strings.Contains(res.Limits.Next, "snippet") {
		t.Fatalf("limits.next must explain the missing snippet: %q", res.Limits.Next)
	}
}

func TestSymbolContextDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func() []byte {
		res, err := Context(context.Background(), Params{Ref: "util.Format", Deps: deps, Reader: formatSource()})
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
