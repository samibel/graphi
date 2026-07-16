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

func TestReleaseGateWorkflowDownloadsModulesBeforeStrictGate(t *testing.T) {
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	workflow := filepath.Join(filepath.Dir(strings.TrimSpace(string(gomod))), ".github", "workflows", "release-gate.yml")
	raw, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read release-gate workflow: %v", err)
	}
	var definition struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &definition); err != nil {
		t.Fatalf("parse release-gate workflow: %v", err)
	}

	job, ok := definition.Jobs["release-gate"]
	if !ok {
		t.Fatal("release-gate workflow does not define jobs.release-gate")
	}
	download, gate := -1, -1
	for i, step := range job.Steps {
		switch strings.TrimSpace(step.Run) {
		case "go mod download":
			download = i
		case "go run ./cmd/release-gate -publish":
			gate = i
		}
	}
	if download < 0 || gate < 0 || download >= gate {
		t.Fatalf("jobs.release-gate must download modules before strict gate: download=%d gate=%d", download, gate)
	}
}
