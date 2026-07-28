package evalreport

// SW-128 (P0-C5): the raw-sample format itself.
//
// Two properties are load-bearing here and everything else is detail.
//
//  1. A raw file carries SAMPLES AND NOTHING DERIVED (AC-1). If a percentile
//     could travel inside the raw format, the aggregator would be checking a
//     number against itself and AC-2 would be theatre.
//  2. A series that RAN and produced nothing is not the same as a series that
//     was never run (AC-5). Collapsing them would let SW-127's silent index —
//     the defect that story's gate exists to catch — read as "no raw data,
//     UNKNOWN" instead of as the failure it is.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coldSamples(wallclock ...int64) []ColdRunSample {
	out := make([]ColdRunSample, 0, len(wallclock))
	for i, ms := range wallclock {
		out = append(out, ColdRunSample{
			Run:             i + 1,
			Status:          ColdRunCompleted,
			Index:           IndexMetrics{WallclockMS: ms, PeakRSSMB: 100 + int64(i), DBSizeBytes: 1000, Nodes: 10, Edges: 20},
			StablePeakRSSMB: 200 + int64(i),
			BytesPerEdge:    50,
		})
	}
	return out
}

// AC-1: the raw format is samples only. The assertion is deliberately crude —
// a substring scan for the words a derived statistic is spelled with — because
// a structural check would only test the struct someone wrote, while this one
// catches a percentile smuggled in through any future field.
func TestRawSampleSet_CarriesNoDerivedStatistics(t *testing.T) {
	sets := []RawSampleSet{
		NewRawColdSet("grpc-go", RunEnvironment{}, coldSamples(1000, 2000, 3000)),
		NewRawQuerySet("grpc-go", RunEnvironment{},
			[]RawQueryOperation{{Operation: "callers", Class: QueryClassStructural, SamplesUS: []int64{10, 20, 30}}},
			[]RawQueryPool{{ID: QueryClassStructural, Kind: RawPoolClass, Operations: []string{"callers"}}}),
		NewRawIncrementalSet("grpc-go", RunEnvironment{},
			[]ChangeSample{{Step: 1, Class: "modify", UpdateUS: 10, UpdateMeasured: true}}),
		NewRawStallSet("grpc-go", RunEnvironment{}, []StallInterval{{Seq: 1, Phase: "parse", US: 42}}),
	}
	for _, set := range sets {
		raw, err := json.Marshal(set)
		if err != nil {
			t.Fatalf("%s: marshal: %v", set.Series, err)
		}
		for _, derived := range []string{"p50", "p95", "aggregate", "\"min", "\"max"} {
			if strings.Contains(string(raw), derived) {
				t.Errorf("%s raw set contains derived statistic %q: %s", set.Series, derived, raw)
			}
		}
	}
}

// AC-5's structural half: a collected-but-empty series and an uncollected one
// must be distinguishable in the artifact, because they mean opposite things.
func TestRawSampleSet_CollectedEmptyIsNotTheSameAsUncollected(t *testing.T) {
	ran := NewRawStallSet("grpc-go", RunEnvironment{}, []StallInterval{})
	if !ran.Collected {
		t.Fatal("a series constructed from a (present, empty) sample slice must be Collected")
	}
	if ran.Samples != 0 {
		t.Fatalf("samples = %d, want 0", ran.Samples)
	}

	var never RawSampleSet
	if never.Collected {
		t.Fatal("the zero RawSampleSet must NOT be Collected — an absent series is not an empty one")
	}

	// And the distinction has to survive a round trip, or it only exists in
	// memory and the on-disk artifact loses it.
	raw, err := json.Marshal(ran)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RawSampleSet
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Collected {
		t.Fatal("Collected did not survive the round trip")
	}
}

// AC-7: a raw file written by a different format version is refused, not read
// as best it can be. Half-understanding an unknown format is how two
// methodologies get averaged together.
func TestCheckRawCompatibility_RefusesAForeignFormatVersion(t *testing.T) {
	set := NewRawColdSet("grpc-go", RunEnvironment{}, coldSamples(1000))
	set.FormatVersion = RawFormatVersion + 1

	err := CheckRawCompatibility([]RawSampleSet{set})
	if err == nil {
		t.Fatal("a future format version was accepted")
	}
	if !strings.Contains(err.Error(), "format_version") {
		t.Errorf("error = %q, want it to name format_version", err)
	}
}

// AC-7, the case PRD risk 9 actually describes: two raw files from DIFFERENT
// harness versions in one run. Neither is wrong on its own; aggregating them
// together is, and it must be refused rather than warned about.
func TestCheckRawCompatibility_RefusesMixedHarnessVersions(t *testing.T) {
	a := NewRawColdSet("grpc-go", RunEnvironment{}, coldSamples(1000))
	b := NewRawStallSet("grpc-go", RunEnvironment{}, []StallInterval{{Seq: 1, Phase: "parse", US: 5}})
	b.HarnessVersion = "p0-perf/0"

	err := CheckRawCompatibility([]RawSampleSet{a, b})
	if err == nil {
		t.Fatal("a run mixing two harness versions was accepted")
	}
	if !strings.Contains(err.Error(), "harness_version") {
		t.Errorf("error = %q, want it to name harness_version", err)
	}
	if !strings.Contains(err.Error(), "p0-perf/0") {
		t.Errorf("error = %q, want it to name the offending version", err)
	}
}

// A run of one harness version is fine, and so is an empty input: refusing
// nothing at all would make the check impossible to adopt incrementally.
func TestCheckRawCompatibility_AcceptsOneVersion(t *testing.T) {
	sets := []RawSampleSet{
		NewRawColdSet("grpc-go", RunEnvironment{}, coldSamples(1000)),
		NewRawStallSet("grpc-go", RunEnvironment{}, []StallInterval{{Seq: 1, Phase: "parse", US: 5}}),
	}
	if err := CheckRawCompatibility(sets); err != nil {
		t.Fatalf("a single-version run was refused: %v", err)
	}
	if err := CheckRawCompatibility(nil); err != nil {
		t.Fatalf("an empty run was refused: %v", err)
	}
}

// An unknown series name is a programming error in the exporter and must be
// caught at the compatibility check rather than produce a raw file the
// aggregator will silently skip.
func TestCheckRawCompatibility_RefusesAnUnknownSeries(t *testing.T) {
	set := NewRawColdSet("grpc-go", RunEnvironment{}, coldSamples(1000))
	set.Series = "cold_indexx"

	err := CheckRawCompatibility([]RawSampleSet{set})
	if err == nil || !strings.Contains(err.Error(), "cold_indexx") {
		t.Fatalf("error = %v, want it to reject the unknown series by name", err)
	}
}

// AC-4: the output path convention is a function, not a habit. Two runs on the
// same class the same day land in the same directory; two classes never do.
func TestRunDirName_FollowsTheExistingConvention(t *testing.T) {
	cases := []struct {
		date, class, want string
	}{
		{"2026-07-28", "ubuntu-latest", "2026-07-28-ubuntu-latest"},
		{"2026-07-15", "local-sandbox", "2026-07-15-local-sandbox"},
		// A class with characters that are not path-safe is normalised rather
		// than rejected: the directory name must be predictable from the class
		// without the caller having to know the rule.
		{"2026-07-28", "Ubuntu Latest/2", "2026-07-28-ubuntu-latest-2"},
	}
	for _, c := range cases {
		if got := RunDirName(c.date, c.class); got != c.want {
			t.Errorf("RunDirName(%q, %q) = %q, want %q", c.date, c.class, got, c.want)
		}
	}
}

// The convention is anchored at the directory the historical runs already live
// in, so a new run sits beside 2026-07-15-ubuntu-latest and compares.
func TestRunDirPath_SitsBesideTheHistoricalRuns(t *testing.T) {
	got := RunDirPath("2026-07-28", "ubuntu-latest")
	want := RunsRoot + "/2026-07-28-ubuntu-latest"
	if got != want {
		t.Fatalf("RunDirPath = %q, want %q", got, want)
	}
	if RunsRoot != "docs/eval/runs" {
		t.Fatalf("RunsRoot = %q, want the existing docs/eval/runs convention", RunsRoot)
	}
}

// An empty runner class would produce a trailing-dash directory that sorts
// oddly and says nothing. It is refused at the naming function, which is the
// only place the convention exists.
func TestRunDirName_RefusesAnUnnamedClass(t *testing.T) {
	if got := RunDirName("2026-07-28", "   "); got != "" {
		t.Fatalf("RunDirName with a blank class = %q, want the empty string", got)
	}
}

// RawFileName is derived from the series, so an exporter cannot invent a
// filename the aggregator will not look for.
func TestRawFileName_IsDerivedFromTheSeries(t *testing.T) {
	for _, series := range RawSeriesNames {
		name := RawFileName(series)
		if name == "" || !strings.HasSuffix(name, ".json") {
			t.Fatalf("RawFileName(%q) = %q, want a .json filename", series, name)
		}
		if strings.Contains(name, "_") {
			t.Errorf("RawFileName(%q) = %q, want the hyphenated file convention", series, name)
		}
	}
	if RawFileName("nonsense") != "" {
		t.Error("RawFileName invented a name for an unknown series")
	}
}

// A run directory is only useful if it reads back as what was written. This is
// the round trip AC-4 rests on: two runs sitting side by side compare only when
// both can be loaded by the same reader.
func TestRunDir_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	env := completeEnvironment()
	runs := coldSamples(1000, 2000)
	report := FullRunReport{RunnerClass: "ubuntu-latest", Repo: FullRepoRun{Name: "grpc-go"}}

	index := RunIndex{Date: "2026-07-28", RunnerClass: "ubuntu-latest", Repo: "grpc-go", Environment: env}
	sets := map[string]RawSampleSet{RawSeriesCold: NewRawColdSet("grpc-go", env, runs)}
	if err := WriteRunDir(dir, index, report, sets); err != nil {
		t.Fatalf("WriteRunDir: %v", err)
	}

	gotIndex, gotReport, gotSets, err := ReadRunDir(dir)
	if err != nil {
		t.Fatalf("ReadRunDir: %v", err)
	}
	if gotIndex.HarnessVersion != HarnessVersion || gotIndex.ScorerVersion != ScorerVersion {
		t.Errorf("versions = %s/%s, want %s/%s", gotIndex.HarnessVersion, gotIndex.ScorerVersion, HarnessVersion, ScorerVersion)
	}
	if gotReport.Repo.Name != "grpc-go" {
		t.Errorf("report repo = %q, want grpc-go", gotReport.Repo.Name)
	}
	cold, ok := gotSets[RawSeriesCold]
	if !ok {
		t.Fatal("the cold series did not read back")
	}
	if len(cold.ColdRuns) != 2 || cold.Samples != 2 {
		t.Fatalf("cold runs = %d (samples %d), want 2", len(cold.ColdRuns), cold.Samples)
	}
	// AC-5: a series that was never collected is still LISTED, so its absence
	// is a stated fact rather than something a reader has to notice.
	listed := map[string]RawFileRef{}
	for _, ref := range gotIndex.Raw {
		listed[ref.Series] = ref
	}
	if len(listed) != len(RawSeriesNames) {
		t.Fatalf("run index lists %d series, want all %d", len(listed), len(RawSeriesNames))
	}
	if listed[RawSeriesStalls].Collected {
		t.Error("an uncollected series is listed as collected")
	}
	if _, present := gotSets[RawSeriesStalls]; present {
		t.Error("an uncollected series produced a raw set")
	}
}

// The index's digest is not decoration: raw data edited after the run must be
// caught, or "reproducible from the raw data" only means "reproducible from
// whatever the raw file says today".
func TestReadRunDir_DetectsEditedRawData(t *testing.T) {
	dir := t.TempDir()
	env := completeEnvironment()
	index := RunIndex{Date: "2026-07-28", RunnerClass: "ubuntu-latest", Environment: env}
	sets := map[string]RawSampleSet{RawSeriesCold: NewRawColdSet("grpc-go", env, coldSamples(1000, 2000))}
	if err := WriteRunDir(dir, index, FullRunReport{}, sets); err != nil {
		t.Fatalf("WriteRunDir: %v", err)
	}

	path := filepath.Join(dir, RawDir, RawFileName(RawSeriesCold))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	edited := strings.Replace(string(raw), "\"wallclock_ms\": 1000", "\"wallclock_ms\": 999", 1)
	if edited == string(raw) {
		t.Fatal("the test edit did not apply — the fixture no longer matches the format")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	if _, _, _, err := ReadRunDir(dir); err == nil {
		t.Fatal("edited raw data was accepted")
	} else if !strings.Contains(err.Error(), "digest") && !strings.Contains(err.Error(), "changed after the run") {
		t.Errorf("error = %q, want it to name the digest mismatch", err)
	}
}

// A run directory written by a future format version is refused whole. Reading
// the parts a current build happens to understand is how a half-understood
// artifact becomes evidence.
func TestReadRunDir_RefusesAForeignFormatVersion(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRunDir(dir, RunIndex{Date: "2026-07-28", RunnerClass: "ubuntu-latest"}, FullRunReport{}, nil); err != nil {
		t.Fatalf("WriteRunDir: %v", err)
	}
	path := filepath.Join(dir, RunIndexFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	bumped := strings.Replace(string(raw), "\"format_version\": 1", "\"format_version\": 99", 1)
	if err := os.WriteFile(path, []byte(bumped), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if _, _, _, err := ReadRunDir(dir); err == nil {
		t.Fatal("a future format version was read")
	}
}
