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
