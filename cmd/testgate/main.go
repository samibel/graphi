// Command testgate runs the default test suite under CGO_ENABLED=0 and requires
// a complete all-green result. No expected test failure is accepted: named
// tests, package/setup failures, build failures, producer stderr, and producer
// status inconsistencies all fail closed.
//
// Usage:
//
//	go run ./cmd/testgate
//	go run ./cmd/testgate -target ./internal/example
//	go run ./cmd/testgate -stdin -producer-exit-code 0 < go-test-events.json
//
// Direct mode is recommended because testgate captures stdout, stderr, and the
// go test exit status itself. Stdin mode requires the producer's recorded exit
// code explicitly; a plain shell pipeline cannot safely communicate it.
//
// Exit code is 0 when the run is fully green, 1 when the tests are
// not green, and 2 when the gate itself cannot obtain or validate a complete run.
package main

import (
	"bytes"
	"context"
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

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("testgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	readStdin := fs.Bool("stdin", false, "read a go test -json stream from stdin instead of running go test")
	target := fs.String("target", "./...", "test target when running go test")
	timeout := fs.Duration("timeout", 15*time.Minute, "overall timeout when running go test")
	producerExitCode := fs.Int("producer-exit-code", -1, "recorded producer exit code (required with -stdin)")
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
	if *readStdin {
		if *producerExitCode < 0 {
			fmt.Fprintln(stderr, "testgate: -stdin requires -producer-exit-code; a JSON pipe alone loses the producer status")
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
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()

		env := append(os.Environ(), "CGO_ENABLED=0")
		targets := []string{*target}
		if *target == "./..." {
			var err error
			targets, err = discoverFirstPartyTargets(ctx, env)
			if err != nil {
				fmt.Fprintf(stderr, "testgate: discover first-party packages: %v\n", err)
				return 2
			}
		}
		cmdArgs := append([]string{"test", "-json"}, targets...)
		cmd := exec.CommandContext(ctx, "go", cmdArgs...)
		cmd.Env = env
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err := cmd.Run()
		stream = outBuf.Bytes()
		status.Stderr = errBuf.String()
		if err == nil {
			status.ExitCode = 0
		} else {
			var exitErr *exec.ExitError
			switch {
			case ctx.Err() != nil:
				fmt.Fprintf(stderr, "testgate: go test did not complete: %v\n", ctx.Err())
				if status.Stderr != "" {
					fmt.Fprintf(stderr, "testgate: go test stderr: %s\n", status.Stderr)
				}
				return 2
			case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
				status.ExitCode = exitErr.ExitCode()
			case errors.As(err, &exitErr):
				fmt.Fprintf(stderr, "testgate: go test terminated without an exit code: %v\n", err)
				return 2
			default:
				fmt.Fprintf(stderr, "testgate: start go test: %v\n", err)
				return 2
			}
		}
	}

	res, err := testgate.EvaluateWithProducer(bytes.NewReader(stream), status)
	if err != nil {
		fmt.Fprintf(stderr, "testgate: evaluate: %v\n", err)
		return 2
	}
	fmt.Fprint(stdout, testgate.FormatVerdict(res))
	if !res.Green {
		return 1
	}
	return 0
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
