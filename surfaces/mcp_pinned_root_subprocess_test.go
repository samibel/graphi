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

// TestSessionProfile_MCPExplicitRootJourney pins the explicit-root escape
// hatch end to end, in the exact shape that motivated it: an MCP client
// launches `graphi mcp` with cwd=$HOME (which carries a dotfiles-.git marker),
// advertises no roots capability and sends no rootUri. Without a pin the cwd
// walk lands on $HOME and guardAutoRoot fails the bind, so every tools/call
// returns -32002. With `-root <repo>` (or GRAPHI_ROOT; flag and env are
// separate subruns) the session must bind the pinned repository and answer a
// real search. The unpinned control keeps the refusal honest: it proves this
// staging actually reproduces the failure the pin is escaping.
func TestSessionProfile_MCPExplicitRootJourney(t *testing.T) {
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

	journey := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"

	// run launches the journey from a fresh fake $HOME carrying a repo marker
	// (the dotfiles-.git shape) with isolated per-run state, and returns the
	// responses by id. extraArgs/extraEnv select the pin channel per subrun.
	// The static journey races the asynchronous bind, so a tools/call that
	// lands as the documented retryable shape re-runs the journey against the
	// SAME home/state — the in-flight index converges (retryableUnbound).
	run := func(t *testing.T, extraArgs, extraEnv []string) map[int]rpcResponse {
		t.Helper()
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		for attempt := 1; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			mcp := exec.CommandContext(ctx, bin, append([]string{"mcp"}, extraArgs...)...)
			mcp.Dir = home // the failing shape: process cwd is the marker-carrying home
			mcp.Env = append(os.Environ(),
				"CGO_ENABLED=0",
				"HOME="+home,
				"USERPROFILE="+home,
				"XDG_STATE_HOME="+filepath.Join(home, "xdg-state"),
			)
			mcp.Env = append(mcp.Env, extraEnv...)
			mcp.Stdin = strings.NewReader(journey)
			var stdout, stderr bytes.Buffer
			mcp.Stdout = &stdout
			mcp.Stderr = &stderr
			err := mcp.Run()
			cancel()
			if err != nil {
				t.Fatalf("graphi mcp subprocess: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
			}
			byID := decodeByID(t, stdout.Bytes())
			if attempt < 5 && retryableUnbound(byID[3]) {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
				continue
			}
			return byID
		}
	}

	// requireBound asserts the pinned session answered the search over the
	// real repository (non-empty matches citing the fixture symbol).
	requireBound := func(t *testing.T, byID map[int]rpcResponse) {
		t.Helper()
		if resp := byID[1]; resp.Error != nil || len(resp.Result) == 0 {
			t.Fatalf("initialize: missing/error result: %+v", resp)
		}
		callResp, ok := byID[3]
		if !ok || callResp.Error != nil {
			t.Fatalf("tools/call with a pinned root: %+v", callResp)
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
			Matches []struct {
				QualifiedName string `json:"qualified_name"`
			} `json:"matches"`
		}
		if err := json.Unmarshal([]byte(call.Content[0].Text), &search); err != nil || len(search.Matches) == 0 {
			t.Fatalf("pinned-root search returned no real matches: err=%v text=%s", err, call.Content[0].Text)
		}
		found := false
		for _, m := range search.Matches {
			if strings.Contains(m.QualifiedName, "Hello") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("pinned-root search matches do not cite the fixture symbol: %s", call.Content[0].Text)
		}
	}

	t.Run("flag", func(t *testing.T) {
		requireBound(t, run(t, []string{"-root", repo}, nil))
	})

	t.Run("env", func(t *testing.T) {
		requireBound(t, run(t, nil, []string{"GRAPHI_ROOT=" + repo}))
	})

	t.Run("unpinned_control", func(t *testing.T) {
		byID := run(t, nil, nil)
		callResp, ok := byID[3]
		if !ok || callResp.Error == nil {
			t.Fatalf("unpinned tools/call from $HOME must fail, got: %+v", callResp)
		}
		var rpcErr struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(callResp.Error, &rpcErr); err != nil {
			t.Fatalf("decode error envelope: %v (%s)", err, callResp.Error)
		}
		if rpcErr.Code != -32002 || !strings.Contains(rpcErr.Message, "no repository could be bound") {
			t.Fatalf("unpinned failure = %d %q, want -32002 with the auto-bind refusal", rpcErr.Code, rpcErr.Message)
		}
	})
}
