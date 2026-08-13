// P0 agent-intelligence parity: the three labs operations return
// byte-identical canonical contract JSON over the CLI and MCP surfaces,
// because both ride the same client seam and the same contract serializer
// (the EP-020 agent-tools parity template).
package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

func TestMCP_CLI_AgentIntelParity(t *testing.T) {
	store, _ := seed(t)
	qsvc := query.New(store)
	ssvc := search.New(store)

	mcpArgs := func(name string, args map[string]any) []byte {
		t.Helper()
		// The default MCP profile is Stable; the P0 tools are labs, so the
		// server opts into the maximal catalog exactly like `graphi mcp -labs`.
		srv := mcp.NewServer(qsvc, ssvc, mcp.WithLabs())
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

	// symbol_context
	var cliOut, cliErr bytes.Buffer
	if err := cli.RunSymbolContext(context.Background(), c, []string{"p.B"}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli symbol-context: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolSymbolContext, map[string]any{"symbol": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("symbol_context parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// task_context
	cliOut.Reset()
	if err := cli.RunTaskContext(context.Background(), c, []string{"p.B"}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli task-context: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolTaskContext, map[string]any{"task": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("task_context parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// repo_overview
	cliOut.Reset()
	if err := cli.RunRepoOverview(context.Background(), c, nil, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli repo-overview: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolRepoOverview, map[string]any{}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("repo_overview parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// test_impact (target mode)
	cliOut.Reset()
	if err := cli.RunTestImpact(context.Background(), c, []string{"p.B"}, strings.NewReader(""), &cliOut, &cliErr); err != nil {
		t.Fatalf("cli test-impact: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolTestImpact, map[string]any{"target": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("test_impact parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// change_impact (target mode)
	cliOut.Reset()
	if err := cli.RunChangeImpact(context.Background(), c, []string{"p.B"}, strings.NewReader(""), &cliOut, &cliErr); err != nil {
		t.Fatalf("cli change-impact: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolChangeImpact, map[string]any{"target": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("change_impact parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// search_hybrid
	cliOut.Reset()
	if err := cli.RunSearchHybrid(context.Background(), c, []string{"p.B"}, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli search-hybrid: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolSearchHybrid, map[string]any{"query": "p.B"}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("search_hybrid parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}

	// hotspots (no provider on either surface: parity over the typed
	// unavailable degradation)
	cliOut.Reset()
	if err := cli.RunHotspots(context.Background(), c, nil, &cliOut, &cliErr); err != nil {
		t.Fatalf("cli hotspots: %v (%s)", err, cliErr.String())
	}
	if got, want := mcpArgs(mcp.ToolHotspots, map[string]any{}), bytes.TrimRight(cliOut.Bytes(), "\n"); !bytes.Equal(got, want) {
		t.Fatalf("hotspots parity mismatch:\n CLI: %s\n MCP: %s", want, got)
	}
}
