package evalreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC-1: the trigger is a MISSED gate, and the profile has to be attributable to
// the scenario that missed it — so the miss carries its series with it.
func TestMissedGates_NamesEveryFailedGateWithItsSeries(t *testing.T) {
	report := FullRunReport{
		ColdSeries: &ColdRunSeries{Gates: []GateResult{
			{ID: "cold_index_p50", Status: StatusPass},
			{ID: "cold_index_p95", Status: StatusFail, Threshold: 120, Unit: "s", Measured: 190, HasMeasurement: true, Reason: "190 s > 120 s"},
			{ID: "db_size", Status: StatusUnknown},
		}},
		Repo: FullRepoRun{
			QueryLatency: &QueryLatencySeries{Gates: []GateResult{{ID: "warm_p95_structural", Status: StatusFail}}},
			Incremental:  &IncrementalSeries{Gates: []GateResult{{ID: "freshness_p95", Status: StatusFail}}},
			Stalls:       &StallSeries{Gates: []GateResult{{ID: "progress_stall_p95", Status: StatusFail}}},
		},
	}

	missed := MissedGates(report)
	if len(missed) != 4 {
		t.Fatalf("MissedGates returned %d gates, want 4: %+v", len(missed), missed)
	}
	want := []struct{ series, id string }{
		{RawSeriesCold, "cold_index_p95"},
		{RawSeriesQuery, "warm_p95_structural"},
		{RawSeriesIncremental, "freshness_p95"},
		{RawSeriesStalls, "progress_stall_p95"},
	}
	for i, w := range want {
		if missed[i].Series != w.series || missed[i].ID != w.id {
			t.Fatalf("missed[%d] = %s/%s, want %s/%s", i, missed[i].Series, missed[i].ID, w.series, w.id)
		}
	}
	if missed[0].Measured != 190 || !missed[0].HasMeasurement || missed[0].Threshold != 120 {
		t.Fatalf("the missed gate lost the numbers that make it actionable: %+v", missed[0])
	}
}

// AC-4's other half: nothing triggers on a run that did not miss anything.
// UNKNOWN deliberately does not trigger — an unmeasured gate has no measurement
// to explain, and profiling one would produce a file that answers no question.
func TestMissedGates_APassingOrUnmeasuredRunMissesNothing(t *testing.T) {
	report := FullRunReport{
		ColdSeries: &ColdRunSeries{Gates: []GateResult{
			{ID: "cold_index_p50", Status: StatusPass},
			{ID: "peak_rss", Status: StatusUnknown, Reason: "not the reference scenario"},
		}},
		Repo: FullRepoRun{
			QueryLatency: &QueryLatencySeries{Gates: []GateResult{{ID: "warm_p95_structural", Status: StatusPass}}},
		},
	}
	if missed := MissedGates(report); len(missed) != 0 {
		t.Fatalf("MissedGates on a green/unknown run returned %+v, want none", missed)
	}
}

// PRD §17's stop rule is not a §12.2 gate, but it is a performance threshold
// that was exceeded, and it is exactly the case a profile is wanted for.
func TestMissedGates_IncludesATriggeredStopRule(t *testing.T) {
	report := FullRunReport{ColdSeries: &ColdRunSeries{
		StopRule: &StopRuleResult{ID: "peak_rss_stop_rule", ThresholdGB: 4, ObservedPeakGB: 9.1, Triggered: true, Status: StatusFail},
	}}
	missed := MissedGates(report)
	if len(missed) != 1 || missed[0].ID != "peak_rss_stop_rule" || missed[0].Series != RawSeriesCold {
		t.Fatalf("MissedGates = %+v, want the triggered stop rule against the cold series", missed)
	}
	if missed[0].Measured != 9.1 || missed[0].Threshold != 4 || missed[0].Unit != "GB" {
		t.Fatalf("the stop rule lost its numbers: %+v", missed[0])
	}
}

// An untriggered stop rule is not a miss, whatever its status field says.
func TestMissedGates_AnUntriggeredStopRuleIsNotAMiss(t *testing.T) {
	report := FullRunReport{ColdSeries: &ColdRunSeries{
		StopRule: &StopRuleResult{ID: "peak_rss_stop_rule", ThresholdGB: 4, ObservedPeakGB: 1.2, Status: StatusPass},
	}}
	if missed := MissedGates(report); len(missed) != 0 {
		t.Fatalf("MissedGates = %+v, want none", missed)
	}
}

// AC-1 names four profiles. They are constants because the capture, the file
// naming and the reader all address them, and a fifth cannot be added without
// a mechanism describing what produced it.
func TestProfileKinds_AreTheFourTheStoryAsksFor(t *testing.T) {
	want := []string{"cpu", "heap", "allocs", "io"}
	if len(ProfileKinds) != len(want) {
		t.Fatalf("ProfileKinds = %v, want %v", ProfileKinds, want)
	}
	for i, k := range want {
		if ProfileKinds[i] != k {
			t.Fatalf("ProfileKinds[%d] = %q, want %q", i, ProfileKinds[i], k)
		}
		if ProfileFileName(k) == "" {
			t.Fatalf("kind %q has no file name", k)
		}
		if !strings.HasSuffix(ProfileFileName(k), ".pprof") {
			t.Fatalf("kind %q writes %q, which does not read as a pprof profile", k, ProfileFileName(k))
		}
		if strings.TrimSpace(ProfileMechanism[k]) == "" {
			t.Fatalf("kind %q does not say what produced it", k)
		}
	}
	if ProfileFileName("flamegraph") != "" {
		t.Fatal("an unknown kind must not be given a file name")
	}
}

// The `io` artifact is the one place a reader could be misled, so the artifact
// itself has to say what Go actually measured. Go has no file-I/O profile; this
// is the block profile, and pretending otherwise is the over-claim this program
// exists to remove.
func TestProfileMechanism_SaysWhatTheIOProfileReallyIs(t *testing.T) {
	m := strings.ToLower(ProfileMechanism[ProfileIO])
	if !strings.Contains(m, "block") {
		t.Fatalf("the io mechanism does not name the block profile: %q", m)
	}
	if !strings.Contains(m, "no ") && !strings.Contains(m, "not ") {
		t.Fatalf("the io mechanism does not state what Go does NOT measure: %q", m)
	}
}

// AC-2: the association is gate → profile set, and it survives the round trip
// through the file a reader opens.
func TestProfileIndex_RoundTripsTheGateAssociation(t *testing.T) {
	dir := t.TempDir()
	set := ProfileSet{
		Series:   RawSeriesCold,
		Scenario: "a cold full index of the measured checkout",
		Gates: []ProfileGateRef{{
			ID: "cold_index_p95", Threshold: 120, Unit: "s", Measured: 190, HasMeasurement: true, Status: StatusFail,
		}},
		Artifacts: []ProfileArtifact{{Kind: ProfileCPU, File: "cold_index/cpu.pprof", Written: true, Bytes: 42}},
		Complete:  true,
	}
	index := NewProfileIndex([]ProfileSet{set})
	if err := WriteProfileIndex(dir, index); err != nil {
		t.Fatalf("WriteProfileIndex: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ProfileIndexFile))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var got ProfileIndex
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	if got.FormatVersion != ProfileFormatVersion || got.HarnessVersion != HarnessVersion {
		t.Fatalf("the index does not stamp its versions: %+v", got)
	}
	if len(got.Sets) != 1 || len(got.Sets[0].Gates) != 1 || got.Sets[0].Gates[0].ID != "cold_index_p95" {
		t.Fatalf("the gate association did not survive the round trip: %+v", got)
	}
	if got.Sets[0].Trigger != ProfileTriggerMissedGate {
		t.Fatalf("set trigger = %q, want %q", got.Sets[0].Trigger, ProfileTriggerMissedGate)
	}
	if !strings.Contains(strings.ToLower(got.Notes), "re-run") && !strings.Contains(strings.ToLower(got.Notes), "re-execut") {
		t.Fatalf("the index does not tell a reader the profiles come from a diagnostic re-execution: %q", got.Notes)
	}
}

// AC-5: an incomplete set has to be answerable as a question — "did profile
// generation fail?" — rather than requiring the caller to re-derive it.
func TestIncompleteProfileSets_NameWhatFailed(t *testing.T) {
	sets := []ProfileSet{
		{Series: RawSeriesCold, Complete: true},
		{Series: RawSeriesQuery, Complete: false, Error: "mkdir /nope/profiles: permission denied"},
		{Series: RawSeriesStalls, Complete: false, Artifacts: []ProfileArtifact{
			{Kind: ProfileHeap, Written: false, Error: "write heap.pprof: no space left on device"},
		}},
	}
	failures := IncompleteProfileSets(sets)
	if len(failures) != 2 {
		t.Fatalf("IncompleteProfileSets = %v, want 2 entries", failures)
	}
	joined := strings.Join(failures, " | ")
	for _, want := range []string{RawSeriesQuery, "permission denied", RawSeriesStalls, "no space left"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the failure list does not mention %q: %s", want, joined)
		}
	}
	if len(IncompleteProfileSets(nil)) != 0 {
		t.Fatal("no profile sets is not a profile failure")
	}
}

// A run that produced profiles says so in the report a reader opens first, and
// the reference points at the directory rather than restating its contents.
func TestProfileRefs_PointAtTheSetsFromTheRunIndex(t *testing.T) {
	refs := ProfileRefs([]ProfileSet{{
		Series: RawSeriesCold, Dir: ProfileDir + "/cold_index", Complete: true,
		Gates:     []ProfileGateRef{{ID: "cold_index_p95"}, {ID: "peak_rss"}},
		Artifacts: []ProfileArtifact{{Kind: ProfileCPU, Written: true}, {Kind: ProfileHeap, Written: false}},
	}})
	if len(refs) != 1 {
		t.Fatalf("ProfileRefs = %+v, want one", refs)
	}
	got := refs[0]
	if got.Dir != ProfileDir+"/cold_index" || got.Series != RawSeriesCold {
		t.Fatalf("ref = %+v", got)
	}
	if len(got.Gates) != 2 || got.Gates[0] != "cold_index_p95" {
		t.Fatalf("ref gates = %v", got.Gates)
	}
	if got.Written != 1 {
		t.Fatalf("ref written = %d, want 1 (one artifact failed)", got.Written)
	}
}

// The story's own test note reaches for "a deliberately unreachable budget so
// the gate is guaranteed red". A blown per-repo ceiling IS a missed performance
// gate — it is the one the matrix jobs enforce — and it triggers a profile of
// the scenario that produced it.
func TestMissedGates_ABlownBudgetIsAMissedGate(t *testing.T) {
	report := FullRunReport{Repo: FullRepoRun{BudgetChecks: []PerfCheck{
		{Name: "index_wallclock_ms", Measured: 90000, Budget: 1, Unit: "ms", Pass: false},
		{Name: "warm_p95_us.structural", Measured: 4000, Budget: 1, Unit: "us", Pass: false},
		{Name: "db_size_mb", Measured: 12, Budget: 300, Unit: "MiB", Pass: true},
	}}}

	missed := MissedGates(report)
	if len(missed) != 2 {
		t.Fatalf("MissedGates = %+v, want the two blown budgets only", missed)
	}
	if missed[0].ID != "budget.index_wallclock_ms" || missed[0].Series != RawSeriesCold {
		t.Fatalf("missed[0] = %+v, want the cold-index scenario", missed[0])
	}
	if missed[1].ID != "budget.warm_p95_us.structural" || missed[1].Series != RawSeriesQuery {
		t.Fatalf("missed[1] = %+v, want the query-latency scenario", missed[1])
	}
	if missed[0].Measured != 90000 || missed[0].Threshold != 1 {
		t.Fatalf("the blown budget lost its numbers: %+v", missed[0])
	}
}
