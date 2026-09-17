// SW-282 bundle-selection regression: a candidate ranked beyond the current
// 5-seed + 3-graph-neighbor snippet-candidate window, with NO graph edge to
// any of the five seeds (so the bounded 1-hop neighbor scan cannot reach it
// either), must still surface as an EXACT cited source snippet in the
// task_context/2 bundle when nothing else in the pool is a better use of the
// budget. Today it does not: the only two seams that feed
// AssembleDefinitions are the 5 retrieval seeds and up to 3 graph neighbors
// of those seeds, so a 6th-ranked, graph-isolated declaration is invisible to
// source selection regardless of how relevant its content is.
package taskctx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

func TestTaskContextV2_ReferencedSymbolDefinitionCompetesForSourceBudget(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
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
	doc := mk("type", "guide.Answer functions", "guide.md", 1)
	target := mk("method", "pkg.Command.RegisterAnswerFunc", "answer.go", 3)
	ret := &stubRetriever{state: "ready", rows: []resolve.RetrieverRow{{NodeID: string(doc.ID()), Path: doc.SourcePath(), Span: "1-1"}}}
	reader := structReader{
		"guide.md": {
			"# Answer functions",
			"Register a function from Go:",
			"```go",
			"cmd.RegisterAnswerFunc(func() string { return \"answer\" })",
			"```",
		},
		"answer.go": {
			"package pkg",
			"// RegisterAnswerFunc installs the function used to produce answers.",
			"func (c *Command) RegisterAnswerFunc(fn func() string) {",
			"	c.answer = fn",
			"}",
		},
	}
	res, err := taskctx.AssembleV2(ctx, taskctx.Params{
		Task:        "how to write answer function in Go",
		TokenBudget: 1200,
		Deps:        resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret},
		Reader:      reader,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	for _, ev := range res.Evidence {
		if ev.Path == target.SourcePath() && strings.Contains(ev.Snippet, "func (c *Command) RegisterAnswerFunc") {
			return
		}
	}
	t.Fatal("definition named by a selected source never competed for the source budget")
}

func TestTaskContextV2_ProcedureFollowsExactCallInsideSelectedDeclaration(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	testNode := mk("function", "pkg.TestOptions", "options_test.go", 2)
	target := mk("method", "pkg.Command.SetInputs", "command.go", 3)
	ret := &stubRetriever{state: "ready", rows: []resolve.RetrieverRow{{NodeID: string(testNode.ID()), Path: testNode.SourcePath(), Span: "2-2"}}}
	reader := structReader{
		"options_test.go": {
			"package pkg",
			"func TestOptions(t *testing.T) {",
			"	c := newCommand()",
			"	// enough setup to put the answer beyond point context",
			"", "", "", "", "", "", "", "",
			"	c.SetInputs([]string{\"--verbose\"})",
			"}",
		},
		"command.go": {
			"package pkg",
			"// SetInputs sets command inputs and is useful when testing.",
			"func (c *Command) SetInputs(args []string) {",
			"	c.args = args",
			"}",
		},
	}
	res, err := taskctx.AssembleV2(ctx, taskctx.Params{
		Task:        "how to set options in test",
		TokenBudget: 1200,
		Deps:        resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret},
		Reader:      reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range res.Evidence {
		if ev.Path == target.SourcePath() && strings.Contains(ev.Snippet, "func (c *Command) SetInputs") {
			return
		}
	}
	t.Fatal("procedure did not follow the exact call inside its selected declaration")
}

func TestTaskContextV2_LifecycleFocusWidensOnlyItsInternalPool(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	seed := mustLookupQualified(t, store, "auth.TokenValidator")
	ret := &stubRetriever{state: "ready", rows: []resolve.RetrieverRow{{NodeID: string(seed.ID()), Path: seed.SourcePath()}}}
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret}

	if _, err := taskctx.AssembleV2(t.Context(), taskctx.Params{Task: "where are token values validated", TokenBudget: -1, Deps: deps}); err != nil {
		t.Fatal(err)
	}
	if ret.lastLimit != 50 {
		t.Fatalf("ordinary natural-language retrieval window = %d, want 50", ret.lastLimit)
	}
	if _, err := taskctx.AssembleV2(t.Context(), taskctx.Params{Task: "when does the initialization function run", TokenBudget: -1, Deps: deps}); err != nil {
		t.Fatal(err)
	}
	if ret.lastLimit != 50 {
		t.Fatalf("lifecycle-focused retrieval window = %d, want 50", ret.lastLimit)
	}
}

// TestTaskContextV2_CandidateBeyondSeedWindowGetsCited is the red-first
// regression at the actual bundle call: a retrieval row ranked 6th (rank
// order, not a graph neighbor of any of the first 5) carries the query's
// only literal answer text. It must appear as an emitted source snippet
// (not merely be absent), because it is the single most relevant candidate
// in the pool and the five ahead of it are deliberately irrelevant filler.
func TestTaskContextV2_CandidateBeyondSeedWindowGetsCited(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	mk := func(qn, path string, line int) model.Node {
		n, err := model.NewNode("function", qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}

	// Five filler seeds: real nodes, no edges to anything, irrelevant bodies.
	// They occupy the entire current seed budget (retrievalSeedLimit == 5)
	// ahead of the one candidate that actually answers the query.
	var fillerIDs []string
	for i := 0; i < 5; i++ {
		n := mk(fillerName(i), fillerPath(i), 1)
		fillerIDs = append(fillerIDs, string(n.ID()))
	}
	// The 6th-ranked candidate: graph-isolated (no edge to any filler seed,
	// so the bounded 1-hop neighbor scan around the five seeds can never
	// reach it), carrying the query's unique answer text.
	target := mk("pkg.RealImplementation", "pkg/real.go", 3)

	rows := make([]resolve.RetrieverRow, 0, 6)
	for i, id := range fillerIDs {
		rows = append(rows, resolve.RetrieverRow{NodeID: id, Path: fillerPath(i), Span: "1-1", Final: 1000 - i})
	}
	rows = append(rows, resolve.RetrieverRow{NodeID: string(target.ID()), Path: "pkg/real.go", Span: "1-4", Final: 500})

	ret := &stubRetriever{state: "ready", strategy: "semantic_first", rows: rows}
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret}

	reader := structReader{}
	for i := range fillerIDs {
		reader[fillerPath(i)] = []string{"package filler", "func " + fillerName(i) + "() { /* irrelevant */ }"}
	}
	reader["pkg/real.go"] = []string{
		"package pkg",
		"// RealImplementation is the only thing that answers the question.",
		"func RealImplementation() string {",
		"    return \"sentinelUniqueMarkerXYZ\"",
		"}",
	}

	res, err := taskctx.AssembleV2(ctx, taskctx.Params{
		Task:        "where is sentinelUniqueMarkerXYZ",
		TokenBudget: 1200,
		Deps:        deps,
		Reader:      reader,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}

	var found bool
	primary := 0
	for _, ev := range res.Evidence {
		if ev.Snippet != "" && strings.Contains(ev.Snippet, "sentinelUniqueMarkerXYZ") {
			if ev.Path != "pkg/real.go" || ev.Span != "2-5" || ev.TextHash == "" {
				t.Fatalf("source lacks exact declaration citation/hash: %+v", ev)
			}
			found = true
		}
	}
	for _, item := range res.Items {
		if strings.HasPrefix(item.Reason, "primary:") {
			primary++
		}
	}
	if primary != 5 || ret.calls != 1 {
		t.Fatalf("wider source pool expanded primary band or retrieval calls: primary=%d calls=%d", primary, ret.calls)
	}
	if !found {
		t.Fatalf("6th-ranked, graph-isolated candidate never entered the snippet-candidate pool: %+v", res.Evidence)
	}
	withoutSources, err := taskctx.AssembleV2(ctx, taskctx.Params{Task: "where is sentinelUniqueMarkerXYZ", TokenBudget: -1, Deps: deps, Reader: reader})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range withoutSources.Items {
		if strings.HasPrefix(item.Reason, "candidate:") {
			t.Fatal("internal candidate charged despite no emitted source", item)
		}
	}
}

func fillerName(i int) string { return "Filler" + string(rune('A'+i)) }
func fillerPath(i int) string { return "filler/" + string(rune('a'+i)) + ".go" }
