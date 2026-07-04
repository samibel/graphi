package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

// DefaultGates returns the hard constituent gates: each must succeed for a
// release, but none contributes a score — the 9/10 verdict comes from the
// measured eval scorecard report (DefaultEvalReport) and the measured web
// suite (DefaultUX).
func DefaultGates() map[string]Runner {
	return map[string]Runner{
		"testgate": &shellRunner{
			name:    "testgate",
			cmd:     "go",
			args:    []string{"run", "./cmd/testgate", "-timeout", "15m"},
			env:     append(os.Environ(), "CGO_ENABLED=0"),
			timeout: 16 * time.Minute,
			score:   100,
		},
		"coverage": &shellRunner{
			name:    "coverage",
			cmd:     "go",
			args:    []string{"run", "./cmd/coverage", "-check"},
			timeout: 5 * time.Minute,
			score:   100,
		},
		"privacy": &privacyRunner{
			timeout: 5 * time.Minute,
			score:   100,
		},
		"bench-budget": &shellRunner{
			name:    "bench-budget",
			cmd:     "go",
			args:    []string{"run", "./cmd/bench", "-budget", "bench/bench-budget.yml"},
			timeout: 10 * time.Minute,
			score:   100,
		},
	}
}

// DefaultEvalReport runs the Tier-1 eval scorecard (measured area scores,
// signal/perf/setup metrics, regressions vs baseline) and parses the report.
func DefaultEvalReport() (evalreport.Report, error) {
	dir, err := os.MkdirTemp("", "graphi-release-gate-eval-*")
	if err != nil {
		return evalreport.Report{}, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "report.json")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/eval",
		"-manifest", "corpus/manifest.json", "-tier", "1", "-out", out)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// A Tier-1 regression exits non-zero but still writes the report; the gate
	// blocks on the report's regression list, so only a MISSING report is fatal.
	runErr := cmd.Run()
	raw, err := os.ReadFile(out)
	if err != nil {
		if runErr != nil {
			return evalreport.Report{}, fmt.Errorf("eval run: %v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return evalreport.Report{}, fmt.Errorf("read eval report: %w", err)
	}
	var report evalreport.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return evalreport.Report{}, fmt.Errorf("parse eval report: %w", err)
	}
	return report, nil
}

// requiredUXSuites are the UX scenarios the PRD names, mapped to their web
// test suites: symbol search, why-connected (edge + two-node compare),
// agent-context export, and the agent-tool panel incl. confidence/evidence
// and unavailable/partial states.
var requiredUXSuites = []string{
	"SymbolSearchPanel.test.tsx",
	"WhyConnectedPanel.test.tsx",
	"CompareConnectionPanel.test.tsx",
	"ExportAgentContext.test.tsx",
	"AgentToolsPanel.test.tsx",
}

// DefaultUX runs the web test suite with the JSON reporter and derives the
// measured ux score: pass fraction × required-suite presence fraction.
func DefaultUX() (evalreport.UXMetrics, error) {
	dir, err := os.MkdirTemp("", "graphi-release-gate-ux-*")
	if err != nil {
		return evalreport.UXMetrics{}, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "vitest.json")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", "vitest", "run", "--reporter=json", "--outputFile="+out)
	cmd.Dir = filepath.Join(".", "web")
	cmd.Env = append(os.Environ(), "CI=true", "NO_UPDATE_NOTIFIER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// A failing suite still writes the JSON report; the pass fraction carries
	// the failure into the score, so only a missing report is fatal.
	runErr := cmd.Run()
	raw, err := os.ReadFile(out)
	if err != nil {
		if runErr != nil {
			return evalreport.UXMetrics{}, fmt.Errorf("web tests: %v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return evalreport.UXMetrics{}, fmt.Errorf("read vitest report: %w", err)
	}
	var parsed struct {
		NumTotalTests  int `json:"numTotalTests"`
		NumPassedTests int `json:"numPassedTests"`
		TestResults    []struct {
			Name string `json:"name"`
		} `json:"testResults"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return evalreport.UXMetrics{}, fmt.Errorf("parse vitest report: %w", err)
	}
	files := make([]string, 0, len(parsed.TestResults))
	for _, tr := range parsed.TestResults {
		files = append(files, tr.Name)
	}
	return DeriveUX(parsed.NumTotalTests, parsed.NumPassedTests, files), nil
}

// DeriveUX computes the ux metrics from suite counts and the executed test
// files. Score = pass fraction × required-suite presence fraction × 100.
func DeriveUX(total, passed int, files []string) evalreport.UXMetrics {
	m := evalreport.UXMetrics{TotalTests: total, PassedTests: passed}
	for _, want := range requiredUXSuites {
		found := false
		for _, f := range files {
			if strings.HasSuffix(f, want) {
				found = true
				break
			}
		}
		if found {
			m.RequiredFound = append(m.RequiredFound, want)
		} else {
			m.RequiredMissing = append(m.RequiredMissing, want)
		}
	}
	sort.Strings(m.RequiredFound)
	sort.Strings(m.RequiredMissing)
	if total > 0 {
		passFraction := float64(passed) / float64(total)
		presence := float64(len(m.RequiredFound)) / float64(len(requiredUXSuites))
		m.Score = passFraction * presence * 100
	}
	return m
}

type shellRunner struct {
	name    string
	cmd     string
	args    []string
	env     []string
	timeout time.Duration
	score   float64
}

func (r *shellRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.cmd, r.args...)
	cmd.Env = r.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%s: %w: %s", r.name, err, strings.TrimSpace(stderr.String()))
	}
	return r.score, nil
}

// privacyRunner runs `graphi privacy-audit` with the elevation the audit's
// zero-outbound check needs: creating the loopback-only network namespace
// requires CAP_SYS_ADMIN, and without it the audit reports UNVERIFIED, which
// by design exits non-zero (false-green prevention). It mirrors the dedicated
// privacy-audit workflow: build the binary once, then execute it — under
// passwordless sudo when the process is not already root (the GitHub runner
// case). `go run` under sudo would resolve the toolchain as root, so the
// build/execute split is deliberate.
type privacyRunner struct {
	timeout time.Duration
	score   float64
}

func (r *privacyRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "graphi-release-gate-*")
	if err != nil {
		return 0, fmt.Errorf("privacy: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "graphi")

	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/graphi")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buildErr bytes.Buffer
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		return 0, fmt.Errorf("privacy: build graphi: %w: %s", err, strings.TrimSpace(buildErr.String()))
	}

	argv := []string{bin, "privacy-audit"}
	if os.Geteuid() != 0 && sudoAvailable(ctx) {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("privacy: %w: %s%s", err, strings.TrimSpace(out.String()), strings.TrimSpace(stderr.String()))
	}
	return r.score, nil
}

// sudoAvailable probes for passwordless sudo (the GitHub-runner case) without
// ever prompting.
func sudoAvailable(ctx context.Context) bool {
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}
