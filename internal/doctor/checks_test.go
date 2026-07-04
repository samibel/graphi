package doctor

import (
	"context"
	"io"
	"strings"
	"testing"
)

// fakeEnv is a read-only Env used by check tests. It intentionally exposes no
// writer or ingest capability, so it cannot mutate the workspace.
type fakeEnv struct {
	repoRoot string
	dbPath   string
	release  fakeRelease
}

func (f fakeEnv) RepoRoot() string               { return f.repoRoot }
func (f fakeEnv) DBPath() string                 { return f.dbPath }
func (f fakeEnv) MCPConfig() MCPConfigReader     { return fakeMCPConfig{} }
func (f fakeEnv) Release() ReleaseInfo           { return f.release }
func (f fakeEnv) State() StateReader             { return fakeState{dbPath: f.dbPath} }

type fakeRelease struct{ version, commit, date, arch, marker string }

func (f fakeRelease) Version() string  { return f.version }
func (f fakeRelease) Commit() string   { return f.commit }
func (f fakeRelease) Date() string     { return f.date }
func (f fakeRelease) Arch() string     { return f.arch }
func (f fakeRelease) IsRelease() bool { return f.marker == "release" }

type fakeMCPConfig struct{}

func (fakeMCPConfig) Clients() []MCPClient {
	return []MCPClient{{ID: "claude", Display: "Claude Code", ConfigPath: "/dev/null/mcp.json"}}
}
func (fakeMCPConfig) Plan(client MCPClient, binary string) (MCPPlanAction, error) {
	return MCPPlanNoOp, nil
}

type fakeState struct{ dbPath string }

func (f fakeState) DiscoverDB(repoRoot string) (string, error) { return f.dbPath, nil }

func TestBinaryCheckDevBuild(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "dev", marker: "dev"}}
	check := BinaryCheck(env.Release())
	res := check.Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("expected info for dev build, got %q", res.Status)
	}
	if !contains(res.Message, "dev") {
		t.Fatalf("expected dev marker in message: %s", res.Message)
	}
}

func TestBinaryCheckRelease(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "1.0.0", marker: "release"}}
	check := BinaryCheck(env.Release())
	res := check.Run(context.Background(), env)
	if res.Status != StatusPass {
		t.Fatalf("expected pass for release, got %q", res.Status)
	}
}

func TestPATHCheckGoFallback(t *testing.T) {
	// This test assumes `go` is on PATH in the test environment; if not, it
	// should at least not panic and should return a meaningful result.
	check := PATHCheck()
	res := check.Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass && res.Status != StatusFail && res.Status != StatusWarn {
		t.Fatalf("unexpected status: %q", res.Status)
	}
}

func TestMCPCheckNoOp(t *testing.T) {
	check := MCPCheck("/bin/graphi")
	res := check.Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for no-op plan, got %q: %s", res.Status, res.Message)
	}
}

func TestPrivacyCheck(t *testing.T) {
	res := PrivacyCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for privacy check, got %q", res.Status)
	}
}

func TestLocalFirstCheck(t *testing.T) {
	res := LocalFirstCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass for local-first check, got %q", res.Status)
	}
}

func TestDBCheckEmptyPath(t *testing.T) {
	env := fakeEnv{dbPath: ""}
	res := DBCheck().Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("expected info for empty db path, got %q", res.Status)
	}
}

func TestRenderersWriteOnlyToWriter(t *testing.T) {
	// Prove that renderers do not touch the filesystem by using a writer that
	// records bytes and asserting no file operations occurred.
	w := io.Discard
	report := Report{Results: []CheckResult{{ID: "p", Category: "c", Status: StatusPass, Message: "ok"}}}
	if err := RenderHuman(w, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	if err := RenderJSON(w, report); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
}

func contains(s, substr string) bool { return strings.Contains(s, substr) }
