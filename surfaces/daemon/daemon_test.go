package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	"github.com/samibel/graphi/surfaces/mcp"
)

func seedStore(t *testing.T) *graphstore.MemStore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	a, _ := model.NewNode("function", "p.A", "p/a.go", 1, 1)
	b, _ := model.NewNode("function", "p.B", "p/b.go", 1, 1)
	for _, n := range []model.Node{a, b} {
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := model.NewEdge(a.ID(), b.ID(), query.EdgeKindCalls, model.TierConfirmed, 1, "ab", []string{"a.go:2"})
	if err := store.PutEdge(ctx, e); err != nil {
		t.Fatal(err)
	}
	return store
}

func newDaemon(t *testing.T) (*daemon.Server, string) {
	t.Helper()
	store := seedStore(t)
	t.Cleanup(func() { _ = store.Close() })
	handler := client.NewDirect(query.New(store), search.New(store))
	srv := daemon.NewServer(handler)
	// Use a short temp dir to stay within Unix socket path limits on macOS.
	dir, err := os.MkdirTemp("", "g*.sock")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	if err := srv.Start(sock); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv, sock
}

func TestDaemon_SocketPermissions(t *testing.T) {
	_, sock := newDaemon(t)
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Fatalf("socket permissions too permissive: %o", perm)
	}
}

func TestDaemon_QueryOverSocket(t *testing.T) {
	_, sock := newDaemon(t)
	c := daemon.NewClient(sock, "")
	b, err := c.Query(context.Background(), "callers", string(b.ID()), 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !bytes.Contains(b, []byte(`"outcome"`)) {
		t.Fatalf("unexpected query response: %s", b)
	}
}

func TestDaemon_SearchOverSocket(t *testing.T) {
	_, sock := newDaemon(t)
	c := daemon.NewClient(sock, "")
	b, err := c.Search(context.Background(), "p.A", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !bytes.Contains(b, []byte(`"matches"`)) {
		t.Fatalf("unexpected search response: %s", b)
	}
}

func TestDaemon_CLIByteParityWithDaemon(t *testing.T) {
	_, sock := newDaemon(t)
	c := daemon.NewClient(sock, "")

	var out bytes.Buffer
	if err := cli.Run(context.Background(), c, []string{"callers", "-symbol", string(b.ID())}, &out, os.Stderr); err != nil {
		t.Fatalf("cli.Run: %v", err)
	}
	cliBytes := bytes.TrimRight(out.Bytes(), "\n")

	b2, err := c.Query(context.Background(), "callers", string(b.ID()), 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !bytes.Equal(cliBytes, b2) {
		t.Fatalf("CLI/daemon parity mismatch:\nCLI: %s\nDaemon: %s", cliBytes, b2)
	}
}

func TestDaemon_MCPThroughDaemon(t *testing.T) {
	_, sock := newDaemon(t)
	c := daemon.NewClient(sock, "")
	srv := mcp.NewServerWithClient(c)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "callers", "arguments": map[string]any{"symbol": string(b.ID())}},
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(req)+"\n"), &out); err != nil {
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
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Result.Content) != 1 || !bytes.Contains([]byte(resp.Result.Content[0].Text), []byte(`"operation":"callers"`)) {
		t.Fatalf("unexpected MCP response: %s", out.Bytes())
	}
}

func TestDaemon_MCPToolsListHidesUnwiredStableRPCs(t *testing.T) {
	// The socket deliberately does not exist. Capability discovery must be local
	// and side-effect free: tools/list may not dial or auto-start the daemon.
	c := daemon.NewClient(filepath.Join(t.TempDir(), "missing.sock"), os.Args[0])
	srv := mcp.NewServerWithClient(c)

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(req)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode tools/list: %v (%s)", err, out.Bytes())
	}
	if resp.Error != nil {
		t.Fatalf("tools/list failed: %s", out.Bytes())
	}
	got := make([]string, 0, len(resp.Result.Tools))
	for _, tool := range resp.Result.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{"callees", "callers", "definition", "impact", "neighborhood", "references", "search"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("daemon MCP catalog advertised unwired Stable RPCs:\n got: %v\nwant: %v", got, want)
	}
	for _, hidden := range []string{"agent_brief", "explain_symbol", "related_files", "change_risk"} {
		if c.SupportsCapability(hidden) {
			t.Errorf("daemon capability report claims unwired %q RPC", hidden)
		}
	}
}

func TestDaemon_MCPLabsToolsListDoesNotDialOrAutoStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("daemon transport and auto-start marker are Unix-only")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "auto-started")
	launcher := filepath.Join(dir, "launcher.sh")
	script := "#!/bin/sh\nprintf called > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(launcher, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := daemon.NewClient(filepath.Join(dir, "missing.sock"), launcher)
	srv := mcp.NewServerWithClient(c, mcp.WithLabs())

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(string(req)+"\n"), &out); err != nil {
		t.Fatalf("mcp.Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode tools/list: %v (%s)", err, out.Bytes())
	}
	if resp.Error != nil || len(resp.Result.Tools) == 0 {
		t.Fatalf("Labs tools/list failed: %s", out.Bytes())
	}
	for _, raw := range resp.Result.Tools {
		var tool struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Name == mcp.ToolSavings {
			t.Fatal("daemon Labs catalog advertised savings although the shipped daemon has no ledger wiring")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Labs tools/list attempted daemon auto-start; marker stat err=%v", err)
	}
}

func TestIsAlive(t *testing.T) {
	if daemon.IsAlive(filepath.Join(t.TempDir(), "nonexistent.sock")) {
		t.Fatal("IsAlive on a missing socket must be false")
	}
	_, sock := newDaemon(t)
	if !daemon.IsAlive(sock) {
		t.Fatalf("IsAlive on a running daemon (%s) must be true", sock)
	}
}

var b model.Node

func init() {
	b, _ = model.NewNode("function", "p.B", "p/b.go", 1, 1)
}
