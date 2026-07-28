package main

// SW-124 (P0-C1), AC-7: extending the harness must not change what the PR path
// does — and the real child-process plumbing must actually work.
//
// The series tests substitute the executor, which is what makes them fast and
// deterministic. That substitution is also exactly what they cannot prove: that
// `os.Executable()` re-invocation, the per-run working directories and the
// report hand-back hold together. This file builds the binary and runs both
// paths for real over the hermetic tier-1 fixture.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// buildEvalBinary compiles cmd/eval so the child invocations exercise the real
// os.Executable() path.
func buildEvalBinary(t *testing.T) (binary, root string) {
	t.Helper()
	root = repoRoot(t)
	binary = filepath.Join(t.TempDir(), "eval")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/eval")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/eval: %v\n%s", err, out)
	}
	return binary, root
}

func runEval(t *testing.T, binary, root string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("run %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func loadReport(t *testing.T, path string) evalreport.FullRunReport {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var report evalreport.FullRunReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return report
}

// AC-7 + the real child path, in one build. The single-run invocation must be
// exactly what it was — one run, no series — and the series invocation must
// really spawn one process per run.
func TestColdHarness_EndToEndOverTheHermeticFixture(t *testing.T) {
	binary, root := buildEvalBinary(t)
	manifest := filepath.Join(root, "corpus", "manifest.json")

	t.Run("the default invocation is the unchanged single-run path", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "single.json")
		stdout, code := runEval(t, binary, root,
			"-manifest", manifest, "-full-run", "tier1-fixture-hero-go",
			"-runner-class", "test", "-out", out)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stdout)
		}
		report := loadReport(t, out)
		if report.ColdSeries != nil {
			t.Error("the default -full-run must not produce a cold series: the PR path does not start running ten times")
		}
		if report.RepoRunIndex != 0 {
			t.Error("a single run must not be labelled as a sample of a series")
		}
		if report.Repo.Index.WallclockMS <= 0 {
			t.Error("the single-run path stopped measuring the cold index")
		}
		// AC-1 also applies to the single-run path: coldness is recorded, and
		// a class with no declared protocol verifies on the fresh-store half
		// alone.
		if !report.Repo.Cold.Verified || report.Repo.Cold.StorePreexisting {
			t.Errorf("single run cold state = %+v", report.Repo.Cold)
		}
	})

	t.Run("a series really runs one process per run", func(t *testing.T) {
		dir := t.TempDir()
		out := filepath.Join(dir, "series.json")
		candidate := writeCandidateIndex(t, "0000000000000000000000000000000000000000")
		stdout, code := runEval(t, binary, root,
			"-manifest", manifest, "-full-run", "tier1-fixture-hero-go",
			"-cold-runs", "3", "-runner-class", "test",
			"-candidate", candidate,
			"-workdir", filepath.Join(dir, "work"), "-out", out)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stdout)
		}
		report := loadReport(t, out)
		if report.ColdSeries == nil {
			t.Fatal("no cold series in the report")
		}
		series := report.ColdSeries
		if series.RunsRequested != 3 || series.RunsCompleted != 3 || series.RunsAborted != 0 {
			t.Fatalf("run accounting = %d requested, %d completed, %d aborted\n%s",
				series.RunsRequested, series.RunsCompleted, series.RunsAborted, stdout)
		}
		for _, sample := range series.Runs {
			if sample.Status != evalreport.ColdRunCompleted {
				t.Fatalf("run %d: %s", sample.Run, sample.Error)
			}
			if sample.Index.WallclockMS <= 0 || sample.Index.Nodes == 0 {
				t.Errorf("run %d produced no measurement: %+v", sample.Run, sample.Index)
			}
			if !sample.Cold.Verified {
				t.Errorf("run %d was not verified cold: %s", sample.Run, sample.Cold.Reason)
			}
			if sample.Cold.StorePreexisting || sample.Cold.MetaPreexisting {
				t.Errorf("run %d reused state from an earlier run — the runs are not independent", sample.Run)
			}
			if sample.Commit == "" || sample.RunnerClass != "test" {
				t.Errorf("run %d is not stamped with its revision and runner class: %+v", sample.Run, sample)
			}
		}
		// Aggregates must reproduce from the samples the artifact carries.
		recomputed := evalreport.RecomputeColdAggregates(series.Runs)
		for metric, published := range series.Aggregates {
			if recomputed[metric] != published {
				t.Errorf("metric %q published %+v, recomputes to %+v", metric, published, recomputed[metric])
			}
		}
		// `repo` stays a single named sample for comparability.
		if report.RepoRunIndex == 0 || report.Repo.Index.WallclockMS <= 0 {
			t.Errorf("repo_run_index = %d: the series report must still carry one named single-run sample", report.RepoRunIndex)
		}
		// Without a contract there are no thresholds, so nothing may read PASS.
		if series.Status == evalreport.StatusPass {
			t.Error("a series with no reference-scenario contract must not read PASS")
		}
		if series.OOMCheck.Status == evalreport.StatusPass {
			t.Error("the OOM gate must not pass without being exercised")
		}
	})
}

// AC-7 as a workflow guard: the PR gate and the per-repo compatibility runs
// must not acquire `-cold-runs`, and the repeated measurement must live in a
// job of its own.
func TestColdHarness_WorkflowsKeepThePRPathSingleRun(t *testing.T) {
	root := repoRoot(t)

	evalYML := readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval.yml"))
	for _, forbidden := range []string{"-cold-runs", "-full-run", "-oom-check", "-drop-caches"} {
		if strings.Contains(evalYML, forbidden) {
			t.Errorf("eval.yml (the PR gate) must not run %s — the PR path stays a single hermetic suite", forbidden)
		}
	}

	path := filepath.Join(root, ".github", "workflows", "eval-full.yml")
	jobs := workflowJobs(t, readWorkflow(t, path))
	if body, ok := jobs["full-run"]; !ok {
		t.Fatal("eval-full.yml no longer has a full-run job")
	} else if strings.Contains(body, "-cold-runs") {
		t.Error("the per-repo full-run matrix must stay single-run: it enforces historical ceilings, and ten runs of each would change what it measures")
	}
	if body, ok := jobs["hero-suite"]; !ok {
		t.Fatal("eval-full.yml no longer has a hero-suite job")
	} else if strings.Contains(body, "-cold-runs") {
		t.Error("the hero suite must not become a repeated measurement")
	}

	series, ok := jobs["cold-index-series"]
	if !ok {
		t.Fatal("eval-full.yml has no cold-index-series job: FR-8's ten runs are not exercised anywhere")
	}
	for _, want := range []string{"-cold-runs 10", "-drop-caches", "-oom-check", "grpc-go", "-reference-scenario", "-candidate"} {
		if !strings.Contains(series, want) {
			t.Errorf("the cold-index-series job does not pass %s", want)
		}
	}
	// grpc-go is not in the fail-closed budget selection; passing -budgets
	// there would fail the run for a configuration reason and hide the gates.
	if strings.Contains(series, "-budgets") {
		t.Error("the cold-index series must be read against the reference-scenario gates, not the historical hero budgets")
	}
}

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// workflowJobs splits a workflow into its top-level jobs by indentation. A
// full YAML parser is deliberately not pulled in for a guard test: the default
// build carries no YAML dependency (see internal/evidence), and the property
// under test is textual.
func workflowJobs(t *testing.T, body string) map[string]string {
	t.Helper()
	jobs := map[string]string{}
	current := ""
	var buf strings.Builder
	inJobs := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		trimmed := strings.TrimSpace(line)
		isJobHeader := strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") &&
			strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "-")
		if isJobHeader {
			if current != "" {
				jobs[current] = buf.String()
			}
			current = strings.TrimSuffix(trimmed, ":")
			buf.Reset()
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	if current != "" {
		jobs[current] = buf.String()
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed from the workflow")
	}
	return jobs
}
