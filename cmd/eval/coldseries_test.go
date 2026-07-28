package main

// SW-124 (P0-C1): the cold-run series.
//
// The child-process layer is substituted so the series logic — abort
// classification, aggregation, gate reading, the candidate check — is tested
// deterministically and in seconds. The REAL child path is covered once, at the
// bottom, by building the binary and running a small series over the hermetic
// tier-1 fixture: everything above it would be worthless if the process
// plumbing did not work.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

type fakeRun struct {
	report evalreport.FullRunReport
	exit   coldRunExit
	err    error
}

// fakeExecutorFactory substitutes the whole child-process layer. It still
// WRITES each report to the path the series asked for, because the series reads
// one back to fill `repo`.
func fakeExecutorFactory(t *testing.T, runs map[int]fakeRun) coldRunExecutorFactory {
	t.Helper()
	return func(o coldSeriesOptions, workDir string) coldRunExecutor {
		return func(ctx context.Context, run int, outPath string, limitBytes int64) (evalreport.FullRunReport, coldRunExit, error) {
			fr, ok := runs[run]
			if !ok {
				t.Fatalf("unexpected run %d requested", run)
			}
			if fr.err == nil {
				if err := evalreport.WriteFullRunJSON(fr.report, outPath); err != nil {
					t.Fatalf("write fake report: %v", err)
				}
			}
			return fr.report, fr.exit, fr.err
		}
	}
}

func fakeReport(wallclockMS, indexRSS, stableRSS, dbBytes int64, nodes, edges int) evalreport.FullRunReport {
	return evalreport.FullRunReport{
		Repo: evalreport.FullRepoRun{
			Name: "grpc-go",
			Cold: evalreport.ColdState{Verified: true, PageCache: evalreport.PageCacheDropped},
			Index: evalreport.IndexMetrics{
				WallclockMS: wallclockMS,
				PeakRSSMB:   indexRSS,
				DBSizeBytes: dbBytes,
				Nodes:       nodes,
				Edges:       edges,
				Files:       nodes,
			},
			StablePeakRSSMB: stableRSS,
			Pass:            true,
		},
	}
}

// writeCandidateIndex writes a minimal but well-formed evidence index so the
// series has a candidate to cite.
func writeCandidateIndex(t *testing.T, sha string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evidence-index.yaml")
	body := "candidate:\n" +
		"  source: docs/decisions/2026-07-m0-candidate-freeze.md\n" +
		"  sha: " + sha + "\n" +
		"  release_digest: UNKNOWN\n" +
		"gates:\n" +
		"  - id: WP0\n" +
		"    gate: Program Control\n" +
		"    section: plan §6 WP0\n" +
		"    status: UNKNOWN\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func baseOptions(t *testing.T, repo, runnerClass string, runs int) coldSeriesOptions {
	t.Helper()
	root := repoRoot(t)
	return coldSeriesOptions{
		manifestPath:  filepath.Join(root, "corpus", "manifest.json"),
		repoName:      repo,
		workDir:       t.TempDir(),
		runnerClass:   runnerClass,
		outPath:       filepath.Join(t.TempDir(), "cold.json"),
		scenarioPath:  filepath.Join(root, "docs", "eval", "reference-scenario.json"),
		candidatePath: writeCandidateIndex(t, "0000000000000000000000000000000000000000"),
		runs:          runs,
	}
}

func readSeries(t *testing.T, path string) evalreport.ColdRunSeries {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read series report: %v", err)
	}
	var report evalreport.FullRunReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("parse series report: %v", err)
	}
	if report.ColdSeries == nil {
		t.Fatal("report carries no cold_series")
	}
	return *report.ColdSeries
}

// AC-1/AC-2/AC-3/AC-6: ten cold runs, p50 AND p95, per-run metrics kept, and
// every aggregate reproducible from the samples.
func TestColdSeries_TenRunsProduceARecomputableDistribution(t *testing.T) {
	wallclocks := []int64{41_000, 44_000, 40_000, 47_000, 42_000, 46_000, 43_000, 45_000, 48_000, 49_000}
	runs := map[int]fakeRun{}
	for i, ms := range wallclocks {
		runs[i+1] = fakeRun{report: fakeReport(ms, 900, 1100, 120*1024*1024, 50_000, 200_000)}
	}
	o := baseOptions(t, "grpc-go", "ubuntu-latest", 10)

	if code := runColdSeries(o, fakeExecutorFactory(t, runs)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	series := readSeries(t, o.outPath)

	if series.RunsRequested != 10 || series.RunsCompleted != 10 || series.RunsAborted != 0 {
		t.Fatalf("run accounting = requested %d completed %d aborted %d", series.RunsRequested, series.RunsCompleted, series.RunsAborted)
	}
	if series.MinimumRuns != evalreport.ColdRunMinimum || !series.Sufficient {
		t.Errorf("minimum %d sufficient %v: ten completed runs must satisfy FR-8", series.MinimumRuns, series.Sufficient)
	}
	if len(series.Runs) != 10 {
		t.Fatalf("per-run samples = %d, want 10 — the report must carry the samples, not only the aggregates", len(series.Runs))
	}

	wall := series.Aggregates[evalreport.MetricIndexWallclockMS]
	if wall.P50 != 44_000 {
		t.Errorf("p50 = %v, want 44000 (nearest rank of ten samples)", wall.P50)
	}
	if wall.P95 != 49_000 {
		t.Errorf("p95 = %v, want 49000 (the maximum at n=10)", wall.P95)
	}
	if wall.P50 == wall.P95 {
		t.Error("p50 and p95 are identical — a single sample dressed as a distribution")
	}

	// AC-3: RSS, DB size, nodes, edges and bytes-per-edge are all aggregated.
	for _, metric := range []string{
		evalreport.MetricIndexPeakRSSMB, evalreport.MetricStablePeakRSSMB,
		evalreport.MetricDBSizeBytes, evalreport.MetricNodes,
		evalreport.MetricEdges, evalreport.MetricBytesPerEdge,
	} {
		if _, ok := series.Aggregates[metric]; !ok {
			t.Errorf("metric %q was not aggregated", metric)
		}
	}

	// AC-6: everything published recomputes from the stored samples.
	recomputed := evalreport.RecomputeColdAggregates(series.Runs)
	for metric, published := range series.Aggregates {
		if recomputed[metric] != published {
			t.Errorf("metric %q published %+v, recomputes to %+v", metric, published, recomputed[metric])
		}
	}
	if series.AggregateMethod == "" {
		t.Error("the series does not document how its aggregates were derived")
	}
}

// AC-5: an aborted run stays in the report, is counted, is warned about, and is
// absent from the distribution. Three different abort shapes, because they
// arrive through three different code paths.
func TestColdSeries_AbortsAreVisibleAndExcluded(t *testing.T) {
	runs := map[int]fakeRun{
		1: {report: fakeReport(41_000, 900, 1100, 120<<20, 50_000, 200_000)},
		2: {err: fmt.Errorf("run 2 produced no readable report: exit status 2")},
		3: {report: fakeReport(43_000, 900, 1100, 120<<20, 50_000, 200_000), exit: coldRunExit{signal: "killed", exitCode: 137}},
		4: {report: evalreport.FullRunReport{Repo: evalreport.FullRepoRun{Failures: []string{"clone: network unreachable"}}}},
		5: {report: fakeReport(45_000, 900, 1100, 120<<20, 50_000, 200_000)},
	}
	o := baseOptions(t, "grpc-go", "ubuntu-latest", 5)
	if code := runColdSeries(o, fakeExecutorFactory(t, runs)); code != 0 {
		t.Fatalf("exit code = %d, want 0 (aborts are reported, not a crash)", code)
	}
	series := readSeries(t, o.outPath)

	if series.RunsCompleted != 2 || series.RunsAborted != 3 {
		t.Fatalf("completed %d aborted %d, want 2 and 3", series.RunsCompleted, series.RunsAborted)
	}
	if len(series.Runs) != 5 {
		t.Fatalf("samples = %d, want 5 — an aborted run must not drop out of the report", len(series.Runs))
	}
	for _, run := range []int{2, 3, 4} {
		sample := series.Runs[run-1]
		if sample.Status != evalreport.ColdRunAborted {
			t.Errorf("run %d status = %q, want aborted", run, sample.Status)
		}
		if sample.Error == "" {
			t.Errorf("run %d aborted without a recorded reason", run)
		}
	}
	if got := series.Runs[2].Error; !strings.Contains(got, "killed") {
		t.Errorf("a signalled child must be reported as killed, got %q", got)
	}
	if got := series.Runs[3].Error; !strings.Contains(got, "network unreachable") {
		t.Errorf("the child's own failure must travel with the abort, got %q", got)
	}
	aborts := 0
	for _, w := range series.Warnings {
		if strings.Contains(w, "aborted") {
			aborts++
		}
	}
	if aborts != 3 {
		t.Errorf("warnings mention %d aborts, want 3: %v", aborts, series.Warnings)
	}
	if wall := series.Aggregates[evalreport.MetricIndexWallclockMS]; wall.N != 2 || wall.Min != 41_000 {
		t.Errorf("aggregate = %+v; aborted runs must not enter the distribution", wall)
	}
	if series.Sufficient {
		t.Error("two completed runs must not count as FR-8's ten")
	}
	if series.Status != evalreport.StatusUnknown {
		t.Errorf("status = %q, want UNKNOWN for an incomplete series", series.Status)
	}
}

// AC-8: every run is stamped with the runner class and the measuring revision,
// and a series measured off the frozen candidate says so unmissably.
func TestColdSeries_StampsRunnerClassAndIdentifiesANonCandidateRevision(t *testing.T) {
	report := fakeReport(41_000, 900, 1100, 120<<20, 50_000, 200_000)
	report.Header.Commit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	runs := map[int]fakeRun{1: {report: report}, 2: {report: report}}

	o := baseOptions(t, "grpc-go", "ubuntu-latest", 2)
	if code := runColdSeries(o, fakeExecutorFactory(t, runs)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	series := readSeries(t, o.outPath)

	if series.CandidateSHA != "0000000000000000000000000000000000000000" {
		t.Errorf("candidate_sha = %q; the frozen candidate must be cited in the report", series.CandidateSHA)
	}
	if series.CandidateSource == "" {
		t.Error("candidate_source is empty — a citation with no source is an assertion")
	}
	if series.CandidateMatch {
		t.Fatal("a run measured on a revision other than the candidate must not report candidate_match")
	}
	if series.MeasuredSHA == "" {
		t.Error("measured_sha is empty — the report cannot say what it measured")
	}
	found := false
	for _, w := range series.Warnings {
		if strings.Contains(w, "not the frozen candidate") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning identifies the non-candidate revision: %v", series.Warnings)
	}
	for _, sample := range series.Runs {
		if sample.RunnerClass != "ubuntu-latest" {
			t.Errorf("run %d carries runner_class %q", sample.Run, sample.RunnerClass)
		}
		if sample.Commit != report.Header.Commit {
			t.Errorf("run %d carries commit %q, want the measuring revision", sample.Run, sample.Commit)
		}
	}
	// Every SW-124 gate must be UNKNOWN, and the candidate is why.
	for _, g := range series.Gates {
		if g.Status != evalreport.StatusUnknown {
			t.Errorf("gate %s = %s off the frozen candidate; only the candidate can produce gate evidence", g.ID, g.Status)
		}
	}
}

// A series is a repeated measurement; one run is the single-run path.
func TestColdSeries_RejectsADegenerateSeries(t *testing.T) {
	o := baseOptions(t, "grpc-go", "ubuntu-latest", 1)
	if code := runColdSeries(o, fakeExecutorFactory(t, nil)); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// AC-8 fail-closed: a series that cannot cite a candidate is a configuration
// error, not a series without one.
func TestColdSeries_UnreadableCandidateFailsClosed(t *testing.T) {
	o := baseOptions(t, "grpc-go", "ubuntu-latest", 2)
	o.candidatePath = filepath.Join(t.TempDir(), "absent.yaml")
	if code := runColdSeries(o, fakeExecutorFactory(t, nil)); code != 2 {
		t.Fatalf("exit code = %d, want 2 for an unreadable evidence index", code)
	}
}

// A run that is not the reference scenario cannot answer a §12.2 gate. The
// numbers are still published; the gates read UNKNOWN and say why.
func TestColdSeries_GatesAreUnknownOffTheReferenceScenario(t *testing.T) {
	runs := map[int]fakeRun{
		1: {report: fakeReport(1_000, 100, 120, 1<<20, 100, 400)},
		2: {report: fakeReport(1_200, 100, 120, 1<<20, 100, 400)},
	}
	o := baseOptions(t, "tier1-fixture-hero-go", "local-sandbox", 2)
	if code := runColdSeries(o, fakeExecutorFactory(t, runs)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	series := readSeries(t, o.outPath)

	if series.ReferenceScenario {
		t.Fatal("a comparison-class run over a fixture must not be stamped the reference scenario")
	}
	if len(series.Gates) == 0 {
		t.Fatal("no gates were reported at all — an absent gate is invisible, not honest")
	}
	for _, g := range series.Gates {
		if g.Status != evalreport.StatusUnknown {
			t.Errorf("gate %s = %s outside the reference scenario", g.ID, g.Status)
		}
		if g.Reason == "" {
			t.Errorf("gate %s is UNKNOWN with no reason", g.ID)
		}
	}
	if series.Status != evalreport.StatusUnknown {
		t.Errorf("series status = %q, want UNKNOWN", series.Status)
	}
	// The stop rule is wider than the gates and does apply here.
	if series.StopRule == nil || series.StopRule.Status == "" {
		t.Fatal("the §17 stop rule applies to every measured scenario and must be reported")
	}
	if series.StopRule.Triggered {
		t.Errorf("stop rule triggered on a 120 MB peak: %+v", series.StopRule)
	}
}

// AC-4: without -oom-check the gate is UNKNOWN, never PASS and never silently
// absent.
func TestColdSeries_OOMGateIsUnknownWhenNotExercised(t *testing.T) {
	runs := map[int]fakeRun{
		1: {report: fakeReport(1_000, 100, 120, 1<<20, 100, 400)},
		2: {report: fakeReport(1_100, 100, 120, 1<<20, 100, 400)},
	}
	o := baseOptions(t, "grpc-go", "ubuntu-latest", 2)
	if code := runColdSeries(o, fakeExecutorFactory(t, runs)); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	series := readSeries(t, o.outPath)

	if series.OOMCheck.Status != evalreport.StatusUnknown {
		t.Fatalf("oom gate = %q, want UNKNOWN when it was not exercised", series.OOMCheck.Status)
	}
	if !strings.Contains(series.OOMCheck.Reason, "not exercised") {
		t.Errorf("oom reason = %q, want it to say the gate was not exercised", series.OOMCheck.Reason)
	}
	if series.OOMCheck.RequiredLimitBytes != 8589934592 {
		t.Errorf("required_limit_bytes = %d, want the contract's 8 GiB figure", series.OOMCheck.RequiredLimitBytes)
	}
	var oomGate *evalreport.GateResult
	for i := range series.Gates {
		if series.Gates[i].ID == oomGateID {
			oomGate = &series.Gates[i]
		}
	}
	if oomGate == nil {
		t.Fatal("the OOM gate is missing from the gate list")
	}
	if oomGate.Status != evalreport.StatusUnknown {
		t.Errorf("oom gate row = %q, want UNKNOWN", oomGate.Status)
	}
}
