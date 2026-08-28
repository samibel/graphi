package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// It does not decide what UNVERIFIED means for a PR or a release. It reports a
// state; policy.go decides what that state costs, and the two are kept apart on
// purpose so that an instrument can never quietly become a policy. UNVERIFIED
// is returned as *UnverifiedError and an unusable answer as *GateError; the
// classification of those two into the four states, and the blocking decision
// over them, both live in policy.go.
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
		// The child never produced an exit code: machinery, not measurement.
		return 0, &GateError{Detail: fmt.Sprintf("testgate: %v: %s", err, strings.TrimSpace(stderr))}
	}

	var res testgate.EvaluateResult
	dec := json.NewDecoder(bytes.NewReader(stdout))
	if err := dec.Decode(&res); err != nil {
		// Fail closed. An unreadable verdict is not an absent verdict; the
		// only safe reading of "the gate produced something I cannot parse"
		// is that the run was not validated.
		return 0, &GateError{Detail: fmt.Sprintf("testgate: unreadable verdict (exit %d): %v: %s",
			exitCode, err, strings.TrimSpace(stderr))}
	}

	// The exit code and the parsed verdict are two independent statements about
	// the same run, and they must agree. A disagreement means one of them is
	// lying, and the gate cannot tell which.
	//
	// The child's stderr carries FormatVerdict's prose, which names the gate and
	// the reason code. It is included here because without it this message is
	// undiagnosable: PR #177's release-gate run printed exactly this line and
	// nothing else, so which gate reported UNVERIFIED could not be recovered
	// from the CI log at all.
	if want := testgate.ExitCode(res); want != exitCode {
		return 0, &GateError{Detail: fmt.Sprintf(
			"testgate: verdict %q implies exit %d but the process exited %d; refusing to guess which is right: %s",
			res.Verdict, want, exitCode, strings.TrimSpace(stderr))}
	}

	switch res.Verdict {
	case testgate.VerdictGreen:
		return r.score, nil
	case testgate.VerdictUnverified:
		// Reported, not decided. SW-251 owns the policy; this story owns the
		// transport, and silently blocking here would be taking that decision.
		return r.score, &UnverifiedError{Detail: formatUnverified(res)}
	case testgate.VerdictNotGreen:
		// A real failure of the thing measured: FAIL, blocking everywhere.
		return 0, fmt.Errorf("testgate: %s", strings.TrimSpace(testgate.FormatVerdict(res)))
	case testgate.VerdictError:
		// The suite could not produce a usable answer at all — a broken
		// instrument, not a broken subject. ERROR, blocking everywhere.
		return 0, &GateError{Detail: "testgate: " + strings.TrimSpace(testgate.FormatVerdict(res))}
	default:
		return 0, &GateError{Detail: fmt.Sprintf("testgate: unknown verdict %q", res.Verdict)}
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
		". This is NOT evidence that what these gates measure is healthy. " +
		"Whether it blocks is decided by the execution context, not by this runner: " +
		"see SW-251's policy in cmd/release-gate/policy.go — on a pull request it does not block, " +
		"on main and the release path it does"
}

// runTestgateJSON builds cmd/testgate once and executes the resulting binary.
// It deliberately does NOT use `go run`.
//
// # Why not `go run` (SW-250 rebuild round 2)
//
// `go run` does not propagate a child's exit code. It reports every non-zero
// child status as the prose line "exit status N" on ITS stderr and then exits 1
// itself. Reproduced with a throwaway program that exits with a chosen code:
//
//	go run . 1 -> exit 1   (stderr: exit status 1)
//	go run . 2 -> exit 1   (stderr: exit status 2)
//	go run . 3 -> exit 1   (stderr: exit status 3)
//	./probe  1 -> exit 1
//	./probe  2 -> exit 2
//	./probe  3 -> exit 3
//
// Only 0 and 1 survive `go run`, so of testgate's four verdicts only GREEN and
// NOT GREEN could cross this boundary. ERROR (2) and UNVERIFIED (3) both
// arrived as 1 and contradicted the parsed verdict, and the cross-check in Run
// correctly refused to guess — which is how PR #177's release-gate failed with
// `verdict "UNVERIFIED" implies exit 3 but the process exited 1`. The guard was
// right; the transport was wrong.
//
// Two alternatives were considered and rejected:
//
//   - Trust the parsed JSON verdict and drop the exit-code cross-check. That
//     deletes the only thing that noticed this defect, and leaves the gate with
//     one unchecked statement about the run instead of two that must agree.
//
//   - Special-case "exit 1 with `exit status 3` on stderr". That is a prose
//     match on a Go toolchain message — the same fragility this story rejected
//     when it refused to recognise UNVERIFIED by matching skip text. The
//     toolchain is free to reword it, and a test that printed that phrase would
//     forge a verdict.
//
// Building and executing makes the exit code real, so both statements are
// genuine and the cross-check keeps its teeth. cmd/release-gate already uses
// this shape for privacyRunner, for a different reason (see runners.go).
func runTestgateJSON(ctx context.Context) ([]byte, string, int, error) {
	dir, err := os.MkdirTemp("", "graphi-release-gate-testgate-*")
	if err != nil {
		return nil, "", 0, fmt.Errorf("temp dir: %w", err)
	}
	// The artifact lives outside the repository and is removed on the way out,
	// so nothing is left in the worktree for a later gate to trip over.
	defer os.RemoveAll(dir)
	return buildAndRun(ctx, "", filepath.Join(dir, "testgate"), "./cmd/testgate",
		"-json", "-timeout", "15m")
}

// buildAndRun compiles pkg into bin and then executes bin with args, returning
// the child's stdout, its stderr, and its REAL exit code. A non-nil error means
// the child could not be built or did not produce an exit code at all.
//
// dir is the working directory for both commands; "" inherits this process's,
// which is what production wants — testgate discovers its own targets with
// `go list ./...` relative to the repository root, exactly as it did under
// `go run`. bin must be an absolute path.
//
// Build and execution share the caller's context, so the compile is charged to
// the same budget it was charged to inside `go run`; the total time available
// to the gate is unchanged. Unlike `go run`, the process this context kills on
// timeout is testgate itself rather than a `go` parent holding it.
func buildAndRun(ctx context.Context, dir, bin, pkg string, args ...string) ([]byte, string, int, error) {
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, pkg)
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buildErr bytes.Buffer
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		return nil, buildErr.String(), 0, fmt.Errorf("build %s: %w", pkg, err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
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
