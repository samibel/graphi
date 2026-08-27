package divergence

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// SW-232 (AX-12a) AC-1: the record is durable. A store writes, the writing
// process disappears, and a completely independent reader still finds the
// counts — that is the whole point of the story, because the pre-AX-12a
// recorder was a process-global that no other process could ever read.
func TestRecordSurvivesTheWritingProcess(t *testing.T) {
	dir := t.TempDir()
	clock := fakeClock(time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC))

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.now = clock.next
	store.RecordDivergence("dead_code", false, "", "", "")
	store.RecordDivergence("dead_code", true, "bytes", "12 bytes: a", "13 bytes: b")
	store.RecordDivergence("compound", false, "", "", "")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// The writer is now gone as far as the reader is concerned: Read opens the
	// state directory from scratch and shares nothing with it.
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rep.Segments != 1 {
		t.Fatalf("segments = %d, want 1", rep.Segments)
	}
	byOp := map[string]OperationRecord{}
	for _, op := range rep.Operations {
		byOp[op.Operation] = op
	}
	dead, ok := byOp["dead_code"]
	if !ok {
		t.Fatalf("no dead_code record in %+v", rep.Operations)
	}
	if dead.Observations != 2 || dead.Mismatches != 1 {
		t.Fatalf("dead_code = %d observations / %d mismatches, want 2/1", dead.Observations, dead.Mismatches)
	}
	if dead.LastMismatch == nil || dead.LastMismatch.Kind != "bytes" {
		t.Fatalf("dead_code last mismatch = %+v, want the recorded bytes divergence", dead.LastMismatch)
	}
	if dead.FirstSeen == nil || dead.LastSeen == nil || !dead.LastSeen.After(*dead.FirstSeen) {
		t.Fatalf("first/last seen must be attributed and ordered: %+v", dead)
	}
	if comp := byOp["compound"]; comp.Observations != 1 || comp.Mismatches != 0 {
		t.Fatalf("compound = %+v, want 1 observation / 0 mismatches", comp)
	}
}

// AC-1: two processes each own their own segment file, so neither can lose the
// other's counts through a read-modify-write race. The reader sums them.
func TestSegmentsFromSeparateStoresAreSummed(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		store, err := NewStore(dir)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		store.RecordDivergence("dead_code", false, "", "", "")
		if err := store.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rep.Segments != 3 {
		t.Fatalf("segments = %d, want 3 (one per store)", rep.Segments)
	}
	if len(rep.Operations) != 1 || rep.Operations[0].Observations != 3 {
		t.Fatalf("merged operations = %+v, want one row with 3 observations", rep.Operations)
	}
}

// AC-3, the honesty rule: an empty state directory is UNKNOWN, never "zero
// deviations". Absence of evidence is not evidence of parity.
func TestNoObservationsReadsUnknownNotZeroDeviations(t *testing.T) {
	dir := t.TempDir()
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	doc := Assess(rep, []string{"dead_code", "compound"})
	if doc.State != StateUnknown {
		t.Fatalf("overall state = %q, want %q", doc.State, StateUnknown)
	}
	for _, op := range doc.Operations {
		if op.State != StateUnknown {
			t.Errorf("%s: state = %q, want UNKNOWN for an unobserved operation", op.Operation, op.State)
		}
	}
	human := renderString(t, doc)
	if !contains(human, "UNKNOWN") {
		t.Fatalf("human output must say UNKNOWN:\n%s", human)
	}
	for _, forbidden := range []string{"zero deviations", "no deviations", "0 deviations"} {
		if contains(human, forbidden) {
			t.Fatalf("human output claims %q for an unobserved seam:\n%s", forbidden, human)
		}
	}
	var machine struct {
		Schema     string `json:"schema"`
		State      string `json:"state"`
		Operations []struct {
			Operation string `json:"operation"`
			State     string `json:"state"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(jsonBytes(t, doc), &machine); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if machine.Schema != Schema || machine.State != string(StateUnknown) {
		t.Fatalf("JSON document = %+v, want schema %q and state UNKNOWN", machine, Schema)
	}
	if len(machine.Operations) != 2 {
		t.Fatalf("JSON must name every migrated operation, got %+v", machine.Operations)
	}
}

// AC-3's other half: an operation that HAS been observed and never diverged is
// reported as observed-and-clean, and it must not launder its unobserved
// neighbours into the same verdict — the document says PARTIAL, not clean.
func TestObservedAndUnobservedOperationsAreNotConflated(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("dead_code", false, "", "", "")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	doc := Assess(rep, []string{"dead_code", "compound"})
	if doc.State != StatePartial {
		t.Fatalf("overall state = %q, want %q while an operation is unobserved", doc.State, StatePartial)
	}
	states := map[string]OperationState{}
	for _, op := range doc.Operations {
		states[op.Operation] = op.State
	}
	if states["dead_code"] != StateAgreed {
		t.Errorf("dead_code state = %q, want %q", states["dead_code"], StateAgreed)
	}
	if states["compound"] != StateUnknown {
		t.Errorf("compound state = %q, want %q", states["compound"], StateUnknown)
	}
}

// A recorded mismatch dominates the verdict: the document reports DIVERGED
// however many clean observations sit beside it.
func TestOneMismatchDominatesTheVerdict(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for i := 0; i < 10; i++ {
		store.RecordDivergence("dead_code", false, "", "", "")
	}
	store.RecordDivergence("compound", true, "error-presence", "ok", "boom")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	doc := Assess(rep, []string{"dead_code", "compound"})
	if doc.State != StateDiverged {
		t.Fatalf("state = %q, want %q", doc.State, StateDiverged)
	}
	if doc.Mismatches != 1 || doc.Observations != 11 {
		t.Fatalf("totals = %d mismatches / %d observations, want 1/11", doc.Mismatches, doc.Observations)
	}
	human := renderString(t, doc)
	if !contains(human, "error-presence") {
		t.Fatalf("the human readout must name the divergence kind:\n%s", human)
	}
}

// An unreadable segment is COUNTED, not skipped: a record that quietly drops
// what it cannot parse would under-report divergence, which is the same false
// green AC-3 forbids.
func TestUnreadableSegmentIsReportedNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("dead_code", false, "", "", "")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	writeFile(t, filepath.Join(dir, dirName, "corrupt.json"), "{not json")

	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rep.Unreadable != 1 {
		t.Fatalf("unreadable = %d, want 1", rep.Unreadable)
	}
	doc := Assess(rep, []string{"dead_code"})
	if !contains(renderString(t, doc), "unreadable") {
		t.Fatalf("the readout must disclose the unreadable segment:\n%s", renderString(t, doc))
	}
}

// The values a repository can influence are bounded before they reach the
// artifact (standards: repository-controlled text is length-bounded).
func TestRecordedValuesAreLengthBounded(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'x'
	}
	store.RecordDivergence("dead_code", true, "bytes", string(long), string(long))
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	m := rep.Operations[0].LastMismatch
	if m == nil {
		t.Fatal("no mismatch recorded")
	}
	if len(m.Legacy) > MaxValueLength || len(m.Executor) > MaxValueLength {
		t.Fatalf("recorded values are unbounded: %d / %d bytes", len(m.Legacy), len(m.Executor))
	}
	if !contains(m.Legacy, "…") {
		t.Fatalf("a truncated value must say so: %q", m.Legacy)
	}
}
