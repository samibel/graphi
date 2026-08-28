// Package testgate validates a complete `CGO_ENABLED=0 go test -json ./...`
// stream and its producer status. There are no expected-failure carve-outs:
// every named test failure, package-level failure, build failure, truncated
// stream, non-zero unexplained producer status, or producer stderr fails closed.
//
// Skips are recorded, never judged. A skipped test is not a failure and does not
// change the verdict, but it is also not evidence of a pass: the assertion never
// ran. EvaluateResult therefore carries SkippedTests and SkippedPackages so a
// consumer can see what a run did not measure without parsing the human verdict.
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
	Output      string `json:"Output"`      // free-form producer output; the only place a skip reason exists
}

// SkippedTest is one test that ended with a "skip" action. Reason is the last
// output line the test emitted before go test's "--- SKIP" marker, which is
// where t.Skip/t.Skipf messages land; it is empty when the stream carries no
// message (t.SkipNow, or a skip with no explanation). A multi-line skip message
// is represented by its final line.
type SkippedTest struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Reason  string `json:"reason,omitempty"`
}

// SkippedPackage is a package that ended with a package-level "skip" action —
// the package as a whole, not one of its tests. In practice this is go test's
// "[no test files]" result. It is deliberately a separate list from
// SkippedTests: "this package ran nothing" and "these tests inside a package
// that ran declined to assert" are different observations.
type SkippedPackage struct {
	Package string `json:"package"`
	Reason  string `json:"reason,omitempty"`
}

// EvaluateResult is the gate verdict.
//
// SkippedTests and SkippedPackages are observational only. They never affect
// Green and are not failures; they exist so that a machine consumer reads what
// was not measured from the struct rather than from FormatVerdict's prose.
type EvaluateResult struct {
	Green            bool             `json:"green"`
	UnexpectedFails  []string         `json:"unexpected_fails,omitempty"`  // every observed test/package/build failure
	ProducerFailures []string         `json:"producer_failures,omitempty"` // exit/stderr inconsistencies from the go test producer
	SkippedTests     []SkippedTest    `json:"skipped_tests,omitempty"`     // tests that reported "skip", sorted by package then test
	SkippedPackages  []SkippedPackage `json:"skipped_packages,omitempty"`  // packages that reported "skip" as a whole, sorted by package
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
	skippedTests := make(map[string]string)    // package\x00test -> skip reason
	skippedPackages := make(map[string]string) // package -> skip reason
	lastOutput := make(map[string]string)      // package or package\x00test -> last non-marker output line
	skipMessage := make(map[string]string)     // package\x00test -> line that preceded "--- SKIP"
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
			// A skip is recorded, not judged: it is neither a pass nor a
			// failure, and this switch keeps treating it exactly as it always
			// did for the purpose of the verdict.
			if ev.Test == "" {
				packageFinished[packageKey] = struct{}{}
				if ev.Action == "skip" {
					skippedPackages[packageKey] = packageSkipReason(lastOutput[packageKey], ev.Package)
				}
				delete(lastOutput, packageKey)
			} else {
				testFinished[testKey] = struct{}{}
				if ev.Action == "skip" {
					skippedTests[testKey] = skipMessage[testKey]
				}
				delete(lastOutput, testKey)
				delete(skipMessage, testKey)
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
			// These events carry no independent verdict. Output is still read,
			// because go test reports a skip reason nowhere else.
			if ev.Action == "output" {
				key := packageKey
				if ev.Test != "" {
					key = testKey
				}
				line := strings.TrimSpace(ev.Output)
				switch {
				case strings.HasPrefix(line, "--- SKIP"):
					if ev.Test != "" {
						skipMessage[testKey] = lastOutput[testKey]
					}
				case isSkipReasonCandidate(line):
					lastOutput[key] = line
				}
			}
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

	for key, reason := range skippedTests {
		pkg, test, _ := strings.Cut(key, "\x00")
		res.SkippedTests = append(res.SkippedTests, SkippedTest{Package: pkg, Test: test, Reason: reason})
	}
	sort.Slice(res.SkippedTests, func(i, j int) bool {
		if res.SkippedTests[i].Package != res.SkippedTests[j].Package {
			return res.SkippedTests[i].Package < res.SkippedTests[j].Package
		}
		return res.SkippedTests[i].Test < res.SkippedTests[j].Test
	})
	for pkg, reason := range skippedPackages {
		res.SkippedPackages = append(res.SkippedPackages, SkippedPackage{Package: pkg, Reason: reason})
	}
	sort.Slice(res.SkippedPackages, func(i, j int) bool {
		return res.SkippedPackages[i].Package < res.SkippedPackages[j].Package
	})

	// Skips are deliberately absent from this expression. SW-249 changes what a
	// verdict reports, not what it decides.
	res.Green = len(res.UnexpectedFails) == 0 && len(res.ProducerFailures) == 0
	return res, nil
}

// isSkipReasonCandidate reports whether an output line could be a test's own
// message rather than one of go test's structural markers.
func isSkipReasonCandidate(line string) bool {
	if line == "" || line == "PASS" || line == "FAIL" {
		return false
	}
	for _, marker := range []string{"=== ", "--- ", "ok  ", "ok\t", "FAIL\t", "PASS\t"} {
		if strings.HasPrefix(line, marker) {
			return false
		}
	}
	return true
}

// packageSkipReason renders a package-level skip line. go test writes it as
// "?   \tsome/import/path\t[no test files]"; the import path is already carried
// by SkippedPackage.Package, so only the explanation is kept.
func packageSkipReason(line, pkg string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "?") {
		return line
	}
	if i := strings.Index(line, pkg); i >= 0 {
		line = line[i+len(pkg):]
	} else {
		line = strings.TrimPrefix(line, "?")
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "[]"))
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
	if len(res.SkippedTests) > 0 {
		fmt.Fprintf(&b, "  skipped tests: %d — a skip is not a failure, and it is not a pass either; these assertions did not run\n", len(res.SkippedTests))
		for _, skipped := range res.SkippedTests {
			reason := skipped.Reason
			if reason == "" {
				reason = "(no reason in stream)"
			}
			fmt.Fprintf(&b, "    - %s.%s: %s\n", skipped.Package, skipped.Test, reason)
		}
	}
	if len(res.SkippedPackages) > 0 {
		fmt.Fprintf(&b, "  skipped packages: %d — the package itself reported skip, so none of its tests were reached\n", len(res.SkippedPackages))
		for _, skipped := range res.SkippedPackages {
			reason := skipped.Reason
			if reason == "" {
				reason = "(no reason in stream)"
			}
			fmt.Fprintf(&b, "    - %s: %s\n", skipped.Package, reason)
		}
	}
	return b.String()
}
