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
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
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
	if len(resp.Result.Tools) != len(query.Operations)+1 {
		t.Fatalf("tools count = %d, want %d", len(resp.Result.Tools), len(query.Operations)+1)
	}
}
