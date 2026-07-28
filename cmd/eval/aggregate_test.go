package main

// SW-128 (P0-C5): what `-aggregate` DOES, expressed as exit codes.
//
// AC-8 is the spine of this file. An incomplete run must not exit 0, and the
// four outcomes must stay distinguishable: reproduced, contradicted,
// unreadable, unfinished. A CI job reacts differently to each, and a harness
// that collapses "the number is wrong" into "we could not check the number"
// would let a real discrepancy be triaged as a flaky job.
//
// The run directories here are written directly rather than measured, so the
// tests are deterministic and the environment is whatever the case needs — the
// probes have their own tests in environment_test.go.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// completeEnv is a fully captured environment, so a case that is meant to be
// publishable is not blocked by a probe that this host cannot answer.
func completeEnv() evalreport.RunEnvironment {
	return evalreport.RunEnvironment{
		CPUModel: "AMD EPYC 7763 64-Core Processor", CPUCount: 4, RAMBytes: 16 << 30,
		OS: "linux", Arch: "amd64", Kernel: "6.11.0-1015-azure", GoVersion: "go1.26.5",
		Filesystem: "ext4", FilesystemPath: "/mnt/work", CacheState: evalreport.PageCacheDropped,
		RunnerClass: "ubuntu-latest", RunnerRole: "reference", CandidateSHA: "deadbeef",
		MeasuredSHA: "deadbeef", CandidateMatch: true,
		HarnessVersion: evalreport.HarnessVersion, ScorerVersion: evalreport.ScorerVersion,
	}
}

// fixtureReport builds a report carrying all four series, with every published
// statistic derived from the samples that travel with it — the shape a real,
// self-consistent run produces.
func fixtureReport() (evalreport.FullRunReport, map[string]evalreport.RawSampleSet) {
	env := completeEnv()

	var runs []evalreport.ColdRunSample
	for i := 0; i < 10; i++ {
		runs = append(runs, evalreport.ColdRunSample{
			Run: i + 1, Status: evalreport.ColdRunCompleted,
			Index: evalreport.IndexMetrics{
				WallclockMS: int64(1000 + i*100), PeakRSSMB: int64(500 + i),
				DBSizeBytes: 200_000_000, Nodes: 1000, Edges: 4000,
			},
			StablePeakRSSMB: int64(600 + i),
			BytesPerEdge:    200_000_000.0 / 4000,
		})
	}
	cold := &evalreport.ColdRunSeries{
		Repo: "grpc-go", RunsRequested: 10, RunsCompleted: 10,
		MinimumRuns: evalreport.ColdRunMinimum, Sufficient: true,
		Runs: runs, Aggregates: evalreport.RecomputeColdAggregates(runs),
	}

	callers := []int64{10, 20, 30, 40, 50}
	pooled := append([]int64{}, callers...)
	query := &evalreport.QueryLatencySeries{
		Repo: "grpc-go",
		Operations: []evalreport.QueryOpLatency{
			{Operation: "callers", Class: evalreport.QueryClassStructural, Measured: true,
				Latency: latencyPtr(evalreport.LatencyStatsFrom(callers)), SamplesUS: callers},
			{Operation: "index", Class: evalreport.QueryClassLifecycle, Measured: false,
				Note: evalreport.LifecycleOperationNote},
		},
		Classes: []evalreport.QueryClassLatency{{
			Class: evalreport.QueryClassStructural, Operations: []string{"callers"},
			Executions: len(pooled), LatencyStats: evalreport.LatencyStatsFrom(pooled),
		}},
	}

	changes := []evalreport.ChangeSample{
		{Step: 1, Class: "add", UpdateUS: 100, UpdateMeasured: true, FreshnessUS: 150, FreshnessMeasured: true},
		{Step: 2, Class: "modify", UpdateUS: 200, UpdateMeasured: true, FreshnessUS: 250, FreshnessMeasured: true},
	}
	rec := evalreport.RecomputeIncremental(changes)
	incremental := &evalreport.IncrementalSeries{
		Repo: "grpc-go", Completed: 2, Changes: changes,
		Update: rec.Update, Freshness: rec.Freshness,
		PerClass: []evalreport.ChangeClassLatency{rec.Classes["add"], rec.Classes["modify"]},
	}

	in := []evalreport.StallInterval{
		{Seq: 1, Phase: "parse", US: 500}, {Seq: 2, Phase: "parse", US: 900}, {Seq: 3, Phase: "link", US: 100},
	}
	stalls := &evalreport.StallSeries{
		Repo: "grpc-go", Events: 4, Observable: true, Intervals: in,
		Stalls: evalreport.RecomputeStalls(in).Stalls, PerPhase: evalreport.PhaseStallsOf(in),
	}

	report := evalreport.FullRunReport{
		RunnerClass: "ubuntu-latest", RunnerRole: "reference", ReferenceScenario: true,
		ColdSeries: cold,
		Repo: evalreport.FullRepoRun{
			Name: "grpc-go", QueryLatency: query, Incremental: incremental, Stalls: stalls,
			Cold: evalreport.ColdState{PageCache: evalreport.PageCacheDropped, Verified: true},
		},
	}
	return report, rawSetsFrom(report, env)
}

func latencyPtr(s evalreport.LatencyStats) *evalreport.LatencyStats { return &s }

// writeFixtureRun writes a self-consistent run directory and returns its path.
func writeFixtureRun(t *testing.T, env evalreport.RunEnvironment, mutate func(map[string]evalreport.RawSampleSet)) string {
	t.Helper()
	dir := t.TempDir()
	report, sets := fixtureReport()
	if mutate != nil {
		mutate(sets)
	}
	index := evalreport.RunIndex{
		Date: "2026-07-28", RunnerClass: "ubuntu-latest", Repo: "grpc-go",
		Report: "report.json", Environment: env,
	}
	if err := evalreport.WriteRunDir(dir, index, report, sets); err != nil {
		t.Fatalf("WriteRunDir: %v", err)
	}
	return dir
}

func readAggregate(t *testing.T, dir string) evalreport.AggregateReport {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, evalreport.AggregateFile))
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var got evalreport.AggregateReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse aggregate: %v", err)
	}
	return got
}

// AC-2 end to end: a self-consistent run reproduces, and exits 0.
func TestRunAggregate_ACompleteRunReproducesAndExitsZero(t *testing.T) {
	dir := writeFixtureRun(t, completeEnv(), nil)

	var log bytes.Buffer
	if code := runAggregate(dir, "", &log); code != exitAggregateReproduced {
		t.Fatalf("exit = %d, want %d\n%s", code, exitAggregateReproduced, log.String())
	}
	got := readAggregate(t, dir)
	if got.Status != evalreport.StatusPass || !got.Publishable {
		t.Fatalf("status = %s, publishable = %v, want PASS/true", got.Status, got.Publishable)
	}
	// All four harnesses must actually be covered — a green aggregate that
	// only checked the easy series would be worse than no check.
	covered := map[string]evalreport.SeriesCoverage{}
	for _, c := range got.Series {
		covered[c.Series] = c
	}
	for _, series := range evalreport.RawSeriesNames {
		c, ok := covered[series]
		if !ok || c.Metrics == 0 {
			t.Errorf("series %s contributed no checked metric", series)
		}
		if c.Status != evalreport.StatusPass {
			t.Errorf("series %s = %s, want PASS", series, c.Status)
		}
	}
	if !strings.Contains(log.String(), "PASS") {
		t.Errorf("summary does not report the pass:\n%s", log.String())
	}
}

// AC-2's teeth: a published number edited away from its samples exits 1 and
// names the metric. This is the case the whole story exists to make impossible
// to miss.
func TestRunAggregate_ADiscrepancyExitsOneAndNamesTheMetric(t *testing.T) {
	dir := writeFixtureRun(t, completeEnv(), nil)

	// Edit the PUBLISHED report, leaving the raw samples untouched — exactly
	// what a hand-maintained artifact or a stale copy looks like.
	path := filepath.Join(dir, "report.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	edited := strings.Replace(string(raw), `"p95": 1900`, `"p95": 1850`, 1)
	if edited == string(raw) {
		t.Fatal("the test edit did not apply — the fixture no longer matches the report shape")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	var log bytes.Buffer
	code := runAggregate(dir, "", &log)
	if code != exitAggregateDiscrepancy {
		t.Fatalf("exit = %d, want %d (discrepancy)\n%s", code, exitAggregateDiscrepancy, log.String())
	}
	if !strings.Contains(log.String(), "index_wallclock_ms.p95") {
		t.Errorf("the log does not name the drifted metric:\n%s", log.String())
	}
	got := readAggregate(t, dir)
	if got.Status != evalreport.StatusFail || got.Publishable {
		t.Fatalf("status = %s, publishable = %v, want FAIL/false", got.Status, got.Publishable)
	}
}

// AC-8, the story's own test note: a deliberately truncated run must not exit 0.
// The raw data for one metric is gone, so that metric is UNKNOWN and the run is
// unpublishable — but nothing was contradicted, so it is not a FAIL either.
func TestRunAggregate_ATruncatedRunDoesNotExitZero(t *testing.T) {
	dir := writeFixtureRun(t, completeEnv(), func(sets map[string]evalreport.RawSampleSet) {
		delete(sets, evalreport.RawSeriesIncremental)
	})

	var log bytes.Buffer
	code := runAggregate(dir, "", &log)
	if code == exitAggregateReproduced {
		t.Fatal("AC-8: a run with missing raw data exited 0")
	}
	if code != exitAggregateIncomplete {
		t.Fatalf("exit = %d, want %d (incomplete)\n%s", code, exitAggregateIncomplete, log.String())
	}
	got := readAggregate(t, dir)
	if got.Discrepant != 0 {
		t.Errorf("discrepant = %d, want 0 — nothing was contradicted, only unmeasured", got.Discrepant)
	}
	if got.Unknown == 0 {
		t.Error("no metric read UNKNOWN despite the freshness raw data being absent")
	}
	if len(got.MissingSeries) != 1 || got.MissingSeries[0] != evalreport.RawSeriesIncremental {
		t.Errorf("missing series = %v, want [incremental]", got.MissingSeries)
	}
	if !strings.Contains(log.String(), "INCOMPLETE") {
		t.Errorf("the log does not say the run is incomplete:\n%s", log.String())
	}
}

// AC-3 as a publication gate: arithmetic that reproduces perfectly is still not
// publishable when nobody documented the machine.
func TestRunAggregate_AnUndocumentedEnvironmentDoesNotExitZero(t *testing.T) {
	env := completeEnv()
	env.Kernel = ""
	dir := writeFixtureRun(t, env, nil)

	var log bytes.Buffer
	code := runAggregate(dir, "", &log)
	if code != exitAggregateIncomplete {
		t.Fatalf("exit = %d, want %d (incomplete)\n%s", code, exitAggregateIncomplete, log.String())
	}
	got := readAggregate(t, dir)
	if got.Discrepant != 0 {
		t.Errorf("discrepant = %d, want 0", got.Discrepant)
	}
	if !strings.Contains(log.String(), "kernel") {
		t.Errorf("the log does not name the undocumented field:\n%s", log.String())
	}
}

// AC-7: a directory whose raw files disagree about the harness version is
// refused before any arithmetic happens. It is a usage error, not a
// discrepancy — the files are individually fine and the DIRECTORY is wrong.
func TestRunAggregate_MixedHarnessVersionsAreRefused(t *testing.T) {
	dir := writeFixtureRun(t, completeEnv(), func(sets map[string]evalreport.RawSampleSet) {
		set := sets[evalreport.RawSeriesStalls]
		set.HarnessVersion = "p0-perf/0"
		sets[evalreport.RawSeriesStalls] = set
	})

	var log bytes.Buffer
	code := runAggregate(dir, "", &log)
	if code != exitAggregateUsage {
		t.Fatalf("exit = %d, want %d (usage)\n%s", code, exitAggregateUsage, log.String())
	}
	if !strings.Contains(log.String(), "harness_version") {
		t.Errorf("the log does not explain the refusal:\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, evalreport.AggregateFile)); err == nil {
		t.Error("an aggregate was written for a directory that mixes methodologies")
	}
}

// A directory that is not a run directory, and no directory at all, are usage
// errors — never a silent zero.
func TestRunAggregate_UnreadableInputExitsTwo(t *testing.T) {
	var log bytes.Buffer
	if code := runAggregate("", "", &log); code != exitAggregateUsage {
		t.Errorf("empty dir: exit = %d, want %d", code, exitAggregateUsage)
	}
	if code := runAggregate(t.TempDir(), "", &log); code != exitAggregateUsage {
		t.Errorf("empty directory: exit = %d, want %d", code, exitAggregateUsage)
	}
}

// AC-8 stated directly: the four outcomes have four distinct codes, and only
// the reproduced one is 0.
func TestAggregateExitCodes_AreUnambiguous(t *testing.T) {
	codes := map[string]int{
		"reproduced":  exitAggregateReproduced,
		"discrepancy": exitAggregateDiscrepancy,
		"usage":       exitAggregateUsage,
		"incomplete":  exitAggregateIncomplete,
	}
	if codes["reproduced"] != 0 {
		t.Fatalf("the reproduced code is %d, want 0", codes["reproduced"])
	}
	seen := map[int]string{}
	for name, code := range codes {
		if other, dup := seen[code]; dup {
			t.Fatalf("exit code %d means both %q and %q", code, other, name)
		}
		seen[code] = name
		if name != "reproduced" && code == 0 {
			t.Fatalf("%q exits 0", name)
		}
	}
}

// The exported directory must be readable by the aggregator that ships with it.
// Without this, the two halves of the story could drift apart and each would
// still pass its own tests.
func TestExportRunDir_ProducesADirectoryTheAggregatorReproduces(t *testing.T) {
	report, _ := fixtureReport()
	target := filepath.Join(t.TempDir(), "run")

	dir, sets, err := exportRunDir(exportOptions{
		target: target, runnerClass: "ubuntu-latest", runnerRole: "reference",
		repo: "grpc-go", workDir: t.TempDir(), date: "2026-07-28",
		candidateSHA: "deadbeef", candidateSource: "docs/rc/evidence-index.yaml",
		measuredSHA: "deadbeef", candidateMatch: true,
	}, report)
	if err != nil {
		t.Fatalf("exportRunDir: %v", err)
	}
	if dir != target {
		t.Fatalf("dir = %q, want %q", dir, target)
	}
	if len(sets) != len(evalreport.RawSeriesNames) {
		t.Fatalf("exported %d series, want all %d", len(sets), len(evalreport.RawSeriesNames))
	}

	var log bytes.Buffer
	code := runAggregate(dir, "", &log)
	// The exit code depends on whether THIS host could probe its own machine,
	// which is not what this test is about. What must hold on every host is
	// that nothing was contradicted.
	if code == exitAggregateDiscrepancy {
		t.Fatalf("the exported directory does not reproduce:\n%s", log.String())
	}
	got := readAggregate(t, dir)
	if got.Discrepant != 0 {
		t.Fatalf("discrepancies in a freshly exported run: %v", got.Discrepancies)
	}
	if got.Checked == 0 {
		t.Fatal("the exported run produced no checkable metric")
	}
}

// AC-4: `-export-raw auto` applies the path convention rather than asking the
// operator to remember it.
func TestExportRunDir_AutoUsesThePathConvention(t *testing.T) {
	report, _ := fixtureReport()
	root := t.TempDir()
	// The convention is repo-root-relative, so the test runs from a temp root
	// and reads the directory back from there.
	restore := chdir(t, root)
	defer restore()

	dir, _, err := exportRunDir(exportOptions{
		target: exportAuto, runnerClass: "ubuntu-latest", repo: "grpc-go",
		workDir: root, date: "2026-07-28",
	}, report)
	if err != nil {
		t.Fatalf("exportRunDir: %v", err)
	}
	if want := evalreport.RunsRoot + "/2026-07-28-ubuntu-latest"; dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), evalreport.RunIndexFile)); err != nil {
		t.Fatalf("the run index is not where the convention says: %v", err)
	}
}

// An unnamed runner class cannot name a directory, and guessing one would put
// evidence somewhere nobody looks for it.
func TestExportRunDir_AutoRefusesAnUnnamedClass(t *testing.T) {
	report, _ := fixtureReport()
	if _, _, err := exportRunDir(exportOptions{target: exportAuto, date: "2026-07-28"}, report); err == nil {
		t.Fatal("auto export with no runner class was accepted")
	}
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() { _ = os.Chdir(prev) }
}

// AC-2 and AC-5 in CI, not only in a library. A reference-scenario job that
// published numbers without exporting the samples behind them, or that exported
// them and never checked they reproduce, would leave the whole story as an
// unused capability.
func TestAggregate_TheWorkflowExportsAndReproducesEveryReferenceRun(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval-full.yml")))

	// The four reference-scenario jobs are the ones whose numbers become P0
	// evidence. The cobra/flask/guava matrix is deliberately not here: it
	// enforces the historical per-repo ceilings and freezes nothing.
	for _, name := range []string{"cold-index-series", "query-latency-series", "freshness-series", "progress-stall-series"} {
		job, ok := jobs[name]
		if !ok {
			t.Fatalf("eval-full.yml no longer has a %s job", name)
		}
		if !strings.Contains(job, "-export-raw") {
			t.Errorf("the %s job publishes numbers without exporting the raw samples behind them", name)
		}
		if !strings.Contains(job, "-aggregate") {
			t.Errorf("the %s job exports raw samples and never checks that its report reproduces from them", name)
		}
		// `if: always()` on the reproduction step: a run whose gates went red
		// still has to be internally consistent, and that is exactly when a
		// contradiction between report and samples matters most. The step is
		// found by looking back from `-aggregate` to the `- name:` that opens it.
		step := job[:strings.Index(job, "-aggregate")]
		if !strings.Contains(step[strings.LastIndex(step, "- name:"):], "always()") {
			t.Errorf("the %s job's reproduction step is not `if: always()`: a red run must still be checked for self-consistency", name)
		}
	}
}
