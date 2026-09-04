// Command testgate runs the default test suite under CGO_ENABLED=0 and requires
// a complete all-green result. No expected test failure is accepted: named
// tests, package/setup failures, build failures, producer stderr, and producer
// status inconsistencies all fail closed.
//
// Usage:
//
//	go run ./cmd/testgate
//	go run ./cmd/testgate -json
//	go run ./cmd/testgate -target ./internal/example
//	go run ./cmd/testgate -stdin -producer-exit-code 0 < go-test-events.json
//	go run ./cmd/testgate -remeasure-unverified
//
// Direct mode is recommended because testgate captures stdout, stderr, and the
// go test exit status itself. Stdin mode requires the producer's recorded exit
// code explicitly; a plain shell pipeline cannot safely communicate it.
//
// -remeasure-unverified (SW-275, direct mode only): when the full run is
// UNVERIFIED — complete, no failure, but a gate reported it could not measure
// under the load of the parallel suite — the reporting gates' packages are run
// again alone (-count=1 -p 1) and that isolated run's own verdict is reported,
// with the first report carried as `remeasured`. Only UNVERIFIED is ever
// re-measured: a failure is never re-run, and a re-measurement that is itself
// UNVERIFIED or NOT GREEN stands as such.
//
// Exit codes are the four verdicts, not a boolean (SW-250):
//
//	0  GREEN       — a complete stream with no failures and nothing unverified
//	1  NOT GREEN   — at least one test, package, build or producer failure
//	2  ERROR       — the gate could not obtain or validate the run, or the
//	                 UNVERIFIED channel carried something it will not interpret
//	3  UNVERIFIED  — no failure, but a gate reported it could not measure
//
// With -json the machine-readable verdict goes to stdout and the human prose
// to stderr, so a caller can read the structured result without parsing prose
// while an operator still sees the same words in the log. Without -json the
// prose goes to stdout, unchanged.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/testgate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// goTestRunner executes `go test` with the given arguments (everything after
// the `go`) and returns the JSON stream and the producer status. A non-nil
// error means the run could not be OBTAINED at all — it did not complete, or
// could not start — which is the ERROR verdict, not a producer failure.
type goTestRunner func(ctx context.Context, env, args []string) ([]byte, testgate.ProducerStatus, error)

func runGoTest(ctx context.Context, env, args []string) ([]byte, testgate.ProducerStatus, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	status := testgate.ProducerStatus{Stderr: errBuf.String()}
	if err == nil {
		return outBuf.Bytes(), status, nil
	}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		if status.Stderr != "" {
			return nil, status, fmt.Errorf("go test did not complete: %v\ntestgate: go test stderr: %s", ctx.Err(), status.Stderr)
		}
		return nil, status, fmt.Errorf("go test did not complete: %v", ctx.Err())
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		status.ExitCode = exitErr.ExitCode()
		return outBuf.Bytes(), status, nil
	case errors.As(err, &exitErr):
		return nil, status, fmt.Errorf("go test terminated without an exit code: %v", err)
	default:
		return nil, status, fmt.Errorf("start go test: %v", err)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWith(args, stdin, stdout, stderr, runGoTest)
}

func runWith(args []string, stdin io.Reader, stdout, stderr io.Writer, runner goTestRunner) int {
	fs := flag.NewFlagSet("testgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	readStdin := fs.Bool("stdin", false, "read a go test -json stream from stdin instead of running go test")
	target := fs.String("target", "./...", "test target when running go test")
	timeout := fs.Duration("timeout", 15*time.Minute, "overall timeout when running go test")
	producerExitCode := fs.Int("producer-exit-code", -1, "recorded producer exit code (required with -stdin)")
	emitJSON := fs.Bool("json", false, "write the machine-readable verdict to stdout and the human prose to stderr")
	remeasure := fs.Bool("remeasure-unverified", false, "direct mode only: when the run is UNVERIFIED and nothing else is wrong, "+
		"re-run the packages of the reporting gates alone (-count=1 -p 1) and report that isolated run's own verdict")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "testgate: unexpected positional arguments: %v\n", fs.Args())
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "testgate: timeout must be greater than zero")
		return 2
	}

	var stream []byte
	status := testgate.ProducerStatus{}
	var env []string
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if *readStdin {
		if *producerExitCode < 0 {
			fmt.Fprintln(stderr, "testgate: -stdin requires -producer-exit-code; a JSON pipe alone loses the producer status")
			return 2
		}
		if *remeasure {
			fmt.Fprintln(stderr, "testgate: -remeasure-unverified needs direct mode; a pre-captured stream cannot be re-measured")
			return 2
		}
		buf, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "testgate: read stdin: %v\n", err)
			return 2
		}
		stream = buf
		status.ExitCode = *producerExitCode
	} else {
		if *producerExitCode >= 0 {
			fmt.Fprintln(stderr, "testgate: -producer-exit-code is only valid with -stdin")
			return 2
		}
		env = append(os.Environ(), "CGO_ENABLED=0")
		targets := []string{*target}
		if *target == "./..." {
			var err error
			targets, err = discoverFirstPartyTargets(ctx, env)
			if err != nil {
				fmt.Fprintf(stderr, "testgate: discover first-party packages: %v\n", err)
				return 2
			}
		}
		var err error
		stream, status, err = runner(ctx, env, append([]string{"test", "-json"}, targets...))
		if err != nil {
			fmt.Fprintf(stderr, "testgate: %v\n", err)
			return 2
		}
	}

	res, err := testgate.EvaluateWithProducer(bytes.NewReader(stream), status)
	if err != nil {
		fmt.Fprintf(stderr, "testgate: evaluate: %v\n", err)
		return 2
	}

	// SW-275: re-measure an UNVERIFIED run in isolation.
	//
	// UNVERIFIED means the suite was complete and clean and exactly one thing
	// is missing: a gate that measures latency against its own same-run control
	// reported that, under the full parallel suite, the machine could not tell
	// two byte-identical paths apart. That is a statement about the load the
	// suite itself imposes, not about the code, and on the trunk it was
	// reproducing in roughly one run in three. The gate's own round loop already
	// answers "could not measure" with "measure again" (best of three); this is
	// the same answer one layer up, with the load removed: the reporting gates'
	// packages are run alone, sequentially, on the now-idle runner, and THAT
	// run's verdict is reported — earned by measuring, never by assuming.
	//
	// The boundary that keeps this honest: only UNVERIFIED is re-measured. A
	// NOT GREEN or ERROR run is never re-run, so no failure is ever retried into
	// a pass; and a re-measurement that is itself UNVERIFIED (or fails) stands
	// as what it is. The first report is carried on the result (Remeasured) so
	// a reader sees both.
	if *remeasure && res.Verdict == testgate.VerdictUnverified {
		pkgs := unverifiedPackages(res)
		fmt.Fprintf(stderr, "testgate: %d gate(s) reported UNVERIFIED under the full suite; re-measuring %v alone\n",
			len(res.Unverified), pkgs)
		fmt.Fprint(stderr, prefixLines("testgate: full-suite ", testgate.FormatVerdict(res)))
		stream2, status2, err := runner(ctx, env, append([]string{"test", "-json", "-count=1", "-p", "1"}, pkgs...))
		if err != nil {
			fmt.Fprintf(stderr, "testgate: re-measure: %v\n", err)
			return 2
		}
		res2, err := testgate.EvaluateWithProducer(bytes.NewReader(stream2), status2)
		if err != nil {
			fmt.Fprintf(stderr, "testgate: re-measure: evaluate: %v\n", err)
			return 2
		}
		res2.Remeasured = res.Unverified
		res = res2
	}
	// SW-250's transport decision. EvaluateResult has carried a machine-readable
	// summary since SW-249, but nothing could get it out of this binary: main
	// printed prose and cmd/release-gate read the exit code alone. Recognising a
	// marker is worthless if the result cannot cross a process boundary, so the
	// struct itself is now an output.
	if *emitJSON {
		encoded, err := json.Marshal(res)
		if err != nil {
			fmt.Fprintf(stderr, "testgate: encode verdict: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "%s\n", encoded)
		fmt.Fprint(stderr, testgate.FormatVerdict(res))
	} else {
		fmt.Fprint(stdout, testgate.FormatVerdict(res))
	}
	return testgate.ExitCode(res)
}

// unverifiedPackages lists the distinct packages of the gates that reported
// UNVERIFIED, in first-seen order (Unverified is sorted by gate id, so the
// order is stable).
func unverifiedPackages(res testgate.EvaluateResult) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, gate := range res.Unverified {
		if gate.Package == "" || seen[gate.Package] {
			continue
		}
		seen[gate.Package] = true
		pkgs = append(pkgs, gate.Package)
	}
	return pkgs
}

// prefixLines prefixes every non-empty line of s.
func prefixLines(prefix, s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// discoverFirstPartyTargets preserves ./... auto-discovery while preventing a
// preceding npm install from turning vendored Go snippets/tests inside
// node_modules into executable CI code. go list only inspects package metadata;
// the returned explicit import paths are what go test is allowed to execute.
func discoverFirstPartyTargets(ctx context.Context, env []string) ([]string, error) {
	return discoverFirstPartyTargetsWithRunner(ctx, env, runGoList)
}

type goListRunner func(context.Context, []string) (stdout, stderr []byte, err error)

func runGoList(ctx context.Context, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "./...")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func discoverFirstPartyTargetsWithRunner(ctx context.Context, env []string, runner goListRunner) ([]string, error) {
	out, stderr, err := runner(ctx, env)
	if err != nil {
		detail := strings.TrimSpace(string(stderr))
		if detail != "" {
			return nil, fmt.Errorf("go list ./...: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("go list ./...: %w", err)
	}
	// A successful go command may emit module-download diagnostics on stderr.
	// They are not package paths and must never enter the strict test target set.
	return filterFirstPartyTargets(strings.Fields(string(out)))
}

func filterFirstPartyTargets(targets []string) ([]string, error) {
	filtered := make([]string, 0, len(targets))
	for _, target := range targets {
		dependencyTree := false
		for _, segment := range strings.Split(target, "/") {
			if segment == "node_modules" {
				dependencyTree = true
				break
			}
		}
		if !dependencyTree {
			filtered = append(filtered, target)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("go list returned no first-party packages")
	}
	return filtered, nil
}
