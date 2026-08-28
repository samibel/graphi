package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/testgate"
)

// testgateRunner runs cmd/testgate and reads its STRUCTURED verdict.
//
// # Why this exists (SW-250's transport decision)
//
// `EvaluateResult` has carried a machine-readable summary since SW-249, but
// nothing could get it out of the testgate binary: `cmd/testgate/main.go`
// printed only `FormatVerdict` prose, and this gate shelled out with a generic
// shellRunner and read the exit code alone. Recognising an UNVERIFIED marker is
// worthless if the result cannot cross the process boundary, so the choice was
// between adding `-json` to cmd/testgate and moving this runner in-process.
//
// `-json` won, for three reasons:
//
//  1. In-process would require moving testgate's producer logic (first-party
//     target discovery, CGO_ENABLED=0 env, stdout/stderr/exit capture) out of
//     package main and into internal/testgate so this binary could call it.
//     That is a refactor of the one component whose job is to fail closed, done
//     in the same change that gives it a new verdict — two risks at once.
//
//  2. The verdict is worth publishing to more consumers than this binary. A
//     `-json` output is readable by the standalone test-gate workflow, by a
//     human, and by whatever SW-251 builds; an in-process call is readable only
//     here.
//
//  3. The extra benefit claimed for in-process does not hold on inspection. The
//     double full-suite execution that produced two contradictory verdicts for
//     the same commit on PR #175 is the standalone `test-gate` workflow running
//     the suite AND this gate running it again — both triggered on
//     `pull_request`. Calling the evaluator in-process would remove one `go run`
//     compile, not one suite execution. Removing the duplication is the release
//     DAG split, which the spec deliberately defers to the backlog.
//
// # What this runner does NOT decide
//
// It does not decide what UNVERIFIED means for a PR or a release — that is
// SW-251. UNVERIFIED is therefore returned as *UnverifiedError, which this gate
// already records as a non-blocking warning. That is deliberately the same
// release outcome as today, where a skip read as a pass: this story makes the
// state visible and named, and changes no one's blocking status. A FAIL still
// blocks, and so does an ERROR.
type testgateRunner struct {
	timeout time.Duration
	score   float64
	// exec is the process invocation, injectable for tests. It returns the
	// child's stdout, its stderr, and its exit code. A non-nil error means the
	// child could not be run or did not produce an exit code at all.
	exec func(ctx context.Context) (stdout []byte, stderr string, exitCode int, err error)
}

func (r *testgateRunner) Run() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	runFn := r.exec
	if runFn == nil {
		runFn = runTestgateJSON
	}
	stdout, stderr, exitCode, err := runFn(ctx)
	if err != nil {
		return 0, fmt.Errorf("testgate: %w: %s", err, strings.TrimSpace(stderr))
	}

	var res testgate.EvaluateResult
	dec := json.NewDecoder(bytes.NewReader(stdout))
	if err := dec.Decode(&res); err != nil {
		// Fail closed. An unreadable verdict is not an absent verdict; the
		// only safe reading of "the gate produced something I cannot parse"
		// is that the run was not validated.
		return 0, fmt.Errorf("testgate: unreadable verdict (exit %d): %v: %s",
			exitCode, err, strings.TrimSpace(stderr))
	}

	// The exit code and the parsed verdict are two independent statements about
	// the same run, and they must agree. A disagreement means one of them is
	// lying, and the gate cannot tell which.
	if want := testgate.ExitCode(res); want != exitCode {
		return 0, fmt.Errorf(
			"testgate: verdict %q implies exit %d but the process exited %d; refusing to guess which is right",
			res.Verdict, want, exitCode)
	}

	switch res.Verdict {
	case testgate.VerdictGreen:
		return r.score, nil
	case testgate.VerdictUnverified:
		// Reported, not decided. SW-251 owns the policy; this story owns the
		// transport, and silently blocking here would be taking that decision.
		return r.score, &UnverifiedError{Detail: formatUnverified(res)}
	case testgate.VerdictNotGreen, testgate.VerdictError:
		return 0, fmt.Errorf("testgate: %s", strings.TrimSpace(testgate.FormatVerdict(res)))
	default:
		return 0, fmt.Errorf("testgate: unknown verdict %q", res.Verdict)
	}
}

// formatUnverified names every gate that reported it could not measure, with
// the reason code and the numbers behind it, so the warning is a report and not
// an excuse.
func formatUnverified(res testgate.EvaluateResult) string {
	parts := make([]string, 0, len(res.Unverified))
	for _, gate := range res.Unverified {
		parts = append(parts, fmt.Sprintf("%s (%s.%s) reason=%s measurements=%v",
			gate.GateID, gate.Package, gate.Test, gate.ReasonCode, gate.Measurements))
	}
	return "test suite reported no failure, but " + strings.Join(parts, "; ") +
		". This is NOT evidence that what these gates measure is healthy; " +
		"what an unverified measurement means for a release is SW-251's decision, not this runner's"
}

func runTestgateJSON(ctx context.Context) ([]byte, string, int, error) {
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/testgate", "-json", "-timeout", "15m")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), stderr.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return stdout.Bytes(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.Bytes(), stderr.String(), 0, err
}
