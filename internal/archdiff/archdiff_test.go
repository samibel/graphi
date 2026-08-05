package archdiff

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// BaselineRelPath is where the recorded artifact lives.
const BaselineRelPath = "docs/rc/archdiff-baseline.json"

func testModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	return root
}

// TestRecordAll_IsReproducibleAcrossEnvironments is the load-bearing test of this
// package. Two environments are built independently — different temp dirs,
// different stores, a freshly ingested graph each time — and must record byte
// identical outcomes.
//
// If this ever fails, the harness is worthless as migration evidence: a later
// phase could not tell "the application layer changed behaviour" apart from "the
// recording is noisy". So this failing is a stop-the-line signal, not a flake to
// retry.
func TestRecordAll_IsReproducibleAcrossEnvironments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live two-environment recording in -short mode")
	}
	root := testModuleRoot(t)
	ctx := context.Background()

	first, err := RecordAll(ctx, root)
	if err != nil {
		t.Fatalf("RecordAll (first): %v", err)
	}
	second, err := RecordAll(ctx, root)
	if err != nil {
		t.Fatalf("RecordAll (second): %v", err)
	}

	if diffs := DiffRecorded(first, second); len(diffs) > 0 {
		var b strings.Builder
		for _, d := range diffs {
			b.WriteString("\n  " + d.String())
		}
		t.Errorf("recording is not reproducible across two independent environments — "+
			"either a use case leaks run-specific state into its output, or normalization misses it:%s", b.String())
	}
}

// TestRecordAll_MatchesCheckedInBaseline is the drift guard.
func TestRecordAll_MatchesCheckedInBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live recording in -short mode")
	}
	root := testModuleRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(BaselineRelPath)))
	if err != nil {
		t.Fatalf("read %s: %v", BaselineRelPath, err)
	}
	baseline, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	recorded, err := RecordAll(context.Background(), root)
	if err != nil {
		t.Fatalf("RecordAll: %v", err)
	}
	diffs := DiffRecorded(baseline.Cases, recorded)
	if len(diffs) == 0 {
		return
	}
	var b strings.Builder
	for _, d := range diffs {
		b.WriteString("\n  " + d.String())
	}
	t.Errorf("%d use case(s) differ from the baseline recorded at %s. A diff here is a behaviour "+
		"change; explain it before continuing the phase, and do not re-record the baseline to make "+
		"this pass:%s", len(diffs), baseline.Commit, b.String())
}

// TestRecordAll_CoversEveryBoundedContext keeps the table from quietly shrinking
// to the easy cases.
func TestRecordAll_CoversEveryBoundedContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live recording in -short mode")
	}
	recorded, err := RecordAll(context.Background(), testModuleRoot(t))
	if err != nil {
		t.Fatalf("RecordAll: %v", err)
	}

	seen := map[string]int{}
	outcomes := map[string]int{}
	for _, entry := range recorded {
		seen[entry.Context]++
		outcomes[entry.Outcome]++
	}
	for _, want := range []string{ContextGraphRead, ContextCodeChange, ContextReview, ContextKnowledge, ContextOperations} {
		if seen[want] == 0 {
			t.Errorf("bounded context %q has no recorded use case", want)
		}
	}

	// Both halves of the contract must actually be exercised. All-ok would mean
	// the fail-closed table is not refusing; all-sentinel would mean the wired
	// client is not wired.
	if outcomes[OutcomeOK] == 0 {
		t.Error("no use case produced a successful result; the wired client is not actually wired")
	}
	if outcomes[OutcomeSentinel] == 0 {
		t.Error("no use case refused with a sentinel; the fail-closed contract is not being exercised")
	}
}

// TestUnwiredCases_AllFailClosed pins the PRD rule that a missing capability must
// never report success. It runs the fail-closed table directly rather than through
// the recorded baseline, so a future baseline re-record cannot bless a regression.
func TestUnwiredCases_AllFailClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live recording in -short mode")
	}
	ctx := context.Background()
	env, err := BuildEnv(ctx, testModuleRoot(t))
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	defer env.Close()

	bare := client.NewDirect(query.New(env.Store), search.New(env.Store))
	recorded, err := Record(ctx, bare, env, UnwiredCases())
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(recorded) == 0 {
		t.Fatal("the fail-closed table is empty")
	}
	for id, entry := range recorded {
		if entry.Outcome == OutcomeOK {
			t.Errorf("%s succeeded on a client with nothing wired — an unavailable capability must "+
				"never report success", id)
		}
		if entry.Outcome == OutcomeSentinel && entry.Sentinel == "" {
			t.Errorf("%s refused with an unnamed sentinel", id)
		}
	}
}

// TestCompare_SelfComparisonIsEmpty exercises the function the migration phases
// call. Comparing an implementation against itself must report nothing; if it
// did not, every future comparison would be full of phantom diffs.
func TestCompare_SelfComparisonIsEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live comparison in -short mode")
	}
	ctx := context.Background()
	env, err := BuildEnv(ctx, testModuleRoot(t))
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	defer env.Close()

	diffs, err := Compare(ctx, env.Client, env.Client, env, Cases())
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(diffs) > 0 {
		var b strings.Builder
		for _, d := range diffs {
			b.WriteString("\n  " + d.String())
		}
		t.Errorf("comparing an implementation with itself reported %d diff(s):%s", len(diffs), b.String())
	}
}

// TestCompare_DetectsADifferentImplementation is the positive control for the
// comparison: a client that behaves differently must be reported. Without it,
// a Compare that always returned nil would pass every test above.
func TestCompare_DetectsADifferentImplementation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live comparison in -short mode")
	}
	ctx := context.Background()
	env, err := BuildEnv(ctx, testModuleRoot(t))
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	defer env.Close()

	// A bare client differs from the fully wired one on every optional capability.
	bare := client.NewDirect(query.New(env.Store), search.New(env.Store))
	diffs, err := Compare(ctx, env.Client, bare, env, Cases())
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(diffs) == 0 {
		t.Error("comparing a fully wired client against a bare one reported no diffs; " +
			"the comparison is not actually comparing anything")
	}
}

// TestCases_HaveUniqueIDsAndKnownContexts guards the table itself: the baseline
// is keyed by ID, so a duplicate would silently drop a recorded use case.
func TestCases_HaveUniqueIDsAndKnownContexts(t *testing.T) {
	valid := map[string]bool{
		ContextGraphRead: true, ContextCodeChange: true, ContextReview: true,
		ContextKnowledge: true, ContextOperations: true,
	}
	seen := map[string]bool{}
	all := append(Cases(), UnwiredCases()...)
	if len(all) == 0 {
		t.Fatal("no use cases defined")
	}
	for _, useCase := range all {
		if useCase.ID == "" {
			t.Error("use case with an empty id")
		}
		if seen[useCase.ID] {
			t.Errorf("duplicate use case id %q — the baseline is keyed by id, so one would be lost", useCase.ID)
		}
		seen[useCase.ID] = true
		if !valid[useCase.Context] {
			t.Errorf("use case %q has unknown bounded context %q", useCase.ID, useCase.Context)
		}
		if useCase.Invoke == nil {
			t.Errorf("use case %q has no invocation", useCase.ID)
		}
	}
}

// TestNormalize_ReplacesRunSpecificValues covers the normalization directly, so a
// gap shows up here rather than as a mysterious reproducibility failure.
func TestNormalize_ReplacesRunSpecificValues(t *testing.T) {
	env := &Env{Root: "/tmp/graphi-archdiff-root-123/repo", tempDirs: []string{"/tmp/graphi-archdiff-store-456"}}

	got := env.Normalize(`{"file":"/tmp/graphi-archdiff-root-123/repo/sample.go"}`)
	if strings.Contains(got, "graphi-archdiff-root-123") {
		t.Errorf("workspace path survived normalization: %s", got)
	}
	got = env.Normalize(`{"session_id":"01J8ZQ","total":1}`)
	if strings.Contains(got, "01J8ZQ") {
		t.Errorf("ledger session id survived normalization: %s", got)
	}
	// Normalization must stay narrow: real content may not be rewritten, or the
	// baseline would stop noticing changes.
	const payload = `{"symbol":"pkg.Hello","kind":"function","line":7}`
	if got := env.Normalize(payload); got != payload {
		t.Errorf("normalization rewrote ordinary content:\n got  %s\n want %s", got, payload)
	}
}

// TestRenderParseRoundTrip keeps the artifact readable by -check.
func TestRenderParseRoundTrip(t *testing.T) {
	original := NewBaseline("abc123", FixtureRelPath, map[string]Entry{
		"graphread/query.definition": {Context: ContextGraphRead, Outcome: OutcomeOK, SHA256: "deadbeef", Bytes: 42},
		"unwired/memory":             {Context: ContextKnowledge, Outcome: OutcomeSentinel, Sentinel: "ErrMemoryUnavailable"},
	})
	raw, err := Render(original)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if diffs := DiffRecorded(original.Cases, parsed.Cases); len(diffs) > 0 {
		t.Errorf("round trip changed the recorded cases: %v", diffs)
	}
	again, err := Render(parsed)
	if err != nil {
		t.Fatalf("Render (second): %v", err)
	}
	if string(raw) != string(again) {
		t.Error("baseline rendering is not deterministic")
	}
}
