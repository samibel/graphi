package taskctx_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

func TestTaskContextV2PreservesRetrievalOrderThroughHydrationAndItemCap(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	nodes := []model.Node{mustLookupQualified(t, store, "auth.TokenValidator"), mustLookupQualified(t, store, "auth.TokenBucket")}
	// NodesByID returns ascending IDs, deliberately opposite retrieval order.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() > nodes[j].ID() })
	ret := &stubRetriever{state: "ready"}
	for _, n := range nodes {
		ret.rows = append(ret.rows, resolve.RetrieverRow{NodeID: string(n.ID()), Path: n.SourcePath()})
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{Task: "validate", MaxItems: 1, TokenBudget: -1,
		Deps: resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].RefID != string(nodes[0].ID()) {
		t.Fatalf("hydration lost top retrieval seed under cap: %+v", res.Items)
	}
	refs := map[string]bool{}
	for _, id := range res.Items[0].EvidenceRefIDs {
		refs[id] = true
	}
	for _, ev := range res.Evidence {
		if !refs[ev.RefID] && ev.Snippet == "" {
			t.Fatalf("item cap left an unreferenced citation in the payload: %+v", ev)
		}
	}
}

func TestTaskContextV2IncludesBodyBeyondDeclarationWindow(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	n, err := model.NewNode("function", "auth.CheckRequest", "auth/request.go", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	lines := []string{"package auth", "// CheckRequest validates the request.", "func CheckRequest() bool {"}
	for i := 0; i < 70; i++ {
		lines = append(lines, "// prepare request")
	}
	lines = append(lines, "return false // request rejected", "}", "func Unrelated() {}")
	ret := &stubRetriever{state: "ready", rows: []resolve.RetrieverRow{{NodeID: string(n.ID()), Path: n.SourcePath(), Span: "3-3"}}}
	res, err := taskctx.AssembleV2(t.Context(), taskctx.Params{Task: "CheckRequest", MaxItems: 1, TokenBudget: 1200,
		Deps: resolve.Deps{Query: query.New(store), Search: search.New(store), Retrieval: ret}, Reader: structReader{n.SourcePath(): lines}})
	if err != nil {
		t.Fatal(err)
	}
	var source string
	for _, ev := range res.Evidence {
		source += ev.Snippet
	}
	if !strings.Contains(source, "return false // request rejected") {
		t.Fatalf("declaration retrieved, body discarded: %s", source)
	}
	if strings.Contains(source, "func Unrelated") {
		t.Fatal("snippet crossed the definition boundary")
	}
}
