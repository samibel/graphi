package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/divergence"
	"github.com/samibel/graphi/internal/state"
)

// withDivergenceState points the state directory at a temp dir for one test.
func withDivergenceState(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return state.StateDir()
}

// SW-232 AC-2/AC-3: the read path answers on an EMPTY record, without starting
// a server, in both forms — and what it answers is UNKNOWN.
//
// The negative half is the one that matters. "0 divergences" is the sentence
// this readout must never produce for a seam that never ran, because a reader
// would act on it as evidence of parity. The SW-238 preconditions were rejected
// for exactly that reason.
func TestSW232_DivergenceReadPathReportsUnknownWhenNothingWasObserved(t *testing.T) {
	withDivergenceState(t)

	var human bytes.Buffer
	if code := runDoctorDivergence(&human, false); code != 0 {
		t.Fatalf("human read path exited %d", code)
	}
	if !strings.Contains(human.String(), "UNKNOWN") {
		t.Fatalf("human output does not report UNKNOWN:\n%s", human.String())
	}
	for _, forbidden := range []string{"zero divergence", "no divergence recorded", "0 divergences"} {
		if strings.Contains(strings.ToLower(human.String()), forbidden) {
			t.Fatalf("human output claims %q for an unobserved seam:\n%s", forbidden, human.String())
		}
	}

	var machine bytes.Buffer
	if code := runDoctorDivergence(&machine, true); code != 0 {
		t.Fatalf("json read path exited %d", code)
	}
	var doc struct {
		Schema     string `json:"schema"`
		State      string `json:"state"`
		Operations []struct {
			Operation string `json:"operation"`
			State     string `json:"state"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(machine.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, machine.String())
	}
	if doc.Schema != divergence.Schema || doc.State != string(divergence.StateUnknown) {
		t.Fatalf("json document = schema %q state %q, want %q / UNKNOWN", doc.Schema, doc.State, divergence.Schema)
	}
	if len(doc.Operations) == 0 {
		t.Fatal("json document names no operations; a future precondition check has nothing to cite")
	}
	for _, op := range doc.Operations {
		if op.State != string(divergence.StateUnknown) {
			t.Errorf("%s: state = %q, want UNKNOWN", op.Operation, op.State)
		}
	}
}

// AC-2: the record a server left behind is what this process reads — no shared
// memory, no daemon, just the state directory.
func TestSW232_DivergenceReadPathReportsAPersistedRecord(t *testing.T) {
	stateDir := withDivergenceState(t)
	store, err := divergence.NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("dead_code", false, "", "", "")
	store.RecordDivergence("dead_code", true, "bytes", "11 bytes: legacy", "13 bytes: executor")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var human bytes.Buffer
	if code := runDoctorDivergence(&human, false); code != 0 {
		t.Fatalf("human read path exited %d", code)
	}
	for _, want := range []string{"DIVERGED", "dead_code", "bytes"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("human output missing %q:\n%s", want, human.String())
		}
	}

	var machine bytes.Buffer
	if code := runDoctorDivergence(&machine, true); code != 0 {
		t.Fatalf("json read path exited %d", code)
	}
	var doc struct {
		State        string `json:"state"`
		Observations int    `json:"observations"`
		Mismatches   int    `json:"mismatches"`
	}
	if err := json.Unmarshal(machine.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if doc.State != string(divergence.StateDiverged) || doc.Observations != 2 || doc.Mismatches != 1 {
		t.Fatalf("json document = %+v, want DIVERGED with 2 observations / 1 mismatch", doc)
	}
}

// The doctor check the composition root registers reads the same record, and it
// must classify an unobserved seam as UNKNOWN rather than reporting it green.
func TestSW232_ExecutorDivergenceCheckIsHonestOnAnEmptyRecord(t *testing.T) {
	withDivergenceState(t)
	got, err := executorDivergence()
	if err != nil {
		t.Fatalf("executorDivergence: %v", err)
	}
	if got.Observations != 0 || got.Mismatches != 0 {
		t.Fatalf("empty record read as %+v", got)
	}
	if len(got.Unobserved) == 0 {
		t.Fatal("every migrated operation is unobserved; the check must be told so, or it " +
			"cannot say UNKNOWN")
	}
	if got.State != string(divergence.StateUnknown) {
		t.Fatalf("state = %q, want UNKNOWN", got.State)
	}
}
