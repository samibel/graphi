package perfbaseline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMeasure_RequiresCommitAndSampleFloor pins the two refusals that keep a
// recorded baseline meaningful: it must name what it measured, and it must rest
// on enough runs to be a measurement rather than an anecdote.
func TestMeasure_RequiresCommitAndSampleFloor(t *testing.T) {
	if _, err := Measure(context.Background(), Config{Samples: 5}); err == nil {
		t.Error("Measure accepted an empty commit")
	}
	if _, err := Measure(context.Background(), Config{Commit: "abc", Samples: MinSamples - 1}); err == nil {
		t.Errorf("Measure accepted %d samples, below the %d-sample floor", MinSamples-1, MinSamples)
	}
}

// TestFixtureDigest_MatchesBenchPinnedWorkload proves this harness hashes the
// bench fixture exactly as internal/bench does. If the two ever diverged, a
// recorded baseline would silently claim to describe the gated workload while
// measuring something else.
func TestFixtureDigest_MatchesBenchPinnedWorkload(t *testing.T) {
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	digest, err := FixtureDigest(filepath.Join(root, "bench", "fixture"))
	if err != nil {
		t.Fatalf("FixtureDigest: %v", err)
	}

	// Read the pinned digest from the budget manifest rather than copying it, so
	// a deliberate re-pin of the bench workload does not need an edit here too.
	budget, err := os.ReadFile(filepath.Join(root, "bench", "bench-budget.yml"))
	if err != nil {
		t.Fatalf("read bench budget: %v", err)
	}
	pinned := ""
	for _, line := range strings.Split(string(budget), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "fixture_digest:"); ok {
			pinned = strings.Trim(strings.TrimSpace(rest), `"`)
			break
		}
	}
	if pinned == "" {
		t.Fatal("bench/bench-budget.yml has no fixture_digest to compare against")
	}
	if digest != pinned {
		t.Errorf("fixture digest %s does not match the bench-pinned digest %s — either the fixture "+
			"changed without re-pinning, or this harness hashes it differently than internal/bench "+
			"(which would make a recorded baseline claim to describe the gated workload while measuring something else)",
			digest, pinned)
	}
}

// TestMeasure_EndToEnd runs the real harness at the sample floor. It is the proof
// that every metric resolves against the live tree — including the trust report,
// which has no other benchmark in the repo.
func TestMeasure_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live measurement in -short mode")
	}
	report, err := Measure(context.Background(), Config{
		Commit:         "test",
		Samples:        MinSamples,
		SkipBinarySize: true,
	})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}

	if report.FullIndex.MedianMS <= 0 || report.FullIndex.P95MS <= 0 {
		t.Errorf("full index produced no timing: %+v", report.FullIndex)
	}
	if report.FullIndex.Samples != MinSamples {
		t.Errorf("full index recorded %d samples, want %d", report.FullIndex.Samples, MinSamples)
	}
	if len(report.WarmQuery) != len(warmQueryOps)+1 {
		t.Errorf("warm query measured %d ops, want %d", len(report.WarmQuery), len(warmQueryOps)+1)
	}
	for op, stat := range report.WarmQuery {
		if stat.MedianMS <= 0 || stat.P95MS <= 0 {
			t.Errorf("warm query op %q produced no timing: %+v", op, stat)
		}
		if stat.P95MS < stat.MedianMS {
			t.Errorf("warm query op %q reports p95 below median (%v < %v)", op, stat.P95MS, stat.MedianMS)
		}
	}
	if report.TrustReport.MedianMS <= 0 {
		t.Error("trust report produced no timing")
	}
	// The whole point of the trust guard: these numbers must describe a real
	// assessment, not the fail-closed path.
	if report.TrustState == "" || report.TrustState == "UNAVAILABLE" {
		t.Errorf("trust report state = %q; the measurement must exercise an observable graph", report.TrustState)
	}
	if report.TrustVerdict == "" {
		t.Error("trust report produced no verdict; the policy assessment path was not exercised")
	}
	if report.FixtureDigest == "" {
		t.Error("report does not pin the workload it measured")
	}
}

// TestRenderParseRoundTrip keeps the artifact readable by its own -diff mode.
func TestRenderParseRoundTrip(t *testing.T) {
	original := Report{
		SchemaVersion: SchemaVersion,
		Commit:        "abc123",
		Toolchain:     "go1.26.5 linux/amd64",
		FixtureDigest: "deadbeef",
		Samples:       15,
		Warmup:        2,
		FullIndex:     Stat{MedianMS: 10, P95MS: 12, Samples: 15},
		WarmQuery:     map[string]Stat{"callers": {MedianMS: 1, P95MS: 2, Samples: 15}},
		TrustReport:   Stat{MedianMS: 3, P95MS: 4, Samples: 15},
		TrustVerdict:  "PASS",
		TrustState:    "CURRENT",
	}
	raw, err := Render(original)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Commit != original.Commit || parsed.FullIndex.P95MS != original.FullIndex.P95MS {
		t.Errorf("round trip lost data: %+v", parsed)
	}
	if parsed.WarmQuery["callers"].P95MS != 2 {
		t.Errorf("round trip lost warm-query data: %+v", parsed.WarmQuery)
	}

	again, err := Render(parsed)
	if err != nil {
		t.Fatalf("Render (second): %v", err)
	}
	if string(raw) != string(again) {
		t.Error("rendering is not deterministic across a round trip")
	}
}

// TestDiff_FlagsRegressionsAndIncomparableRuns covers the A/B logic the later
// phases gate on.
func TestDiff_FlagsRegressionsAndIncomparableRuns(t *testing.T) {
	base := Report{
		Commit: "base", Toolchain: "go1.26.5", Environment: "linux/amd64, 4 cpu",
		FixtureDigest: "same", Samples: 15,
		FullIndex:   Stat{P95MS: 100},
		WarmQuery:   map[string]Stat{"callers": {P95MS: 10}},
		TrustReport: Stat{P95MS: 5},
	}

	// Within budget: 2% slower full index (budget 3%).
	ok := base
	ok.Commit = "cand"
	ok.FullIndex = Stat{P95MS: 102}
	if report := Diff(base, ok); !report.Pass() {
		t.Errorf("a 2%% full-index regression should be inside the %.0f%% budget:\n%s", FullIndexBudgetPct, report.Format())
	}

	// Over budget: 10% slower full index.
	bad := base
	bad.Commit = "cand"
	bad.FullIndex = Stat{P95MS: 110}
	report := Diff(base, bad)
	if report.Pass() {
		t.Error("a 10% full-index regression was not flagged")
	}

	// Over budget on a warm-query op only.
	slowOp := base
	slowOp.Commit = "cand"
	slowOp.WarmQuery = map[string]Stat{"callers": {P95MS: 11}}
	if report := Diff(base, slowOp); report.Pass() {
		t.Error("a 10% warm-query regression was not flagged")
	}

	// A faster run must never fail.
	fast := base
	fast.Commit = "cand"
	fast.FullIndex = Stat{P95MS: 50}
	if report := Diff(base, fast); !report.Pass() {
		t.Error("an improvement was reported as a regression")
	}

	// Incomparable runs must warn, because a cross-workload delta is not evidence.
	other := base
	other.Commit = "cand"
	other.FixtureDigest = "different"
	other.Environment = "darwin/arm64, 10 cpu"
	if warnings := Diff(base, other).Warnings; len(warnings) < 2 {
		t.Errorf("a different fixture and machine produced only %d warnings: %v", len(warnings), warnings)
	}
}

// TestStatOf_ReportsTheWholeDistribution guards against a stat helper that
// silently reports a single number.
func TestStatOf_ReportsTheWholeDistribution(t *testing.T) {
	samples := []time.Duration{
		5 * time.Millisecond, 1 * time.Millisecond, 3 * time.Millisecond,
		2 * time.Millisecond, 4 * time.Millisecond,
	}
	stat := statOf(samples)
	if stat.Samples != 5 {
		t.Errorf("Samples = %d, want 5", stat.Samples)
	}
	if stat.MinMS != 1 || stat.MaxMS != 5 {
		t.Errorf("min/max = %v/%v, want 1/5", stat.MinMS, stat.MaxMS)
	}
	if stat.MedianMS != 3 {
		t.Errorf("MedianMS = %v, want 3", stat.MedianMS)
	}
	if stat.P95MS < stat.MedianMS {
		t.Errorf("p95 %v below median %v", stat.P95MS, stat.MedianMS)
	}
	if got := statOf(nil); got.Samples != 0 {
		t.Errorf("statOf(nil) = %+v, want a zero stat", got)
	}
}
