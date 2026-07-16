package surfaces_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/state"
)

// TestSessionProfile_MCPRepositoryJourney is the standing G3 gate for the
// Session/Profile contract of ADR 0002: a FRESH setup with NO pre-indexing and
// NO explicit `-db` must still answer a real `tools/call` over the user's repo,
// because the server resolves the repo from the MCP client's roots (even when
// the process cwd is outside that repo), performs the initial ingest, signals
// readiness, and then serves the 11 MCP-applicable stable operations (the
// frozen 12 minus lifecycle-only index) against the REAL indexed graph.
//
// History: authored by SW-113 (SP-10) as a red-now / green-later journey and
// DELIBERATELY skipped, because the pre-RUN-01 zero-config path bound an EMPTY
// in-memory graph. GREEN since SW-121 (RUN-01): cmd/internal/runtime.Runtime
// implements the ADR 0002 session — protocol-root resolution, per-repo state,
// open→recover→ingest→ready (sync-before-serve), single owned Close — and the
// skip was removed with this journey passing unchanged.
//
// Distinct from TestCharacterization_MCPStdioJourney_Subprocess (SW-110), which
// pins the explicit path where the caller pre-indexes and passes `-db`. This
// one asserts the zero-config setup→initialize→list→call contract.
func TestSessionProfile_MCPRepositoryJourney(t *testing.T) {
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

	// 3. Drive the zero-config journey: `graphi mcp` (NO -db), launched OUTSIDE
	//    the repo. initialize.rootUri is therefore the only valid binding input;
	//    falling back to process cwd would fail or recreate the empty-graph bug.
	//    Then list and call the stable search op over the bound repository.
	journey := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{}},"rootUri":"file://` + repo + `"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	mcp := exec.CommandContext(ctx, bin, "mcp") // NO -db, NO -daemon: contract must self-resolve.
	mcp.Dir = work                              // process cwd deliberately has no repository marker.
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

	// tools/list → the MCP-applicable stable operations, including the frozen
	// "search" tool (index remains a CLI lifecycle operation, not an MCP tool).
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

// TestSessionProfile_MCPRootsListJourney exercises the current MCP roots
// lifecycle rather than the legacy initialize.rootUri compatibility field.
// The process starts outside the repository, advertises roots capability,
// receives graphi's roots/list request, answers it, and only then calls a tool.
func TestSessionProfile_MCPRootsListJourney(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root := moduleRoot(t)
	work := t.TempDir()
	bin := filepath.Join(work, "graphi")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}

	repo := filepath.Join(work, "repo")
	if err := copyTree(filepath.Join(root, "corpus", "fixtures", "go"), repo); err != nil {
		t.Fatalf("stage fixture repo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mark repo root: %v", err)
	}
	stateHome := filepath.Join(work, "xdg-state")
	journey := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"graphi-roots-1","result":{"roots":[{"uri":"file://` + repo + `","name":"fixture"}]}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "XDG_STATE_HOME="+stateHome)
	cmd.Stdin = strings.NewReader(journey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("graphi mcp roots/list subprocess: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"method":"roots/list"`)) {
		t.Fatalf("MCP server did not request roots/list:\n%s", stdout.String())
	}
	byID := decodeByID(t, stdout.Bytes())
	if response := byID[1]; response.Error != nil || len(response.Result) == 0 {
		t.Fatalf("initialize failed: %+v (stderr: %s)", response, stderr.String())
	}
	if response := byID[2]; response.Error != nil || len(response.Result) == 0 {
		t.Fatalf("tools/list after roots binding failed: %+v", response)
	}
	callResp := byID[3]
	if callResp.Error != nil {
		t.Fatalf("tools/call after roots binding failed: %+v", callResp)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(callResp.Result, &call); err != nil || len(call.Content) == 0 {
		t.Fatalf("decode tools/call: err=%v result=%s", err, callResp.Result)
	}
	var search struct {
		Matches []json.RawMessage `json:"matches"`
	}
	if err := json.Unmarshal([]byte(call.Content[0].Text), &search); err != nil || len(search.Matches) == 0 {
		t.Fatalf("roots/list-bound search returned no real matches: err=%v text=%s", err, call.Content[0].Text)
	}
}

// TestSessionProfile_CLIZeroConfigAgentToolParity proves the agent-oriented CLI
// verbs discover exactly the same per-repo durable state as search/query. Each
// zero-flag invocation is compared byte-for-byte with an explicit -db call;
// previously the four agent paths silently constructed empty MemStores.
func TestSessionProfile_CLIZeroConfigAgentToolParity(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root := moduleRoot(t)
	work := t.TempDir()
	bin := filepath.Join(work, "graphi")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}

	repo := filepath.Join(work, "repo")
	if err := copyTree(filepath.Join(root, "corpus", "fixtures", "go"), repo); err != nil {
		t.Fatalf("stage fixture repo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mark repo root: %v", err)
	}
	// macOS temp roots have a /var → /private/var alias. Use the physical path
	// for both state fingerprinting and subprocess cwd so this test targets CLI
	// discovery rather than path-alias compatibility.
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("canonicalize repo: %v", err)
	}
	repo = canonicalRepo
	stateHome := filepath.Join(work, "xdg-state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	paths, err := state.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	index := exec.Command(bin, "index", "-root", repo, "-db", paths.DB, "-meta", paths.Meta)
	index.Env = append(os.Environ(), "CGO_ENABLED=0", "XDG_STATE_HOME="+stateHome)
	if out, err := index.CombinedOutput(); err != nil {
		t.Fatalf("index per-repo state: %v\n%s", err, out)
	}

	run := func(args []string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "XDG_STATE_HOME="+stateHome)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("graphi %v: %v\nstderr: %s\nstdout: %s", args, err, stderr.String(), stdout.String())
		}
		return stdout.Bytes()
	}

	cases := []struct {
		name string
		args []string
	}{
		{name: "search", args: []string{"search", "Hello"}},
		{name: "agent-brief", args: []string{"agent-brief", "-topic", "Hello"}},
		{name: "explain-symbol", args: []string{"explain-symbol", "Hello"}},
		{name: "related-files", args: []string{"related-files", "Hello"}},
		{name: "change-risk", args: []string{"change-risk", "Hello"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			implicit := run(tc.args)
			explicitArgs := append(append([]string(nil), tc.args...), "-db", paths.DB)
			explicit := run(explicitArgs)
			if !bytes.Equal(implicit, explicit) {
				t.Fatalf("zero-config output differs from explicit per-repo state:\nimplicit: %s\nexplicit: %s", implicit, explicit)
			}
			if tc.name == "search" && !bytes.Contains(implicit, []byte(`"qualified_name":"fixture.Hello"`)) {
				t.Fatalf("zero-config search did not return the indexed fixture: %s", implicit)
			}
			if tc.name != "search" && (bytes.Contains(implicit, []byte(`"method":"unresolved"`)) || bytes.Contains(implicit, []byte("graph of 0 symbols"))) {
				t.Fatalf("zero-config agent command used an empty graph: %s", implicit)
			}
		})
	}
}

// TestSessionProfile_MCPSignalGracefulShutdown keeps stdin deliberately open,
// sends SIGTERM after a repository-bound initialize, and requires a clean exit.
// This proves signal cancellation—not EOF—unblocks Serve and reaches the
// deferred Server/Runtime Close path; the resulting SQLite state must reopen.
func TestSessionProfile_MCPSignalGracefulShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM subprocess contract is POSIX-only")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root := moduleRoot(t)
	work := t.TempDir()
	bin := filepath.Join(work, "graphi")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}

	repo := filepath.Join(work, "repo")
	if err := copyTree(filepath.Join(root, "corpus", "fixtures", "go"), repo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	stateHome := filepath.Join(work, "xdg-state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	cmd := exec.Command(bin, "mcp")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "XDG_STATE_HOME="+stateHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	}()

	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		var response rpcResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil || response.Error != nil || len(response.Result) == 0 {
			t.Fatalf("initialize response: err=%v response=%+v line=%s", err, response, line)
		}
	case err := <-errCh:
		t.Fatalf("read initialize response: %v (stderr: %s)", err, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("initialize timed out (stderr: %s)", stderr.String())
	}

	// Keep stdin OPEN: without signal-aware cancellation Scanner.Scan remains
	// blocked and this process never exits.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Fatalf("MCP SIGTERM exit: %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("MCP did not exit after SIGTERM; Serve likely remained blocked (stderr: %s)", stderr.String())
	}

	paths, err := state.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := graphstore.OpenSQLite(paths.DB)
	if err != nil {
		t.Fatalf("reopen Runtime store after SIGTERM: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
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
