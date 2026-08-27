package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// recorderStub is a DivergenceRecorder that keeps what it was handed, so a test
// can assert on the OBSERVATIONS the seam reports rather than on the durable
// file (which internal/divergence owns and tests for itself).
type recorderStub struct {
	observations []observation
}

type observation struct {
	operation string
	mismatch  bool
	kind      string
	legacy    string
	executor  string
}

func (r *recorderStub) RecordDivergence(operation string, mismatch bool, kind, legacy, executor string) {
	r.observations = append(r.observations, observation{
		operation: operation, mismatch: mismatch, kind: kind, legacy: legacy, executor: executor,
	})
}

// withDivergenceRecorder installs a recorder for one test and removes it again,
// so no other case in this package (or in the fixture suites that dispatch
// through the seam) inherits one.
func withDivergenceRecorder(t *testing.T) *recorderStub {
	t.Helper()
	stub := &recorderStub{}
	SetDivergenceRecorder(stub)
	t.Cleanup(func() { SetDivergenceRecorder(nil) })
	return stub
}

// SW-232 AC-1/AC-3: the seam reports AGREEMENTS too. This is the observation
// that lets a reader distinguish "compared and identical" from "never ran",
// and without it the durable record could only ever count mismatches — a zero
// that means two different things.
func TestSW232_ShadowAgreementIsObserved(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := &canaryStub{answer: steady([]byte(`{"tool":"dead_code"`), nil)}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{MaxItems: 3}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("recorded %d observation(s), want 1", len(recorder.observations))
	}
	obs := recorder.observations[0]
	if obs.operation != CanaryOperation || obs.mismatch {
		t.Fatalf("observation = %+v, want an agreement on %q", obs, CanaryOperation)
	}
	if count, _ := CanaryMismatches(); count != 0 {
		t.Fatalf("an agreement recorded %d in-process mismatch(es)", count)
	}
}

// AC-1: a divergence reaches the recorder with its kind and both renderings, so
// the persisted record points at the investigation instead of merely counting.
func TestSW232_ShadowMismatchIsObservedWithItsRendering(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := &canaryStub{answer: func(call int, _ DeadCodeParams) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"call":%d}`, call)), nil
	}}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("recorded %d observation(s), want 1", len(recorder.observations))
	}
	obs := recorder.observations[0]
	if !obs.mismatch || obs.kind != "bytes" {
		t.Fatalf("observation = %+v, want a `bytes` mismatch", obs)
	}
	if obs.legacy == "" || obs.executor == "" {
		t.Fatalf("a mismatch observation must carry both renderings: %+v", obs)
	}
	// The in-process counter and the durable record must agree about what
	// happened — they are fed from one place on purpose.
	if count, last := CanaryMismatches(); count != 1 || last.Kind != obs.kind {
		t.Fatalf("in-process recorder (%d, %q) disagrees with the observation (%+v)", count, last.Kind, obs)
	}
}

// AC-1: the executor-unavailable case is a divergence too — the seam could not
// even build the path it was asked to compare. It was already recorded
// in-process; it must reach the durable record as well, or an operator reading
// the file would see a seam that looks untouched rather than one that failed to
// run.
//
// It is asserted through observeCanary rather than through DispatchOperation
// because the only construction failure reachable from a fixture is a nil
// Client (NewExecutor's own guard), and a nil Client would panic in the legacy
// call the shadow position makes first. The dispatch arm and this test share
// the one recording helper, which is why one place is enough.
func TestSW232_ExecutorUnavailableIsObserved(t *testing.T) {
	withCleanCanaryRecorder(t)
	recorder := withDivergenceRecorder(t)

	observeCanary(CanaryOperation, CanaryMismatch{
		Operation: CanaryOperation,
		Kind:      "executor-unavailable",
		Legacy:    "(ran)",
		Executor:  "client: executor requires a client",
	}, true)

	if len(recorder.observations) != 1 {
		t.Fatalf("recorded %d observation(s), want 1", len(recorder.observations))
	}
	if obs := recorder.observations[0]; !obs.mismatch || obs.kind != "executor-unavailable" {
		t.Fatalf("observation = %+v, want an executor-unavailable mismatch", obs)
	}
	if count, _ := CanaryMismatches(); count != 1 {
		t.Fatalf("in-process mismatches = %d, want 1", count)
	}
}

// AC-6: the shipped position does not touch the recorder at all. `legacy` is
// byte-for-byte the pre-AX-06 call, and SW-232 may not have added an
// observation — let alone a file write — to it.
func TestSW232_LegacyPositionRecordsNothing(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeLegacy)
	recorder := withDivergenceRecorder(t)

	stub := &canaryStub{answer: steady([]byte(`{"tool":"dead_code"`), nil)}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(recorder.observations) != 0 {
		t.Fatalf("the shipped legacy position recorded %+v", recorder.observations)
	}
}

// `active` runs one path, so there is nothing to compare and nothing honest to
// record. Recording a zero-mismatch observation there would be the same
// laundering AC-3 forbids: it would look like evidence of parity.
func TestSW232_ActivePositionRecordsNoComparison(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeActive)
	recorder := withDivergenceRecorder(t)

	direct, _ := executorFixture(t)
	if _, err := DispatchOperation(context.Background(), direct, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(recorder.observations) != 0 {
		t.Fatalf("active recorded %+v, but it ran only one path", recorder.observations)
	}
}

// With no recorder installed the seam behaves exactly as it did before SW-232:
// the in-process counter still fires and nothing else happens. This is what
// keeps every existing fixture suite (and any library embedding) free of state.
func TestSW232_NoRecorderInstalledIsTheDefault(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	SetDivergenceRecorder(nil)

	stub := &canaryStub{answer: func(call int, _ DeadCodeParams) ([]byte, error) {
		if call == 2 {
			return nil, errors.New("different")
		}
		return []byte(`{"ok":true}`), nil
	}}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if count, _ := CanaryMismatches(); count != 1 {
		t.Fatalf("in-process mismatches = %d, want 1 with no recorder installed", count)
	}
}
