package testgate

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/gatemarker"
)

// IMPORTANT for everything in this file: a failure message must never print a
// raw marker line at the start of a line. These tests run inside the very suite
// cmd/testgate reads, so an unquoted marker echoed by a failing assertion would
// arrive at the outer gate as a marker from an unlisted gate id — a fail-closed
// ERROR for the whole run. Marker text is always printed with %q.

const (
	unverifiedTestPkg = "github.com/samibel/graphi/surfaces/client"
	otherPkg          = "github.com/samibel/graphi/internal/example"
)

// event renders one go test -json event, escaping the payload properly.
func event(action, pkg, test, output string) string {
	ev := struct {
		Action  string `json:"Action"`
		Package string `json:"Package"`
		Test    string `json:"Test,omitempty"`
		Output  string `json:"Output,omitempty"`
	}{action, pkg, test, output}
	raw, err := json.Marshal(ev)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func markerLine(t *testing.T, m gatemarker.Marker) string {
	t.Helper()
	line, err := gatemarker.Format(m)
	if err != nil {
		t.Fatalf("test fixture builds an invalid marker: %v", err)
	}
	return line
}

func ax06Marker() gatemarker.Marker {
	return gatemarker.Marker{
		GateID:     "ax06_executor_seam_latency",
		ReasonCode: gatemarker.ReasonControlAboveCeiling,
		Measurements: map[string]float64{
			"rounds":           3,
			"control_delta_us": 412.5,
			"noise_term_us":    1237.5,
			"ceiling_us":       750,
		},
		Detail: "two byte-identical legacy paths could not be told apart",
	}
}

func sw244Marker() gatemarker.Marker {
	return gatemarker.Marker{
		GateID:     "sw244_shadow_default_cost",
		ReasonCode: gatemarker.ReasonControlAboveCeiling,
		Measurements: map[string]float64{
			"rounds":           3,
			"control_delta_us": 500,
			"noise_term_us":    1500,
			"ceiling_us":       750,
		},
	}
}

// skippedWithMarker renders the events one gate emits when it skips carrying a
// marker: the human line, the marker on its own indented continuation line,
// go test's own "--- SKIP" marker, then the terminal skip action.
func skippedWithMarker(pkg, test, human, marker string) []string {
	return []string{
		event("run", pkg, test, ""),
		event("output", pkg, test, "=== RUN   "+test+"\n"),
		event("output", pkg, test, "    canary_latency_test.go:972: "+human+"\n"),
		event("output", pkg, test, "        "+marker+"\n"),
		event("output", pkg, test, "--- SKIP: "+test+" (12.00s)\n"),
		event("skip", pkg, test, ""),
	}
}

func ordinarySkip(pkg, test, reason string) []string {
	return []string{
		event("run", pkg, test, ""),
		event("output", pkg, test, "=== RUN   "+test+"\n"),
		event("output", pkg, test, "    example_test.go:9: "+reason+"\n"),
		event("output", pkg, test, "--- SKIP: "+test+" (0.00s)\n"),
		event("skip", pkg, test, ""),
	}
}

func stream(parts ...[]string) string {
	var lines []string
	for _, part := range parts {
		lines = append(lines, part...)
	}
	return strings.Join(lines, "\n")
}

// AC-1/AC-2/AC-3: a permitted gate's structured marker becomes a distinct
// UNVERIFIED verdict carrying the gate id, the reason code and the raw
// measurements — and ordinary skips in the same run are reported separately.
func TestEvaluate_PermittedMarkerProducesUnverified(t *testing.T) {
	s := stream(
		[]string{event("start", unverifiedTestPkg, "", "")},
		skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold",
			"AX-06-LATENCY-VERDICT: UNKNOWN after 3 round(s)", markerLine(t, ax06Marker())),
		ordinarySkip(unverifiedTestPkg, "TestCanaryNoiseModel", "latency measurement is not a -short gate"),
		[]string{event("pass", unverifiedTestPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictUnverified {
		t.Fatalf("verdict = %q, want %q\n%s", res.Verdict, VerdictUnverified, FormatVerdict(res))
	}
	if res.Green {
		t.Fatalf("an unverified measurement is not a green run:\n%s", FormatVerdict(res))
	}
	if got := ExitCode(res); got != 3 {
		t.Fatalf("exit code = %d, want 3 (the distinct third verdict)", got)
	}
	want := []UnverifiedGate{{
		GateID:     "ax06_executor_seam_latency",
		Package:    unverifiedTestPkg,
		Test:       "TestAX06_ExecutorSeamLatencyWithinThreshold",
		ReasonCode: "control_above_ceiling",
		Measurements: map[string]float64{
			"rounds":           3,
			"control_delta_us": 412.5,
			"noise_term_us":    1237.5,
			"ceiling_us":       750,
		},
		Detail: "two byte-identical legacy paths could not be told apart",
	}}
	if !reflect.DeepEqual(res.Unverified, want) {
		t.Fatalf("Unverified = %+v, want %+v", res.Unverified, want)
	}
	// AC-3: ordinary skips are reported SEPARATELY from unverified
	// measurements. The gate's own skip is not in the ordinary list.
	wantSkips := []SkippedTest{{
		Package: unverifiedTestPkg,
		Test:    "TestCanaryNoiseModel",
		Reason:  "example_test.go:9: latency measurement is not a -short gate",
	}}
	if !reflect.DeepEqual(res.SkippedTests, wantSkips) {
		t.Fatalf("SkippedTests = %+v, want only the ordinary skip %+v", res.SkippedTests, wantSkips)
	}
	verdict := FormatVerdict(res)
	for _, fragment := range []string{
		"UNVERIFIED",
		"ax06_executor_seam_latency",
		"control_above_ceiling",
		"ceiling_us=750",
		"skipped tests: 1",
	} {
		if !strings.Contains(verdict, fragment) {
			t.Fatalf("verdict is missing %q:\n%s", fragment, verdict)
		}
	}
}

// AC-4: FAIL > UNVERIFIED > PASS. An instrument may report its own inability to
// measure; it may never downgrade someone else's failure.
func TestEvaluate_FailBeatsUnverifiedInTheSameRun(t *testing.T) {
	s := stream(
		[]string{event("start", unverifiedTestPkg, "", "")},
		skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold",
			"AX-06-LATENCY-VERDICT: UNKNOWN after 3 round(s)", markerLine(t, ax06Marker())),
		[]string{event("pass", unverifiedTestPkg, "", "")},
		[]string{
			event("start", otherPkg, "", ""),
			event("run", otherPkg, "TestSomethingRealBroke", ""),
			event("fail", otherPkg, "TestSomethingRealBroke", ""),
			event("fail", otherPkg, "", ""),
		},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictNotGreen {
		t.Fatalf("a real failure must produce NOT GREEN regardless of any UNVERIFIED marker; verdict = %q\n%s",
			res.Verdict, FormatVerdict(res))
	}
	if got := ExitCode(res); got != 1 {
		t.Fatalf("exit code = %d, want 1 (NOT GREEN)", got)
	}
	if len(res.UnexpectedFails) != 1 || res.UnexpectedFails[0] != otherPkg+".TestSomethingRealBroke" {
		t.Fatalf("the failure was lost or renamed: %v", res.UnexpectedFails)
	}
	// The UNVERIFIED report is still carried — precedence orders the verdict,
	// it does not erase the instrument's own report.
	if len(res.Unverified) != 1 {
		t.Fatalf("the unverified report was erased by the failure: %+v", res.Unverified)
	}
}

// AC-5, shape 1: a marker from a gate id that is not on the in-code permitted
// list is ERROR, fail-closed. A channel that ignores unknown senders is a
// channel someone will use.
func TestEvaluate_UnlistedGateIDIsErrorNotSilence(t *testing.T) {
	rogue := ax06Marker()
	rogue.GateID = "some_flaky_test_that_wants_a_pass"
	s := stream(
		[]string{event("start", otherPkg, "", "")},
		skippedWithMarker(otherPkg, "TestFlaky", "this runner is busy", markerLine(t, rogue)),
		[]string{event("pass", otherPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictError {
		t.Fatalf("an unlisted gate id must be ERROR, got %q\n%s", res.Verdict, FormatVerdict(res))
	}
	if got := ExitCode(res); got != 2 {
		t.Fatalf("exit code = %d, want 2 (ERROR)", got)
	}
	if len(res.Unverified) != 0 {
		t.Fatalf("an unlisted gate id must not reach the unverified list: %+v", res.Unverified)
	}
	if len(res.MarkerErrors) != 1 || !strings.Contains(res.MarkerErrors[0], "not on") {
		t.Fatalf("marker errors = %q, want one naming the permitted list", res.MarkerErrors)
	}
}

// AC-5, shape 2: the same gate id emitting twice in one run is ERROR. Two
// reports from one instrument in one run means the reader cannot know which
// measurement the verdict rests on.
func TestEvaluate_DuplicateGateIDIsErrorNotSilence(t *testing.T) {
	s := stream(
		[]string{event("start", unverifiedTestPkg, "", "")},
		skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold",
			"UNKNOWN", markerLine(t, ax06Marker())),
		skippedWithMarker(unverifiedTestPkg, "TestAX06_SomethingElse",
			"UNKNOWN", markerLine(t, ax06Marker())),
		[]string{event("pass", unverifiedTestPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictError {
		t.Fatalf("a duplicate gate id must be ERROR, got %q\n%s", res.Verdict, FormatVerdict(res))
	}
	if len(res.MarkerErrors) != 1 || !strings.Contains(res.MarkerErrors[0], "twice") {
		t.Fatalf("marker errors = %q, want one naming the duplicate", res.MarkerErrors)
	}
	// The first, well-formed report is still carried; the run is ERROR anyway.
	if len(res.Unverified) != 1 {
		t.Fatalf("Unverified = %+v, want the first report kept", res.Unverified)
	}
}

// AC-5, shape 3: a malformed marker is ERROR. Every way of announcing a marker
// and not being one fails closed.
func TestEvaluate_MalformedMarkerIsErrorNotSilence(t *testing.T) {
	cases := map[string]string{
		"truncated payload":             gatemarker.LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_`,
		"unknown version":               gatemarker.LinePrefix + `/v7 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","measurements":{"rounds":1,"control_delta_us":1,"noise_term_us":1,"ceiling_us":1}}`,
		"reason outside the closed set": gatemarker.LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"runner_was_busy","measurements":{"rounds":1}}`,
		"free text instead of numbers":  gatemarker.LinePrefix + `/v1 {"gate_id":"ax06_executor_seam_latency","reason_code":"control_above_ceiling","detail":"too noisy to measure"}`,
	}
	for name, marker := range cases {
		s := stream(
			[]string{event("start", unverifiedTestPkg, "", "")},
			skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", "UNKNOWN", marker),
			[]string{event("pass", unverifiedTestPkg, "", "")},
		)
		res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Verdict != VerdictError {
			t.Fatalf("%s: malformed marker must be ERROR, got %q\n%s", name, res.Verdict, FormatVerdict(res))
		}
		if len(res.Unverified) != 0 {
			t.Fatalf("%s: a malformed marker reached the unverified list: %+v", name, res.Unverified)
		}
		if len(res.MarkerErrors) == 0 {
			t.Fatalf("%s: malformed marker produced no diagnostic", name)
		}
	}
}

// A marker must name the test that could not measure, and that test must have
// declined to assert. A gate that reports both "I could not measure" and a PASS
// has contradicted itself; that is ERROR, not a quiet preference for one half.
func TestEvaluate_MarkerMustComeFromATestThatDidNotPass(t *testing.T) {
	packageLevel := stream(
		[]string{
			event("start", unverifiedTestPkg, "", ""),
			event("output", unverifiedTestPkg, "", markerLine(t, ax06Marker())+"\n"),
			event("pass", unverifiedTestPkg, "", ""),
		},
	)
	res, err := EvaluateWithProducer(strings.NewReader(packageLevel), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictError || len(res.MarkerErrors) == 0 {
		t.Fatalf("a package-level marker must be ERROR, got %q\n%s", res.Verdict, FormatVerdict(res))
	}

	passing := stream(
		[]string{
			event("start", unverifiedTestPkg, "", ""),
			event("run", unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", ""),
			event("output", unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", "        "+markerLine(t, ax06Marker())+"\n"),
			event("pass", unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", ""),
			event("pass", unverifiedTestPkg, "", ""),
		},
	)
	res, err = EvaluateWithProducer(strings.NewReader(passing), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictError || len(res.MarkerErrors) == 0 {
		t.Fatalf("a marker from a PASSING test must be ERROR, got %q\n%s", res.Verdict, FormatVerdict(res))
	}
}

// AC-1: recognition is on the structured marker, never on a prose match of the
// skip text. Skip messages that talk about being unverified, that name a gate
// id, or that begin with a go-test marker prefix stay ordinary skips.
func TestEvaluate_ProseIsNeverAMarker(t *testing.T) {
	s := stream(
		[]string{event("start", otherPkg, "", "")},
		ordinarySkip(otherPkg, "TestTalksAboutIt",
			"UNVERIFIED: ax06_executor_seam_latency could not measure, control_above_ceiling"),
		ordinarySkip(otherPkg, "TestBeginsWithAGoTestPrefix", "--- this reads like a go test marker"),
		[]string{event("pass", otherPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictGreen {
		t.Fatalf("prose was interpreted as a marker; verdict = %q\n%s", res.Verdict, FormatVerdict(res))
	}
	if len(res.Unverified) != 0 || len(res.MarkerErrors) != 0 {
		t.Fatalf("prose reached the marker channel: unverified=%+v errors=%q", res.Unverified, res.MarkerErrors)
	}
	if len(res.SkippedTests) != 2 {
		t.Fatalf("ordinary skips = %+v, want both", res.SkippedTests)
	}
}

// AC-7 / out of scope: the permitted list is the deliberate two-entry migration
// mechanism, not a general registry. Growing it is a decision, not a detail —
// this test is where that decision has to be argued.
func TestPermittedUnverifiedGatesIsTheTwoEntryMigrationList(t *testing.T) {
	got := PermittedUnverifiedGates()
	want := []string{"ax06_executor_seam_latency", "sw244_shadow_default_cost"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permitted gates = %v, want exactly %v — SW-250 migrates two measurements that "+
			"already make a reasoned UNKNOWN decision and invents no new ones", got, want)
	}
}

// Both permitted emitters must be able to speak on the channel.
func TestBothPermittedGatesAreRecognised(t *testing.T) {
	s := stream(
		[]string{event("start", unverifiedTestPkg, "", "")},
		skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", "UNKNOWN", markerLine(t, ax06Marker())),
		skippedWithMarker(unverifiedTestPkg, "TestSW244_ShadowDefaultCostIsAccounted", "UNKNOWN", markerLine(t, sw244Marker())),
		[]string{event("pass", unverifiedTestPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictUnverified || len(res.Unverified) != 2 {
		t.Fatalf("verdict = %q with %d unverified, want UNVERIFIED with 2\n%s",
			res.Verdict, len(res.Unverified), FormatVerdict(res))
	}
	// Sorted by gate id so a consumer gets a stable order across runs.
	if res.Unverified[0].GateID != "ax06_executor_seam_latency" || res.Unverified[1].GateID != "sw244_shadow_default_cost" {
		t.Fatalf("unverified list is not in a stable order: %+v", res.Unverified)
	}
}

// AC-3: the three verdicts have three distinct exit codes, and ERROR keeps the
// exit code the gate already reserved for "cannot obtain or validate a run".
func TestExitCodesAreDistinct(t *testing.T) {
	codes := map[Verdict]int{
		VerdictGreen:      0,
		VerdictNotGreen:   1,
		VerdictError:      2,
		VerdictUnverified: 3,
	}
	seen := map[int]Verdict{}
	for verdict, want := range codes {
		got := ExitCode(EvaluateResult{Verdict: verdict})
		if got != want {
			t.Fatalf("ExitCode(%q) = %d, want %d", verdict, got, want)
		}
		if other, dup := seen[got]; dup {
			t.Fatalf("verdicts %q and %q share exit code %d", other, verdict, got)
		}
		seen[got] = verdict
	}
}

// The whole point of the transport: the verdict must survive JSON, because
// nothing else can carry it across a process boundary.
func TestUnverifiedVerdictSurvivesJSON(t *testing.T) {
	s := stream(
		[]string{event("start", unverifiedTestPkg, "", "")},
		skippedWithMarker(unverifiedTestPkg, "TestAX06_ExecutorSeamLatencyWithinThreshold", "UNKNOWN", markerLine(t, ax06Marker())),
		[]string{event("pass", unverifiedTestPkg, "", "")},
	)
	res, err := EvaluateWithProducer(strings.NewReader(s), ProducerStatus{ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EvaluateResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Verdict != VerdictUnverified {
		t.Fatalf("verdict did not survive JSON: %s", encoded)
	}
	if len(decoded.Unverified) != 1 ||
		decoded.Unverified[0].ReasonCode != "control_above_ceiling" ||
		decoded.Unverified[0].Measurements["ceiling_us"] != 750 {
		t.Fatalf("the measurements did not survive JSON: %s", encoded)
	}
}
