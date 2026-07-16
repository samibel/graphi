package surfaces_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/forge"
	"github.com/samibel/graphi/surfaces/mcp"
)

// TestConflictsPRs_CrossSurfaceParity (AC-3, AC-6): conflicts_prs returns
// byte-identical output across CLI, MCP, and HTTP through the single
// dispatch/encoder path. The shared seed has two PRs touching the same file, so a
// real conflicting pair is produced (not an empty report).
func TestConflictsPRs_CrossSurfaceParity(t *testing.T) {
	// triageSeed (defined in triage_parity_test.go) has c.go as a shared hub.
	store := triageSeed(t)
	mock := forge.NewMockForge([]forge.PR{
		{Number: 1, Title: "p1", Author: "a", HeadSHA: "s1", ChangedFiles: []string{"c.go"}},
		{Number: 2, Title: "p2", Author: "b", HeadSHA: "s2", ChangedFiles: []string{"c.go"}},
	})
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithForge(mock)

	var cliOut, cliErr bytes.Buffer
	if err := cli.RunConflictsPRs(context.Background(), c, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli conflicts-prs: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")
	mcpBytes := mcpToolText(t, c, mcp.ToolConflictsPRs)
	httpBytes := httpPayload(t, c, "/prs/conflicts")

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("conflicts_prs CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("conflicts_prs CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte(`"analyzer_version":"conflicts-prs/1"`)) {
		t.Fatalf("conflicts_prs output missing analyzer_version: %s", cliBytes)
	}
	// Two PRs touching the same file (c.go, a high-centrality hub) must collide.
	if !bytes.Contains(cliBytes, []byte(`"shared-file"`)) {
		t.Fatalf("conflicts_prs expected a shared-file pair: %s", cliBytes)
	}
}

// TestConflictsPRsTool_Advertised (AC-6): conflicts_prs is advertised when a forge
// boundary is wired, and probe-hidden when it is absent.
func TestConflictsPRsTool_Advertised(t *testing.T) {
	store := triageSeed(t)
	withForge := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithForge(triageMockForge())
	names := listToolNames(t, mcp.NewServerWithClient(withForge, mcp.WithLabs()))
	if !containsStr(names, mcp.ToolConflictsPRs) {
		t.Fatalf("conflicts_prs not advertised when forge wired; got %v", names)
	}

	noForge := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	namesNo := listToolNames(t, mcp.NewServerWithClient(noForge, mcp.WithLabs()))
	if containsStr(namesNo, mcp.ToolConflictsPRs) {
		t.Fatalf("conflicts_prs advertised when forge absent (should probe-hide); got %v", namesNo)
	}
}
