package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/surfaces/client"
)

func TestRunTrustReport_NotARepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out bytes.Buffer
	if code := runTrustReportAt(t.TempDir(), nil, &out); code != 2 {
		t.Fatalf("trust-report outside a repo exit = %d, want 2", code)
	}
}

func TestRunTrustReport_UnindexedRepo_FailClosed(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	// JSON: a valid UNAVAILABLE document, exit 4 — never healthy.
	var out bytes.Buffer
	code := runTrustReportAt(repo, []string{"--json"}, &out)
	if code != 4 {
		t.Fatalf("trust-report of an unindexed repo exit = %d, want 4 (output: %s)", code, out.String())
	}
	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		SnapshotState string `json:"snapshot_state"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("--json output is not a JSON document: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 || doc.SnapshotState != string(trust.StateUnavailable) {
		t.Fatalf("document = schema %d state %q, want schema 1 state UNAVAILABLE", doc.SchemaVersion, doc.SnapshotState)
	}

	// Human: the state is unmissable.
	out.Reset()
	if code := runTrustReportAt(repo, nil, &out); code != 4 {
		t.Fatalf("human trust-report exit = %d, want 4", code)
	}
	if !strings.Contains(out.String(), "UNAVAILABLE") {
		t.Fatalf("human output hides the UNAVAILABLE state:\n%s", out.String())
	}

	// A pure observer must not create the per-repo state dir.
	if matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*")); len(matches) != 0 {
		t.Fatalf("trust-report created state entries: %v", matches)
	}
}

func TestRunTrustReport_CurrentAfterSync(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	var out bytes.Buffer
	if code := runTrustReportAt(repo, nil, &out); code != 0 {
		t.Fatalf("trust-report after sync exit = %d, want 0 (output: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "CURRENT") {
		t.Fatalf("human output missing CURRENT state:\n%s", out.String())
	}

	// Determinism: two --json runs are byte-identical.
	var a, b bytes.Buffer
	if code := runTrustReportAt(repo, []string{"--json", "--details", "--limit", "3"}, &a); code != 0 {
		t.Fatalf("first --json run exit = %d", code)
	}
	if code := runTrustReportAt(repo, []string{"--json", "--details", "--limit", "3"}, &b); code != 0 {
		t.Fatalf("second --json run exit = %d", code)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("--json output is not deterministic:\n%s\nvs\n%s", a.String(), b.String())
	}
}

func TestRunTrustReport_UnknownPolicy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	var out bytes.Buffer
	if code := runTrustReportAt(repo, []string{"--policy", "yolo"}, &out); code != 2 {
		t.Fatalf("unknown policy exit = %d, want 2 (operational)", code)
	}
}

// TestRunTrustReport_PolicyExitMatchesDocument pins the seam between the wire
// document and the exit code: whatever verdict the canonical document carries,
// the exit code must be its documented mapping — the CLI can never report a
// friendlier code than the document justifies.
func TestRunTrustReport_PolicyExitMatchesDocument(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	for _, policy := range []string{"exploratory", "review", "automated_change"} {
		var out bytes.Buffer
		code := runTrustReportAt(repo, []string{"--json", "--policy", policy}, &out)
		var doc struct {
			Policy struct {
				Verdict string `json:"verdict"`
			} `json:"policy"`
		}
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatalf("[%s] bad document: %v", policy, err)
		}
		want := trustExitCode(true, trust.Verdict(doc.Policy.Verdict), trust.StateCurrent)
		if code != want {
			t.Fatalf("[%s] exit = %d, but document verdict %q maps to %d", policy, code, doc.Policy.Verdict, want)
		}
	}
}

// TestRunTrustReport_LimitRejectsGarbage pins the --limit input boundary:
// values fmt.Sscanf used to half-parse ("3abc" → 3, "0x10" → 0 = uncapped,
// "1e3" → 1) must exit 2 with NO document on stdout — a malformed limit is an
// input error, never a silently different evidence bound.
func TestRunTrustReport_LimitRejectsGarbage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	for _, bad := range []string{"3abc", "0x10", "1e3", "", " 3", "-1", "two"} {
		var out bytes.Buffer
		code := runTrustReportAt(repo, []string{"--json", "--limit", bad}, &out)
		if code != 2 {
			t.Errorf("--limit %q exit = %d, want 2", bad, code)
		}
		if out.Len() != 0 {
			t.Errorf("--limit %q wrote a document before failing:\n%s", bad, out.String())
		}
	}
}

// TestRunTrustReport_FlagValueMissing pins the trailing-flag boundary: a flag
// given without its value is an input error (exit 2), never a silent drop —
// dropping "--policy" would launder the requested policy gate into the
// friendlier no-policy exit code.
func TestRunTrustReport_FlagValueMissing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	for _, args := range [][]string{
		{"--policy"}, {"--target"}, {"--limit"},
		{"-policy"}, {"-target"}, {"-limit"},
		{"--json", "--policy"},
	} {
		var out bytes.Buffer
		code := runTrustReportAt(repo, args, &out)
		if code != 2 {
			t.Errorf("%v exit = %d, want 2 (flag without value must fail closed)", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote output before failing:\n%s", args, out.String())
		}
	}
}

// TestRunTrustReport_SingleDashFlagsRecognized pins the single-dash space form
// ("-policy review", "-target x", "-limit 3"): a broken alias used to drop
// these silently into the ingest-flag remainder, composing a repository-scope,
// no-policy report the user never asked for (scope/policy laundering).
func TestRunTrustReport_SingleDashFlagsRecognized(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	var out bytes.Buffer
	code := runTrustReportAt(repo, []string{"-json", "-policy", "review", "-target", "no_such_symbol", "-limit", "3"}, &out)
	var doc struct {
		Scope struct {
			Kind string `json:"kind"`
		} `json:"scope"`
		Policy struct {
			Name string `json:"name"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("bad document: %v\n%s", err, out.String())
	}
	if doc.Policy.Name != "review" {
		t.Errorf("policy.name = %q, want %q (-policy space form was dropped)", doc.Policy.Name, "review")
	}
	if doc.Scope.Kind != "symbol" {
		t.Errorf("scope.kind = %q, want %q (-target space form was dropped)", doc.Scope.Kind, "symbol")
	}
	// Unindexed repo + review policy: verdict UNKNOWN maps to exit 4, and the
	// laundered variant (policy dropped, state UNAVAILABLE) also exits 4 — the
	// document assertions above are the discriminating pin; the exit code
	// simply must not be friendlier.
	if code != 4 {
		t.Errorf("exit = %d, want 4", code)
	}
}

// TestRunTrustReport_HumanAndJSONExitCodesAgree pins that the rendering flag
// can never change the verdict: for identical inputs the human path and the
// --json path exit with the same code.
func TestRunTrustReport_HumanAndJSONExitCodesAgree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	for _, args := range [][]string{
		nil,
		{"--policy", "review"},
		{"--policy", "automated_change", "--target", "no_such_symbol"},
	} {
		human := runTrustReportAt(repo, args, new(bytes.Buffer))
		jsonCode := runTrustReportAt(repo, append([]string{"--json"}, args...), new(bytes.Buffer))
		if human != jsonCode {
			t.Errorf("%v: human exit %d != --json exit %d", args, human, jsonCode)
		}
	}
}

// TestRunTrustReport_UnknownPolicyFailsBeforeOutput pins the §4 CLI error
// mapping: an unknown --policy is exit 2 with an error line on stderr, no
// document on stdout (in either rendering), and no stack trace.
func TestRunTrustReport_UnknownPolicyFailsBeforeOutput(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var human, asJSON bytes.Buffer
	humanCode := runTrustReportAt(repo, []string{"--policy", "yolo"}, &human)
	jsonCode := runTrustReportAt(repo, []string{"--json", "--policy", "yolo"}, &asJSON)
	os.Stderr = origStderr
	_ = w.Close()
	stderrBytes, _ := io.ReadAll(r)
	_ = r.Close()

	if humanCode != 2 || jsonCode != 2 {
		t.Fatalf("unknown policy exits = (%d, %d), want (2, 2)", humanCode, jsonCode)
	}
	if human.Len() != 0 || asJSON.Len() != 0 {
		t.Errorf("unknown policy wrote to stdout before failing:\nhuman: %s\njson: %s", human.String(), asJSON.String())
	}
	stderr := string(stderrBytes)
	if !strings.Contains(stderr, "trust-report") || !strings.Contains(stderr, "yolo") {
		t.Errorf("stderr misses the clear error line: %q", stderr)
	}
	if strings.Contains(stderr, "goroutine ") || strings.Contains(stderr, ".go:") {
		t.Errorf("stderr looks like a stack trace: %q", stderr)
	}
}

// TestRunTrustReport_JSONMatchesSharedCompositionBytes is the CLI half of the
// executable CLI↔MCP parity pin (the MCP half is
// TestGraphHealth_ParityWithSharedComposition in surfaces/mcp): the --json
// stdout is EXACTLY the shared client.TrustReport bytes plus one trailing
// newline, for representative argument combinations. The MCP tool text is
// those same bytes without the newline, so the two surfaces cannot diverge
// without one of the two pins failing — even if the structural
// one-shared-function seam is ever refactored away.
func TestRunTrustReport_JSONMatchesSharedCompositionBytes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	cases := []struct {
		args []string
		opts client.TrustReportOptions
	}{
		{nil, client.TrustReportOptions{}},
		{[]string{"--details", "--limit", "2"}, client.TrustReportOptions{Details: true, Limit: 2}},
		{[]string{"--policy", "review", "--target", "no_such_symbol"}, client.TrustReportOptions{Policy: "review", Target: "no_such_symbol"}},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		runTrustReportAt(repo, append([]string{"--json"}, tc.args...), &out)
		opts := tc.opts
		opts.Root = repo
		want, _, _, err := client.TrustReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("%v: client.TrustReport: %v", tc.args, err)
		}
		if !bytes.Equal(out.Bytes(), append(want, '\n')) {
			t.Errorf("%v: CLI --json bytes != shared composition bytes + newline:\ncli:    %s\nclient: %s", tc.args, out.String(), want)
		}
	}
}

func TestTrustExitCode_Mapping(t *testing.T) {
	cases := []struct {
		policyGiven bool
		verdict     trust.Verdict
		state       trust.State
		want        int
	}{
		{true, trust.VerdictPass, trust.StateCurrent, 0},
		{true, trust.VerdictWarn, trust.StateCurrent, 1},
		{true, trust.VerdictFail, trust.StateCurrent, 3},
		{true, trust.VerdictUnknown, trust.StateCurrent, 4},
		{true, trust.Verdict(""), trust.StateCurrent, 4},      // outside the closed set: fail closed
		{true, trust.Verdict("passish"), trust.StateStale, 4}, // outside the closed set: fail closed
		{false, trust.Verdict(""), trust.StateCurrent, 0},
		{false, trust.Verdict(""), trust.StateStale, 4},
		{false, trust.Verdict(""), trust.StateIncomplete, 4},
		{false, trust.Verdict(""), trust.StateUnavailable, 4},
		{false, trust.Verdict(""), trust.State(""), 4}, // outside the closed set: fail closed
	}
	for _, c := range cases {
		if got := trustExitCode(c.policyGiven, c.verdict, c.state); got != c.want {
			t.Fatalf("trustExitCode(%v, %q, %q) = %d, want %d", c.policyGiven, c.verdict, c.state, got, c.want)
		}
	}
}
