package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
			// claims failure, exits 0; the prose stderr is what an operator
			// needs to see to know WHICH gate the contradiction is about.
			return encoded, "test gate: NOT GREEN\n  test/package/build failures: [pkg.TestBroken]\n", 0, nil
		},
	}
	_, err := runner.Run()
	if err == nil {
		t.Fatal("a verdict that contradicts its own exit code must block")
	}
	if !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("error = %v, want the contradiction named", err)
	}
	// PR #177's release-gate log carried this line and nothing else, so which
	// gate had reported UNVERIFIED could not be recovered from CI at all. The
	// child's prose must travel with the contradiction.
	if !strings.Contains(err.Error(), "pkg.TestBroken") {
		t.Fatalf("the contradiction is undiagnosable without the child's prose: %v", err)
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

// ---------------------------------------------------------------------------
// The invocation path itself (SW-250 rebuild round 2).
//
// Every test above injects `exec`, so none of them exercised how release-gate
// actually reaches testgate — which is precisely how a transport that cannot
// carry an exit code shipped. These two do exercise it.
// ---------------------------------------------------------------------------

// writeExitCodeProbe writes a throwaway main package whose only job is to print
// a payload on stdout, an optional payload on stderr, and exit with a chosen
// code. It is the smallest thing that can tell an invocation path which
// propagates exit codes apart from one which does not.
func writeExitCodeProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module exitcodeprobe\n\ngo 1.22\n")
	// Its own workspace file, so the probe builds the same way wherever
	// t.TempDir() lands. Without it, a go.work anywhere ABOVE the temp
	// directory (this repository has one at its root) makes the build fail with
	// "no modules were found in the current workspace" — verified by putting a
	// go.work in an ancestor and watching it break, then adding this and
	// watching it pass.
	write("go.work", "go 1.22\n\nuse .\n")
	write("main.go", `package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	code, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe: bad exit code")
		os.Exit(9)
	}
	fmt.Fprint(os.Stdout, os.Args[2])
	if len(os.Args) > 3 {
		fmt.Fprint(os.Stderr, os.Args[3])
	}
	os.Exit(code)
}
`)
	return dir
}

// AC-3's "distinct exit code" is only real if it survives the invocation.
// testgate's four verdicts are exit 0/1/2/3; `go run` collapses every non-zero
// child status to 1 and prints "exit status N" on its own stderr instead, so
// under `go run` this test passes for GREEN and NOT GREEN and FAILS for ERROR
// and UNVERIFIED. See runTestgateJSON's comment for the reproduction.
func TestBuildAndRunPropagatesEveryVerdictExitCode(t *testing.T) {
	probe := writeExitCodeProbe(t)
	bin := filepath.Join(t.TempDir(), "probe")

	for _, res := range []testgate.EvaluateResult{
		{Verdict: testgate.VerdictGreen, Green: true},
		{Verdict: testgate.VerdictNotGreen},
		{Verdict: testgate.VerdictError},
		{Verdict: testgate.VerdictUnverified},
	} {
		want := testgate.ExitCode(res)
		t.Run(string(res.Verdict), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			payload := "stdout-of-" + string(res.Verdict)
			stdout, stderr, code, err := buildAndRun(ctx, probe, bin, ".",
				strconv.Itoa(want), payload, "prose-of-"+string(res.Verdict))
			if err != nil {
				t.Fatalf("buildAndRun: %v: %s", err, stderr)
			}
			if code != want {
				t.Fatalf("verdict %q implies exit %d but the invocation reported %d; "+
					"an invocation that cannot carry the code makes AC-3's distinct exit code fiction",
					res.Verdict, want, code)
			}
			if string(stdout) != payload {
				t.Fatalf("stdout = %q, want %q", stdout, payload)
			}
			if !strings.Contains(stderr, "prose-of-"+string(res.Verdict)) {
				t.Fatalf("stderr lost the child's prose: %q", stderr)
			}
		})
	}
}

// The defect PR #177's release-gate hit, at the level it hit it: an UNVERIFIED
// verdict produced by a real process and read by the real runner. Only the
// program being executed is substituted — the runner, the JSON decode, the
// exit-code cross-check and the invocation helper are all the production ones.
//
// Against the `go run` invocation this fails with the CI message verbatim:
// `verdict "UNVERIFIED" implies exit 3 but the process exited 1; refusing to
// guess which is right`.
func TestUnverifiedSurvivesARealProcessBoundary(t *testing.T) {
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
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	probe := writeExitCodeProbe(t)
	bin := filepath.Join(t.TempDir(), "probe")
	runner := &testgateRunner{
		timeout: 2 * time.Minute,
		score:   100,
		exec: func(ctx context.Context) ([]byte, string, int, error) {
			return buildAndRun(ctx, probe, bin, ".",
				strconv.Itoa(testgate.ExitCode(res)),
				string(encoded)+"\n",
				strings.TrimSpace(testgate.FormatVerdict(res)))
		},
	}

	score, runErr := runner.Run()
	var unverified *UnverifiedError
	if !errors.As(runErr, &unverified) {
		t.Fatalf("UNVERIFIED did not survive the process boundary: %v", runErr)
	}
	if score != 100 {
		t.Fatalf("score = %v, want the informational 100", score)
	}
	for _, fragment := range []string{"ax06_executor_seam_latency", "control_above_ceiling", "SW-251"} {
		if !strings.Contains(unverified.Detail, fragment) {
			t.Fatalf("the report lost %q: %s", fragment, unverified.Detail)
		}
	}
}
