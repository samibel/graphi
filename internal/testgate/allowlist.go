// Package testgate validates a complete `CGO_ENABLED=0 go test -json ./...`
// stream and its producer status. There are no expected-failure carve-outs:
// every named test failure, package-level failure, build failure, truncated
// stream, non-zero unexplained producer status, or producer stderr fails closed.
//
// Skips are recorded, never judged. A skipped test is not a failure and does not
// change the verdict, but it is also not evidence of a pass: the assertion never
// ran. EvaluateResult therefore carries SkippedTests and SkippedPackages so a
// consumer can see what a run did not measure without parsing the human verdict.
//
// SW-250 adds one narrow exception to "skips are recorded, never judged". A
// measurement that can positively demonstrate its own inability to measure may
// say so on a structured channel (internal/gatemarker), and the verdict then
// reports UNVERIFIED instead of GREEN. The exception is deliberately not a
// rule: only the gate ids in permittedUnverifiedGates may use the channel, and
// a marker from anyone else — or twice from the same gate, or malformed — is
// ERROR, fail-closed. Ordinary skips remain ordinary skips.
package testgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/internal/gatemarker"
)

// Verdict is the answer a run produces. A gate has four answers, not two.
//
// ERROR is not a degree of failure: it is "this gate could not validate the
// run", and it outranks everything because a reader that cannot trust its own
// input must not report a verdict about the code.
type Verdict string

const (
	VerdictGreen      Verdict = "GREEN"
	VerdictNotGreen   Verdict = "NOT GREEN"
	VerdictUnverified Verdict = "UNVERIFIED"
	VerdictError      Verdict = "ERROR"
)

// permittedUnverifiedGates is SW-250's deliberate migration mechanism: the
// complete, hardcoded list of gate ids allowed to report UNVERIFIED.
//
// It is two entries because exactly two measurements in this repository can
// positively demonstrate their own inability to measure — AX-06's executor-seam
// latency gate and SW-244's shadow-cost accounting, both of which compare two
// byte-identical paths against each other in the same run and can therefore
// show that their own resolution was insufficient. Every other gate here
// (daemon lifecycle, cold_start_p95_ms, the rest of the suite) cannot yet tell
// "the thing under test is broken" from "this runner could not schedule", so a
// timeout stays FAIL and a budget breach stays FAIL.
//
// This is NOT a registry, and it is not an extension point. Adding an entry
// means asserting that a new instrument can prove its own blindness, which is a
// decision with an argument attached — see the spec at
// projects/graphi/specs/gate-states-not-a-boolean.md.
var permittedUnverifiedGates = map[string]struct{}{
	"ax06_executor_seam_latency": {},
	"sw244_shadow_default_cost":  {},
}

// PermittedUnverifiedGates returns the permitted list, sorted.
func PermittedUnverifiedGates() []string {
	out := make([]string, 0, len(permittedUnverifiedGates))
	for id := range permittedUnverifiedGates {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// UnverifiedGate is one gate's report that it could not measure, carried out of
// the go test -json stream. It is neither a pass nor a failure: the assertion
// the gate exists to make did not run, and the gate said so with numbers.
type UnverifiedGate struct {
	GateID       string             `json:"gate_id"`
	Package      string             `json:"package"`
	Test         string             `json:"test"`
	ReasonCode   string             `json:"reason_code"`
	Measurements map[string]float64 `json:"measurements,omitempty"`
	Detail       string             `json:"detail,omitempty"`
}

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
	// Verdict is the four-valued answer. Green is kept as the boolean
	// shorthand for VerdictGreen so existing consumers keep working.
	Verdict          Verdict          `json:"verdict"`
	Green            bool             `json:"green"`
	UnexpectedFails  []string         `json:"unexpected_fails,omitempty"`  // every observed test/package/build failure
	ProducerFailures []string         `json:"producer_failures,omitempty"` // exit/stderr inconsistencies from the go test producer
	SkippedTests     []SkippedTest    `json:"skipped_tests,omitempty"`     // tests that reported "skip", sorted by package then test
	SkippedPackages  []SkippedPackage `json:"skipped_packages,omitempty"`  // packages that reported "skip" as a whole, sorted by package
	Unverified       []UnverifiedGate `json:"unverified,omitempty"`        // gates that demonstrated they could not measure, sorted by gate id
	MarkerErrors     []string         `json:"marker_errors,omitempty"`     // fail-closed rejections on the UNVERIFIED channel
}

// decide applies the precedence the whole design turns on:
//
//	ERROR > FAIL > UNVERIFIED > PASS
//
// An instrument may report its own inability to measure. It may never
// downgrade someone else's failure, so UNVERIFIED is only ever consulted once
// the run is otherwise clean. And a marker the reader could not trust outranks
// both, because the alternative is a verdict computed from input the gate
// admits it does not understand.
func (res *EvaluateResult) decide() {
	switch {
	case len(res.MarkerErrors) > 0:
		res.Verdict = VerdictError
	case len(res.UnexpectedFails) > 0 || len(res.ProducerFailures) > 0:
		res.Verdict = VerdictNotGreen
	case len(res.Unverified) > 0:
		res.Verdict = VerdictUnverified
	default:
		res.Verdict = VerdictGreen
	}
	res.Green = res.Verdict == VerdictGreen
}

// ExitCode maps a verdict to a process exit status. The three verdicts AC-3
// names have three distinct codes; ERROR keeps 2, the status this gate already
// reserved for "cannot obtain or validate a complete run".
func ExitCode(res EvaluateResult) int {
	switch res.Verdict {
	case VerdictGreen:
		return 0
	case VerdictNotGreen:
		return 1
	case VerdictUnverified:
		return 3
	case VerdictError:
		return 2
	default:
		// An unset verdict is a programming error in this package, and the
		// fail-closed answer is the one that blocks.
		return 2
	}
}

// markerHit is one marker line observed in the stream, held until the whole
// stream is classified. Whether the emitting test declined to assert is only
// known once its terminal event has arrived, so the fail-closed rules are
// applied after the loop rather than inside it.
type markerHit struct {
	line    int
	pkg     string
	test    string
	testKey string
	marker  gatemarker.Marker
	err     error
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
	var markerHits []markerHit                 // UNVERIFIED markers, in stream order
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
				outLine := strings.TrimSpace(ev.Output)
				// The UNVERIFIED channel is read FIRST and structurally. It is
				// never a prose match on the skip text: SW-249 showed that
				// position to be fragile (a skip reason that happens to begin
				// with a go test marker prefix is already mishandled there),
				// and a laundering path built on prose would be trivial to
				// open by accident.
				marker, isMarker, markerErr := gatemarker.Parse(outLine)
				switch {
				case isMarker:
					markerHits = append(markerHits, markerHit{
						line: lineNumber, pkg: ev.Package, test: ev.Test,
						testKey: testKey, marker: marker, err: markerErr,
					})
				case strings.HasPrefix(outLine, "--- SKIP"):
					if ev.Test != "" {
						skipMessage[testKey] = lastOutput[testKey]
					}
				case isSkipReasonCandidate(outLine):
					lastOutput[key] = outLine
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

	unverified, markerErrors, markerTests := reconcileMarkers(markerHits, skippedTests, failed)

	res := EvaluateResult{Unverified: unverified, MarkerErrors: markerErrors}
	for failure := range unexpected {
		res.UnexpectedFails = append(res.UnexpectedFails, failure)
	}
	sort.Strings(res.UnexpectedFails)

	for key, reason := range skippedTests {
		// AC-3: ordinary skips are reported SEPARATELY from unverified
		// measurements. A gate that reported UNVERIFIED is listed as such and
		// is not also counted among the platform guards and -short skips.
		if _, isGate := markerTests[key]; isGate {
			continue
		}
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

	// Ordinary skips are deliberately absent from this decision. SW-249 changed
	// what a verdict reports, not what it decides; SW-250 changes the decision
	// only for the two gates on the permitted list, and only via a structured
	// marker they had to construct with numbers attached.
	res.decide()
	return res, nil
}

// reconcileMarkers applies AC-5's fail-closed rules once the whole stream has
// been classified, and returns the accepted reports, the rejections, and the
// set of tests whose skip belongs to a gate rather than to a platform guard.
//
// Every rejection is a diagnostic. None of them is a silent drop: a channel
// that ignores unknown senders is a channel someone will use.
func reconcileMarkers(hits []markerHit, skippedTests map[string]string, failed map[string]struct{}) ([]UnverifiedGate, []string, map[string]struct{}) {
	var (
		accepted     []UnverifiedGate
		markerErrors []string
		markerTests  = make(map[string]struct{})
		seen         = make(map[string]int) // gate id -> line of its first marker
	)
	for _, hit := range hits {
		switch {
		case hit.err != nil:
			markerErrors = append(markerErrors, fmt.Sprintf(
				"line %d: malformed UNVERIFIED marker: %v", hit.line, hit.err))
		case hit.test == "":
			markerErrors = append(markerErrors, fmt.Sprintf(
				"line %d: gate %q emitted an UNVERIFIED marker on a package-level output line; a marker must name the test that could not measure",
				hit.line, hit.marker.GateID))
		case !isPermittedUnverifiedGate(hit.marker.GateID):
			markerErrors = append(markerErrors, fmt.Sprintf(
				"line %d: gate id %q is not on testgate's permitted UNVERIFIED list %v; only a gate that can positively demonstrate its own inability to measure may report UNVERIFIED",
				hit.line, hit.marker.GateID, PermittedUnverifiedGates()))
		case seen[hit.marker.GateID] != 0:
			markerErrors = append(markerErrors, fmt.Sprintf(
				"line %d: gate id %q emitted an UNVERIFIED marker twice in one run (first at line %d); the verdict cannot rest on two measurements from one instrument",
				hit.line, hit.marker.GateID, seen[hit.marker.GateID]))
		default:
			// Only a skip is a declined assertion. A FAIL is a result as
			// much as a PASS is — and the more consequential one — so a
			// marker alongside either is the same contradiction and is
			// rejected the same way.
			if _, didSkip := skippedTests[hit.testKey]; !didSkip {
				outcome := "it passed"
				if _, didFail := failed[hit.testKey]; didFail {
					outcome = "it failed"
				}
				markerErrors = append(markerErrors, fmt.Sprintf(
					"line %d: gate %q reported UNVERIFIED but %s.%s did not decline to assert — %s; a gate cannot report both that it could not measure and a result",
					hit.line, hit.marker.GateID, hit.pkg, hit.test, outcome))
				continue
			}
			seen[hit.marker.GateID] = hit.line
			markerTests[hit.testKey] = struct{}{}
			accepted = append(accepted, UnverifiedGate{
				GateID:       hit.marker.GateID,
				Package:      hit.pkg,
				Test:         hit.test,
				ReasonCode:   string(hit.marker.ReasonCode),
				Measurements: hit.marker.Measurements,
				Detail:       hit.marker.Detail,
			})
		}
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].GateID < accepted[j].GateID })
	sort.Strings(markerErrors)
	return accepted, markerErrors, markerTests
}

func isPermittedUnverifiedGate(id string) bool {
	_, ok := permittedUnverifiedGates[id]
	return ok
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
	res.decide()
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
	switch res.Verdict {
	case VerdictGreen:
		b.WriteString("GREEN — complete go test stream contains no failures\n")
	case VerdictUnverified:
		b.WriteString("UNVERIFIED — no failure, but a gate reported that it could not measure; " +
			"this run is not evidence that the thing it gates is healthy\n")
	case VerdictError:
		b.WriteString("ERROR — the UNVERIFIED channel carried something this gate will not " +
			"interpret; failing closed rather than guessing\n")
	default:
		b.WriteString("NOT GREEN\n")
	}
	if len(res.MarkerErrors) > 0 {
		fmt.Fprintf(&b, "  marker errors: %d — every one of these is fail-closed, never ignored\n", len(res.MarkerErrors))
		for _, markerErr := range res.MarkerErrors {
			fmt.Fprintf(&b, "    - %s\n", markerErr)
		}
	}
	if len(res.Unverified) > 0 {
		fmt.Fprintf(&b, "  unverified measurements: %d — a gate that can demonstrate its own "+
			"inability to measure did so; the assertion did not run\n", len(res.Unverified))
		for _, gate := range res.Unverified {
			fmt.Fprintf(&b, "    - %s (%s.%s): %s %s\n",
				gate.GateID, gate.Package, gate.Test, gate.ReasonCode, formatMeasurements(gate.Measurements))
			if gate.Detail != "" {
				fmt.Fprintf(&b, "      %s\n", gate.Detail)
			}
		}
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

// formatMeasurements renders a marker's raw numbers in a stable key order, so
// two runs of the same shape produce the same line.
func formatMeasurements(m map[string]float64) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, strconv.FormatFloat(m[key], 'f', -1, 64)))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
