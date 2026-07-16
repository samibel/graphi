package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// mcpToolTextArgs runs a tools/call with explicit arguments and returns the
// canonical text payload (mirrors mcpToolText, which passes empty arguments).
func mcpToolTextArgs(t *testing.T, c client.Client, tool string, args map[string]any) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(c, mcp.WithLabs())
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve %s: %v", tool, err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp %s %q: %v", tool, out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s error: %s", tool, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("mcp %s: unexpected content", tool)
	}
	return []byte(resp.Result.Content[0].Text)
}

func httpPayloadGet(t *testing.T, c client.Client, path string) []byte {
	t.Helper()
	// The routes this helper drives are Labs routes, fail-closed by default
	// (SW-112 / SAFE-01); parity is asserted for the opted-in configuration.
	t.Setenv(httpsrv.LabsEnvVar, "1")
	srv := httpsrv.New(c, observe.New())
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http %s: code=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http %s decode envelope: %v", path, err)
	}
	return env.Payload
}

// TestSuggestReviewers_CrossSurfaceParity (AC-6): suggest_reviewers returns
// byte-identical output across CLI, MCP, and HTTP through the single
// dispatch/encoder path, for the same diff input.
func TestSuggestReviewers_CrossSurfaceParity(t *testing.T) {
	store := triageSeed(t)
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	const diff = "c.go:C"
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunSuggestReviewers(context.Background(), c, diff, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli suggest-reviewers: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")
	mcpBytes := mcpToolTextArgs(t, c, mcp.ToolSuggestReviewers, map[string]any{"diff": diff})
	httpBytes := httpPayloadGet(t, c, "/prs/suggest-reviewers?diff="+diff)

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("suggest_reviewers CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("suggest_reviewers CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte(`"analyzer_version":"suggest-reviewers/1"`)) {
		t.Fatalf("suggest_reviewers output missing analyzer_version: %s", cliBytes)
	}
}

// TestSuggestReviewersTool_Advertised (AC-6): suggest_reviewers is advertised when
// the analysis service is wired.
func TestSuggestReviewersTool_Advertised(t *testing.T) {
	store := triageSeed(t)
	withAnalysis := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	names := listToolNames(t, mcp.NewServerWithClient(withAnalysis, mcp.WithLabs()))
	if !containsStr(names, mcp.ToolSuggestReviewers) {
		t.Fatalf("suggest_reviewers not advertised when analysis wired; got %v", names)
	}
}
