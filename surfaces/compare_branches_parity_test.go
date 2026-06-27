package surfaces_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// mapMaterializer is the test BranchStateMaterializer: it maps a branch ref to a
// pre-built read-only graph state. An unknown ref materializes to an empty state
// (the engine diffs it to a well-defined result), exactly as the production
// indexer/snapshot materializer would for an unresolvable ref.
type mapMaterializer map[string]query.Reader

func (m mapMaterializer) StateForRef(_ context.Context, ref string) (query.Reader, error) {
	if r, ok := m[ref]; ok {
		return r, nil
	}
	return graphstore.NewMemStore(), nil
}

// compareSeed builds base + head states with a single known structural change:
// head adds a node "p.New" not present in base.
func compareSeed(t *testing.T) (base, head *graphstore.MemStore) {
	t.Helper()
	ctx := context.Background()
	base = graphstore.NewMemStore()
	head = graphstore.NewMemStore()
	mk := func(store *graphstore.MemStore, qn, path string) {
		n, err := model.NewNode("function", qn, path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	mk(base, "p.Stable", "stable.go")
	mk(head, "p.Stable", "stable.go")
	mk(head, "p.New", "new.go") // added on head only
	return base, head
}

// TestCompareBranches_CrossSurfaceParity (AC-6): compare_branches returns
// byte-identical output across CLI, MCP, and HTTP through the single
// dispatch/encoder path, for the same base/head refs.
func TestCompareBranches_CrossSurfaceParity(t *testing.T) {
	base, head := compareSeed(t)
	store := graphstore.NewMemStore() // backing store is unused by the comparator
	mat := mapMaterializer{"base": base, "head": head}
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithBranchStates(mat)

	var cliOut, cliErr bytes.Buffer
	if err := cli.RunCompareBranches(context.Background(), c, "base", "head", &cliOut, &cliErr); err != nil {
		t.Fatalf("cli compare-branches: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")
	mcpBytes := mcpToolTextArgs(t, c, mcp.ToolCompareBranches, map[string]any{"base": "base", "head": "head"})
	httpBytes := httpPayloadGet(t, c, "/branches/compare?base=base&head=head")

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("compare_branches CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("compare_branches CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte(`"analyzer_version":"compare-branches/1"`)) {
		t.Fatalf("compare_branches output missing analyzer_version: %s", cliBytes)
	}
	// The single known change (p.New added on head) must surface.
	if !bytes.Contains(cliBytes, []byte(`"qualified_name":"p.New"`)) {
		t.Fatalf("compare_branches expected p.New added: %s", cliBytes)
	}
}

// TestCompareBranchesTool_Advertised (AC-6): compare_branches is advertised when a
// branch-state materializer is wired, and probe-hidden when it is absent.
func TestCompareBranchesTool_Advertised(t *testing.T) {
	store := graphstore.NewMemStore()
	withMat := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithBranchStates(mapMaterializer{})
	names := listToolNames(t, mcp.NewServerWithClient(withMat))
	if !containsStr(names, mcp.ToolCompareBranches) {
		t.Fatalf("compare_branches not advertised when materializer wired; got %v", names)
	}

	noMat := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	namesNo := listToolNames(t, mcp.NewServerWithClient(noMat))
	if containsStr(namesNo, mcp.ToolCompareBranches) {
		t.Fatalf("compare_branches advertised when materializer absent (should probe-hide); got %v", namesNo)
	}
}
