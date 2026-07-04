package brief

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds the shared in-memory relation graph (see explain tests).
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
	edge(helper, format, "references", model.TierHeuristic, 0.4, "pkg/helper.go:7")
	edge(run, helper, "calls", model.TierDerived, 0.8, "cmd/app/main.go:14")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func memStoreWith(t *testing.T, facts ...memory.ProvenanceInput) *memory.Store {
	t.Helper()
	s, err := memory.NewMemStore(nil)
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, f := range facts {
		if _, err := s.StoreMemoryWithProvenance(context.Background(), f); err != nil {
			t.Fatalf("store fact: %v", err)
		}
	}
	return s
}

func TestAssembleGraphDerived(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t)})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if r.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s", r.Outcome)
	}
	if err := contract.ValidateResult(r); err != nil {
		t.Fatalf("ValidateResult: %v", err)
	}
	md, err := Markdown(r)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	// Graph-derived content: the most-referenced symbol and most-connected file.
	if !strings.Contains(md, "util.Format") {
		t.Fatalf("expected top symbol util.Format in brief:\n%s", md)
	}
	if !strings.Contains(md, "util/format.go") {
		t.Fatalf("expected hotspot file in brief:\n%s", md)
	}
	if !strings.Contains(r.Summary, "3 symbols") && !strings.Contains(r.Summary, "3 edges") {
		t.Fatalf("summary must state graph size: %q", r.Summary)
	}
	// Heuristic edge share appears in Risks and Unknowns.
	if !strings.Contains(md, "heuristic") {
		t.Fatalf("expected heuristic risk note:\n%s", md)
	}
}

func TestAssembleMarkdownSections(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), ProjectName: "graphi"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, err := Markdown(r)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	for _, heading := range []string{"# Agent Brief", "## Summary", "## Start Here", "## Relevant Symbols", "## Risks and Unknowns", "## Suggested Next Calls"} {
		if !strings.Contains(md, heading) {
			t.Fatalf("markdown missing heading %q:\n%s", heading, md)
		}
	}
}

func TestAssembleWithMemory(t *testing.T) {
	mem := memStoreWith(t,
		memory.ProvenanceInput{Scope: "repo", Notebook: "conventions", Payload: "tests run via make test", Kind: "command", Source: "user", Confidence: "confirmed"},
	)
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), Memory: mem})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "## Known Facts") || !strings.Contains(md, "make test") {
		t.Fatalf("expected stored fact in Known Facts:\n%s", md)
	}
	if !strings.Contains(md, "[source: user]") {
		t.Fatalf("facts must cite their source:\n%s", md)
	}
}

func TestAssembleWithoutMemoryStatesRisk(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t)})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "no memory") {
		t.Fatalf("missing memory must be stated in Risks and Unknowns:\n%s", md)
	}
}

func TestAssembleWithholdsSecrets(t *testing.T) {
	mem := memStoreWith(t,
		memory.ProvenanceInput{Scope: "repo", Notebook: "n", Payload: "api_key=sk-abcdef0123456789abcdef", Kind: "risk"},
		memory.ProvenanceInput{Scope: "repo", Notebook: "n", Payload: "plain fact", Kind: "convention"},
	)
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), Memory: mem})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, _ := Markdown(r)
	if strings.Contains(md, "api_key=sk-") {
		t.Fatalf("secret-suspect payload leaked into brief:\n%s", md)
	}
	if !strings.Contains(md, "withheld") {
		t.Fatalf("withheld secrets must be counted in Risks and Unknowns:\n%s", md)
	}
}

func TestAssembleTopicScoped(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), Topic: "util.Format"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(r.Summary, "util.Format") {
		t.Fatalf("topic not reflected in summary: %q", r.Summary)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "related to topic") {
		t.Fatalf("expected topic-related start-here rows:\n%s", md)
	}
}

func TestAssembleUnresolvedTopicIsARisk(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), Topic: "zzz.NotThere"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "topic unresolved") {
		t.Fatalf("unresolved topic must appear in Risks and Unknowns:\n%s", md)
	}
}

func TestAssembleTruncation(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t), MaxItems: 3})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if r.Outcome != contract.OutcomePartial {
		t.Fatalf("expected partial under cap, got %s", r.Outcome)
	}
	if !r.Limits.Truncated || len(r.Items) != 3 {
		t.Fatalf("expected truncated 3-item brief, got %d items (%+v)", len(r.Items), r.Limits)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "Truncated") {
		t.Fatalf("markdown must state truncation:\n%s", md)
	}
}

func TestAssembleWithoutGraphIsHonest(t *testing.T) {
	r, err := Assemble(context.Background(), Params{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(r.Summary, "Graph unavailable") {
		t.Fatalf("summary must state graph unavailability: %q", r.Summary)
	}
	md, _ := Markdown(r)
	if !strings.Contains(md, "graph unavailable") {
		t.Fatalf("risks must state graph unavailability:\n%s", md)
	}
}

func TestAssembleEvidenceForGraphItems(t *testing.T) {
	r, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t)})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(r.Evidence) == 0 {
		t.Fatal("graph-derived brief must carry evidence")
	}
	seen := false
	for _, ev := range r.Evidence {
		if ev.Path == "util/format.go" && ev.Line == 3 {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("expected util/format.go:3 evidence, got %+v", r.Evidence)
	}
}

func TestAssembleDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Assemble(context.Background(), Params{Deps: deps, ProjectName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Assemble(context.Background(), Params{Deps: deps, ProjectName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := contract.Serialize(a)
	bb, _ := contract.Serialize(b)
	if string(ab) != string(bb) {
		t.Fatal("brief output is not deterministic")
	}
}

func TestMarkdownNil(t *testing.T) {
	if _, err := Markdown(nil); err == nil {
		t.Fatal("expected error for nil result")
	}
}
