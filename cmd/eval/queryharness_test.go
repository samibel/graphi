package main

// SW-125 (P0-C2): the end-to-end properties no unit test can prove — that the
// sample really is reproducible across two independent indexes of the same
// tree, that all twelve stable operations really are exercised, that the FR-8
// floor really is reachable, and that the PR path really is unchanged.
//
// These run the harness over the hermetic tier-1 fixture (no network, no
// clone), which is the same fixture the pre-existing full-run gate uses.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/surfaces/mcp"
)

func runFixtureFullRun(t *testing.T, queryExecutions int) evalreport.FullRunReport {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "report.json")
	code := runFullRun(fullRunOptions{
		manifestPath:    filepath.Join(repoRoot(t), "corpus", "manifest.json"),
		repoName:        "tier1-fixture-hero-go",
		workDir:         t.TempDir(),
		runnerClass:     "test",
		outPath:         outPath,
		queryExecutions: queryExecutions,
	})
	if code != 0 {
		t.Fatalf("runFullRun exit code = %d, want 0", code)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var rep evalreport.FullRunReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

// AC-5, end to end: the SAME GRAPH yields the SAME SAMPLE. Two runs index the
// fixture into two separate temporary stores from scratch, and the published
// symbol list and its digest must be byte-identical — otherwise PRD §16's "two
// consecutive green runs" would be two runs of different questions.
func TestQueryLatency_SymbolSampleIsDeterministicAcrossRuns(t *testing.T) {
	first := runFixtureFullRun(t, 0).Repo.QueryLatency
	second := runFixtureFullRun(t, 0).Repo.QueryLatency
	if first == nil || second == nil {
		t.Fatal("the run published no query-latency evidence")
	}

	if first.Sample.Digest == "" || len(first.Sample.SymbolIDs) == 0 {
		t.Fatalf("the sample is not reproducible from the report: %+v", first.Sample)
	}
	if first.Sample.Digest != second.Sample.Digest {
		t.Fatalf("sample digest drifted between two runs over the same graph: %s vs %s\n%v\n%v",
			first.Sample.Digest, second.Sample.Digest, first.Sample.SymbolIDs, second.Sample.SymbolIDs)
	}
	if strings.Join(first.Sample.SymbolIDs, "|") != strings.Join(second.Sample.SymbolIDs, "|") {
		t.Fatalf("the sampled symbols drifted:\n%v\n%v", first.Sample.SymbolIDs, second.Sample.SymbolIDs)
	}
	// The digest must be the digest OF the published ids, not an independent
	// value a reader has to take on trust.
	if want := evalreport.SampleDigest(first.Sample.SymbolIDs); first.Sample.Digest != want {
		t.Errorf("digest %s does not match the published ids (%s)", first.Sample.Digest, want)
	}
	if first.Sample.Method == "" {
		t.Error("the sample must state the ordering that makes it deterministic")
	}
	if first.Sample.Returned != len(first.Sample.SymbolIDs) {
		t.Errorf("returned = %d but %d ids were published", first.Sample.Returned, len(first.Sample.SymbolIDs))
	}
	if first.Sample.AgentSymbols <= 0 || first.Sample.AgentSymbols > first.Sample.Returned {
		t.Errorf("agent_symbols = %d over a %d-symbol sample", first.Sample.AgentSymbols, first.Sample.Returned)
	}
}

// AC-3/AC-4/AC-7 over a real run: p50 and p95 per class AND per operation, all
// twelve stable operations accounted for, and every published statistic
// recomputable from the retained samples.
func TestQueryLatency_ReportCoversTheTwelveAndRecomputes(t *testing.T) {
	rep := runFixtureFullRun(t, 0)
	s := rep.Repo.QueryLatency
	if s == nil {
		t.Fatal("the run published no query-latency evidence")
	}

	if len(s.Operations) != len(mcp.StableOperations) {
		t.Fatalf("the report covers %d operations, want all %d stable ones", len(s.Operations), len(mcp.StableOperations))
	}
	measured := 0
	for _, op := range s.Operations {
		if op.Class == "" {
			t.Errorf("operation %s has no declared class", op.Operation)
		}
		if op.Operation == "index" {
			if op.Class != evalreport.QueryClassLifecycle || op.Measured || op.Note == "" || op.Latency != nil {
				t.Errorf("index must be declared lifecycle-only, with no distribution at all: %+v", op)
			}
			continue
		}
		if !op.Measured || op.Latency == nil || op.Latency.N == 0 {
			t.Errorf("stable operation %s produced no timed measurements", op.Operation)
			continue
		}
		measured++
		if op.Latency.P50US > op.Latency.P95US {
			t.Errorf("operation %s p50 %d > p95 %d", op.Operation, op.Latency.P50US, op.Latency.P95US)
		}
		if len(op.SamplesUS) != op.Latency.N {
			t.Errorf("operation %s retained %d samples for n=%d (AC-7 needs every measurement)", op.Operation, len(op.SamplesUS), op.Latency.N)
		}
		if op.Warmup == 0 {
			t.Errorf("operation %s recorded no warmup executions", op.Operation)
		}
	}
	if measured != 11 {
		t.Errorf("%d operations were measured, want 11 (the frozen 12 minus lifecycle-only index)", measured)
	}

	// Per class, p50 and p95 both present and derived from the same samples.
	reproduced := evalreport.RecomputeQueryLatency(*s)
	for _, c := range s.Classes {
		if c.N == 0 {
			t.Errorf("class %s pooled no executions", c.Class)
		}
		if c.LatencyStats != reproduced.Classes[c.Class] {
			t.Errorf("class %s published %+v but recomputes to %+v", c.Class, c.LatencyStats, reproduced.Classes[c.Class])
		}
	}
	for _, op := range s.Operations {
		if op.Measured && *op.Latency != reproduced.Operations[op.Operation] {
			t.Errorf("operation %s published %+v but recomputes to %+v", op.Operation, *op.Latency, reproduced.Operations[op.Operation])
		}
	}

	// The legacy per-class maps gained p50 beside the pre-existing p95, and
	// the two agree with the series (one measurement, not two).
	for class, p95 := range rep.Repo.WarmP95US {
		if _, ok := rep.Repo.WarmP50US[class]; !ok {
			t.Errorf("class %s has a p95 but no p50", class)
		}
		if rep.Repo.WarmP50US[class] > p95 {
			t.Errorf("class %s p50 %d > p95 %d", class, rep.Repo.WarmP50US[class], p95)
		}
	}
	for _, c := range s.Classes {
		if c.N == 0 {
			continue
		}
		if rep.Repo.WarmP95US[c.Class] != c.P95US || rep.Repo.WarmP50US[c.Class] != c.P50US {
			t.Errorf("class %s: warm_p*_us says %d/%d but the series says %d/%d",
				c.Class, rep.Repo.WarmP50US[c.Class], rep.Repo.WarmP95US[c.Class], c.P50US, c.P95US)
		}
	}
}

// AC-2 on a real default run: the fixture cannot reach 1000 executions per
// class, and the report says so instead of publishing percentiles that look
// like FR-8's.
func TestQueryLatency_DefaultRunReportsItselfUndersampled(t *testing.T) {
	s := runFixtureFullRun(t, 0).Repo.QueryLatency
	if s.Requested != 0 {
		t.Errorf("requested = %d, want 0 on the default path", s.Requested)
	}
	if s.Minimum != evalreport.QueryExecutionMinimum {
		t.Errorf("minimum = %d, want FR-8's floor even on the default path", s.Minimum)
	}
	if s.Sufficient {
		t.Fatal("a default run over the tier-1 fixture cannot be a sufficient FR-8 measurement")
	}
	if len(s.Warnings) == 0 {
		t.Error("an undersampled run must warn")
	}
}

// AC-1 end to end: with the floor requested, every query class really does
// reach 1000 timed executions over a real store — the plan is not just
// arithmetic on paper.
func TestQueryLatency_FloorIsReachedOverARealStore(t *testing.T) {
	if testing.Short() {
		t.Skip("the 1000-execution floor is a multi-thousand-operation run")
	}
	s := runFixtureFullRun(t, evalreport.QueryExecutionMinimum).Repo.QueryLatency
	if s.Requested != evalreport.QueryExecutionMinimum {
		t.Fatalf("requested = %d, want %d", s.Requested, evalreport.QueryExecutionMinimum)
	}
	for _, c := range s.Classes {
		if !c.Sufficient || c.Executions < evalreport.QueryExecutionMinimum {
			t.Errorf("class %s ran %d executions, below FR-8's floor of %d", c.Class, c.Executions, c.Minimum)
		}
		if c.Executions != c.N {
			t.Errorf("class %s: executions %d != retained n %d", c.Class, c.Executions, c.N)
		}
	}
	if !s.Sufficient {
		t.Errorf("the series is not sufficient: %v", s.Warnings)
	}
	if s.TotalExecutions < 3*evalreport.QueryExecutionMinimum {
		t.Errorf("total executions = %d, want at least three full classes", s.TotalExecutions)
	}
	// Every retained measurement is still there: AC-7 must hold at 1000×, not
	// only at 25×.
	for _, op := range s.Operations {
		if op.Measured && len(op.SamplesUS) != op.Latency.N {
			t.Errorf("operation %s retained %d of %d measurements", op.Operation, len(op.SamplesUS), op.Latency.N)
		}
	}
	if recomputed := evalreport.RecomputeQueryLatency(*s); recomputed.Classes[evalreport.QueryClassSearch] != classStats(s, evalreport.QueryClassSearch) {
		t.Error("the published search class statistics do not recompute from its samples")
	}
}

func classStats(s *evalreport.QueryLatencySeries, class string) evalreport.LatencyStats {
	for _, c := range s.Classes {
		if c.Class == class {
			return c.LatencyStats
		}
	}
	return evalreport.LatencyStats{}
}

// AC-8: the PR gate path is unchanged, and the 1000-execution runs go through
// eval-full.yml. Asserted the same way SW-124 asserts its own repetition flag.
func TestQueryLatency_WorkflowsKeepTheFloorOutOfThePRPath(t *testing.T) {
	root := repoRoot(t)

	evalYML := readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval.yml"))
	if strings.Contains(evalYML, "-query-executions") {
		t.Error("eval.yml (the PR gate) must not run the FR-8 floor: thousands of executions per class is not a PR-time measurement")
	}

	jobs := workflowJobs(t, readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval-full.yml")))
	for _, job := range []string{"full-run", "hero-suite", "cold-index-series"} {
		body, ok := jobs[job]
		if !ok {
			t.Fatalf("eval-full.yml no longer has a %s job", job)
		}
		if strings.Contains(body, "-query-executions") {
			t.Errorf("the %s job must keep the default warm sample counts: it measures something else", job)
		}
	}

	series, ok := jobs["query-latency-series"]
	if !ok {
		t.Fatal("eval-full.yml has no query-latency-series job: FR-8's 1000 executions per class are not exercised anywhere")
	}
	for _, want := range []string{"-query-executions 1000", "grpc-go", "-reference-scenario", "-candidate", "-runner-class ubuntu-latest"} {
		if !strings.Contains(series, want) {
			t.Errorf("the query-latency-series job does not pass %s", want)
		}
	}
	// grpc-go is not in the fail-closed budget selection; passing -budgets
	// there would fail the run for a configuration reason and hide the gates.
	if strings.Contains(series, "-budgets") {
		t.Error("the query-latency series must be read against the reference-scenario gates, not the historical hero budgets")
	}
}
