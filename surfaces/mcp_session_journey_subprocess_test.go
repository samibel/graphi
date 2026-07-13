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

// TestSessionProfile_MCPRepositoryJourney_RedUntilRUN01 is the SW-113 (SP-10)
// red-now / green-later journey. It encodes the Session/Profile contract of
// ADR 0002 as an executable target: a FRESH setup with NO pre-indexing and NO
// explicit `-db` must still answer a real `tools/call` over the user's repo,
// because the server resolves the repo from the MCP client's roots / the process
// cwd, performs the initial ingest, signals readiness, and then serves the 12
// stable operations against the REAL indexed graph.
//
// It is DELIBERATELY SKIPPED ("red until RUN-01"): on `main` today `graphi setup`
// writes a bare `["mcp"]` command (internal/mcpconfig GraphiEntry) and `runMCP`
// never calls resolveSession, so the zero-config path binds an EMPTY in-memory
// graph — the very gap ADR 0002 specifies and RUN-01 implements. Keeping this
// test skipped (rather than failing) keeps `go test ./...` and the testgate
// allowlist GREEN while still checking the target contract into the tree as a
// reviewed diff. When RUN-01 wires the composition root, delete the t.Skip line
// and this journey must pass unchanged.
//
// Distinct from TestCharacterization_MCPStdioJourney_Subprocess (SW-110), which
// pins TODAY's honest path where the caller pre-indexes and passes `-db`. This
// one asserts the NOT-YET-BUILT zero-config setup→initialize→list→call contract.
func TestSessionProfile_MCPRepositoryJourney_RedUntilRUN01(t *testing.T) {
	// --- red until RUN-01 ---------------------------------------------------
	// Remove this single line once RUN-01 has wired cmd/internal/runtime.Runtime
	// per ADR 0002 (repo resolution from roots/cwd + initial ingest + readiness).
	t.Skip("red until RUN-01: zero-config MCP session/repo resolution (ADR 0002) is not implemented yet; see docs/adr/0002-session-profile-contract.md")
	// ------------------------------------------------------------------------

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

	// 2. Stage a FRESH repo copy as the working tree an agent would point at, and
	//    an isolated per-user state home so the run touches no real ~/.graphi.
	//    Crucially we DO NOT index it and DO NOT pass `-db`: the contract requires
	//    the session to resolve + ingest the repo itself.
	repo := filepath.Join(work, "repo")
	fixture := filepath.Join(root, "corpus", "fixtures", "go")
	if err := copyTree(fixture, repo); err != nil {
		t.Fatalf("stage fixture repo: %v", err)
	}
	// Make it look like a repository root (ADR 0002 D1: .git marker wins the walk).
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mark repo root: %v", err)
	}
	stateHome := filepath.Join(work, "xdg-state")

	// 3. Drive the zero-config journey: `graphi mcp` (NO -db), launched with the
	//    repo as the process cwd (ADR 0002 D4 fallback: cwd → DetectRepo). initialize
	//    then tools/list then a real tools/call on the stable `search` op.
	journey := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{}},"rootUri":"file://` + repo + `"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	mcp := exec.CommandContext(ctx, bin, "mcp") // NO -db, NO -daemon: contract must self-resolve.
	mcp.Dir = repo                              // process cwd IS the repo (D4 fallback).
	mcp.Env = append(os.Environ(), "CGO_ENABLED=0", "XDG_STATE_HOME="+stateHome)
	mcp.Stdin = strings.NewReader(journey)
	var stdout, stderr bytes.Buffer
	mcp.Stdout = &stdout
	mcp.Stderr = &stderr
	if err := mcp.Run(); err != nil {
		t.Fatalf("graphi mcp subprocess: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}

	byID := decodeByID(t, stdout.Bytes())

	// initialize → a result envelope; under ADR 0002 D3 (sync-before-serve) a
	// successful handshake already means the repo is resolved + ingested + ready.
	initResp, ok := byID[1]
	if !ok || initResp.Error != nil || len(initResp.Result) == 0 {
		t.Fatalf("initialize: missing/error result: %+v (stderr: %s)", initResp, stderr.String())
	}

	// tools/list → the 12 stable operations, including the frozen "search" tool.
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
	if !hasTool(list.Tools, "search") {
		t.Fatalf("tools/list missing the stable 'search' tool; got %d tools", len(list.Tools))
	}

	// tools/call(search "Hello") → text content citing the fixture symbol, proving
	// the session resolved + ingested the repo WITHOUT any pre-indexing or -db, and
	// queried the REAL graph end-to-end (the crux of ADR 0002 D2/D3/D4).
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
	// The text payload is the canonical engine/search.Response JSON
	// ({"query":...,"matches":[...]}). A raw substring check for "Hello" over the
	// whole blob is a placebo oracle: engine/search.Response echoes the query
	// verbatim in the "query" field, so "Hello" appears even when matches is EMPTY
	// — i.e. against today's empty-in-memory graph (the very bug ADR 0002 fixes).
	// A faithful oracle must prove the repo was really resolved + ingested +
	// queried, so decode the payload and require a NON-EMPTY matches array with a
	// match whose qualified name cites the fixture symbol.
	var search struct {
		Query   string `json:"query"`
		Matches []struct {
			QualifiedName string `json:"qualified_name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(call.Content[0].Text), &search); err != nil {
		t.Fatalf("tools/call search-response decode: %v (%s)", err, call.Content[0].Text)
	}
	if len(search.Matches) == 0 {
		t.Fatalf("tools/call(search Hello) returned zero matches — the repo was not ingested/queried (empty-graph bug); got: %s", call.Content[0].Text)
	}
	found := false
	for _, m := range search.Matches {
		if strings.Contains(m.QualifiedName, "Hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tools/call(search Hello) returned matches but none cite the fixture 'Hello' symbol; got: %s", call.Content[0].Text)
	}
}

// copyTree recursively copies the regular files and directories under src into
// dst. It is intentionally minimal (the fixture is small, files only) and exists
// so the journey can stage a pristine, un-indexed working tree per run.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if merr := os.MkdirAll(filepath.Dir(target), 0o755); merr != nil {
			return merr
		}
		return os.WriteFile(target, data, 0o644)
	})
}
