package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// agentToolsServer builds an MCP server over a real in-memory graph so the
// EP-020 tools/call paths execute live engine logic end-to-end.
func agentToolsServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	format := mk("function", "util.Format", "util/format.go", 3)
	mk("function", "Dup", "a/dup.go", 1)
	mk("function", "Dup", "b/dup.go", 2)

	e, err := model.NewEdge(run.ID(), format.ID(), "calls", model.TierConfirmed, 0.95, "fixture", []string{"cmd/app/main.go:12"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(ctx, e); err != nil {
		t.Fatal(err)
	}
	return NewServer(query.New(store), search.New(store))
}

// callTool sends one tools/call over the stdio framing and returns the inner
// contract JSON from the first text content block.
func callTool(t *testing.T, srv *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	argBytes, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + string(argBytes) + `}}`
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("bad rpc envelope %q: %v", out.String(), err)
	}
	if envelope.Error != nil {
		t.Fatalf("rpc error: %s", envelope.Error.Message)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("no content in result: %q", out.String())
	}
	text := envelope.Result.Content[0].Text
	// agent_brief wraps the canonical JSON in Markdown + fenced block.
	if i := strings.Index(text, "```json\n"); i >= 0 {
		text = text[i+len("```json\n"):]
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("content is not contract JSON: %v\n%s", err, text)
	}
	return payload
}

func TestMCP_ExplainSymbol_Found(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolExplainSymbol, map[string]any{"symbol": "util.Format"})
	if res["outcome"] != "found" {
		t.Fatalf("expected found, got %v (%v)", res["outcome"], res["summary"])
	}
	summary, _ := res["summary"].(string)
	if !strings.Contains(summary, "1 callers") {
		t.Fatalf("expected caller count in summary: %q", summary)
	}
	// Confidence/evidence propagation through the MCP boundary.
	conf, _ := res["confidence"].(map[string]any)
	if conf == nil || conf["top"] != "confirmed" {
		t.Fatalf("expected confirmed confidence, got %v", conf)
	}
	ev, _ := res["evidence"].([]any)
	if len(ev) == 0 {
		t.Fatal("expected evidence entries")
	}
}

func TestMCP_ExplainSymbol_Ambiguous(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolExplainSymbol, map[string]any{"symbol": "Dup"})
	if res["outcome"] != "ambiguous" {
		t.Fatalf("expected ambiguous, got %v", res["outcome"])
	}
	items, _ := res["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(items))
	}
}

func TestMCP_ExplainSymbol_Empty(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolExplainSymbol, map[string]any{"symbol": "nope.Missing"})
	if res["outcome"] != "empty" {
		t.Fatalf("expected empty, got %v", res["outcome"])
	}
}

func TestMCP_RelatedFiles_Found(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolRelatedFiles, map[string]any{"target": "util.Format", "direction": "dependents"})
	if res["outcome"] != "found" {
		t.Fatalf("expected found, got %v (%v)", res["outcome"], res["summary"])
	}
	items, _ := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 related file, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["ref_id"] != "cmd/app/main.go" {
		t.Fatalf("expected cmd/app/main.go, got %v", first["ref_id"])
	}
	if first["reason"] == "" {
		t.Fatal("expected a reason per ranked file")
	}
}

func TestMCP_ChangeRisk_Found(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolChangeRisk, map[string]any{"target": "util.Format"})
	summary, _ := res["summary"].(string)
	if !strings.Contains(summary, "risk: low") {
		t.Fatalf("expected an explicit risk level, got %q", summary)
	}
	if !strings.Contains(summary, "resolved exactly") {
		t.Fatalf("expected resolution statement, got %q", summary)
	}
}

func TestMCP_TruncationMarksPartial(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolExplainSymbol, map[string]any{"symbol": "util.Format", "limit": 1})
	if res["outcome"] != "partial" {
		t.Fatalf("expected partial under cap, got %v", res["outcome"])
	}
	limits, _ := res["limits"].(map[string]any)
	if limits == nil || limits["truncated"] != true {
		t.Fatalf("expected truncated limits, got %v", limits)
	}
}

func TestMCP_AgentBrief_ContractPayload(t *testing.T) {
	srv := agentToolsServer(t)
	res := callTool(t, srv, ToolAgentBrief, map[string]any{})
	if res["outcome"] == nil || res["summary"] == nil {
		t.Fatalf("agent_brief must return the contract envelope, got %v", res)
	}
}
