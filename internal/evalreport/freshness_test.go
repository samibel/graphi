package evalreport

// SW-126 (P0-C3): the schema's own invariants. These are the properties the
// harness relies on and a consumer reproduces — that a failed change is
// retained but contributes no latency, that both distributions come out of the
// same samples, and that coverage is read from COMPLETED changes.

import (
	"encoding/json"
	"testing"
)

func completed(step int, class string, updateUS, freshnessUS int64) ChangeSample {
	return ChangeSample{
		Step: step, Class: class, Status: ChangeCompleted,
		UpdateUS: updateUS, UpdateMeasured: true,
		FreshnessUS: freshnessUS, FreshnessMeasured: true,
		Probes: 1,
	}
}

// AC-7: every published statistic is derivable from the retained samples, for
// both metrics and per class.
func TestRecomputeIncremental_DerivesBothDistributions(t *testing.T) {
	changes := []ChangeSample{
		completed(1, ChangeClassModify, 100, 400),
		completed(2, ChangeClassAdd, 200, 500),
		completed(3, ChangeClassDelete, 300, 600),
		completed(4, ChangeClassCrossPackage, 400, 700),
	}
	got := RecomputeIncremental(changes)

	if got.Update.N != 4 || got.Freshness.N != 4 {
		t.Fatalf("n = %d update / %d freshness, want 4 each", got.Update.N, got.Freshness.N)
	}
	// Nearest rank over 4 samples: p50 is the 2nd, p95 the 4th.
	if got.Update.P50US != 200 || got.Update.P95US != 400 {
		t.Errorf("update p50/p95 = %d/%d, want 200/400", got.Update.P50US, got.Update.P95US)
	}
	if got.Freshness.P50US != 500 || got.Freshness.P95US != 700 {
		t.Errorf("freshness p50/p95 = %d/%d, want 500/700", got.Freshness.P50US, got.Freshness.P95US)
	}
	if got.Update.MinUS != 100 || got.Freshness.MaxUS != 700 {
		t.Errorf("min/max = %d/%d, want 100/700", got.Update.MinUS, got.Freshness.MaxUS)
	}
	for _, class := range RequiredChangeClasses {
		if got.Classes[class].Changes != 1 {
			t.Errorf("class %s recomputed %d changes, want 1", class, got.Classes[class].Changes)
		}
	}
}

// AC-6: a change that failed is present in the input and contributes to
// NEITHER distribution — it is not silently averaged in as a zero, and it is
// not removed.
func TestRecomputeIncremental_FailedChangeContributesNoLatency(t *testing.T) {
	changes := []ChangeSample{
		completed(1, ChangeClassModify, 100, 400),
		{
			Step: 2, Class: ChangeClassModify, Status: ChangeFailed,
			FailedStage: ChangeStageUpdate, Error: "sync failed: boom",
		},
		completed(3, ChangeClassModify, 300, 600),
	}
	got := RecomputeIncremental(changes)

	if got.Update.N != 2 || got.Freshness.N != 2 {
		t.Fatalf("a failed change leaked into a distribution: n = %d/%d, want 2/2", got.Update.N, got.Freshness.N)
	}
	if got.Update.MinUS != 100 {
		t.Errorf("min update = %d — a failed change was counted as a zero-microsecond update", got.Update.MinUS)
	}
	// The class row still counts the attempt, so the sequence reconciles.
	if got.Classes[ChangeClassModify].Changes != 3 {
		t.Errorf("class modify counted %d changes, want all 3 attempts", got.Classes[ChangeClassModify].Changes)
	}
}

// A change whose update completed but which never converged keeps its update
// latency and has no freshness. Collapsing the two flags would discard a real
// measurement or invent one.
func TestRecomputeIncremental_UnconvergedChangeKeepsItsUpdateLatency(t *testing.T) {
	changes := []ChangeSample{
		completed(1, ChangeClassAdd, 100, 400),
		{
			Step: 2, Class: ChangeClassAdd, Status: ChangeFailed,
			FailedStage: ChangeStageConverge, Error: "never became answerable",
			UpdateUS: 250, UpdateMeasured: true, Probes: 5,
		},
	}
	got := RecomputeIncremental(changes)
	if got.Update.N != 2 {
		t.Errorf("update n = %d, want 2: the update completed and was timed", got.Update.N)
	}
	if got.Freshness.N != 1 {
		t.Errorf("freshness n = %d, want 1: the change never became answerable", got.Freshness.N)
	}
}

// AC-2: coverage is read from COMPLETED changes, so a class whose only step
// failed does not read as covered.
func TestChangeClassCoverage_RequiresACompletedChangePerClass(t *testing.T) {
	full := []ChangeSample{
		completed(1, ChangeClassAdd, 1, 2),
		completed(2, ChangeClassModify, 1, 2),
		completed(3, ChangeClassDelete, 1, 2),
		completed(4, ChangeClassCrossPackage, 1, 2),
	}
	if !AllRequiredClassesCovered(full) {
		t.Fatal("a sequence exercising all four classes must read as covered")
	}

	broken := append([]ChangeSample(nil), full...)
	broken[3] = ChangeSample{Step: 4, Class: ChangeClassCrossPackage, Status: ChangeFailed, FailedStage: ChangeStageUpdate}
	if AllRequiredClassesCovered(broken) {
		t.Error("a class whose only change failed must not read as covered")
	}

	rows := ChangeClassCoverageOf(broken)
	if len(rows) != len(RequiredChangeClasses) {
		t.Fatalf("coverage has %d rows, want one per required class", len(rows))
	}
	for i, row := range rows {
		if row.Class != RequiredChangeClasses[i] || !row.Required {
			t.Errorf("row %d = %+v, want required class %s in order", i, row, RequiredChangeClasses[i])
		}
		if row.Steps != 1 {
			t.Errorf("class %s counted %d steps, want the attempt to be counted", row.Class, row.Steps)
		}
	}
}

// A class the sequence never attempted still gets a row, with zero steps —
// silence would read as "not applicable" rather than "not measured".
func TestChangeClassCoverage_MissingClassGetsAZeroRow(t *testing.T) {
	rows := ChangeClassCoverageOf([]ChangeSample{completed(1, ChangeClassModify, 1, 2)})
	for _, row := range rows {
		if row.Class == ChangeClassDelete && row.Steps != 0 {
			t.Errorf("delete row = %+v, want an explicit zero", row)
		}
	}
	if len(rows) != len(RequiredChangeClasses) {
		t.Errorf("coverage has %d rows, want %d", len(rows), len(RequiredChangeClasses))
	}
}

// The measured flags must survive a JSON round trip: they are what a consumer
// reads to decide whether a zero is a measurement or an absence, and an
// `omitempty` on either would make "measured 0 µs" indistinguishable from "not
// measured".
func TestChangeSample_MeasuredFlagsSurviveJSON(t *testing.T) {
	raw, err := json.Marshal(ChangeSample{Step: 1, Class: ChangeClassAdd, Status: ChangeCompleted})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"update_measured", "freshness_measured", "update_us", "freshness_us"} {
		if _, ok := back[key]; !ok {
			t.Errorf("%s is omitted when false/zero: an absent measurement must stay visible (%s)", key, raw)
		}
	}
}
