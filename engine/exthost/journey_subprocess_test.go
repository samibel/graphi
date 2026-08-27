package exthost

// AC-3's evidence: crash, hang and oversize, against a REAL separate process.
//
// These are subprocess journey tests in the sense
// surfaces/mcp_session_journey_subprocess_test.go established: the contract
// under test is a process-lifecycle contract, so it is exercised with real
// processes rather than mocks. A fake that returns io.EOF proves the host's
// error mapping; only a real process that actually dies proves the host survives
// it.
//
// Every case asserts the same three things, and the third is the acceptance
// criterion the other two only support:
//
//  1. the failure is DIAGNOSABLE — the right sentinel, and a message naming what
//     happened and what limit was crossed;
//  2. no result crosses the boundary;
//  3. the HOST IS STILL HEALTHY — proven by starting a second extension in the
//     same process and getting a correct answer out of it.

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// hostStillHealthy is proof (3): after the failure, this very process starts a
// fresh extension and gets the right bytes back.
func hostStillHealthy(t *testing.T) {
	t.Helper()
	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "")
	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if err != nil {
		t.Fatalf("the host did not survive: a fresh extension failed with %v", err)
	}
	if !strings.Contains(string(res.Findings), `"total":4`) {
		t.Fatalf("the host survived but answers wrongly: %s", res.Findings)
	}
}

// AC-3 — a CRASHING extension leaves the host healthy and produces a
// diagnosable error.
func TestSW231Journey_AC3_CrashIsContainedAndDiagnosable(t *testing.T) {
	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "crash")

	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("Call against a crashing extension = %v, want ErrCrashed", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a crash must yield no findings; got %s", res.Findings)
	}
	// Diagnosable means: which extension, how it ended, and what it said.
	for _, want := range []string{exampleID, "exit status 3", "deliberate crash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the crash report must contain %q so a user who did not write the extension can "+
				"act on it; got: %v", want, err)
		}
	}
	hostStillHealthy(t)
}

// AC-3 — a HANGING extension is killed at its declared limit.
//
// The descriptor's timeout is shortened to 300 ms so the test measures the
// HOST's limit rather than its own patience: the analyzer sleeps ten minutes,
// so anything under a minute here is the host's kill and not the sleep ending.
func TestSW231Journey_AC3_HangIsKilledAtTheDeclaredLimit(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{
		Mutate: func(d *Descriptor) { d.Limits.TimeoutMS = 300 },
	})
	ext, _ := startExample(t, descriptor, "hang")

	start := time.Now()
	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Call against a hanging extension = %v, want ErrTimeout", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a timeout must yield no findings; got %s", res.Findings)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("the host waited %v for a 300 ms limit — the limit is not enforced", elapsed)
	}
	if !strings.Contains(err.Error(), "300 ms") {
		t.Errorf("the timeout report must quote the limit that was crossed; got: %v", err)
	}

	// The process is not merely abandoned — it is dead. This is the assertion
	// that needs the process handle, and the reason this suite is internal.
	if ext.cmd.ProcessState == nil {
		t.Fatal("the hung process was never reaped: the host abandoned it instead of killing it")
	}
	if ext.cmd.ProcessState.Exited() {
		t.Errorf("the hung process exited on its own (%v); the host was supposed to kill it",
			ext.cmd.ProcessState)
	}
	hostStillHealthy(t)
}

// AC-3 — an OVERSIZED response is refused, and refused on the declared length.
//
// The analyzer pads its answer past max_response_bytes. The host must refuse
// before reading the body, which is why the assertion is on the sentinel rather
// than on a truncated result: a host that read the body and then complained
// would have already paid the memory it promised not to.
func TestSW231Journey_AC3_OversizeResponseIsRefused(t *testing.T) {
	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "flood")

	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Call against a flooding extension = %v, want ErrResponseTooLarge", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("an oversize frame must yield no findings; got %d bytes of them", len(res.Findings))
	}
	if !strings.Contains(err.Error(), "the limit is 65536 bytes") {
		t.Errorf("the refusal must quote the limit; got: %v", err)
	}
	hostStillHealthy(t)
}

// AC-3 — Close is idempotent and leaves no process behind, including for an
// extension that ignores the polite shutdown.
func TestSW231Journey_AC3_CloseReapsEvenAnUncooperativeExtension(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{
		Mutate: func(d *Descriptor) { d.Limits.TimeoutMS = 300 },
	})
	ext, _ := startExample(t, descriptor, "hang")

	// Do not call: just close. A host that only reaps on the call path would
	// leak a process for every extension a user activated and never used.
	if err := ext.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := ext.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
	if ext.cmd.ProcessState == nil {
		t.Fatal("Close returned without reaping the process")
	}
	if _, err := ext.Call(callCtx(t), exampleOperation, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Call after Close = %v, want ErrClosed", err)
	}
}

// AC-3/AC-4 — the child is spawned with an EMPTY environment by default.
//
// Read it with ADR 0013 D3 in hand: this reduces ACCIDENTAL leakage of the
// host's environment into a process that might forward it. It is not a sandbox
// and the test's name says so. What it genuinely proves is that the default is
// "hand over nothing" rather than "hand over everything", which is the
// difference between a deliberate grant and an inherited one.
func TestSW231Journey_ChildEnvironmentIsEmptyByDefaultNotInherited(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	t.Setenv("GRAPHI_SW231_SECRET", "must-not-reach-the-child")
	// The analyzer reads GRAPHI_SPIKE_FAULT from its environment. With the
	// default (nil Env → empty environment) it cannot see one, so a fault set in
	// the HOST's environment must have no effect.
	t.Setenv("GRAPHI_SPIKE_FAULT", "crash")

	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "")
	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if err != nil {
		t.Fatalf("the child inherited the host's environment and crashed: %v", err)
	}
	if !strings.Contains(string(res.Findings), `"total":4`) {
		t.Fatalf("unexpected findings: %s", res.Findings)
	}
}

// AC-3 — CANCELLATION, as distinct from expiry.
//
// The acceptance criterion names timeout AND cancellation, and they are
// different events: a timeout means the extension failed its contract, a
// cancellation means the caller changed its mind. Both must kill the process —
// an abandoned analysis is still a running analysis — and the messages must not
// blame the extension for the caller's decision.
func TestSW231Journey_AC3_CallerCancellationKillsTheProcess(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{
		// A generous descriptor limit, so a pass proves the CALLER's context
		// ended the call and not the descriptor's deadline.
		Mutate: func(d *Descriptor) { d.Limits.TimeoutMS = 30_000 },
	})
	ext, _ := startExample(t, descriptor, "hang")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := ext.Call(ctx, exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Call on a cancelled context = %v, want ErrTimeout", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a cancelled call must yield no findings; got %s", res.Findings)
	}
	if elapsed > 25*time.Second {
		t.Fatalf("cancellation took %v — the call waited for the descriptor deadline instead", elapsed)
	}
	if !strings.Contains(err.Error(), "the caller cancelled") {
		t.Errorf("a cancellation must not read as the extension missing a deadline; got: %v", err)
	}
	if ext.cmd.ProcessState == nil {
		t.Fatal("cancellation left the process running: an abandoned analysis is still a running one")
	}
	hostStillHealthy(t)
}
