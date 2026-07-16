// Package testgate validates a complete `CGO_ENABLED=0 go test -json ./...`
// stream and its producer status. There are no expected-failure carve-outs:
// every named test failure, package-level failure, build failure, truncated
// stream, non-zero unexplained producer status, or producer stderr fails closed.
package testgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// TestEvent is the subset of a `go test -json` event this gate consumes.
type TestEvent struct {
	Action      string `json:"Action"` // "pass" | "fail" | "skip" | "run" | "output" | ...
	Package     string `json:"Package"`
	Test        string `json:"Test"`
	ImportPath  string `json:"ImportPath"`  // build-output/build-fail events emitted by recent Go versions
	FailedBuild string `json:"FailedBuild"` // package fail caused by a compile/setup failure
}

// EvaluateResult is the gate verdict.
type EvaluateResult struct {
	Green            bool
	UnexpectedFails  []string // every observed test/package/build failure
	ProducerFailures []string // exit/stderr inconsistencies from the go test producer
}

// ProducerStatus is the out-of-band status of the command that produced the
// JSON stream. go test's exit status cannot be encoded in its stdout, so callers
// must provide it explicitly instead of relying on a shell pipeline's last
// command. Stderr is also kept out of the JSON stream and must be supplied here.
type ProducerStatus struct {
	ExitCode int
	Stderr   string
}

// Evaluate consumes a `go test -json` stream. The run is GREEN only when the
// stream is structurally complete and contains no test, package, or build
// failure. The evaluator has deliberately no allowlist or privilege input.
func Evaluate(r io.Reader) (EvaluateResult, error) {
	failed := make(map[string]struct{}) // package\x00test of every failing test
	unexpected := make(map[string]struct{})
	packageFails := make(map[string]struct{})
	packageBuildFails := make(map[string]struct{})
	packageSeen := make(map[string]struct{})
	packageStarted := make(map[string]struct{})
	packageFinished := make(map[string]struct{})
	testSeen := make(map[string]struct{})
	testStarted := make(map[string]struct{})
	testFinished := make(map[string]struct{})
	eventCount := 0

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	lineNumber := 0
	for sc.Scan() {
		lineNumber++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev TestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: %w", lineNumber, err)
		}
		if ev.Action == "" {
			return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: missing Action", lineNumber)
		}
		eventCount++

		if ev.Action == "build-fail" {
			name := ev.ImportPath
			if name == "" {
				name = "<unknown package>"
			}
			unexpected[name+" (build failure)"] = struct{}{}
			continue
		}
		if ev.Action == "build-output" {
			continue
		}
		if ev.Package == "" {
			return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: action %q has no Package", lineNumber, ev.Action)
		}

		packageKey := ev.Package
		testKey := ev.Package + "\x00" + ev.Test
		packageSeen[packageKey] = struct{}{}
		if ev.Test != "" {
			testSeen[testKey] = struct{}{}
		}
		switch ev.Action {
		case "start":
			if ev.Test != "" {
				return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: package start names test %q", lineNumber, ev.Test)
			}
			packageStarted[packageKey] = struct{}{}
		case "run":
			if ev.Test == "" {
				return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: run event has no Test", lineNumber)
			}
			testStarted[testKey] = struct{}{}
		case "pass", "skip":
			if ev.Test == "" {
				packageFinished[packageKey] = struct{}{}
			} else {
				testFinished[testKey] = struct{}{}
			}
		case "fail":
			if ev.Test == "" {
				// This is either the summary for named test failures or an
				// unstructured package/compile/TestMain failure. Reconcile it
				// after the whole stream has been classified.
				packageFails[packageKey] = struct{}{}
				packageFinished[packageKey] = struct{}{}
				if ev.FailedBuild != "" {
					packageBuildFails[packageKey] = struct{}{}
					unexpected[ev.Package+" (build failure)"] = struct{}{}
				}
				continue
			}
			testFinished[testKey] = struct{}{}
		case "output", "pause", "cont", "bench":
			// These events carry no independent verdict.
		default:
			return EvaluateResult{}, fmt.Errorf("testgate: invalid go test -json event on line %d: unknown Action %q", lineNumber, ev.Action)
		}
		if ev.Action != "fail" {
			continue
		}
		failed[testKey] = struct{}{}
		unexpected[ev.Package+"."+ev.Test] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		return EvaluateResult{}, fmt.Errorf("testgate: read go test -json stream: %w", err)
	}
	if eventCount == 0 {
		return EvaluateResult{}, fmt.Errorf("testgate: empty go test -json stream")
	}
	if len(packageSeen) == 0 {
		return EvaluateResult{}, fmt.Errorf("testgate: truncated go test -json stream: no package events")
	}

	for pkg := range packageSeen {
		if _, ok := packageStarted[pkg]; !ok {
			return EvaluateResult{}, fmt.Errorf("testgate: truncated go test -json stream: package %s has no start event", pkg)
		}
		if _, ok := packageFinished[pkg]; !ok {
			return EvaluateResult{}, fmt.Errorf("testgate: truncated go test -json stream: package %s has no terminal event", pkg)
		}
	}
	for key := range testSeen {
		if _, ok := testStarted[key]; !ok {
			pkg, test, _ := strings.Cut(key, "\x00")
			return EvaluateResult{}, fmt.Errorf("testgate: truncated go test -json stream: test %s.%s has no run event", pkg, test)
		}
		if _, ok := testFinished[key]; !ok {
			pkg, test, _ := strings.Cut(key, "\x00")
			return EvaluateResult{}, fmt.Errorf("testgate: truncated go test -json stream: test %s.%s has no terminal event", pkg, test)
		}
	}

	// A package-level fail is a normal summary when the package also emitted at
	// least one named failing test; each named failure has already been matched
	// exactly or reported as unexpected. With no named failure, it is an
	// unstructured compile/setup/TestMain failure and must fail closed.
	for pkg := range packageFails {
		hasNamedFailure := false
		for key := range failed {
			failedPkg, _, _ := strings.Cut(key, "\x00")
			if failedPkg == pkg {
				hasNamedFailure = true
				break
			}
		}
		_, hasBuildFailure := packageBuildFails[pkg]
		if !hasNamedFailure && !hasBuildFailure {
			unexpected[pkg+" (package-level failure)"] = struct{}{}
		}
	}

	res := EvaluateResult{}
	for failure := range unexpected {
		res.UnexpectedFails = append(res.UnexpectedFails, failure)
	}
	sort.Strings(res.UnexpectedFails)

	res.Green = len(res.UnexpectedFails) == 0 && len(res.ProducerFailures) == 0
	return res, nil
}

// EvaluateWithProducer additionally validates the status of the command that
// generated the stream. Exit code 1 is consistent with structured failures,
// but those failures still make the verdict red. Exit code 0 with failures and
// non-zero without failures are producer inconsistencies. Any stderr is an
// out-of-band producer failure because it was not classified by go test -json.
func EvaluateWithProducer(r io.Reader, status ProducerStatus) (EvaluateResult, error) {
	if status.ExitCode < 0 {
		return EvaluateResult{}, fmt.Errorf("testgate: invalid producer exit code %d", status.ExitCode)
	}
	stderrFailure := formatProducerStderr(status.Stderr)
	res, err := Evaluate(r)
	if err != nil {
		if stderrFailure != "" {
			return EvaluateResult{}, fmt.Errorf("%w (go test exit %d; %s)", err, status.ExitCode, stderrFailure)
		}
		return EvaluateResult{}, fmt.Errorf("%w (go test exit %d)", err, status.ExitCode)
	}

	if stderrFailure != "" {
		res.ProducerFailures = append(res.ProducerFailures, stderrFailure)
	}
	hasObservedFailure := len(res.UnexpectedFails) > 0
	if status.ExitCode == 0 && hasObservedFailure {
		res.ProducerFailures = append(res.ProducerFailures, "go test exited 0 despite structured failure events")
	}
	if status.ExitCode > 1 {
		res.ProducerFailures = append(res.ProducerFailures, fmt.Sprintf("go test exited with unsupported status %d (only status 1 can represent classified test failures)", status.ExitCode))
	} else if status.ExitCode != 0 && !hasObservedFailure {
		res.ProducerFailures = append(res.ProducerFailures, fmt.Sprintf("go test exited %d without a structured failure event", status.ExitCode))
	}
	sort.Strings(res.ProducerFailures)
	res.Green = len(res.UnexpectedFails) == 0 && len(res.ProducerFailures) == 0
	return res, nil
}

func formatProducerStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	const maxStderr = 2048
	if len(stderr) > maxStderr {
		stderr = stderr[:maxStderr] + "..."
	}
	return "go test wrote stderr: " + strconv.Quote(stderr)
}

// FormatVerdict renders a human-readable summary of an EvaluateResult.
func FormatVerdict(res EvaluateResult) string {
	var b strings.Builder
	b.WriteString("test gate: ")
	if res.Green {
		b.WriteString("GREEN — complete go test stream contains no failures\n")
	} else {
		b.WriteString("NOT GREEN\n")
	}
	if len(res.UnexpectedFails) > 0 {
		fmt.Fprintf(&b, "  test/package/build failures: %v\n", res.UnexpectedFails)
	}
	if len(res.ProducerFailures) > 0 {
		fmt.Fprintf(&b, "  producer failures (exit/stderr inconsistency): %v\n", res.ProducerFailures)
	}
	return b.String()
}
