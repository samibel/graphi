// Package surfaces_test holds the cross-surface conformance test: it drives the
// SAME query through the CLI path and the MCP stdio path against the SAME seeded
// store and asserts the canonical serialized output bytes are byte-identical
// (MCP↔CLI parity contract). It lives above both surfaces so it can import them
// together without creating an import cycle.
package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

func seed(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}

	for _, name := range []string{"A", "B", "C", "D"} {
		n, err := model.NewNode("function", "p."+name, "p/"+name+".go", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID()
		nodes[name] = n
	}
	mk := func(from, to, kind string, tier model.ConfidenceTier, conf float64, reason string, ev []string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, conf, reason, ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	mk("A", "B", query.EdgeKindCalls, model.TierConfirmed, 1, "ab", []string{"e1"})
	mk("B", "C", query.EdgeKindCalls, model.TierDerived, 0.8, "bc", []string{"e2"})
	mk("A", "C", query.EdgeKindCalls, model.TierHeuristic, 0.4, "ac", []string{"e3"})
	mk("D", "B", query.EdgeKindReferences, model.TierDerived, 0.7, "db", []string{"e4"})
	return store, ids
}

// cliOutput runs the CLI surface and returns the printed bytes (trailing newline
// trimmed so it compares to the MCP text payload).
func cliOutput(t *testing.T, qsvc *query.Service, ssvc *search.Service, op, symbol string, depth int) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{op, "-symbol", symbol}
	if op == query.OpNeighborhood {
		args = append(args, "-depth", fmt.Sprintf("%d", depth))
	}
	c := client.NewDirect(qsvc, ssvc)
	if err := cli.Run(context.Background(), c, args, &out, &errOut); err != nil {
		t.Fatalf("cli.Run(%s): %v (stderr: %s)", op, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// mcpOutput runs one tools/call through the MCP stdio server and extracts the
// canonical text payload from the JSON-RPC response.
func mcpOutput(t *testing.T, qsvc *query.Service, ssvc *search.Service, op, symbol string, depth int) []byte {
	t.Helper()
	srv := mcp.NewServer(qsvc, ssvc)

	args := map[string]any{"symbol": symbol}
	if op == query.OpNeighborhood {
		args["depth"] = depth
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": op, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp response %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// AC3 + refinement AC5: contract-conformance — MCP output == CLI output byte-for-byte.
func TestMCP_CLI_Parity(t *testing.T) {
	store, ids := seed(t)
	svc := query.New(store)

	type tc struct {
		op     string
		symbol model.NodeId
		depth  int
	}
	cases := []tc{
		{query.OpCallers, ids["C"], 0},
		{query.OpCallees, ids["A"], 0},
		{query.OpReferences, ids["B"], 0},
		{query.OpDefinition, ids["A"], 0},
		{query.OpNeighborhood, ids["A"], 2},
		{query.OpNeighborhood, ids["A"], query.MaxNeighborhoodDepth + 5}, // clamp parity
		{query.OpCallers, model.NodeId("missing"), 0},                    // not-found parity
	}
	for _, c := range cases {
		cliBytes := cliOutput(t, svc, nil, c.op, string(c.symbol), c.depth)
		mcpBytes := mcpOutput(t, svc, nil, c.op, string(c.symbol), c.depth)
		if !bytes.Equal(cliBytes, mcpBytes) {
			t.Fatalf("%s parity mismatch:\nCLI: %s\nMCP: %s", c.op, cliBytes, mcpBytes)
		}
	}
}

// searchCLIOutput runs the CLI search surface and returns the printed bytes.
func searchCLIOutput(t *testing.T, qsvc *query.Service, ssvc *search.Service, q string, limit int) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{q}
	if limit > 0 {
		args = append([]string{"-limit", fmt.Sprintf("%d", limit)}, args...)
	}
	c := client.NewDirect(qsvc, ssvc)
	if err := cli.RunSearch(context.Background(), c, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunSearch(%q): %v (stderr: %s)", q, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// searchMCPOutput runs a search tools/call through the MCP stdio server.
func searchMCPOutput(t *testing.T, qsvc *query.Service, ssvc *search.Service, q string, limit int) []byte {
	t.Helper()
	srv := mcp.NewServer(qsvc, ssvc)

	args := map[string]any{"symbol": q}
	if limit > 0 {
		args["depth"] = limit
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "search", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve search: %v", err)
	}

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp search response %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp search error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp search content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// TestMCP_CLI_SearchParity asserts search returns identical bytes through CLI
// and MCP surfaces.
func TestMCP_CLI_SearchParity(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)

	cases := []struct {
		q     string
		limit int
	}{
		{"p.A", 0},
		{"p", 2},
		{"missing-token", 0},
	}
	for _, c := range cases {
		cliBytes := searchCLIOutput(t, qsvc, ssvc, c.q, c.limit)
		mcpBytes := searchMCPOutput(t, qsvc, ssvc, c.q, c.limit)
		if !bytes.Equal(cliBytes, mcpBytes) {
			t.Fatalf("search parity mismatch for %q:\nCLI: %s\nMCP: %s", c.q, cliBytes, mcpBytes)
		}
	}
}

// semanticCLIOutput runs the CLI `search -semantic <q>` surface.
func semanticCLIOutput(t *testing.T, direct *client.Direct, q string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := cli.RunSearch(context.Background(), direct, []string{"-semantic", q}, &out, &errOut); err != nil {
		t.Fatalf("cli.RunSearch -semantic %q: %v (stderr: %s)", q, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// semanticMCPOutput runs the MCP search_semantic tool.
func semanticMCPOutput(t *testing.T, direct *client.Direct, q string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "search_semantic", "arguments": map[string]any{"symbol": q}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve search_semantic: %v", err)
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
		t.Fatalf("decode mcp search_semantic %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp search_semantic error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("unexpected search_semantic content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// semanticHTTPOutput drives the HTTP /search/semantic endpoint and returns the
// envelope payload bytes.
func semanticHTTPOutput(t *testing.T, direct *client.Direct, q string) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/search/semantic?q="+q, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http /search/semantic: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Payload
}

// TestSemanticSearch_UnavailableParity (SW-059): with NO embedder configured, the
// typed graceful-skip "unavailable" response is BYTE-IDENTICAL across CLI, MCP,
// and HTTP (serialized-byte parity, not struct equality). The default client is
// constructed with no embedder, so all three take the graceful-skip path.
func TestSemanticSearch_UnavailableParity(t *testing.T) {
	store, _ := seed(t)
	// Default Direct: lexical search wired, semantic OFF (graceful skip).
	direct := client.NewDirect(query.New(store), search.New(store))

	for _, q := range []string{"ParseGraph", "p.A", "missing"} {
		cliBytes := semanticCLIOutput(t, direct, q)
		mcpBytes := semanticMCPOutput(t, direct, q)
		httpBytes := semanticHTTPOutput(t, direct, q)

		if !bytes.Equal(cliBytes, mcpBytes) {
			t.Fatalf("semantic unavailable CLI<->MCP mismatch for %q:\nCLI:  %s\nMCP:  %s", q, cliBytes, mcpBytes)
		}
		if !bytes.Equal(cliBytes, httpBytes) {
			t.Fatalf("semantic unavailable CLI<->HTTP mismatch for %q:\nCLI:  %s\nHTTP: %s", q, cliBytes, httpBytes)
		}
		// Sanity: it really is the typed Unavailable response.
		if !bytes.Contains(cliBytes, []byte(`"available":false`)) {
			t.Fatalf("semantic response not the typed Unavailable for %q: %s", q, cliBytes)
		}
		if !bytes.Contains(cliBytes, []byte(search.UnavailableReason)) {
			t.Fatalf("semantic response missing canonical reason for %q: %s", q, cliBytes)
		}
	}
}

// MCP tools/list advertises one tool per canonical operation.
func TestMCP_ToolsList(t *testing.T) {
	store, _ := seed(t)
	svc := query.New(store)
	ssvc := search.New(store)
	srv := mcp.NewServer(svc, ssvc)

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
	// query ops + the "search" tool + the optional "search_semantic" tool (SW-059)
	// + the EP-011 G1 "compound" tool (unconditionally advertised).
	if len(resp.Result.Tools) != len(query.Operations)+3 {
		t.Fatalf("tools count = %d, want %d", len(resp.Result.Tools), len(query.Operations)+3)
	}
}

// analysisCLIOutput runs the CLI analyzer surface (SW-022) and returns the
// printed bytes (trailing newline trimmed).
func analysisCLIOutput(t *testing.T, direct *client.Direct, analyzer, symbol, direction string, maxNodes int) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{analyzer, "-symbol", symbol, "-direction", direction}
	if maxNodes > 0 {
		args = append(args, "-max-nodes", fmt.Sprintf("%d", maxNodes))
	}
	if err := cli.RunAnalysis(context.Background(), direct, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunAnalysis(%s): %v (stderr: %s)", analyzer, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// analysisMCPOutput runs an analyze tools/call through the MCP stdio server
// bound to the same in-process client and extracts the canonical text payload.
func analysisMCPOutput(t *testing.T, direct *client.Direct, analyzer, symbol, direction string, maxNodes int) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)

	args := map[string]any{"analyzer": analyzer, "symbol": symbol, "direction": direction}
	if maxNodes > 0 {
		args["max_nodes"] = maxNodes
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "analyze", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve analyze: %v", err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp analyze response %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp analyze error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp analyze content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// TestMCP_CLI_AnalysisParity (SW-022): the impact analyzer returns byte-identical
// output through the CLI and MCP surfaces for identical inputs (parity by
// construction through the single client.Analyze seam). Also covers the not-found
// outcome and both directions.
func TestMCP_CLI_AnalysisParity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	cases := []struct {
		name      string
		analyzer  string
		symbol    string
		direction string
		maxNodes  int
	}{
		{"impact-forward-C", "impact", string(ids["C"]), "forward", 0},
		{"impact-reverse-A", "impact", string(ids["A"]), "reverse", 0},
		{"impact-bounded", "impact", string(ids["C"]), "forward", 2},
		{"impact-not-found", "impact", "missing-symbol", "forward", 0},
	}
	for _, c := range cases {
		cliBytes := analysisCLIOutput(t, direct, c.analyzer, c.symbol, c.direction, c.maxNodes)
		mcpBytes := analysisMCPOutput(t, direct, c.analyzer, c.symbol, c.direction, c.maxNodes)
		if !bytes.Equal(cliBytes, mcpBytes) {
			t.Fatalf("%s parity mismatch:\nCLI: %s\nMCP: %s", c.name, cliBytes, mcpBytes)
		}
	}
}

// TestMCP_AnalyzeToolAdvertised (SW-022): the analyze tool is advertised when an
// analysis service is attached, and NOT advertised when it is absent.
func TestMCP_AnalyzeToolAdvertised(t *testing.T) {
	store, _ := seed(t)

	// With analysis attached -> analyze tool advertised.
	withAnalysis := client.NewDirect(query.New(store), nil).
		WithAnalysis(analysis.NewDefaultService(store))
	srv := mcp.NewServerWithClient(withAnalysis)
	tools := listTools(t, srv)
	if !containsName(tools, "analyze") {
		t.Fatal("analyze tool not advertised when analysis service is attached")
	}

	// Without analysis (the legacy constructor) -> analyze tool NOT advertised.
	srvNoAnalysis := mcp.NewServer(query.New(store), nil)
	toolsNo := listTools(t, srvNoAnalysis)
	if containsName(toolsNo, "analyze") {
		t.Fatal("analyze tool advertised when analysis service is absent (should probe-hide)")
	}
}

func listTools(t *testing.T, srv *mcp.Server) []string {
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

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestMCP_CLI_BatchedParity (SW-026): the batched orchestrator returns
// byte-identical output through CLI and MCP for identical symbol+target, and
// the batched analyzer is advertised alongside its siblings.
func TestMCP_CLI_BatchedParity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	// CLI: analyze batched -symbol C -target A
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), direct,
		[]string{"batched", "-symbol", string(ids["C"]), "-target", string(ids["A"])},
		&cliOut, &cliErr); err != nil {
		t.Fatalf("cli batched: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	// MCP: tools/call analyze {analyzer:batched, symbol:C, target:A}
	args := map[string]any{
		"analyzer": "batched",
		"symbol":   string(ids["C"]),
		"target":   string(ids["A"]),
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "analyze", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.NewServerWithClient(direct)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp batched serve: %v", err)
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
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp batched error: %s", resp.Error.Message)
	}
	mcpBytes := []byte(resp.Result.Content[0].Text)
	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("batched parity mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
}

// TestMCP_CLI_CallChainParity (SW-023): the call-chain analyzer returns
// byte-identical output through CLI and MCP for identical source+target, and
// the call-chain analyzer is advertised alongside impact.
func TestMCP_CLI_CallChainParity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	// CLI: analyze call-chain -symbol A -target C
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), direct,
		[]string{"call-chain", "-symbol", string(ids["A"]), "-target", string(ids["C"])},
		&cliOut, &cliErr); err != nil {
		t.Fatalf("cli call-chain: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	// MCP: tools/call analyze {analyzer:call-chain, symbol:A, target:C}
	args := map[string]any{
		"analyzer": "call-chain",
		"symbol":   string(ids["A"]),
		"target":   string(ids["C"]),
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "analyze", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.NewServerWithClient(direct)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp call-chain serve: %v", err)
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
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp call-chain error: %s", resp.Error.Message)
	}
	mcpBytes := []byte(resp.Result.Content[0].Text)
	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("call-chain parity mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}

	// Both analyzers must be advertised now.
	names := listTools(t, srv)
	if !containsName(names, "analyze") {
		t.Fatal("analyze tool not advertised")
	}
}

// ---------------------------------------------------------------------------
// EP-005 deep-analyzer tests (SW-033)
// ---------------------------------------------------------------------------

// deepAnalyzerMCPOutput runs a dedicated EP-005 MCP tool (e.g. analyze_taint)
// and extracts the canonical text payload from the JSON-RPC response.
func deepAnalyzerMCPOutput(t *testing.T, direct *client.Direct, toolName, symbol string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)

	args := map[string]any{"symbol": symbol}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": toolName, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve %s: %v", toolName, err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode mcp %s response %q: %v", toolName, out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s error: %s", toolName, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp %s content: %+v", toolName, resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// TestMCP_CLI_EP005_Parity (SW-033): each EP-005 deep analyzer returns
// byte-identical output through the CLI (via generic analyze) and MCP (via
// both the generic analyze tool and the dedicated analyze_* tool).
func TestMCP_CLI_EP005_Parity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	// Each EP-005 analyzer is tested through:
	//   1. CLI:  analyze <analyzer> -symbol <id>
	//   2. MCP:  tools/call analyze {analyzer:<name>, symbol:<id>}
	//   3. MCP:  tools/call analyze_<name> {symbol:<id>}
	// All three must produce byte-identical output.
	cases := []struct {
		name         string
		analyzerName string // dispatch name for generic analyze tool
		mcpToolName  string // dedicated MCP tool name (analyze_*)
		symbol       string
	}{
		{"taint", "taint", "analyze_taint", string(ids["A"])},
		{"pdg", "pdg", "analyze_pdg", string(ids["A"])},
		{"interproc", "interproc", "analyze_interproc", string(ids["A"])},
		{"contracts", "contracts", "analyze_contracts", string(ids["A"])},
		{"githistory", "git-history", "analyze_githistory", string(ids["A"])},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// CLI path.
			cliBytes := analysisCLIOutput(t, direct, c.analyzerName, c.symbol, "forward", 0)
			// MCP generic analyze path.
			mcpGenericBytes := analysisMCPOutput(t, direct, c.analyzerName, c.symbol, "forward", 0)
			// MCP dedicated tool path.
			mcpDedicatedBytes := deepAnalyzerMCPOutput(t, direct, c.mcpToolName, c.symbol)

			if !bytes.Equal(cliBytes, mcpGenericBytes) {
				t.Fatalf("%s CLI<->MCP-generic parity mismatch:\nCLI: %s\nMCP: %s", c.name, cliBytes, mcpGenericBytes)
			}
			if !bytes.Equal(cliBytes, mcpDedicatedBytes) {
				t.Fatalf("%s CLI<->MCP-dedicated parity mismatch:\nCLI: %s\nMCP-dedicated: %s", c.name, cliBytes, mcpDedicatedBytes)
			}
		})
	}
}

// TestMCP_CLI_PriskParity (SW-039): the pr-risk scorer returns byte-identical
// output through the CLI (analyze pr-risk -diff ...) and MCP (analyze
// {analyzer:pr-risk,diff:...} AND the dedicated analyze_pr_risk tool), proving
// the versioned risk-record schema is emitted identically over both surfaces.
func TestMCP_CLI_PriskParity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	// A diff resolving one real region (p.A) plus an unresolved one (degraded).
	diff := "p/A.go:A\np/ghost.go:Ghost"
	_ = ids

	// CLI: analyze pr-risk -diff <diff>
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), direct,
		[]string{"pr-risk", "-diff", diff}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli pr-risk: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	// MCP generic analyze tool.
	mcpGeneric := priskMCPOutput(t, direct, "analyze", map[string]any{"analyzer": "pr-risk", "diff": diff})
	// MCP dedicated analyze_pr_risk tool.
	mcpDedicated := priskMCPOutput(t, direct, "analyze_pr_risk", map[string]any{"diff": diff})

	if !bytes.Equal(cliBytes, mcpGeneric) {
		t.Fatalf("pr-risk CLI<->MCP-generic mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpGeneric)
	}
	if !bytes.Equal(cliBytes, mcpDedicated) {
		t.Fatalf("pr-risk CLI<->MCP-dedicated mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpDedicated)
	}
	// Sanity: the output is the versioned risk report with a degraded record.
	if !bytes.Contains(cliBytes, []byte("\"scorer_version\":\"pr-risk/1\"")) {
		t.Fatalf("pr-risk output missing scorer_version: %s", cliBytes)
	}
	if !bytes.Contains(cliBytes, []byte("\"degraded\":true")) {
		t.Fatalf("pr-risk output missing degraded record: %s", cliBytes)
	}
}

// priskMCPOutput runs a tools/call with arbitrary arguments and returns the
// canonical text payload.
func priskMCPOutput(t *testing.T, direct *client.Direct, tool string, args map[string]any) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
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
		t.Fatalf("decode %s: %v", tool, err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s error: %s", tool, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("mcp %s: unexpected content", tool)
	}
	return []byte(resp.Result.Content[0].Text)
}

// TestMCP_CLI_PrQuestionsParity (SW-041): the pr-questions generator returns
// byte-identical output through the CLI (analyze pr-questions -diff ...) and MCP
// (analyze {analyzer:pr-questions,diff:...} AND the dedicated analyze_pr_questions
// tool), proving the versioned, deterministic question schema is emitted
// identically over both surfaces.
func TestMCP_CLI_PrQuestionsParity(t *testing.T) {
	store, ids := seed(t)
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))

	// A diff resolving one real region (p.A) plus an unresolved one (degraded).
	diff := "p/A.go:A\np/ghost.go:Ghost"
	_ = ids

	// CLI: analyze pr-questions -diff <diff>
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), direct,
		[]string{"pr-questions", "-diff", diff}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli pr-questions: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	// MCP generic analyze tool and dedicated analyze_pr_questions tool.
	mcpGeneric := priskMCPOutput(t, direct, "analyze", map[string]any{"analyzer": "pr-questions", "diff": diff})
	mcpDedicated := priskMCPOutput(t, direct, "analyze_pr_questions", map[string]any{"diff": diff})

	if !bytes.Equal(cliBytes, mcpGeneric) {
		t.Fatalf("pr-questions CLI<->MCP-generic mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpGeneric)
	}
	if !bytes.Equal(cliBytes, mcpDedicated) {
		t.Fatalf("pr-questions CLI<->MCP-dedicated mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpDedicated)
	}
	// Sanity: the output is the versioned question report.
	if !bytes.Contains(cliBytes, []byte("\"generator_version\":\"pr-questions/1\"")) {
		t.Fatalf("pr-questions output missing generator_version: %s", cliBytes)
	}
}

// TestMCP_PrQuestions_ToolAdvertised (SW-041): the dedicated analyze_pr_questions
// tool is advertised when analysis is attached and probe-hidden when absent.
func TestMCP_PrQuestions_ToolAdvertised(t *testing.T) {
	store, _ := seed(t)
	withAnalysis := client.NewDirect(query.New(store), nil).
		WithAnalysis(analysis.NewDefaultService(store))
	if !containsName(listTools(t, withAnalysis2srv(withAnalysis)), "analyze_pr_questions") {
		t.Fatal("analyze_pr_questions not advertised when analysis attached")
	}
	if containsName(listTools(t, mcp.NewServer(query.New(store), nil)), "analyze_pr_questions") {
		t.Fatal("analyze_pr_questions advertised when analysis absent (should probe-hide)")
	}
}

// TestMCP_Prisk_ToolAdvertised (SW-039): the dedicated analyze_pr_risk tool is
// advertised when analysis is attached and probe-hidden when it is absent.
func TestMCP_Prisk_ToolAdvertised(t *testing.T) {
	store, _ := seed(t)
	withAnalysis := client.NewDirect(query.New(store), nil).
		WithAnalysis(analysis.NewDefaultService(store))
	if !containsName(listTools(t, withAnalysis2srv(withAnalysis)), "analyze_pr_risk") {
		t.Fatal("analyze_pr_risk not advertised when analysis attached")
	}
	if containsName(listTools(t, mcp.NewServer(query.New(store), nil)), "analyze_pr_risk") {
		t.Fatal("analyze_pr_risk advertised when analysis absent (should probe-hide)")
	}
}

func withAnalysis2srv(c *client.Direct) *mcp.Server { return mcp.NewServerWithClient(c) }

// TestMCP_EP005_ToolsAdvertised (SW-033): all 5 EP-005 dedicated tools are
// advertised in tools/list when the analysis service is attached, and NOT
// advertised when it is absent.
func TestMCP_EP005_ToolsAdvertised(t *testing.T) {
	store, _ := seed(t)

	// With analysis service attached: all EP-005 tools should be advertised.
	withAnalysis := client.NewDirect(query.New(store), nil).
		WithAnalysis(analysis.NewDefaultService(store))
	srv := mcp.NewServerWithClient(withAnalysis)
	names := listTools(t, srv)

	ep005Tools := []string{"analyze_taint", "analyze_pdg", "analyze_interproc", "analyze_contracts", "analyze_githistory"}
	for _, toolName := range ep005Tools {
		if !containsName(names, toolName) {
			t.Errorf("EP-005 tool %q not advertised when analysis service is attached; got tools: %v", toolName, names)
		}
	}

	// Without analysis service: EP-005 tools should NOT be advertised.
	srvNoAnalysis := mcp.NewServer(query.New(store), nil)
	namesNo := listTools(t, srvNoAnalysis)
	for _, toolName := range ep005Tools {
		if containsName(namesNo, toolName) {
			t.Errorf("EP-005 tool %q advertised when analysis service is absent (should probe-hide)", toolName)
		}
	}
}

// TestMCP_EP005_EmptyResult (SW-033): three-state error model — analyzers return
// the empty outcome (not an error) when there is no data to report. This ensures
// the MCP surface passes the empty result through without converting it to an error.
func TestMCP_EP005_EmptyResult(t *testing.T) {
	// Use an empty store (no nodes, no edges) so all analyzers return empty.
	store := graphstore.NewMemStore()
	direct := client.NewDirect(query.New(store), nil).
		WithAnalysis(analysis.NewDefaultService(store))

	// Each dedicated MCP tool should return a non-error response with empty output.
	ep005Tools := []string{"analyze_taint", "analyze_pdg", "analyze_interproc", "analyze_contracts", "analyze_githistory"}
	for _, toolName := range ep005Tools {
		t.Run(toolName, func(t *testing.T) {
			srv := mcp.NewServerWithClient(direct)
			args := map[string]any{"symbol": "nonexistent"}
			reqBody, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params":  map[string]any{"name": toolName, "arguments": args},
			})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
				t.Fatalf("mcp.Serve %s: %v", toolName, err)
			}
			var resp struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				} `json:"result"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error != nil {
				t.Fatalf("%s returned JSON-RPC error for empty result: %s", toolName, resp.Error.Message)
			}
			if resp.Result.IsError {
				t.Fatalf("%s returned isError=true for empty result", toolName)
			}
			if len(resp.Result.Content) != 1 {
				t.Fatalf("%s returned %d content items, want 1", toolName, len(resp.Result.Content))
			}
			// The text payload must be valid JSON (canonical serialized output).
			if !json.Valid([]byte(resp.Result.Content[0].Text)) {
				t.Fatalf("%s returned invalid JSON: %s", toolName, resp.Result.Content[0].Text)
			}
		})
	}
}

// reviewDirect builds an in-process Direct client with the SW-042 review
// publisher wired over the same analysis service the siblings use.
func reviewDirect(store *graphstore.MemStore) *client.Direct {
	asvc := analysis.NewDefaultService(store)
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc))
}

// TestMCP_CLI_PrCommentParity (SW-042): the sticky PR-comment writer + merge gate
// returns byte-identical output through the CLI (pr-comment -diff ... -gate) and
// the MCP pr_comment tool, proving the versioned, deterministic PublishResult is
// emitted identically over both surfaces (parity by construction).
func TestMCP_CLI_PrCommentParity(t *testing.T) {
	store, _ := seed(t)
	direct := reviewDirect(store)

	diff := "p/A.go:A\np/ghost.go:Ghost"

	// CLI: pr-comment -diff <diff> -pr ref -gate -gate-threshold 100
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunPrComment(context.Background(), direct,
		[]string{"-diff", diff, "-pr", "owner/repo#7", "-gate", "-gate-threshold", "100"},
		&cliOut, &cliErr); err != nil {
		t.Fatalf("cli pr-comment: %v (stderr %s)", err, cliErr.String())
	}
	cliBytes := bytes.TrimRight(cliOut.Bytes(), "\n")

	// MCP pr_comment tool with the same arguments.
	mcpBytes := priskMCPOutput(t, direct, "pr_comment", map[string]any{
		"diff": diff, "pr": "owner/repo#7", "gate_enabled": true, "gate_threshold": 100,
	})

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("pr-comment CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
	// Sanity: the versioned writer + the hidden sticky marker are present.
	if !bytes.Contains(cliBytes, []byte("\"writer_version\":\"pr-comment/1\"")) {
		t.Fatalf("pr-comment output missing writer_version: %s", cliBytes)
	}
	if !bytes.Contains(cliBytes, []byte(review.StickyMarker)) {
		t.Fatalf("pr-comment output missing sticky marker: %s", cliBytes)
	}
}

// TestMCP_PrComment_ToolAdvertised (SW-042): the pr_comment tool is advertised
// when the review publisher is attached and probe-hidden when it is absent.
func TestMCP_PrComment_ToolAdvertised(t *testing.T) {
	store, _ := seed(t)
	withReview := reviewDirect(store)
	if !containsName(listTools(t, mcp.NewServerWithClient(withReview)), "pr_comment") {
		t.Fatal("pr_comment not advertised when review publisher attached")
	}
	// A client WITHOUT review wired must probe-hide pr_comment.
	noReview := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	if containsName(listTools(t, mcp.NewServerWithClient(noReview)), "pr_comment") {
		t.Fatal("pr_comment advertised when review publisher absent (should probe-hide)")
	}
}

// ---------------------------------------------------------------------------
// SW-044 (AC-1): cross-surface HTTP parity. The HTTP surface delegates 100% to
// the shared client.Client seam and embeds the engine bytes verbatim inside the
// versioned envelope. This test locks "parity by construction": the HTTP
// envelope payload is byte-identical to the CLI- and MCP-printed bytes for the
// SAME operation over the SAME Direct client + fixture.
// ---------------------------------------------------------------------------

// httpQueryOutput drives the read-only HTTP surface over the given Direct client
// and returns the envelope payload bytes (the provenance/answer subset that must
// match MCP/CLI).
func httpQueryOutput(t *testing.T, direct *client.Direct, op, symbol string, depth int) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	target := fmt.Sprintf("/query/%s?symbol=%s", op, symbol)
	if op == query.OpNeighborhood {
		target = fmt.Sprintf("%s&depth=%d", target, depth)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http %s: code=%d body=%s", target, rec.Code, rec.Body.String())
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http %s: decode envelope: %v", target, err)
	}
	return env.Payload
}

// httpSearchOutput drives the HTTP /search endpoint and returns the payload.
func httpSearchOutput(t *testing.T, direct *client.Direct, q string, limit int) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	target := fmt.Sprintf("/search?q=%s", q)
	if limit > 0 {
		target = fmt.Sprintf("%s&limit=%d", target, limit)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http %s: code=%d body=%s", target, rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http %s: decode envelope: %v", target, err)
	}
	return env.Payload
}

// TestHTTP_MCP_CLI_QueryParity (SW-044 AC-1): the HTTP envelope payload is
// byte-identical to the MCP and CLI output for the same query over the same
// in-process Direct client + fixture. This is the cross-surface parity lock.
func TestHTTP_MCP_CLI_QueryParity(t *testing.T) {
	store, ids := seed(t)
	svc := query.New(store)
	direct := client.NewDirect(svc, nil)

	cases := []struct {
		op     string
		symbol model.NodeId
		depth  int
	}{
		{query.OpCallers, ids["C"], 0},
		{query.OpCallees, ids["A"], 0},
		{query.OpReferences, ids["B"], 0},
		{query.OpDefinition, ids["A"], 0},
		{query.OpNeighborhood, ids["A"], 2},
		{query.OpCallers, model.NodeId("missing"), 0}, // not-found parity
	}
	for _, c := range cases {
		cliBytes := cliOutput(t, svc, nil, c.op, string(c.symbol), c.depth)
		mcpBytes := mcpOutput(t, svc, nil, c.op, string(c.symbol), c.depth)
		httpBytes := httpQueryOutput(t, direct, c.op, string(c.symbol), c.depth)
		if !bytes.Equal(httpBytes, cliBytes) {
			t.Fatalf("%s HTTP<->CLI parity mismatch:\nHTTP: %s\nCLI: %s", c.op, httpBytes, cliBytes)
		}
		if !bytes.Equal(httpBytes, mcpBytes) {
			t.Fatalf("%s HTTP<->MCP parity mismatch:\nHTTP: %s\nMCP: %s", c.op, httpBytes, mcpBytes)
		}
	}
}

// TestHTTP_MCP_CLI_SearchParity (SW-044 AC-1): the HTTP /search payload is
// byte-identical to the CLI and MCP search output over the same client.
func TestHTTP_MCP_CLI_SearchParity(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)
	direct := client.NewDirect(qsvc, ssvc)

	cases := []struct {
		q     string
		limit int
	}{
		{"p.A", 0},
		{"p", 2},
		{"missing-token", 0},
	}
	for _, c := range cases {
		cliBytes := searchCLIOutput(t, qsvc, ssvc, c.q, c.limit)
		mcpBytes := searchMCPOutput(t, qsvc, ssvc, c.q, c.limit)
		httpBytes := httpSearchOutput(t, direct, c.q, c.limit)
		if !bytes.Equal(httpBytes, cliBytes) {
			t.Fatalf("search %q HTTP<->CLI mismatch:\nHTTP: %s\nCLI: %s", c.q, httpBytes, cliBytes)
		}
		if !bytes.Equal(httpBytes, mcpBytes) {
			t.Fatalf("search %q HTTP<->MCP mismatch:\nHTTP: %s\nMCP: %s", c.q, httpBytes, mcpBytes)
		}
	}
}
