package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
