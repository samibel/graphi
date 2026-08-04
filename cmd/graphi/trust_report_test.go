package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
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
