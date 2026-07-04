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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
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
	// + the EP-011 G1 "compound" tool + the SW-085 "search_ast" and "find_clones"
	// pattern-query tools + the EP-020 "explain_symbol", "related_files", and
	// "change_risk" tools (advertised unconditionally).
	if len(resp.Result.Tools) != len(query.Operations)+9 {
		t.Fatalf("tools count = %d, want %d", len(resp.Result.Tools), len(query.Operations)+9)
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

// agentDirect builds an in-process Direct client with EP-012 memory,
// distillation, and skill-generation services wired over a temp ledger.
func agentDirect(t *testing.T) *client.Direct {
	t.Helper()
	store := graphstore.NewMemStore()
	l, err := ledger.Open(t.TempDir() + "/ledger.jsonl")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	memStore, err := memory.NewMemStore(memory.NewLedgerHook(l, "", true))
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	t.Cleanup(func() { _ = memStore.Close() })
	return client.NewDirect(query.New(store), search.New(store)).
		WithLedger(l).
		WithMemory(memStore).
		WithDistill(distill.NewDistiller(distill.NewLedgerHook(l, "", true))).
		WithSkillGen(skillgen.NewGenerator(skillgen.NewLedgerHook(l, "", true)))
}

func memoryCLIOutput(t *testing.T, direct *client.Direct, op string, args ...string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	allArgs := append([]string{op}, args...)
	if err := cli.RunMemory(context.Background(), direct, allArgs, &out, &errOut); err != nil {
		t.Fatalf("cli.RunMemory(%s): %v (stderr: %s)", op, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

func memoryMCPOutput(t *testing.T, direct *client.Direct, op string, args map[string]any) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
	args["op"] = op
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "memory", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve memory: %v", err)
	}
	return extractMCPText(t, &out)
}

func memoryHTTPOutput(t *testing.T, direct *client.Direct, op string, body client.MemoryRequest) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/memory", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http /memory: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Payload
}

func extractMCPText(t *testing.T, out *bytes.Buffer) []byte {
	t.Helper()
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
		t.Fatalf("decode mcp response %q: %v", out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp error: %s", resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("unexpected mcp content: %+v", resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// TestMCP_CLI_MemoryParity (EP-012): memory recall returns byte-identical
// output through CLI, MCP, and HTTP surfaces when the same entry is stored
// first. Store is stateful (assigns monotonic IDs), so parity is asserted on
// the read-side response.
func TestMCP_CLI_MemoryParity(t *testing.T) {
	direct := agentDirect(t)

	// Seed one entry directly through the shared client so all surfaces recall
	// the same state.
	storeBytes := memoryCLIOutput(t, direct, "store", "-scope", "aria", "-notebook", "notes", "-payload", "hello")
	var stored client.MemoryResponse
	if err := json.Unmarshal(storeBytes, &stored); err != nil {
		t.Fatalf("unmarshal store response: %v", err)
	}

	recallBytes := memoryCLIOutput(t, direct, "recall", "-scope", "aria")
	mcpRecallBytes := memoryMCPOutput(t, direct, "recall", map[string]any{"scope": "aria"})
	httpRecallBytes := memoryHTTPOutput(t, direct, "recall", client.MemoryRequest{Op: "recall", Scope: "aria"})
	if !bytes.Equal(recallBytes, mcpRecallBytes) {
		t.Fatalf("memory recall CLI<->MCP mismatch:\nCLI: %s\nMCP: %s", recallBytes, mcpRecallBytes)
	}
	if !bytes.Equal(recallBytes, httpRecallBytes) {
		t.Fatalf("memory recall CLI<->HTTP mismatch:\nCLI: %s\nHTTP: %s", recallBytes, httpRecallBytes)
	}
	if !bytes.Contains(recallBytes, []byte(stored.ID)) {
		t.Fatalf("recall response missing stored id %s: %s", stored.ID, recallBytes)
	}
}

func distillCLIOutput(t *testing.T, direct *client.Direct, session string, decisions []string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"-session", session, "-decisions", strings.Join(decisions, ",")}
	if err := cli.RunDistill(context.Background(), direct, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunDistill: %v (stderr: %s)", err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

func distillMCPOutput(t *testing.T, direct *client.Direct, session string, decisions []string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
	args := map[string]any{"session_id": session, "decisions": decisions}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "distill", "arguments": args},
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve distill: %v", err)
	}
	return extractMCPText(t, &out)
}

// TestMCP_CLI_DistillParity (EP-012): session distillation returns byte-identical
// output through CLI and MCP surfaces.
func TestMCP_CLI_DistillParity(t *testing.T) {
	direct := agentDirect(t)
	cliBytes := distillCLIOutput(t, direct, "sess-1", []string{"use JSONL", "no LLM"})
	mcpBytes := distillMCPOutput(t, direct, "sess-1", []string{"use JSONL", "no LLM"})
	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("distill parity mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
	}
}

func skillGenCLIOutput(t *testing.T, direct *client.Direct, name, trigger string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"-name", name, "-trigger", trigger}
	if err := cli.RunSkillGen(context.Background(), direct, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunSkillGen: %v (stderr: %s)", err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

func skillGenMCPOutput(t *testing.T, direct *client.Direct, name, trigger string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
	args := map[string]any{"name": name, "trigger": trigger}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "skillgen", "arguments": args},
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve skillgen: %v", err)
	}
	return extractMCPText(t, &out)
}

// TestMCP_CLI_SkillGenParity (EP-012): skill generation returns byte-identical
// output through CLI and MCP surfaces.
func TestMCP_CLI_SkillGenParity(t *testing.T) {
	direct := agentDirect(t)
	cliBytes := skillGenCLIOutput(t, direct, "Run Refactor", "/refactor")
	mcpBytes := skillGenMCPOutput(t, direct, "Run Refactor", "/refactor")
	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("skillgen parity mismatch:\nCLI: %s\nMCP: %s", cliBytes, mcpBytes)
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

// --- SW-085: search_ast / find_clones cross-surface parity --------------------

// callMCPTool runs one tools/call through the MCP stdio server and returns the
// canonical text payload, mirroring mcpOutput but with arbitrary tool+arguments.
func callMCPTool(t *testing.T, qsvc *query.Service, ssvc *search.Service, name string, args map[string]any) []byte {
	t.Helper()
	srv := mcp.NewServer(qsvc, ssvc)
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve %s: %v", name, err)
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
		t.Fatalf("decode mcp %s response %q: %v", name, out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s error: %s", name, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp %s content: %+v", name, resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// httpPostOutput drives one POST through the HTTP handler backed by the same
// Direct client and returns the unwrapped canonical payload bytes, mirroring the
// existing HTTP parity helpers (envelope = {"payload": <canonical bytes>}).
func httpPostOutput(t *testing.T, direct *client.Direct, path, body string) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http POST %s: code=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http POST %s decode envelope %q: %v", path, rec.Body.String(), err)
	}
	return []byte(env.Payload)
}

// TestParity_SearchAST asserts search_ast returns byte-identical canonical output
// through CLI, MCP, and HTTP (SW-085 AC1).
func TestParity_SearchAST(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)
	direct := client.NewDirect(qsvc, ssvc)

	pattern := `{"kind":"function"}`

	// CLI
	var out, errOut bytes.Buffer
	if err := cli.RunSearchAST(context.Background(), direct, []string{pattern}, &out, &errOut); err != nil {
		t.Fatalf("cli.RunSearchAST: %v (stderr: %s)", err, errOut.String())
	}
	cliBytes := bytes.TrimRight(out.Bytes(), "\n")

	// MCP
	mcpBytes := callMCPTool(t, qsvc, ssvc, "search_ast", map[string]any{"pattern": pattern})

	// HTTP
	httpBytes := httpPostOutput(t, direct, "/query-ast", pattern)

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("search_ast CLI<->MCP mismatch:\nCLI:  %s\nMCP:  %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("search_ast CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	// Sanity: the pattern actually matched (non-empty), so parity is meaningful.
	if !bytes.Contains(cliBytes, []byte("\"nodes\"")) {
		t.Fatalf("search_ast result missing nodes envelope: %s", cliBytes)
	}
}

// TestParity_FindClones asserts find_clones returns byte-identical canonical
// output through CLI, MCP, and HTTP (SW-085 AC2). The seed produces no clone
// group, so all three return the identical typed-empty envelope — parity holds
// for the empty variant exactly as for the populated one.
func TestParity_FindClones(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)
	direct := client.NewDirect(qsvc, ssvc)

	var out, errOut bytes.Buffer
	if err := cli.RunFindClones(context.Background(), direct, nil, &out, &errOut); err != nil {
		t.Fatalf("cli.RunFindClones: %v (stderr: %s)", err, errOut.String())
	}
	cliBytes := bytes.TrimRight(out.Bytes(), "\n")

	mcpBytes := callMCPTool(t, qsvc, ssvc, "find_clones", map[string]any{})
	httpBytes := httpPostOutput(t, direct, "/find-clones", "")

	if !bytes.Equal(cliBytes, mcpBytes) {
		t.Fatalf("find_clones CLI<->MCP mismatch:\nCLI:  %s\nMCP:  %s", cliBytes, mcpBytes)
	}
	if !bytes.Equal(cliBytes, httpBytes) {
		t.Fatalf("find_clones CLI<->HTTP mismatch:\nCLI:  %s\nHTTP: %s", cliBytes, httpBytes)
	}
	if !bytes.Contains(cliBytes, []byte("\"find_clones\"")) {
		t.Fatalf("find_clones envelope missing operation field: %s", cliBytes)
	}
}

// TestMCP_NewToolAnnotations asserts the three new pattern-query tools carry the
// SW-085 AC4 annotation set (readOnly / idempotent / !destructive / !openWorld).
func TestMCP_NewToolAnnotations(t *testing.T) {
	store, _ := seed(t)
	srv := mcp.NewServer(query.New(store), search.New(store))
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve tools/list: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Annotations map[string]bool `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode tools/list %q: %v", out.String(), err)
	}
	want := map[string]struct{}{"search_ast": {}, "find_clones": {}}
	seen := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		if _, ok := want[tool.Name]; !ok {
			continue
		}
		seen[tool.Name] = true
		a := tool.Annotations
		if !a["readOnlyHint"] || a["destructiveHint"] || !a["idempotentHint"] || a["openWorldHint"] {
			t.Errorf("%s annotations = %+v, want readOnly+idempotent, !destructive+!openWorld", tool.Name, a)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("tool %q not advertised in tools/list", name)
		}
	}
}

// ---------------------------------------------------------------------------
// SW-104 (EP-017 capstone): cross-surface byte-parity for the four canonical
// EP-017 operations over ALL FIVE surfaces (CLI, MCP stdio, MCP HTTP/REST, SSE,
// daemon). Each operation is dispatched through the ONE shared path
// ((*Direct).Analyze -> Service.Dispatch -> analysis.Marshal); every surface must
// return a byte-identical envelope (AC-2), and each returns a successful
// non-empty envelope on every surface (AC-1).
// ---------------------------------------------------------------------------

var ep017Ops = []string{"notebook-ingest", "watcher-status", "taint-query", "communities"}

// seedEP017 builds an in-process Direct client over a graph that carries content
// for every EP-017 operation: the standard function/call graph (communities +
// taint-query) plus one SW-100 notebook_cell provenance edge (notebook-ingest).
func seedEP017(t *testing.T) *client.Direct {
	t.Helper()
	store, _ := seed(t)
	ctx := context.Background()
	fileNode, err := model.NewNode("file", "nb.ipynb", "nb.ipynb", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, fileNode); err != nil {
		t.Fatal(err)
	}
	symNode, err := model.NewNode("function", "nb.nb_func", "nb.ipynb", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, symNode); err != nil {
		t.Fatal(err)
	}
	e, err := model.NewEdge(symNode.ID(), fileNode.ID(), "notebook_cell",
		model.TierConfirmed, 1.0, "notebook cell provenance",
		[]string{"nb.ipynb#cell=1;id=c1;line=2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(ctx, e); err != nil {
		t.Fatal(err)
	}
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

// opCLIOutput runs an EP-017 (symbol-optional) operation through the CLI surface.
func opCLIOutput(t *testing.T, direct *client.Direct, op string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), direct, []string{op}, &out, &errOut); err != nil {
		t.Fatalf("cli.RunAnalysis(%s): %v (stderr %s)", op, err, errOut.String())
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// opMCPStdioOutput runs an EP-017 operation through the MCP stdio analyze tool.
func opMCPStdioOutput(t *testing.T, direct *client.Direct, op string) []byte {
	t.Helper()
	srv := mcp.NewServerWithClient(direct)
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "analyze", "arguments": map[string]any{"analyzer": op}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(reqBody)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve analyze %s: %v", op, err)
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
		t.Fatalf("decode mcp %s response %q: %v", op, out.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp %s error: %s", op, resp.Error.Message)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected mcp %s content: %+v", op, resp.Result.Content)
	}
	return []byte(resp.Result.Content[0].Text)
}

// opHTTPOutput runs an EP-017 operation through the HTTP REST /analyze endpoint
// and returns the unwrapped canonical payload (the envelope wraps the SAME bytes).
func opHTTPOutput(t *testing.T, direct *client.Direct, op string) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/analyze/"+op, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("http /analyze/%s: code=%d body=%s", op, rec.Code, rec.Body.String())
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("http /analyze/%s: decode envelope: %v", op, err)
	}
	return env.Payload
}

// opSSEOutput runs an EP-017 operation through the SSE /events?analyzer= one-shot
// analysis path and extracts the canonical bytes from the `analysis` frame's data
// line. The SSE adapter routes through the SAME (*Direct).Analyze path, so the
// data payload is byte-identical to the other surfaces' envelopes.
func opSSEOutput(t *testing.T, direct *client.Direct, op string) []byte {
	t.Helper()
	srv := httpsrv.New(direct, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/events?analyzer="+op, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sse /events?analyzer=%s: code=%d body=%s", op, rec.Code, rec.Body.String())
	}
	// Find the data line of the `event: analysis` frame.
	lines := strings.Split(rec.Body.String(), "\n")
	for i, ln := range lines {
		if ln == "event: analysis" {
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "data: ") {
					return []byte(strings.TrimPrefix(lines[j], "data: "))
				}
			}
		}
	}
	t.Fatalf("sse /events?analyzer=%s: no analysis frame in body:\n%s", op, rec.Body.String())
	return nil
}

// opDaemonOutput runs an EP-017 operation through the daemon over a real Unix
// socket (no auto-start: the server is already listening), exercising the SW-104
// daemon analyze RPC. The handler is the same in-process Direct client.
func opDaemonOutput(t *testing.T, direct *client.Direct, op string) []byte {
	t.Helper()
	srv := daemon.NewServer(direct)
	dir, err := os.MkdirTemp("", "g*.sock") // short path (macOS socket limit)
	if err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	if err := srv.Start(sock); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	c := daemon.NewClient(sock, "")
	b, err := c.Analyze(context.Background(), client.AnalyzeParams{Name: op})
	if err != nil {
		t.Fatalf("daemon analyze %s: %v", op, err)
	}
	return b
}

// TestCrossSurface_EP017_OpParity is the AC-1 + AC-2 cross-surface gate: for each
// of the four EP-017 operations, all five surfaces return a successful,
// byte-identical envelope.
func TestCrossSurface_EP017_OpParity(t *testing.T) {
	direct := seedEP017(t)
	for _, op := range ep017Ops {
		t.Run(op, func(t *testing.T) {
			cliBytes := opCLIOutput(t, direct, op)
			if len(cliBytes) == 0 {
				t.Fatalf("op %q: empty CLI envelope (AC-1 reachability)", op)
			}
			surfaces := []struct {
				name  string
				bytes []byte
			}{
				{"mcp-stdio", opMCPStdioOutput(t, direct, op)},
				{"mcp-http", opHTTPOutput(t, direct, op)},
				{"sse", opSSEOutput(t, direct, op)},
				{"daemon", opDaemonOutput(t, direct, op)},
			}
			for _, s := range surfaces {
				if !bytes.Equal(cliBytes, s.bytes) {
					t.Fatalf("op %q: CLI<->%s byte-parity mismatch:\n CLI: %s\n %s: %s", op, s.name, cliBytes, s.name, s.bytes)
				}
			}
		})
	}
}

// TestCrossSurface_EP017_WatcherStatusHonest asserts the SW-101 honesty
// obligation: the watcher-status envelope reports a watcher error verbatim (here
// the SW-101 Reconcile-on-non-code-files error class) rather than masking it
// behind a green status, and that the honest report is byte-identical across
// surfaces. It uses a fake provider so the test does not depend on a running
// watcher (the SW-101 Reconcile bug itself is out of scope here).
func TestCrossSurface_EP017_WatcherStatusHonest(t *testing.T) {
	store, _ := seed(t)
	svc := analysis.NewDefaultServiceWithWatch(store, fakeWatchProvider{})
	direct := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(svc)

	cliBytes := opCLIOutput(t, direct, "watcher-status")
	// The error is surfaced, not masked.
	if !bytes.Contains(cliBytes, []byte(`"healthy":false`)) ||
		!bytes.Contains(cliBytes, []byte("reconcile: non-code file")) {
		t.Fatalf("watcher-status did not honestly surface the watcher error: %s", cliBytes)
	}
	// And it is byte-identical across surfaces.
	for _, got := range [][]byte{
		opMCPStdioOutput(t, direct, "watcher-status"),
		opHTTPOutput(t, direct, "watcher-status"),
		opSSEOutput(t, direct, "watcher-status"),
		opDaemonOutput(t, direct, "watcher-status"),
	} {
		if !bytes.Equal(cliBytes, got) {
			t.Fatalf("watcher-status honest-report parity mismatch:\n CLI: %s\n got: %s", cliBytes, got)
		}
	}
}

// fakeWatchProvider reports an unhealthy watcher (simulating the SW-101
// Reconcile-on-non-code-files error) so the honesty obligation can be asserted
// deterministically.
type fakeWatchProvider struct{}

func (fakeWatchProvider) WatchStatus(_ context.Context) analysis.WatcherStatusReport {
	return analysis.WatcherStatusReport{
		Active: true,
		Roots: []analysis.WatchRootStatus{
			{Root: "/repo", Watching: true, Healthy: false, LastError: "reconcile: non-code file encountered"},
		},
	}
}

// TestMCP_CLI_AgentToolsParity (EP-020): the explain_symbol / related_files /
// change_risk tools return byte-identical canonical contract JSON over the CLI
// and MCP surfaces, because both ride the same client seam and the same
// contract serializer.
func TestMCP_CLI_AgentToolsParity(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)

	mcpArgs := func(name string, args map[string]any) []byte {
		t.Helper()
		srv := mcp.NewServer(qsvc, ssvc)
		reqBody, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params":  map[string]any{"name": name, "arguments": args},
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
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("decode mcp response: %v", err)
		}
		if len(resp.Result.Content) != 1 {
			t.Fatalf("unexpected content: %+v", resp.Result.Content)
		}
		return []byte(resp.Result.Content[0].Text)
	}

	c := client.NewDirect(qsvc, ssvc)

	// explain_symbol
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunExplainSymbol(context.Background(), c, []string{"p.B"}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli explain-symbol: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolExplainSymbol, map[string]any{"symbol": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("explain_symbol parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// related_files
	cliOut.Reset()
	if err := cli.RunRelatedFiles(context.Background(), c, []string{"-direction", "dependents", "p.B"}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli related-files: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolRelatedFiles, map[string]any{"target": "p.B", "direction": "dependents"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("related_files parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// change_risk
	cliOut.Reset()
	if err := cli.RunChangeRisk(context.Background(), c, []string{"p.B"}, strings.NewReader(""), &cliOut, &cliErr); err != nil {
		t.Fatalf("cli change-risk: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolChangeRisk, map[string]any{"target": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("change_risk parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}
}
