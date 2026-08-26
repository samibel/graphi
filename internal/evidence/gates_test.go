package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AX-00 (SW-220) AC-4 tests for the aggregated protection-gate view.
//
// The rule under test is the same one internal/evidence enforces for index rows,
// applied to gates: absence is UNKNOWN, and UNKNOWN is never PASS. Every way a
// result can be missing or untrustworthy gets its own case, because "absent"
// and "present but meaningless" fail in the same direction and a view that
// conflates them tells a maintainer less than it appears to.

// testGates is a two-gate declaration pointing at workflows the fixture writes,
// so the declaration-resolution check is exercised for real rather than stubbed.
func testGates() []ProtectionGate {
	return []ProtectionGate{
		{ID: "cgo", Gate: "CGo-free default build", Enforces: "no cgo in the default graph", Workflow: "cgoconformance.yml", Job: "cgo-free-conformance", Cadence: "every PR"},
		{ID: "parity", Gate: "parity matrix", Enforces: "full equals incremental", Workflow: "parity.yml", Job: "parity-matrix", Cadence: "dispatch/nightly"},
	}
}

// fixtureRoot builds a temp checkout containing exactly the workflows testGates
// declares, so BuildGateView's declaration check resolves.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(WorkflowsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("cgoconformance.yml", "name: cgo-conformance\n\non:\n  pull_request:\n\njobs:\n  cgo-free-conformance:\n    runs-on: ubuntu-latest\n")
	write("parity.yml", "name: parity\n\non:\n  workflow_dispatch:\n\njobs:\n  parity-matrix:\n    runs-on: ubuntu-latest\n")
	return root
}

// writeResult drops one gate result record into dir.
func writeResult(t *testing.T, dir string, result GateResult) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, result.Gate+".json"), raw, 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
}

func rowByID(t *testing.T, view GateView, id string) GateRow {
	t.Helper()
	for _, row := range view.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("gate %q not present in the view", id)
	return GateRow{}
}

// TestGateView_MissingResultRendersUnknownNeverPass is the AC-4 honesty rule and
// the case the story's test notes call out by name.
func TestGateView_MissingResultRendersUnknownNeverPass(t *testing.T) {
	root := fixtureRoot(t)
	results := t.TempDir()
	// Only ONE of the two gates reports. The other must not inherit its verdict.
	writeResult(t, results, GateResult{Gate: "cgo", Status: StatusPass, EvidenceURI: "https://ci/run/1", SHA: "deadbeef"})

	view := BuildGateView(root, results, "abc1234", testGates())
	if !view.Pass() {
		t.Fatalf("declaration should resolve in the fixture checkout: %v", view.DeclarationErrors)
	}

	missing := rowByID(t, view, "parity")
	if missing.Status != StatusUnknown {
		t.Errorf("gate with no recorded result: status = %q, want %q", missing.Status, StatusUnknown)
	}
	if missing.Reason == "" {
		t.Error("an UNKNOWN row must say WHY it is unknown; an unexplained UNKNOWN is not much better than a blank")
	}

	rendered := RenderGateView(view)
	if !strings.Contains(rendered, "❔ UNKNOWN") {
		t.Errorf("rendered view does not show the UNKNOWN badge:\n%s", rendered)
	}
	// The decisive assertion: the absent gate's ROW must not read PASS.
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "**parity**") && strings.Contains(line, "PASS") {
			t.Errorf("a gate with NO result rendered as PASS — this is the exact false-green this view exists to prevent:\n%s", line)
		}
	}
	if !strings.Contains(rendered, "UNKNOWN=1") {
		t.Errorf("tally does not count the UNKNOWN gate:\n%s", rendered)
	}
	if !strings.Contains(rendered, "UNKNOWN counts as NOT PASSED") {
		t.Errorf("tally does not state that UNKNOWN is not a pass:\n%s", rendered)
	}
}

// TestGateView_NoResultsDirectoryIsAllUnknown pins the ordinary case: with no
// results collected at all the view is seven honest UNKNOWNs, not an error that
// would tempt someone to stop running it.
func TestGateView_NoResultsDirectoryIsAllUnknown(t *testing.T) {
	view := BuildGateView(fixtureRoot(t), "", "", testGates())
	if !view.Pass() {
		t.Fatalf("an empty results dir is not a declaration error: %v", view.DeclarationErrors)
	}
	for _, row := range view.Rows {
		if row.Status != StatusUnknown {
			t.Errorf("gate %q: status = %q with no results collected, want %q", row.ID, row.Status, StatusUnknown)
		}
	}
	if got := RenderGateView(view); !strings.Contains(got, "**Commit:** `UNKNOWN`") {
		t.Errorf("a view with no commit must say UNKNOWN, not render a blank:\n%s", got)
	}
}

// TestGateView_UnbackedPassIsDowngraded carries the internal/evidence honesty
// rule into this view: a job can claim PASS, but a claim without a versioned
// artifact behind it is not evidence.
func TestGateView_UnbackedPassIsDowngraded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result GateResult
		want   string
	}{
		{"no evidence uri", GateResult{Gate: "cgo", Status: StatusPass, SHA: "deadbeef"}, "evidence_uri"},
		{"no sha", GateResult{Gate: "cgo", Status: StatusPass, EvidenceURI: "https://ci/run/1"}, "sha"},
		{"neither", GateResult{Gate: "cgo", Status: StatusPass}, "evidence_uri and sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := t.TempDir()
			writeResult(t, results, tc.result)
			row := rowByID(t, BuildGateView(fixtureRoot(t), results, "abc", testGates()), "cgo")
			if row.Status != StatusUnknown {
				t.Errorf("unbacked PASS: status = %q, want %q", row.Status, StatusUnknown)
			}
			if !strings.Contains(row.Reason, tc.want) {
				t.Errorf("reason %q does not name the missing backing %q", row.Reason, tc.want)
			}
		})
	}
}

// TestGateView_BackedPassIsReachable is the anti-vacuity case: if nothing could
// ever read PASS the previous tests would pass for the wrong reason.
func TestGateView_BackedPassIsReachable(t *testing.T) {
	results := t.TempDir()
	writeResult(t, results, GateResult{Gate: "cgo", Status: "pass", EvidenceURI: "https://ci/run/7", SHA: "cafebabe", Run: "https://ci/run/7"})
	row := rowByID(t, BuildGateView(fixtureRoot(t), results, "abc", testGates()), "cgo")
	if row.Status != StatusPass {
		t.Fatalf("a fully backed PASS (lowercase on the wire) was not accepted: status = %q, reason = %q", row.Status, row.Reason)
	}
	if got := RenderGateView(BuildGateView(fixtureRoot(t), results, "abc", testGates())); !strings.Contains(got, "([run](https://ci/run/7))") {
		t.Errorf("a result carrying a run URL should link it:\n%s", got)
	}
}

// TestGateView_UntrustworthyResultsAllRenderUnknown covers every way a present
// result can still be worthless. They must all land on UNKNOWN, each with its
// own reason so "absent" and "unreadable" stay distinguishable.
func TestGateView_UntrustworthyResultsAllRenderUnknown(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		results := t.TempDir()
		if err := os.WriteFile(filepath.Join(results, "cgo.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		row := rowByID(t, BuildGateView(fixtureRoot(t), results, "abc", testGates()), "cgo")
		if row.Status != StatusUnknown {
			t.Errorf("malformed result: status = %q, want %q", row.Status, StatusUnknown)
		}
		if !strings.Contains(row.Reason, "unreadable") {
			t.Errorf("reason %q should distinguish an unreadable result from an absent one", row.Reason)
		}
	})

	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{"blank status", "", "no status"},
		{"invented status", "GREEN", "invalid status"},
		{"explicit unknown", StatusUnknown, "reported UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := t.TempDir()
			writeResult(t, results, GateResult{Gate: "cgo", Status: tc.status, EvidenceURI: "u", SHA: "s"})
			row := rowByID(t, BuildGateView(fixtureRoot(t), results, "abc", testGates()), "cgo")
			if row.Status != StatusUnknown {
				t.Errorf("status %q on the wire: rendered %q, want %q", tc.status, row.Status, StatusUnknown)
			}
			if !strings.Contains(row.Reason, tc.want) {
				t.Errorf("reason %q does not explain %q", row.Reason, tc.name)
			}
		})
	}

	t.Run("fail stays fail", func(t *testing.T) {
		results := t.TempDir()
		writeResult(t, results, GateResult{Gate: "cgo", Status: StatusFail})
		row := rowByID(t, BuildGateView(fixtureRoot(t), results, "abc", testGates()), "cgo")
		if row.Status != StatusFail {
			t.Errorf("a reported FAIL must not be softened into UNKNOWN: got %q", row.Status)
		}
	})
}

// TestGateView_BrokenDeclarationIsAnError separates rot in the INSTRUMENT from a
// missing result. A renamed job would otherwise leave a row plausibly and
// permanently UNKNOWN, which reads like "not measured yet" forever.
func TestGateView_BrokenDeclarationIsAnError(t *testing.T) {
	gates := []ProtectionGate{
		{ID: "cgo", Gate: "CGo-free", Enforces: "x", Workflow: "cgoconformance.yml", Job: "renamed-away", Cadence: "every PR"},
		{ID: "ghost", Gate: "Ghost", Enforces: "x", Workflow: "no-such-workflow.yml", Job: "whatever", Cadence: "every PR"},
	}
	view := BuildGateView(fixtureRoot(t), "", "", gates)
	if view.Pass() {
		t.Fatal("a declaration naming a missing job and a missing workflow must not pass")
	}
	if len(view.DeclarationErrors) != 2 {
		t.Errorf("declaration errors = %d, want 2: %v", len(view.DeclarationErrors), view.DeclarationErrors)
	}
	for _, row := range view.Rows {
		if row.Status != StatusUnknown {
			t.Errorf("gate %q with a broken declaration: status = %q, want %q", row.ID, row.Status, StatusUnknown)
		}
	}
	rendered := RenderGateView(view)
	if !strings.Contains(rendered, "Declaration errors") {
		t.Errorf("rendered view hides the declaration errors:\n%s", rendered)
	}
}

// TestGateView_BlankDeclarationFieldIsAnError pins that a half-filled row cannot
// silently become a gate nobody owns.
func TestGateView_BlankDeclarationFieldIsAnError(t *testing.T) {
	view := BuildGateView(fixtureRoot(t), "", "", []ProtectionGate{{ID: "cgo", Gate: "", Workflow: "cgoconformance.yml", Job: "cgo-free-conformance"}})
	if view.Pass() {
		t.Fatal("a gate declaration with a blank human name must not pass")
	}
}

func TestRenderGateView_IsDeterministic(t *testing.T) {
	results := t.TempDir()
	writeResult(t, results, GateResult{Gate: "cgo", Status: StatusPass, EvidenceURI: "u", SHA: "s"})
	view := BuildGateView(fixtureRoot(t), results, "abc", testGates())
	if first, second := RenderGateView(view), RenderGateView(view); first != second {
		t.Errorf("RenderGateView is not a pure function of the view:\n first =%s\n second=%s", first, second)
	}
}

func TestWorkflowDeclaresJob(t *testing.T) {
	yaml := "name: x\n\non:\n  pull_request:\n\njobs:\n  first-job:\n    name: a\n    steps:\n      - uses: actions/checkout@sha\n  second-job:\n    name: b\n"
	for _, job := range []string{"first-job", "second-job"} {
		if !workflowDeclaresJob(yaml, job) {
			t.Errorf("job %q not found in the workflow", job)
		}
	}
	// A step name, an `on:` key and a nested key must never be mistaken for a job.
	for _, notAJob := range []string{"steps", "name", "pull_request", "uses"} {
		if workflowDeclaresJob(yaml, notAJob) {
			t.Errorf("%q was resolved as a job key; only top-level entries under jobs: are jobs", notAJob)
		}
	}
}

// TestProtectionGatesDeclaration_IsIntactInThisCheckout is the CI wiring for
// AC-4: it runs inside the ordinary test suite, so a workflow or job renamed by
// some later PR breaks the declaration on that PR rather than quietly turning a
// gate into a permanent UNKNOWN.
func TestProtectionGatesDeclaration_IsIntactInThisCheckout(t *testing.T) {
	root := repoRootFromTest(t)
	gates, err := LoadProtectionGates(filepath.Join(root, filepath.FromSlash(ProtectionGatesPath)))
	if err != nil {
		t.Fatalf("load %s: %v", ProtectionGatesPath, err)
	}

	// AC-4 names the seven invariants this view must cover. Pin them so a gate
	// cannot be dropped from the declaration to make the view look tidy.
	want := []string{"binary-size", "cgo", "coverage", "egress", "layer", "parity", "repro"}
	got := GateIDs(gates)
	if len(got) != len(want) {
		t.Fatalf("declared gates = %v, want exactly %v (SW-220 AC-4)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("declared gates = %v, want exactly %v (SW-220 AC-4)", got, want)
		}
	}

	view := BuildGateView(root, "", "", gates)
	if !view.Pass() {
		for _, violation := range view.DeclarationErrors {
			t.Errorf("protection-gate declaration is stale: [%s] %s", violation.GateID, violation.Reason)
		}
		t.Fatalf("fix %s (or the workflow it names) — a gate the view cannot resolve is a gate nobody is watching", ProtectionGatesPath)
	}
	// Every row must be UNKNOWN here: this test collects no results, and that is
	// the honest reading.
	for _, row := range view.Rows {
		if row.Status != StatusUnknown {
			t.Errorf("gate %q reads %q with no results collected — the view invented a status", row.ID, row.Status)
		}
	}
}

// repoRootFromTest walks up to the module root without invoking the toolchain,
// so this test stays hermetic.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}
