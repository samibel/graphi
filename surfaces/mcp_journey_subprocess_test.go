package surfaces_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCharacterization_MCPStdioJourney_Subprocess is the SW-110 (TEST-01) AC4
// characterization: it drives a REAL `initialize → tools/list → tools/call` MCP
// stdio journey against a freshly indexed fixture repo, with the server running
// as an out-of-process `graphi mcp` SUBPROCESS reading JSON-RPC from stdin and
// writing framed responses to stdout — the honest path an AI agent actually
// takes. It pins that journey so SCOPE-01/SP-10 changes to the MCP surface are
// visible as reviewed diffs against observed subprocess behavior.
//
// It builds the graphi binary, indexes the pinned corpus/fixtures/go sample into
// a durable store, then dials the subprocess with the three-step journey and
// asserts each observed response.
func TestCharacterization_MCPStdioJourney_Subprocess(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root := moduleRoot(t)
	work := t.TempDir()

	// 1. Build the real graphi binary (CGo-free, matching the shipped flavor).
	bin := filepath.Join(work, "graphi")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}

	// 2. Index the pinned fixture repo into a durable store.
	db := filepath.Join(work, "graph.db")
	meta := filepath.Join(work, "meta")
	fixture := filepath.Join(root, "corpus", "fixtures", "go")
	idx := exec.Command(bin, "index", "-root", fixture, "-db", db, "-meta", meta)
	idx.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := idx.CombinedOutput(); err != nil {
		t.Fatalf("index fixture: %v\n%s", err, out)
	}

	// 3. Drive the stdio journey against `graphi mcp -db <db>` as a subprocess.
	journey := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mcp := exec.CommandContext(ctx, bin, "mcp", "-db", db)
	mcp.Env = append(os.Environ(), "CGO_ENABLED=0")
	mcp.Stdin = strings.NewReader(journey)
	var stdout, stderr bytes.Buffer
	mcp.Stdout = &stdout
	mcp.Stderr = &stderr
	if err := mcp.Run(); err != nil {
		t.Fatalf("graphi mcp subprocess: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}

	byID := decodeByID(t, stdout.Bytes())

	// initialize → a result envelope (protocol handshake).
	initResp, ok := byID[1]
	if !ok || initResp.Error != nil || len(initResp.Result) == 0 {
		t.Fatalf("initialize: missing/error result: %+v (stderr: %s)", initResp, stderr.String())
	}

	// tools/list → a non-empty tools array that includes the frozen "search" tool.
	listResp, ok := byID[2]
	if !ok || listResp.Error != nil {
		t.Fatalf("tools/list: missing/error result: %+v", listResp)
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &list); err != nil {
		t.Fatalf("tools/list decode: %v (%s)", err, listResp.Result)
	}
	if len(list.Tools) == 0 {
		t.Fatalf("tools/list returned no tools")
	}
	if !hasTool(list.Tools, "search") {
		t.Fatalf("tools/list missing the stable 'search' tool; got %d tools", len(list.Tools))
	}

	// tools/call(search "Hello") → text content citing the fixture symbol, proving
	// the subprocess queried the REAL indexed graph end-to-end.
	callResp, ok := byID[3]
	if !ok || callResp.Error != nil {
		t.Fatalf("tools/call: missing/error result: %+v", callResp)
	}
	var call struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(callResp.Result, &call); err != nil {
		t.Fatalf("tools/call decode: %v (%s)", err, callResp.Result)
	}
	if len(call.Content) == 0 || call.Content[0].Type != "text" {
		t.Fatalf("tools/call unexpected content: %+v", call.Content)
	}
	if !strings.Contains(call.Content[0].Text, "Hello") {
		t.Fatalf("tools/call(search Hello) did not cite the fixture symbol; got: %s", call.Content[0].Text)
	}
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// decodeByID parses the newline-framed JSON-RPC responses the MCP subprocess
// emitted and indexes them by request id.
func decodeByID(t *testing.T, out []byte) map[int]rpcResponse {
	t.Helper()
	byID := map[int]rpcResponse{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal(line, &r); err != nil {
			continue // tolerate any non-response log lines
		}
		byID[r.ID] = r
	}
	if len(byID) == 0 {
		t.Fatalf("no JSON-RPC responses parsed from subprocess stdout:\n%s", out)
	}
	return byID
}

func hasTool(tools []struct {
	Name string `json:"name"`
}, want string) bool {
	for _, tl := range tools {
		if tl.Name == want {
			return true
		}
	}
	return false
}
