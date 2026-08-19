package doctor

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
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

// TestKnownDefectsCheck pins the disclosure contract (D8): an OPEN published
// defect that affects a GA operation is named on the doctor surface at INFO
// severity — never silently green, never a health failure of this install.
//
// Restored for LINK-002 after ADR 0011 closed LINK-001 and removed the check for
// the third time, and extended to LINK-003 in review round 1 of the same story.
// The assertions are deliberately about the SHAPE of a disclosure rather than its
// prose, so the message can be improved without breaking the test, but cannot
// lose the parts that make it useful: the defect id, the affected operations, and
// a workaround.
//
// One assertion is about CONTENT rather than shape, and deliberately so. The
// first draft of the LINK-002 disclosure told users the defect "never emits a
// wrong one", which was false — it also REDIRECTS calls to the wrong declaration
// (docs/rc/link-002-clause-by-dir-recall.md §3.2). A user who reads "incomplete"
// and acts on it is misled differently from one who reads "possibly wrong", so
// the negative assertion below pins that the retraction cannot silently come back.
func TestKnownDefectsCheck(t *testing.T) {
	res := KnownDefectsCheck().Run(context.Background(), fakeEnv{})
	if res.Status != StatusInfo {
		t.Fatalf("known-defects must be INFO (an open defect is disclosed, not a local "+
			"health failure, and never silently green), got %q", res.Status)
	}
	for _, want := range []string{"LINK-002", "LINK-003", "callers", "callees", "Workaround"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects message must mention %q; got: %s", want, res.Message)
		}
	}
	// The soundness half must be disclosed, not just the recall half.
	for _, want := range []string{"REDIRECTED", "WRONG EDGES"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("known-defects must disclose that LINK-002 emits WRONG edges and not "+
				"only that it drops true ones (missing %q). See "+
				"docs/rc/link-002-clause-by-dir-recall.md §3.2; got: %s", want, res.Message)
		}
	}
	if strings.Contains(res.Message, "never emits a wrong one") ||
		strings.Contains(res.Message, "drops true edges only") {
		t.Errorf("known-defects has regressed to the FALSE claim that LINK-002 only drops " +
			"true edges. It also redirects them — reproduced through the CLI in " +
			"docs/rc/link-002-clause-by-dir-recall.md §3.2, and pinned by " +
			"engine/link/clausebydir_test.go::TestLink002_RedirectsToWrongDeclaration.")
	}
	// The `-profile full` incident: a published workaround named a profile the
	// CLI rejects. Pin that this disclosure names only real profiles.
	if strings.Contains(res.Message, "-profile full") {
		t.Errorf("known-defects names a profile the CLI rejects; the accepted set is " +
			"fast|balanced|deep")
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

// fakeContendingMCPConfig implements both MCPConfigReader and the optional
// MCPContentionReader, reporting two zero-config graphi entries.
type fakeContendingMCPConfig struct{ fakeMCPConfig }

func (fakeContendingMCPConfig) Contending(client MCPClient) ([]string, error) {
	return []string{"graphi", "graphi-mars"}, nil
}

type fakeContendingEnv struct{ fakeEnv }

func (fakeContendingEnv) MCPConfig() MCPConfigReader { return fakeContendingMCPConfig{} }

// TestMCPCheckWarnsOnContendingEntries pins the duplicate-entry warning: two
// zero-config graphi entries in one client config downgrade the mcp check to
// warn, name both entries, and the action says keep-one-or-pin. Readers
// without the optional interface (fakeMCPConfig, pinned by TestMCPCheckNoOp)
// stay unaffected.
func TestMCPCheckWarnsOnContendingEntries(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeContendingEnv{})
	if res.Status != StatusWarn {
		t.Fatalf("expected warn for contending entries, got %q: %s", res.Status, res.Message)
	}
	for _, want := range []string{"graphi, graphi-mars", "contend on its ingest lock"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("message missing %q: %s", want, res.Message)
		}
	}
	if !strings.Contains(res.Action, "keep one zero-config graphi entry") {
		t.Fatalf("action not actionable: %s", res.Action)
	}
}

// fakeMixedMCPConfig reports four clients whose plans exercise every per-client
// finding the aggregate mcp check can produce: registered-and-current,
// not-registered, stale-command, and cannot-read-config.
type fakeMixedMCPConfig struct{}

func (fakeMixedMCPConfig) Clients() []MCPClient {
	// Deliberately NOT in sorted display order, so a test asserting sorted
	// detail lines is actually asserting the sort and not the input order.
	return []MCPClient{
		{ID: "zed", Display: "Zed", ConfigPath: "/dev/null/zed.json"},
		{ID: "cursor", Display: "Cursor", ConfigPath: "/dev/null/cursor.json"},
		{ID: "claude", Display: "Claude Code", ConfigPath: "/dev/null/claude.json"},
		{ID: "vscode", Display: "VS Code", ConfigPath: "/dev/null/vscode.json"},
	}
}

func (fakeMixedMCPConfig) Plan(client MCPClient, binary string) (MCPPlanAction, error) {
	switch client.ID {
	case "claude":
		return MCPPlanNoOp, nil
	case "cursor":
		return MCPPlanUpdate, nil
	case "vscode":
		return MCPPlanCreate, nil
	default:
		return "", errors.New("unexpected end of JSON input")
	}
}

type fakeMixedEnv struct{ fakeEnv }

func (fakeMixedEnv) MCPConfig() MCPConfigReader { return fakeMixedMCPConfig{} }

// TestMCPCheckDetailAttributesEveryClient pins SW-159 AC1/AC2: the per-client
// lines the aggregate check computes are surfaced in CheckResult.Detail, one
// client per line, sorted, each naming the client and its specific finding.
func TestMCPCheckDetailAttributesEveryClient(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeMixedEnv{})
	if res.Status != StatusFail {
		t.Fatalf("expected fail (one client is not registered), got %q: %s", res.Status, res.Message)
	}
	if res.Detail == "" {
		t.Fatal("detail is empty: the per-client findings were discarded again")
	}
	lines := strings.Split(res.Detail, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected one line per client (4), got %d: %q", len(lines), res.Detail)
	}
	want := []string{
		"Claude Code: registered and current",
		"Cursor: stale command path or args",
		"VS Code: not registered",
		"Zed: cannot read config: unexpected end of JSON input",
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("detail line %d: got %q, want %q (full detail:\n%s)", i, lines[i], w, res.Detail)
		}
	}
	if !sort.StringsAreSorted(lines) {
		t.Fatalf("detail lines are not in sorted order: %q", lines)
	}
}

// TestMCPCheckDetailDoesNotChangeAggregate pins SW-159 AC7: adding Detail
// leaves the check's id, category, status derivation, message and action
// exactly as they were.
func TestMCPCheckDetailDoesNotChangeAggregate(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeMixedEnv{})
	if res.ID != "mcp" || res.Category != "mcp" {
		t.Fatalf("id/category changed: %q/%q", res.ID, res.Category)
	}
	if res.Message != "one or more MCP clients need attention" {
		t.Fatalf("aggregate message changed: %q", res.Message)
	}
	if res.Action != "re-run `graphi setup` to update registrations" {
		t.Fatalf("aggregate action changed: %q", res.Action)
	}
}

// TestMCPCheckDetailReportsContentionPerClient pins SW-159 AC3: contention is
// attributed to the client it was found in inside Detail, in addition to the
// existing aggregate message (which keeps carrying it).
func TestMCPCheckDetailReportsContentionPerClient(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeContendingEnv{})
	wantDetail := "Claude Code: registered and current\n" +
		"Claude Code: 2 zero-config graphi entries (graphi, graphi-mars) will resolve the same repository and contend on its ingest lock"
	if res.Detail != wantDetail {
		t.Fatalf("detail:\ngot:\n%s\nwant:\n%s", res.Detail, wantDetail)
	}
	// AC3 says "in addition to" — the aggregate message must still carry it.
	if !strings.Contains(res.Message, "contend on its ingest lock") {
		t.Fatalf("aggregate message lost its contention text: %q", res.Message)
	}
}

// TestMCPCheckDetailEmptyWhenAllPass pins SW-159 AC5: an all-pass run leaves
// Detail empty so `json:"detail,omitempty"` omits it entirely.
func TestMCPCheckDetailEmptyWhenAllPass(t *testing.T) {
	res := MCPCheck("/bin/graphi").Run(context.Background(), fakeEnv{})
	if res.Status != StatusPass {
		t.Fatalf("expected pass, got %q: %s", res.Status, res.Message)
	}
	if res.Detail != "" {
		t.Fatalf("all clients pass, detail must be empty, got %q", res.Detail)
	}
}
