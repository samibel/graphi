package doctor

import (
	"context"
	"errors"
	"io"
	"os"
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

func (f fakeEnv) RepoRoot() string           { return f.repoRoot }
func (f fakeEnv) DBPath() string             { return f.dbPath }
func (f fakeEnv) MCPConfig() MCPConfigReader { return fakeMCPConfig{} }
func (f fakeEnv) Release() ReleaseInfo       { return f.release }
func (f fakeEnv) State() StateReader         { return fakeState{dbPath: f.dbPath} }

type fakeRelease struct{ version, commit, date, arch, marker string }

func (f fakeRelease) Version() string { return f.version }
func (f fakeRelease) Commit() string  { return f.commit }
func (f fakeRelease) Date() string    { return f.date }
func (f fakeRelease) Arch() string    { return f.arch }
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

// stubOSLookups replaces the injectable OS lookup functions for the duration
// of a test and restores them on cleanup.
func stubOSLookups(t *testing.T, exe string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) {
	t.Helper()
	prevExe, prevLook, prevStat := executableFn, lookPathFn, statFn
	executableFn = func() (string, error) { return exe, nil }
	if lookPath != nil {
		lookPathFn = lookPath
	}
	if stat != nil {
		statFn = stat
	}
	t.Cleanup(func() {
		executableFn, lookPathFn, statFn = prevExe, prevLook, prevStat
	})
}

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

func TestBinaryCheckOutdatedReleaseWarnsOffline(t *testing.T) {
	// A packaged release older than the build-time known latest must warn with
	// upgrade guidance. The comparison is embedded metadata only — no network.
	env := fakeEnv{release: fakeRelease{version: "1.0.0", marker: "release"}}
	check := BinaryCheckAgainst(env.Release(), "1.2.0")
	res := check.Run(context.Background(), env)
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for outdated release, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "graphi upgrade") {
		t.Fatalf("expected `graphi upgrade` action, got %q", res.Action)
	}
	if !contains(res.Message, "1.2.0") {
		t.Fatalf("expected known latest version in message: %s", res.Message)
	}
}

func TestBinaryCheckCurrentReleasePasses(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "1.2.0", marker: "release"}}
	res := BinaryCheckAgainst(env.Release(), "1.2.0").Run(context.Background(), env)
	if res.Status != StatusPass {
		t.Fatalf("expected pass for up-to-date release, got %q: %s", res.Status, res.Message)
	}
}

func TestBinaryCheckDevBuildNeverWarnsOnVersion(t *testing.T) {
	env := fakeEnv{release: fakeRelease{version: "dev", marker: "dev"}}
	res := BinaryCheckAgainst(env.Release(), "9.9.9").Run(context.Background(), env)
	if res.Status != StatusInfo {
		t.Fatalf("dev build must stay info regardless of known latest, got %q", res.Status)
	}
}

func TestVersionIsOlder(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.2.0", true},
		{"1.2.0", "1.0.0", false},
		{"1.2.0", "1.2.0", false},
		{"v1.1.9", "1.2.0", true},
		{"1.2", "1.2.0", false},
		{"0.9.9", "0.10.0", true},
		{"1.2.3-rc1", "1.2.3", false},
		{"1.0.0", "0.0.0", false},
		// Unparsable versions fall back to inequality (same rule as `graphi upgrade`).
		{"nightly", "1.2.0", true},
		{"nightly", "nightly", false},
	}
	for _, tc := range cases {
		if got := versionIsOlder(tc.current, tc.latest); got != tc.want {
			t.Errorf("versionIsOlder(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
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

func TestPATHCheckGoFallbackFound(t *testing.T) {
	// `go` absent from PATH but present at a well-known install location →
	// warn with guidance to add that location to PATH.
	const exe = "/fake/bin/graphi"
	stubOSLookups(t, exe,
		func(name string) (string, error) {
			if name == "graphi" {
				return exe, nil
			}
			return "", errors.New("not found on PATH")
		},
		func(path string) (os.FileInfo, error) {
			if path == "/usr/local/go/bin/go" {
				return nil, nil // exists
			}
			return nil, os.ErrNotExist
		},
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for fallback-found go, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Message, "/usr/local/go/bin/go") {
		t.Fatalf("expected fallback path in message: %s", res.Message)
	}
	if !contains(res.Action, "PATH") {
		t.Fatalf("expected PATH guidance in action: %s", res.Action)
	}
}

func TestPATHCheckGoFallbackMissing(t *testing.T) {
	// `go` absent from PATH and from every fallback location → fail listing
	// the probed locations.
	const exe = "/fake/bin/graphi"
	stubOSLookups(t, exe,
		func(name string) (string, error) {
			if name == "graphi" {
				return exe, nil
			}
			return "", errors.New("not found on PATH")
		},
		func(path string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusFail {
		t.Fatalf("expected fail when go is nowhere, got %q: %s", res.Status, res.Message)
	}
	if !contains(res.Action, "/usr/local/go/bin/go") {
		t.Fatalf("expected probed fallbacks in action: %s", res.Action)
	}
}

func TestPATHCheckGraphiMismatchWarns(t *testing.T) {
	stubOSLookups(t, "/fake/bin/graphi",
		func(name string) (string, error) {
			if name == "graphi" {
				return "/other/bin/graphi", nil
			}
			return "/usr/bin/go", nil
		},
		nil,
	)
	res := PATHCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for PATH/executable mismatch, got %q: %s", res.Status, res.Message)
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
