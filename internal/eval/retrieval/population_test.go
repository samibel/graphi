package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The exact counts. SW-279's approval recorded 41 answerable development
// queries and TestDatasets_CobraV2Shape's `>= 30` floor passes under BOTH
// readings, which is exactly why the miscount survived. These are equality
// assertions on purpose.
const (
	wantAnswerableDev     = 40
	wantAnswerableHoldout = 64
	wantSavingsPopulation = 104
	// cb31 is the only query on which the two readings of "answerable"
	// disagree: ambiguous stratum, five grade-2 judgements, no grade-3 span.
	cb31 = "cb-31"
)

func loadCobraV2(t *testing.T) *Loaded {
	t.Helper()
	ds, err := LoadDataset(cobraV2Dataset)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// TestAnswerableQueries_ExactCounts is AC-1: one exported function, the
// contractual definition, and equality — not a floor.
func TestAnswerableQueries_ExactCounts(t *testing.T) {
	ds := loadCobraV2(t)

	dev, devExcluded, err := AnswerableQueries(ds.Dataset, SplitDev)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev) != wantAnswerableDev {
		t.Errorf("answerable dev = %d, want exactly %d (SW-266 AC-2: not no_hit AND at least one grade-3 span)", len(dev), wantAnswerableDev)
	}
	holdout, holdoutExcluded, err := AnswerableQueries(ds.Dataset, SplitHoldout)
	if err != nil {
		t.Fatal(err)
	}
	if len(holdout) != wantAnswerableHoldout {
		t.Errorf("answerable holdout = %d, want exactly %d", len(holdout), wantAnswerableHoldout)
	}
	if got := len(dev) + len(holdout); got != wantSavingsPopulation {
		t.Errorf("full answerable savings population = %d, want exactly %d", got, wantSavingsPopulation)
	}
	if len(holdoutExcluded) != 0 {
		t.Errorf("holdout excluded = %+v, want none (the two readings agree on the holdout)", holdoutExcluded)
	}

	t.Run("cb-31 is excluded from the population and named with its reason", func(t *testing.T) {
		for _, q := range dev {
			if q.ID == cb31 {
				t.Fatalf("%s is in the answerable development population; it carries no grade-3 answer span", cb31)
			}
		}
		var found *ExcludedQuery
		for i := range devExcluded {
			if devExcluded[i].QueryID == cb31 {
				found = &devExcluded[i]
			}
		}
		if found == nil {
			t.Fatalf("%s is absent from the population AND from the exclusion list; a silently dropped row is how the miscount survived", cb31)
		}
		if found.Reason == "" || len(found.Grades) == 0 {
			t.Errorf("exclusion of %s = %+v, want a reason and the judgement grades", cb31, *found)
		}
		for _, g := range found.Grades {
			if g == GradeMax {
				t.Errorf("%s is excluded but carries a grade-%d judgement", cb31, GradeMax)
			}
		}
	})

	t.Run("the checked-in record names the same exclusion", func(t *testing.T) {
		rec, _, err := ReadAnswerablePopulationRecord(repoRootForTest(t))
		if err != nil {
			t.Fatal(err)
		}
		named := false
		for _, e := range rec.Excluded {
			if e.QueryID == cb31 {
				named = true
			}
		}
		if !named {
			t.Errorf("%s is not named in %s", cb31, AnswerablePopulationRecordPath)
		}
	})

	t.Run("an unknown split is refused rather than counted as empty", func(t *testing.T) {
		if _, _, err := AnswerableQueries(ds.Dataset, "both"); err == nil {
			t.Error("AnswerableQueries(split=both) = nil error")
		}
	})
}

// TestAnswerableHoldout_StillRefusesAmbiguity is the other half of AC-1: the
// development split resolves the disagreement, the holdout refuses it, and
// neither behaviour may be generalised over the other.
func TestAnswerableHoldout_StillRefusesAmbiguity(t *testing.T) {
	ds := loadCobraV2(t)
	got, err := AnswerableHoldout(ds.Dataset)
	if err != nil {
		t.Fatalf("AnswerableHoldout on the frozen release dataset: %v", err)
	}
	if len(got) != wantAnswerableHoldout {
		t.Errorf("AnswerableHoldout = %d queries, want %d", len(got), wantAnswerableHoldout)
	}

	// Move cb-31's shape into the holdout and the run must stop, because N
	// fixes a pre-registered k.
	ambiguous := &Dataset{ID: "ambiguous-holdout", Queries: []Query{
		{ID: "h1", Stratum: StratumNLBehaviour, Split: SplitHoldout, Judgements: []Judgement{{Grade: GradeMax}}},
		{ID: "h2", Stratum: StratumAmbiguous, Split: SplitHoldout, Judgements: []Judgement{{Grade: 2}, {Grade: 2}}},
	}}
	if _, err := AnswerableHoldout(ambiguous); err == nil {
		t.Fatal("AnswerableHoldout accepted a holdout population the two readings disagree about; N would then depend on which reading was used, and k is pre-registered from N")
	} else if !strings.Contains(err.Error(), "h2") {
		t.Errorf("the refusal does not name the disagreeing query: %v", err)
	}

	// The same shape on the development split is RESOLVED, not refused.
	devVersion := &Dataset{ID: "ambiguous-dev", Queries: []Query{
		{ID: "d1", Stratum: StratumNLBehaviour, Split: SplitDev, Judgements: []Judgement{{Grade: GradeMax}}},
		{ID: "d2", Stratum: StratumAmbiguous, Split: SplitDev, Judgements: []Judgement{{Grade: 2}, {Grade: 2}}},
	}}
	answerable, excluded, err := AnswerableQueries(devVersion, SplitDev)
	if err != nil {
		t.Fatalf("AnswerableQueries refused a development population it should have resolved: %v", err)
	}
	if len(answerable) != 1 || len(excluded) != 1 || excluded[0].QueryID != "d2" {
		t.Errorf("answerable=%d excluded=%+v, want 1 answerable and d2 excluded by name", len(answerable), excluded)
	}
}

// TestAnswerablePopulation_RecordMatchesDataset is AC-11: the composition
// record is recomputed from cobra-v2.json at test time, so a future dataset
// edit fails here rather than inside a claim.
func TestAnswerablePopulation_RecordMatchesDataset(t *testing.T) {
	root := repoRootForTest(t)
	ds := loadCobraV2(t)
	want, err := ComputeAnswerablePopulation(ds, "internal/eval/retrieval/testdata/datasets/cobra-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalAnswerablePopulation(want)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(AnswerablePopulationRecordPath))
	if os.Getenv("SW282_WRITE_POPULATION_RECORD") == "1" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", AnswerablePopulationRecordPath)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(raw) {
		t.Errorf("%s has drifted from the dataset it claims to describe. Recompute with SW282_WRITE_POPULATION_RECORD=1 and review the diff — a composition record that no longer matches the dataset is how a claim ends up resting on a population nobody checked.", AnswerablePopulationRecordPath)
	}

	var got AnswerablePopulationRecord
	if err := json.Unmarshal(onDisk, &got); err != nil {
		t.Fatal(err)
	}
	if got.Dev.Answerable != wantAnswerableDev || got.Holdout.Answerable != wantAnswerableHoldout || got.SavingsPopulation != wantSavingsPopulation {
		t.Errorf("record counts dev=%d holdout=%d savings=%d, want %d/%d/%d",
			got.Dev.Answerable, got.Holdout.Answerable, got.SavingsPopulation, wantAnswerableDev, wantAnswerableHoldout, wantSavingsPopulation)
	}
	// The concentration AC-11 requires stated: 57 of 64 holdout, 76 of 104 overall.
	if got.Holdout.ConfigDocs != 57 || got.Holdout.ConfigDocsFraction != "57/64" || got.Holdout.ConfigDocsRendered != "89%" {
		t.Errorf("holdout config_docs = %d (%s, %s), want 57 (57/64, 89%%)", got.Holdout.ConfigDocs, got.Holdout.ConfigDocsFraction, got.Holdout.ConfigDocsRendered)
	}
	if got.SavingsConfigDocs != 76 || got.SavingsConfigDocsFraction != "76/104" || got.SavingsConfigDocsRendered != "73%" {
		t.Errorf("savings config_docs = %d (%s, %s), want 76 (76/104, 73%%)", got.SavingsConfigDocs, got.SavingsConfigDocsFraction, got.SavingsConfigDocsRendered)
	}
	if got.BindingConstraint != HoldoutBindingConstraint {
		t.Errorf("binding constraint has drifted from the constant")
	}
	if !reflect.DeepEqual(got.Dev.ByStratum, want.Dev.ByStratum) {
		t.Errorf("dev stratum composition = %v, recomputed %v", got.Dev.ByStratum, want.Dev.ByStratum)
	}
}

// TestSelectDevSplit is the slice the recalibration is measured over: every
// development query, no holdout query, and a dataset that still validates.
func TestSelectDevSplit(t *testing.T) {
	src := loadCobraV2(t)
	slice, err := SelectDevSplit(src)
	if err != nil {
		t.Fatal(err)
	}
	if slice.Dataset.ID != src.Dataset.ID+"-dev" {
		t.Errorf("slice id = %q", slice.Dataset.ID)
	}
	devInSource := 0
	for _, q := range src.Dataset.Queries {
		if q.Split == SplitDev {
			devInSource++
		}
	}
	if len(slice.Dataset.Queries) != devInSource {
		t.Errorf("slice carries %d queries, the source holds %d development queries", len(slice.Dataset.Queries), devInSource)
	}
	for _, q := range slice.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatalf("slice carries %s query %s", q.Split, q.ID)
		}
	}
	if _, err := AnswerableHoldout(slice.Dataset); err == nil {
		t.Error("the development slice yielded an answerable holdout; it must carry none")
	}
}

// repoRootForTest walks up to the directory holding go.mod.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", dir)
		}
		dir = parent
	}
}
