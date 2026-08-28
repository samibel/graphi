package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/testgate"
)

func testgateRunnerWith(res testgate.EvaluateResult, stderr string) *testgateRunner {
	return &testgateRunner{
		timeout: time.Second,
		score:   100,
		exec: func(context.Context) ([]byte, string, int, error) {
			encoded, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			return append(encoded, '\n'), stderr, testgate.ExitCode(res), nil
		},
	}
}

func TestTestgateRunnerGreenPasses(t *testing.T) {
	score, err := testgateRunnerWith(testgate.EvaluateResult{Verdict: testgate.VerdictGreen, Green: true}, "").Run()
	if err != nil || score != 100 {
		t.Fatalf("green run = (%v, %v), want (100, nil)", score, err)
	}
}

func TestTestgateRunnerNotGreenBlocks(t *testing.T) {
	res := testgate.EvaluateResult{
		Verdict:         testgate.VerdictNotGreen,
		UnexpectedFails: []string{"github.com/samibel/graphi/surfaces.TestDaemonLifecycle"},
	}
	_, err := testgateRunnerWith(res, "").Run()
	if err == nil {
		t.Fatal("a NOT GREEN suite must block the release")
	}
	var unverified *UnverifiedError
	if errors.As(err, &unverified) {
		t.Fatalf("a failure was laundered into a warning: %v", err)
	}
	if !strings.Contains(err.Error(), "TestDaemonLifecycle") {
		t.Fatalf("the failure lost its name: %v", err)
	}
}

// The verdict crosses the process boundary intact: the gate id, the reason code
// and the raw measurements are all in the warning, so the record says what
// could not be measured and on what numbers.
func TestTestgateRunnerUnverifiedIsReportedNotDecided(t *testing.T) {
	res := testgate.EvaluateResult{
		Verdict: testgate.VerdictUnverified,
		Unverified: []testgate.UnverifiedGate{{
			GateID:       "ax06_executor_seam_latency",
			Package:      "github.com/samibel/graphi/surfaces/client",
			Test:         "TestAX06_ExecutorSeamLatencyWithinThreshold",
			ReasonCode:   "control_above_ceiling",
			Measurements: map[string]float64{"ceiling_us": 750, "control_delta_us": 412.5},
		}},
	}
	score, err := testgateRunnerWith(res, "").Run()
	if score != 100 {
		t.Fatalf("score = %v, want the informational 100 (the score is not the verdict)", score)
	}
	var unverified *UnverifiedError
	if !errors.As(err, &unverified) {
		t.Fatalf("UNVERIFIED must reach the gate as an UnverifiedError, got %v", err)
	}
	for _, fragment := range []string{
		"ax06_executor_seam_latency",
		"control_above_ceiling",
		"ceiling_us:750",
		"SW-251",
	} {
		if !strings.Contains(unverified.Detail, fragment) {
			t.Fatalf("warning is missing %q: %s", fragment, unverified.Detail)
		}
	}

	// And the gate records it as a warning rather than a blocker: SW-250
	// transports the state, SW-251 decides who it blocks. Changing that here
	// would be taking SW-251's decision inside SW-250.
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})
	gates := allPassGates()
	gates["testgate"] = testgateRunnerWith(res, "")
	result, err := Run(gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("SW-250 must not decide the policy SW-251 owns; errors = %v", result.Errors)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "ax06_executor_seam_latency") {
		t.Fatalf("the unverified measurement is not in the record: %v", result.Warnings)
	}
}

// Fail-closed at the boundary: an unreadable verdict is not an absent verdict.
func TestTestgateRunnerUnreadableVerdictBlocks(t *testing.T) {
	runner := &testgateRunner{
		timeout: time.Second,
		score:   100,
		exec: func(context.Context) ([]byte, string, int, error) {
			return []byte("test gate: GREEN — complete go test stream contains no failures\n"), "", 0, nil
		},
	}
	_, err := runner.Run()
	if err == nil {
		t.Fatal("prose on stdout must not be read as a green verdict")
	}
	if !strings.Contains(err.Error(), "unreadable verdict") {
		t.Fatalf("error = %v, want an unreadable-verdict diagnostic", err)
	}
}

// The exit code and the parsed verdict are independent statements about the
// same run. A disagreement is fail-closed, not a preference for one of them.
func TestTestgateRunnerExitCodeMustAgreeWithTheVerdict(t *testing.T) {
	runner := &testgateRunner{
		timeout: time.Second,
		score:   100,
		exec: func(context.Context) ([]byte, string, int, error) {
			encoded, err := json.Marshal(testgate.EvaluateResult{
				Verdict:         testgate.VerdictNotGreen,
				UnexpectedFails: []string{"pkg.TestBroken"},
			})
			if err != nil {
				panic(err)
			}
			return encoded, "", 0, nil // claims failure, exits 0
		},
	}
	_, err := runner.Run()
	if err == nil {
		t.Fatal("a verdict that contradicts its own exit code must block")
	}
	if !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("error = %v, want the contradiction named", err)
	}
}

func TestTestgateRunnerProcessFailureBlocks(t *testing.T) {
	runner := &testgateRunner{
		timeout: time.Second,
		score:   100,
		exec: func(context.Context) ([]byte, string, int, error) {
			return nil, "go: toolchain unavailable", 0, errors.New("exec: \"go\": executable file not found in $PATH")
		},
	}
	if _, err := runner.Run(); err == nil || !strings.Contains(err.Error(), "toolchain unavailable") {
		t.Fatalf("a child that could not run must block with its diagnostics, got %v", err)
	}
}
