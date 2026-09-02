package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/bench"
	"gopkg.in/yaml.v3"
)

func TestShellRunnerIncludesStdoutAndStderrOnFailure(t *testing.T) {
	runner := &shellRunner{
		name:    "diagnostic-probe",
		cmd:     os.Args[0],
		args:    []string{"-test.run=TestShellRunnerHelperProcess"},
		env:     append(os.Environ(), "GRAPHI_SHELL_RUNNER_HELPER=1"),
		timeout: 5 * time.Second,
	}

	_, err := runner.Run()
	if err == nil {
		t.Fatal("failing helper unexpectedly passed")
	}
	for _, marker := range []string{"stdout diagnostic", "stderr diagnostic"} {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("runner error %q missing %q", err, marker)
		}
	}
}

func TestShellRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GRAPHI_SHELL_RUNNER_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "stdout diagnostic")
	fmt.Fprintln(os.Stderr, "stderr diagnostic")
	os.Exit(1)
}

func TestDefaultBenchGateDoesNotRemeasureWallClockTimings(t *testing.T) {
	runner, ok := DefaultGates()["bench-budget"]
	if !ok {
		t.Fatal("DefaultGates omits bench-budget")
	}
	if shell, ok := runner.(*shellRunner); ok {
		t.Fatalf("bench-budget still shells out to the full timing harness: %s %v", shell.cmd, shell.args)
	}
	invariant, ok := runner.(*invariantBenchRunner)
	if !ok {
		t.Fatalf("bench-budget runner = %T, want invariantBenchRunner", runner)
	}
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	manifest, err := bench.LoadManifest(filepath.Join(filepath.Dir(strings.TrimSpace(string(gomod))), invariant.budgetPath))
	if err != nil {
		t.Fatalf("load repository benchmark manifest: %v", err)
	}
	projected, err := bench.EnvironmentIndependentManifest(manifest)
	if err != nil {
		t.Fatalf("project repository benchmark manifest: %v", err)
	}
	if len(projected.Metrics) != 7 {
		t.Fatalf("projected metric count = %d, want 7", len(projected.Metrics))
	}
	if binary := projected.Metrics["binary_size_bytes"]; binary.Severity != bench.SeverityFail {
		t.Fatalf("binary_size_bytes severity = %q, want %q", binary.Severity, bench.SeverityFail)
	}
	for _, timing := range []string{"cold_start_p95_ms", "full_index_ms", "balanced_index_ms"} {
		if _, ok := projected.Metrics[timing]; ok {
			t.Fatalf("release-gate manifest projection contains timing %q", timing)
		}
	}
}

func TestInvariantBenchRunnerIgnoresTimingsButEnforcesSizesAndCounts(t *testing.T) {
	dir := t.TempDir()
	budgetPath := filepath.Join(dir, "bench-budget.yml")
	manifest := `version: 1
baseline_version: "test"
fixture_digest: "fixture"
metrics:
  cold_start_p95_ms:
    baseline: 1
    budget: 1
    unit: ms
  binary_size_bytes:
    baseline: 90
    budget: 100
    unit: bytes
  fast_db_size_bytes:
    baseline: 80
    budget: 100
    unit: bytes
  fast_edge_count:
    baseline: 8
    budget: 10
    unit: count
  balanced_db_size_bytes:
    baseline: 80
    budget: 100
    unit: bytes
  balanced_edge_count:
    baseline: 8
    budget: 10
    unit: count
  deep_db_size_bytes:
    baseline: 80
    budget: 100
    unit: bytes
  deep_edge_count:
    baseline: 8
    budget: 10
    unit: count
`
	if err := os.WriteFile(budgetPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	base := func() bench.Metrics {
		return bench.Metrics{
			ColdStartP95MS:       1_000_000,
			FullIndexMS:          1_000_000,
			FreshnessLagMS:       1_000_000,
			BinarySizeBytes:      90,
			IncrementalTenFileMS: 1_000_000,
			BranchSwitchSimMS:    1_000_000,
			FixtureDigest:        "fixture",
			ProfileMetrics: map[string]bench.ProfileMetric{
				"fast":     {IndexMS: 1_000_000, DBSizeBytes: 80, EdgeCount: 8, QueryLatencyMS: 1_000_000},
				"balanced": {IndexMS: 1_000_000, DBSizeBytes: 80, EdgeCount: 8, QueryLatencyMS: 1_000_000},
				"deep":     {IndexMS: 1_000_000, DBSizeBytes: 80, EdgeCount: 8, QueryLatencyMS: 1_000_000},
			},
		}
	}
	tests := []struct {
		name    string
		mutate  func(*bench.Metrics)
		wantErr string
	}{
		{name: "catastrophic timings do not block", mutate: func(*bench.Metrics) {}},
		{name: "binary size still blocks", mutate: func(m *bench.Metrics) { m.BinarySizeBytes = 101 }, wantErr: "binary_size_bytes"},
		{name: "graph count still blocks when manifest severity is fail", mutate: func(m *bench.Metrics) {
			pm := m.ProfileMetrics["fast"]
			pm.EdgeCount = 11
			m.ProfileMetrics["fast"] = pm
		}, wantErr: "fast_edge_count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := base()
			tt.mutate(&metrics)
			runner := &invariantBenchRunner{
				budgetPath: budgetPath,
				timeout:    time.Second,
				score:      100,
				run: func(context.Context, bench.HarnessConfig) (bench.Metrics, error) {
					return metrics, nil
				},
			}
			score, err := runner.Run()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("timing-only regression blocked invariant gate: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("runner error = %v, want %q named", err, tt.wantErr)
			case tt.wantErr == "" && score != 100:
				t.Fatalf("score = %v, want 100", score)
			}
			if err != nil && strings.Contains(err.Error(), "cold_start_p95_ms") {
				t.Fatalf("timing metric leaked into invariant verdict: %v", err)
			}
		})
	}
}

func readReleaseGateWorkflow(t *testing.T) string {
	t.Helper()
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	workflow := filepath.Join(filepath.Dir(strings.TrimSpace(string(gomod))), ".github", "workflows", "release-gate.yml")
	raw, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read release-gate workflow: %v", err)
	}
	return string(raw)
}

type workflowJob struct {
	Steps []struct {
		Run string            `yaml:"run"`
		Env map[string]string `yaml:"env"`
	} `yaml:"steps"`
}

func releaseGateJob(t *testing.T) workflowJob {
	t.Helper()
	var definition struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readReleaseGateWorkflow(t)), &definition); err != nil {
		t.Fatalf("parse release-gate workflow: %v", err)
	}
	job, ok := definition.Jobs["release-gate"]
	if !ok {
		t.Fatal("release-gate workflow does not define jobs.release-gate")
	}
	return job
}

func TestReleaseGateWorkflowDownloadsModulesBeforeStrictGate(t *testing.T) {
	job := releaseGateJob(t)
	download, gate := -1, -1
	for i, step := range job.Steps {
		switch {
		case strings.TrimSpace(step.Run) == "go mod download":
			download = i
		case strings.Contains(step.Run, "go run ./cmd/release-gate"):
			gate = i
		}
	}
	if download < 0 || gate < 0 || download >= gate {
		t.Fatalf("jobs.release-gate must download modules before strict gate: download=%d gate=%d", download, gate)
	}
}

// TestReleaseGateWorkflowPassesTheContextAndHoldsNoPolicy — AC-1's other half.
//
// The whole point of SW-251 is WHERE the decision lives. The workflow may state
// where it is running; it may not decide what that costs. This test fails if
// somebody moves the rule back into YAML — by tolerating a non-zero gate exit,
// by branching on an exit code, or by re-implementing the publish assertion the
// gate now makes itself.
func TestReleaseGateWorkflowPassesTheContextAndHoldsNoPolicy(t *testing.T) {
	job := releaseGateJob(t)

	var gate string
	var gateEnv map[string]string
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "go run ./cmd/release-gate") {
			gate = step.Run
			gateEnv = step.Env
		}
	}
	if gate == "" {
		t.Fatal("jobs.release-gate never runs cmd/release-gate")
	}
	if !strings.Contains(gate, "-context=") {
		t.Fatalf("the release gate must be told which context it runs in: %q", gate)
	}
	expr, ok := gateEnv["GATE_CONTEXT"]
	if !ok {
		t.Fatalf("the context must come from the workflow's own event, not be hardcoded: env %v", gateEnv)
	}
	for _, want := range []string{"github.event_name", "pull_request", "'pr'", "'release'"} {
		if !strings.Contains(expr, want) {
			t.Fatalf("GATE_CONTEXT %q does not derive the context from the event (missing %q)", expr, want)
		}
	}

	raw := readReleaseGateWorkflow(t)
	for _, forbidden := range []string{
		"continue-on-error", // a gate the workflow is allowed to ignore is not a gate
		"|| true",
		"exit 3",  // the UNVERIFIED exit code must not be interpreted here
		"test -f", // the publish assertion moved into Publish, where it can tell
		// a refusal from a silent no-op
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("release-gate.yml contains %q — the blocking policy belongs in "+
				"cmd/release-gate/policy.go, not in a workflow file", forbidden)
		}
	}
}
