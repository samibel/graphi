package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/forge"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// triageSeed builds a small graph with a blast-radius gradient + a test node, so
// triage_prs produces a meaningful ranking. A->C, B->C (C is a hub); Atest->A.
func triageSeed(t *testing.T) *graphstore.MemStore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	nodes := map[string]model.Node{}
	mk := func(name, path string) {
		n, err := model.NewNode("function", "p."+name, path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		nodes[name] = n
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	mk("A", "a.go")
	mk("B", "b.go")
	mk("C", "c.go")
	mk("Atest", "a_test.go")
	edge := func(from, to string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), query.EdgeKindCalls, model.TierConfirmed, 1, from+to, []string{from + to})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	edge("A", "C")
	edge("B", "C")
	edge("Atest", "A")
	return store
}

// triageMockForge is the fixed offline PR set every surface enumerates.
func triageMockForge() *forge.MockForge {
	return forge.NewMockForge([]forge.PR{
		{Number: 1, Title: "hub", Author: "alice", HeadSHA: "sha1", ChangedFiles: []string{"c.go"}},
		{Number: 2, Title: "tested", Author: "bob", HeadSHA: "sha2", ChangedFiles: []string{"a.go"}},
	})
}

func triageDirect(t *testing.T) *client.Direct {
	t.Helper()
	store := triageSeed(t)
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithForge(triageMockForge())
}

func mcpToolText(t *testing.T, c client.Client, tool string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(c, mcp.WithLabs())
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": map[string]any{}},
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

func httpPayload(t *testing.T, c client.Client, path string) []byte {
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

// TestListPRs_CrossSurfaceParity (AC-1, AC-6): list_prs returns byte-identical
// forge metadata across CLI, MCP, and HTTP, and the output is metadata-only (no
// scoring).
func TestListPRs_CrossSurfaceParity(t *testing.T) {
	c := triageDirect(t)

	var cliOut, cliErr bytes.Buffer
	if err := cli.RunListPRs(context.Background(), c, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli list-prs: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")
	mcpBytes := mcpToolText(t, c, mcp.ToolListPRs)
	httpBytes := httpPayload(t, c, "/prs")

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("list_prs CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("list_prs CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if bytes.Contains(cliBytes, []byte("composite")) {
		t.Fatalf("list_prs must perform no scoring: %s", cliBytes)
	}
}

// TestTriagePRs_CrossSurfaceParity (AC-2, AC-6): triage_prs returns byte-identical
// ranked output across CLI, MCP, and HTTP through the single dispatch/encoder path.
func TestTriagePRs_CrossSurfaceParity(t *testing.T) {
	c := triageDirect(t)

	var cliOut, cliErr bytes.Buffer
	if err := cli.RunTriagePRs(context.Background(), c, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli triage-prs: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")
	mcpBytes := mcpToolText(t, c, mcp.ToolTriagePRs)
	httpBytes := httpPayload(t, c, "/prs/triage")

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("triage_prs CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("triage_prs CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte(`"analyzer_version":"triage-prs/1"`)) {
		t.Fatalf("triage_prs output missing analyzer_version: %s", cliBytes)
	}
}

// TestTriageTools_Advertised (AC-6): list_prs/triage_prs are advertised when a
// forge boundary is wired, and probe-hidden when it is absent.
func TestTriageTools_Advertised(t *testing.T) {
	withForge := triageDirect(t)
	names := listToolNames(t, mcp.NewServerWithClient(withForge, mcp.WithLabs()))
	if !containsStr(names, mcp.ToolListPRs) || !containsStr(names, mcp.ToolTriagePRs) {
		t.Fatalf("list_prs/triage_prs not advertised when forge wired; got %v", names)
	}

	store := triageSeed(t)
	noForge := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	namesNo := listToolNames(t, mcp.NewServerWithClient(noForge, mcp.WithLabs()))
	if containsStr(namesNo, mcp.ToolListPRs) || containsStr(namesNo, mcp.ToolTriagePRs) {
		t.Fatalf("triage tools advertised when forge absent (should probe-hide); got %v", namesNo)
	}
}

func listToolNames(t *testing.T, srv *mcp.Server) []string {
	t.Helper()
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(resp.Result.Tools))
	for _, tk := range resp.Result.Tools {
		names = append(names, tk.Name)
	}
	return names
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
