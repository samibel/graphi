package divergence

// SW-245 AC-4 — the coverage disclosure.
//
// Before SW-245 the seam compared every call it dispatched, so "observations"
// and "dispatches" were the same number and the record could report one and
// mean both. The dual run now happens on a bounded worker queue, so a call can
// reach the seam and never be compared. These tests pin the property the story
// makes load-bearing: an observation count must never be readable as full
// coverage when it is not.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSkippedComparisonsAreCountedAndBrokenDownByReason is the storage half:
// a skip is durable, is attributed to its operation, and keeps its cause.
//
// The cause is kept because "40 000 dropped under load" and "3 abandoned at
// shutdown" call for different actions, and a single total would leave an
// operator unable to tell which happened.
func TestSkippedComparisonsAreCountedAndBrokenDownByReason(t *testing.T) {
	dir := t.TempDir()
	clock := fakeClock(time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.now = clock.next
	store.RecordDivergence("dead_code", false, "", "", "")
	store.RecordSkipped("dead_code", 3, "queue-full")
	store.RecordSkipped("dead_code", 1, "drain-abandoned")
	store.RecordSkipped("compound", 2, "queue-full")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	byOp := map[string]OperationRecord{}
	for _, op := range rep.Operations {
		byOp[op.Operation] = op
	}
	dead := byOp["dead_code"]
	if dead.Skipped != 4 {
		t.Fatalf("dead_code skipped = %d, want 4", dead.Skipped)
	}
	if dead.SkipReasons["queue-full"] != 3 || dead.SkipReasons["drain-abandoned"] != 1 {
		t.Fatalf("dead_code skip reasons = %v, want queue-full=3 drain-abandoned=1", dead.SkipReasons)
	}
	if dead.Observations != 1 || dead.Mismatches != 0 {
		t.Fatalf("a skip must not move the observation or mismatch counters: %+v", dead)
	}
	if comp := byOp["compound"]; comp.Skipped != 2 || comp.Observations != 0 {
		t.Fatalf("compound = %+v, want 2 skipped / 0 observed", comp)
	}
}

// A skip is not a divergence. An operation that dropped comparisons and never
// saw one disagree must not read DIVERGED — that would turn a capacity problem
// into a false finding, which is the mirror image of the false green AC-4 is
// about and no better.
func TestSkippedComparisonsAreNotDivergences(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordDivergence("dead_code", false, "", "", "")
	store.RecordSkipped("dead_code", 9, "queue-full")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	doc := Assess(rep, []string{"dead_code"}, nil)
	if doc.State != StateAgreed {
		t.Fatalf("state = %q, want %q — a dropped comparison is not a divergence", doc.State, StateAgreed)
	}
	if doc.Mismatches != 0 {
		t.Fatalf("mismatches = %d, want 0", doc.Mismatches)
	}
}

// TestFirstSkipIsFlushedImmediately: a coverage gap an operator never gets to
// read is the same as one that was never disclosed, so the first skip goes to
// disk at once rather than waiting for the coalescing interval. Repeats
// coalesce, because a queue that is dropping is dropping fast.
func TestFirstSkipIsFlushedImmediately(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.flushEvery = time.Hour
	store.RecordSkipped("dead_code", 1, "queue-full")
	// No Flush call: the record must already be on disk.
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rep.Operations) != 1 || rep.Operations[0].Skipped != 1 {
		t.Fatalf("the first skip was not persisted: %+v", rep.Operations)
	}
}

// TestAssessReportsCoverageNotJustObservations is the read half of AC-4: the
// document has to carry dispatches and coverage, per operation and overall, so
// nothing downstream has to derive them (and nothing downstream can forget to).
func TestAssessReportsCoverageNotJustObservations(t *testing.T) {
	rep := Report{
		Directory: "/tmp/x",
		Segments:  1,
		Operations: []OperationRecord{
			{Operation: "dead_code", Observations: 6, Skipped: 4, SkipReasons: map[string]int{"queue-full": 4}},
			{Operation: "compound", Observations: 10},
		},
	}
	doc := Assess(rep, []string{"dead_code", "compound"}, nil)
	if doc.Dispatches != 20 || doc.Skipped != 4 || doc.Observations != 16 {
		t.Fatalf("totals = %d dispatches / %d observations / %d skipped, want 20/16/4",
			doc.Dispatches, doc.Observations, doc.Skipped)
	}
	if doc.Coverage != 0.8 {
		t.Fatalf("coverage = %v, want 0.8", doc.Coverage)
	}
	if doc.SkipReasons["queue-full"] != 4 {
		t.Fatalf("skip reasons = %v", doc.SkipReasons)
	}
	byOp := map[string]OperationView{}
	for _, op := range doc.Operations {
		byOp[op.Operation] = op
	}
	if dead := byOp["dead_code"]; dead.Dispatches != 10 || dead.Coverage != 0.6 {
		t.Fatalf("dead_code = %d dispatches at coverage %v, want 10 at 0.6", dead.Dispatches, dead.Coverage)
	}
	if comp := byOp["compound"]; comp.Dispatches != 10 || comp.Coverage != 1 {
		t.Fatalf("compound = %d dispatches at coverage %v, want 10 at 1", comp.Dispatches, comp.Coverage)
	}
}

// An operation nothing was ever dispatched to reads coverage 1, not 0.
//
// Coverage answers "of what reached the seam, how much was compared", and 0/0
// is vacuously complete. Whether anything is KNOWN about the operation is the
// state's job — reporting it twice, once as UNKNOWN and once as a 0 % that
// means something else entirely, is how a reader learns to distrust both.
func TestCoverageOfNothingIsCompleteNotZero(t *testing.T) {
	doc := Assess(Report{}, []string{"dead_code"}, nil)
	if doc.Coverage != 1 {
		t.Fatalf("coverage = %v with nothing dispatched, want 1", doc.Coverage)
	}
	if doc.State != StateUnknown {
		t.Fatalf("state = %q, want %q — the STATE is what says nothing is known", doc.State, StateUnknown)
	}
	if doc.Operations[0].Dispatches != 0 {
		t.Fatalf("dispatches = %d, want 0", doc.Operations[0].Dispatches)
	}
}

// TestHumanRenderStatesCoverageOnEveryDocument: the coverage line is printed
// whether or not anything was skipped.
//
// A line that appears only on the bad case teaches a reader that its absence
// means nothing was measured, when the absence would in fact mean full
// coverage. So the clean case says so out loud, and the partial case is a
// different sentence in the same place rather than a new one elsewhere.
func TestHumanRenderStatesCoverageOnEveryDocument(t *testing.T) {
	clean := Assess(Report{Segments: 1, Operations: []OperationRecord{
		{Operation: "dead_code", Observations: 5},
	}}, []string{"dead_code"}, nil)
	out := renderString(t, clean)
	if !contains(out, "coverage:") {
		t.Fatalf("a full-coverage document does not state its coverage:\n%s", out)
	}
	if !contains(out, "5 of 5 dispatch(es) compared (100%)") {
		t.Fatalf("the clean coverage line is missing or wrong:\n%s", out)
	}
	if !contains(out, "no sampling, nothing dropped") {
		t.Fatalf("the clean case does not say that nothing was sampled away:\n%s", out)
	}
	if contains(out, "were NOT compared") {
		t.Fatalf("a clean document warns about a gap it does not have:\n%s", out)
	}
}

// The partial case has to be unmissable: the header line, the per-operation
// column, and a paragraph that says in words that an uncompared call is not
// evidence of agreement.
func TestHumanRenderDisclosesPartialCoverage(t *testing.T) {
	doc := Assess(Report{Segments: 1, Operations: []OperationRecord{
		{Operation: "dead_code", Observations: 6, Skipped: 4, SkipReasons: map[string]int{
			"queue-full": 3, "drain-abandoned": 1,
		}},
	}}, []string{"dead_code"}, nil)
	out := renderString(t, doc)

	for _, want := range []string{
		"6 of 10 dispatch(es) compared (60.0%)",
		"drain-abandoned=1, queue-full=3",
		"SKIPPED",
		"4 dispatch(es) reached the seam and were NOT compared",
		"is NOT evidence that the two paths agree",
	} {
		if !contains(out, want) {
			t.Errorf("the partial-coverage disclosure is missing %q:\n%s", want, out)
		}
	}
}

// A coverage just short of whole must not round up to 100 %. "100%" beside a
// non-zero skipped count is a contradiction a skimming reader resolves in
// favour of the percentage, which is the wrong half.
func TestNearCompleteCoverageDoesNotRoundToWhole(t *testing.T) {
	doc := Assess(Report{Segments: 1, Operations: []OperationRecord{
		{Operation: "dead_code", Observations: 9999, Skipped: 1, SkipReasons: map[string]int{"queue-full": 1}},
	}}, []string{"dead_code"}, nil)
	out := renderString(t, doc)
	if !contains(out, "<100%") {
		t.Fatalf("9999 of 10000 rendered without the <100%% qualifier:\n%s", out)
	}
	if contains(out, "compared (100%)") {
		t.Fatalf("a partial coverage rendered as 100%%:\n%s", out)
	}
}

// The JSON form carries the same facts, because `graphi doctor -divergence
// --json` is what a machine reads and a field only a human sees is not a
// disclosure to anything automated.
func TestJSONCarriesCoverage(t *testing.T) {
	doc := Assess(Report{Segments: 1, Operations: []OperationRecord{
		{Operation: "dead_code", Observations: 6, Skipped: 4, SkipReasons: map[string]int{"queue-full": 4}},
	}}, []string{"dead_code"}, nil)

	var out struct {
		Dispatches  int            `json:"dispatches"`
		Skipped     int            `json:"skipped"`
		Coverage    float64        `json:"coverage"`
		SkipReasons map[string]int `json:"skip_reasons"`
		Operations  []struct {
			Operation   string  `json:"operation"`
			Dispatches  int     `json:"dispatches"`
			Skipped     int     `json:"skipped"`
			Coverage    float64 `json:"coverage"`
			SkipReasons map[string]int
		} `json:"operations"`
	}
	if err := json.Unmarshal(jsonBytes(t, doc), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Dispatches != 10 || out.Skipped != 4 || out.Coverage != 0.6 {
		t.Fatalf("json totals = %+v, want 10 dispatches / 4 skipped / 0.6 coverage", out)
	}
	if out.SkipReasons["queue-full"] != 4 {
		t.Fatalf("json skip reasons = %v", out.SkipReasons)
	}
	if len(out.Operations) != 1 || out.Operations[0].Dispatches != 10 || out.Operations[0].Coverage != 0.6 {
		t.Fatalf("json operation row = %+v", out.Operations)
	}
	// The schema is unchanged: these are additive fields on the same document,
	// not a new shape, so a reader written against executor-divergence-v1 keeps
	// working and simply does not see them.
	if !strings.Contains(string(jsonBytes(t, doc)), `"schema": "`+Schema+`"`) {
		t.Fatal("the document no longer identifies itself as " + Schema)
	}
}

// A skip reason is repository-independent today, but it travels the same path a
// repository-influenced string does, so it is bounded like one. An unbounded
// value in a file every diagnostic prints is how a bounded record becomes an
// unbounded one.
func TestSkipReasonIsLengthBounded(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordSkipped("dead_code", 1, strings.Repeat("r", MaxValueLength*3))
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for reason := range rep.Operations[0].SkipReasons {
		if len(reason) > MaxValueLength {
			t.Fatalf("a %d-byte skip reason reached the record unbounded", len(reason))
		}
	}
}

// A malformed call records nothing rather than inventing a row: a zero or
// negative count and an empty operation are programmer errors, and the record's
// job is to stay true, not to store them.
func TestMalformedSkipIsIgnored(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.RecordSkipped("", 3, "queue-full")
	store.RecordSkipped("dead_code", 0, "queue-full")
	store.RecordSkipped("dead_code", -1, "queue-full")
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Fatalf("a malformed skip created %+v", rep.Operations)
	}
}
