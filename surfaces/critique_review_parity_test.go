package surfaces_test

import (
	"bytes"
	"context"
	"net/url"
	"testing"

	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// TestCritiqueReview_CrossSurfaceParity (AC-7): critique_review returns
// byte-identical output across CLI, MCP, and HTTP through the single
// dispatch/encoder path, for the same touched-set diff + inline review.
func TestCritiqueReview_CrossSurfaceParity(t *testing.T) {
	store := triageSeed(t)
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	const diff = "c.go:C"
	// An inline review flagging C (a low-risk leaf in the triage seed) — exercises a
	// deterministic over_flag without any network fetch.
	const review = `{"verdict":"CHANGES_REQUESTED","comments":[{"id":"c1","path":"c.go","symbol":"C"}]}`

	var cliOut, cliErr bytes.Buffer
	if err := cli.RunCritiqueReview(context.Background(), c,
		[]string{"-diff", diff, "-review", review}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli critique-review: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	mcpBytes := mcpToolTextArgs(t, c, mcp.ToolCritiqueReview, map[string]any{
		"diff":   diff,
		"review": review,
	})
	httpBytes := httpPayloadGet(t, c, "/reviews/critique?diff="+url.QueryEscape(diff)+"&review="+url.QueryEscape(review))

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("critique_review CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("critique_review CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte(`"analyzer_version":"critique-review/1"`)) {
		t.Fatalf("critique_review output missing analyzer_version: %s", cliBytes)
	}
}

// TestCritiqueReviewTool_Advertised (AC-7): critique_review is advertised when the
// analysis service is wired.
func TestCritiqueReviewTool_Advertised(t *testing.T) {
	store := triageSeed(t)
	withAnalysis := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	names := listToolNames(t, mcp.NewServerWithClient(withAnalysis))
	if !containsStr(names, mcp.ToolCritiqueReview) {
		t.Fatalf("critique_review not advertised when analysis wired; got %v", names)
	}
}
