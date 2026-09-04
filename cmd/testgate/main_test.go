package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/gatemarker"
	"github.com/samibel/graphi/internal/testgate"
)

func TestDiscoverFirstPartyTargetsIgnoresSuccessfulStderr(t *testing.T) {
	want := []string{"github.com/samibel/graphi/core/model"}
	got, err := discoverFirstPartyTargetsWithRunner(
		context.Background(),
		nil,
		func(context.Context, []string) ([]byte, []byte, error) {
			return []byte(want[0] + "\n"), []byte("go: downloading example.invalid/module v1.0.0\n"), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestDiscoverFirstPartyTargetsReportsFailedStderr(t *testing.T) {
	_, err := discoverFirstPartyTargetsWithRunner(
		context.Background(),
		nil,
		func(context.Context, []string) ([]byte, []byte, error) {
			return nil, []byte("module resolution failed\n"), errors.New("exit status 1")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "module resolution failed") {
		t.Fatalf("error = %v, want failed go-list stderr", err)
	}
}

func TestFilterFirstPartyTargetsExcludesNodeModulesWithoutLosingAutoDiscovery(t *testing.T) {
	got, err := filterFirstPartyTargets([]string{
		"github.com/samibel/graphi/cmd/graphi",
		"github.com/samibel/graphi/extensions/vscode/node_modules/flatted/golang/pkg/flatted",
		"github.com/samibel/graphi/newmodule/pkg",
		"github.com/samibel/graphi/web/node_modules/flatted/golang/pkg/flatted",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"github.com/samibel/graphi/cmd/graphi",
		"github.com/samibel/graphi/newmodule/pkg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestFilterFirstPartyTargetsFailsWhenOnlyDependencyTreesRemain(t *testing.T) {
	if _, err := filterFirstPartyTargets([]string{"example/node_modules/foreign"}); err == nil {
		t.Fatal("dependency-only discovery must fail closed")
	}
}

func TestRun_NonexistentTargetFailsClosed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-target", "./definitely-not-a-package", "-timeout", "30s"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code == 0 {
		t.Fatalf("nonexistent package returned GREEN\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NOT GREEN") {
		t.Fatalf("nonexistent package verdict did not name a failed gate\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRun_StdinRequiresProducerExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-stdin"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code == 0 || !strings.Contains(stderr.String(), "requires -producer-exit-code") {
		t.Fatalf("unverified stdin producer must fail closed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRun_StdinValidatesProducerExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/example"}`,
	}, "\n")
	code := run(
		[]string{"-stdin", "-producer-exit-code", "1"},
		strings.NewReader(stream),
		&stdout,
		&stderr,
	)
	if code == 0 || !strings.Contains(stdout.String(), "go test exited 1") {
		t.Fatalf("producer exit mismatch must fail closed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// SW-250 fixtures. Marker text is only ever printed with %q: these tests run
// inside the suite the real gate reads, and an unquoted marker echoed by a
// failing assertion would arrive there as a marker from an unlisted gate id.
func unverifiedStream(t *testing.T) string {
	t.Helper()
	marker, err := gatemarker.Format(gatemarker.Marker{
		GateID:     "ax06_executor_seam_latency",
		ReasonCode: gatemarker.ReasonControlAboveCeiling,
		Measurements: map[string]float64{
			"rounds": 3, "control_delta_us": 412.5, "noise_term_us": 1237.5, "ceiling_us": 750,
		},
	})
	if err != nil {
		t.Fatalf("fixture marker is invalid: %v", err)
	}
	pkg := "github.com/samibel/graphi/surfaces/client"
	test := "TestAX06_ExecutorSeamLatencyWithinThreshold"
	events := []any{
		map[string]string{"Action": "start", "Package": pkg},
		map[string]string{"Action": "run", "Package": pkg, "Test": test},
		map[string]string{"Action": "output", "Package": pkg, "Test": test, "Output": "        " + marker + "\n"},
		map[string]string{"Action": "output", "Package": pkg, "Test": test, "Output": "--- SKIP: " + test + " (12.00s)\n"},
		map[string]string{"Action": "skip", "Package": pkg, "Test": test},
		map[string]string{"Action": "pass", "Package": pkg},
	}
	lines := make([]string, 0, len(events))
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	return strings.Join(lines, "\n")
}

// AC-3: UNVERIFIED has its own exit code, distinct from GREEN and NOT GREEN.
func TestRun_UnverifiedHasItsOwnExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-stdin", "-producer-exit-code", "0"},
		strings.NewReader(unverifiedStream(t)),
		&stdout,
		&stderr,
	)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "UNVERIFIED") {
		t.Fatalf("verdict did not name the state: stdout=%q", stdout.String())
	}
}

// The transport decision: -json puts the structured verdict on stdout so a
// caller can read it across a process boundary, and keeps the prose on stderr
// so an operator still reads the same words.
func TestRun_JSONCarriesTheVerdictAcrossTheProcessBoundary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-stdin", "-producer-exit-code", "0", "-json"},
		strings.NewReader(unverifiedStream(t)),
		&stdout,
		&stderr,
	)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
	var decoded testgate.EvaluateResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not the machine-readable verdict: %v (stdout=%q)", err, stdout.String())
	}
	if decoded.Verdict != testgate.VerdictUnverified || decoded.Green {
		t.Fatalf("verdict did not cross the boundary: %q", stdout.String())
	}
	if len(decoded.Unverified) != 1 ||
		decoded.Unverified[0].GateID != "ax06_executor_seam_latency" ||
		decoded.Unverified[0].ReasonCode != "control_above_ceiling" ||
		decoded.Unverified[0].Measurements["ceiling_us"] != 750 {
		t.Fatalf("the gate's own report did not cross the boundary: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "UNVERIFIED") {
		t.Fatalf("the human prose was lost with -json: stderr=%q", stderr.String())
	}
}

// SW-275 fixtures for the re-measure path. A recording runner hands back one
// canned stream per call and records the arguments each call was made with.
type recordingRunner struct {
	streams []string
	calls   [][]string
}

func (r *recordingRunner) run(_ context.Context, _ []string, args []string) ([]byte, testgate.ProducerStatus, error) {
	r.calls = append(r.calls, args)
	if len(r.calls) > len(r.streams) {
		return nil, testgate.ProducerStatus{}, errors.New("runner called more times than the fixture allows")
	}
	stream := r.streams[len(r.calls)-1]
	status := testgate.ProducerStatus{}
	if strings.Contains(stream, `"Action":"fail"`) {
		status.ExitCode = 1
	}
	return []byte(stream), status, nil
}

const remeasurePkg = "github.com/samibel/graphi/surfaces/client"

func greenStream() string {
	return strings.Join([]string{
		`{"Action":"start","Package":"` + remeasurePkg + `"}`,
		`{"Action":"pass","Package":"` + remeasurePkg + `"}`,
	}, "\n")
}

func failingStream() string {
	return strings.Join([]string{
		`{"Action":"start","Package":"` + remeasurePkg + `"}`,
		`{"Action":"run","Package":"` + remeasurePkg + `","Test":"TestSomething"}`,
		`{"Action":"output","Package":"` + remeasurePkg + `","Test":"TestSomething","Output":"--- FAIL: TestSomething (0.00s)\n"}`,
		`{"Action":"fail","Package":"` + remeasurePkg + `","Test":"TestSomething"}`,
		`{"Action":"fail","Package":"` + remeasurePkg + `"}`,
	}, "\n")
}

func runRemeasure(t *testing.T, flags []string, streams ...string) (code int, stdout, stderr string, runner *recordingRunner) {
	t.Helper()
	runner = &recordingRunner{streams: streams}
	var out, errb bytes.Buffer
	args := append([]string{"-target", remeasurePkg}, flags...)
	code = runWith(args, strings.NewReader(""), &out, &errb, runner.run)
	return code, out.String(), errb.String(), runner
}

// SW-275: an UNVERIFIED full run is re-measured in isolation, and the isolated
// run's own GREEN is the verdict. The re-run targets exactly the reporting
// gates' packages, defeats the test cache, and runs packages one at a time.
func TestRun_RemeasureUnverifiedInIsolationReportsTheIsolatedVerdict(t *testing.T) {
	code, stdout, stderr, runner := runRemeasure(t, []string{"-remeasure-unverified", "-json"},
		unverifiedStream(t), greenStream())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (the isolated run measured and passed)\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d, want 2 (full suite, then the isolated re-measurement)", len(runner.calls))
	}
	want := []string{"test", "-json", "-count=1", "-p", "1", remeasurePkg}
	if !reflect.DeepEqual(runner.calls[1], want) {
		t.Fatalf("re-measurement args = %v, want %v", runner.calls[1], want)
	}
	var decoded testgate.EvaluateResult
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("stdout is not the machine-readable verdict: %v (stdout=%q)", err, stdout)
	}
	if decoded.Verdict != testgate.VerdictGreen || !decoded.Green || len(decoded.Unverified) != 0 {
		t.Fatalf("the isolated run's verdict did not become the result: %q", stdout)
	}
	if len(decoded.Remeasured) != 1 || decoded.Remeasured[0].GateID != "ax06_executor_seam_latency" {
		t.Fatalf("the full run's UNVERIFIED report must be carried on the result, got %+v", decoded.Remeasured)
	}
	for _, needle := range []string{"re-measured in isolation: 1", "ax06_executor_seam_latency", "full-suite test gate: UNVERIFIED"} {
		if !strings.Contains(stderr, needle) {
			t.Fatalf("the prose must show both runs; missing %q in stderr=%q", needle, stderr)
		}
	}
}

// A re-measurement that is itself UNVERIFIED stays UNVERIFIED: the isolated
// run did not measure either, and nothing is downgraded.
func TestRun_RemeasureStillUnverifiedStaysUnverified(t *testing.T) {
	code, stdout, stderr, runner := runRemeasure(t, []string{"-remeasure-unverified"},
		unverifiedStream(t), unverifiedStream(t))
	if code != 3 || len(runner.calls) != 2 {
		t.Fatalf("exit code = %d (calls=%d), want 3 after two UNVERIFIED measurements\nstdout=%q\nstderr=%q",
			code, len(runner.calls), stdout, stderr)
	}
	if !strings.Contains(stdout, "UNVERIFIED") || !strings.Contains(stdout, "re-measured in isolation: 1") {
		t.Fatalf("verdict must name the state and the re-measurement: stdout=%q", stdout)
	}
}

// A re-measurement that FAILS is NOT GREEN. The isolated run is a real run and
// its failure stands.
func TestRun_RemeasureFailureIsNotGreen(t *testing.T) {
	code, stdout, stderr, _ := runRemeasure(t, []string{"-remeasure-unverified"},
		unverifiedStream(t), failingStream())
	if code != 1 || !strings.Contains(stdout, "NOT GREEN") {
		t.Fatalf("exit code = %d, want 1\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
}

// The boundary: only UNVERIFIED is ever re-measured. A failing full run is
// reported as it is and the runner is not called again — no failure is retried
// into a pass.
func TestRun_RemeasureNeverRerunsAFailure(t *testing.T) {
	code, stdout, stderr, runner := runRemeasure(t, []string{"-remeasure-unverified"},
		failingStream(), greenStream())
	if code != 1 || len(runner.calls) != 1 {
		t.Fatalf("exit code = %d (calls=%d), want 1 with a single run\nstdout=%q\nstderr=%q",
			code, len(runner.calls), stdout, stderr)
	}
}

// Without the flag nothing changes: an UNVERIFIED run exits 3 after one run.
func TestRun_WithoutRemeasureFlagUnverifiedIsUnchanged(t *testing.T) {
	code, stdout, stderr, runner := runRemeasure(t, nil, unverifiedStream(t), greenStream())
	if code != 3 || len(runner.calls) != 1 {
		t.Fatalf("exit code = %d (calls=%d), want 3 with a single run\nstdout=%q\nstderr=%q",
			code, len(runner.calls), stdout, stderr)
	}
}

// A pre-captured stream cannot be re-measured; the combination fails closed.
func TestRun_RemeasureRequiresDirectMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-stdin", "-producer-exit-code", "0", "-remeasure-unverified"},
		strings.NewReader(unverifiedStream(t)),
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), "needs direct mode") {
		t.Fatalf("exit code = %d, want 2\nstdout=%q\nstderr=%q", code, stdout.String(), stderr.String())
	}
}

// A green run still exits 0 and still prints prose on stdout without -json.
func TestRun_GreenIsUnchanged(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/example"}`,
	}, "\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-stdin", "-producer-exit-code", "0"}, strings.NewReader(stream), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "GREEN") {
		t.Fatalf("green verdict lost: stdout=%q", stdout.String())
	}
}
