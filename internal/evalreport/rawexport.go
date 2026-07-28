package evalreport

// SW-128 (P0-C5): the raw-sample format, and the versioning that keeps two
// methodologies from being averaged together.
//
// FR-9 asks for the individual measurements to be stored, separately from the
// aggregate report, in a defined machine-readable format. SW-124…SW-127 each
// already retain their samples INSIDE their series — but a series also carries
// the percentiles derived from them, so "recompute the report from the raw
// data" over that shape would be checking a number against a file that already
// contains it. This file is the separation: a RawSampleSet carries samples and
// the membership lists a recomputation needs, and NOTHING derived.
//
// The other half is PRD risk 9. A raw file states the format version it was
// written in and the harness version that produced it. An unknown format is
// refused rather than half-read, and a run whose raw files disagree about the
// harness version is refused outright — that mixture is precisely the silent
// methodology drift the risk describes, and a warning would not stop it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawFormatVersion versions the raw-sample ENVELOPE — the field names and their
// meaning. It changes when an old reader could misread a new file.
const RawFormatVersion = 1

// HarnessVersion versions the measurement METHOD: what is inside a timed
// region, which runs count, how a sample is produced. Two runs whose harness
// versions differ did not measure the same question, so their numbers are not
// comparable and must never be pooled. It is stamped on every raw file
// (AC-7).
//
// Bump it when the meaning of a sample changes, not when the code moves.
const HarnessVersion = "p0-perf/1"

// ScorerVersion versions the DERIVATION — the aggregator's arithmetic. It is
// separate from HarnessVersion because the two drift independently: a
// percentile definition can change while the measurement is untouched, and a
// reader needs to know which of the two moved.
const ScorerVersion = "p0-aggregate/1"

// Raw series names. One name per SW-124…SW-127 harness; they are constants
// because the exporter, the file naming and the aggregator all address them.
const (
	RawSeriesCold        = "cold_index"
	RawSeriesQuery       = "query_latency"
	RawSeriesIncremental = "incremental"
	RawSeriesStalls      = "progress_stalls"
)

// RawSeriesNames is every known series, in report order.
var RawSeriesNames = []string{RawSeriesCold, RawSeriesQuery, RawSeriesIncremental, RawSeriesStalls}

// Query pool kinds. A class pools a whole query class; a gate pool is the
// operation subset ONE PRD §12.2 gate is read over, which is not always a whole
// class.
const (
	RawPoolClass = "class"
	RawPoolGate  = "gate_pool"
)

// RunsRoot is the checked-in run directory the historical evidence already
// lives in. AC-4 is that a new run sits beside 2026-07-15-ubuntu-latest and
// compares, so the convention is anchored here rather than passed around.
const RunsRoot = "docs/eval/runs"

// RunIndexFile and EnvironmentFile are the fixed names inside a run directory.
// AggregateFile is what the aggregator writes.
const (
	RunIndexFile    = "run.json"
	EnvironmentFile = "environment.json"
	AggregateFile   = "aggregate.json"
	RawDir          = "raw"
)

// RawQueryOperation is one stable operation's raw timings plus the class it
// belongs to. The class is carried rather than inferred: SW-125 made the
// operation → class mapping explicit precisely so nothing derives it from an
// operation's name.
type RawQueryOperation struct {
	Operation string  `json:"operation"`
	Class     string  `json:"class"`
	SamplesUS []int64 `json:"samples_us"`
}

// RawQueryPool is a membership list — which operations pool into one class or
// one gate. Without it a recomputation could not reproduce a class or pool
// statistic from per-operation samples, and inferring membership would be a
// second definition of the mapping.
type RawQueryPool struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Operations []string `json:"operations"`
}

// RawSampleSet is ONE series' raw measurements.
//
// Exactly one payload slice is populated, chosen by Series. Collected says the
// harness RAN this series: a collected set with zero samples and an absent set
// are different claims, and SW-127's silent index is the case that proves it —
// an index that emitted no progress ran and produced nothing, which is a
// failure, not missing data.
type RawSampleSet struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	Series         string `json:"series"`
	Repo           string `json:"repo,omitempty"`

	// Collected is whether the series was measured at all; Samples is how many
	// raw records it produced. Both are published so a reader never has to
	// infer either from the length of a slice that JSON may have omitted.
	Collected bool `json:"collected"`
	Samples   int  `json:"samples"`

	// Environment is stamped on every raw file, not only on the run index, so a
	// file separated from its directory still says what produced it.
	Environment RunEnvironment `json:"environment"`

	ColdRuns        []ColdRunSample     `json:"cold_runs,omitempty"`
	QueryOperations []RawQueryOperation `json:"query_operations,omitempty"`
	QueryPools      []RawQueryPool      `json:"query_pools,omitempty"`
	Changes         []ChangeSample      `json:"changes,omitempty"`
	Intervals       []StallInterval     `json:"intervals,omitempty"`
}

// newRawSet stamps the version fields every set shares. Centralised so a new
// series cannot be added without them.
func newRawSet(series, repo string, env RunEnvironment) RawSampleSet {
	return RawSampleSet{
		FormatVersion:  RawFormatVersion,
		HarnessVersion: HarnessVersion,
		Series:         series,
		Repo:           repo,
		Collected:      true,
		Environment:    env,
	}
}

// NewRawColdSet is SW-124's per-run samples. Aborted runs travel too: the count
// of attempted runs must reconcile with the count of measurements, and a
// recomputation that never saw the aborts could not check that it did.
func NewRawColdSet(repo string, env RunEnvironment, runs []ColdRunSample) RawSampleSet {
	set := newRawSet(RawSeriesCold, repo, env)
	set.ColdRuns = runs
	set.Samples = len(runs)
	return set
}

// NewRawQuerySet is SW-125's per-execution timings plus the membership lists.
func NewRawQuerySet(repo string, env RunEnvironment, ops []RawQueryOperation, pools []RawQueryPool) RawSampleSet {
	set := newRawSet(RawSeriesQuery, repo, env)
	set.QueryOperations = ops
	set.QueryPools = pools
	for _, op := range ops {
		set.Samples += len(op.SamplesUS)
	}
	return set
}

// NewRawIncrementalSet is SW-126's per-change samples, failures included — a
// failed change is retained and counted, never dropped.
func NewRawIncrementalSet(repo string, env RunEnvironment, changes []ChangeSample) RawSampleSet {
	set := newRawSet(RawSeriesIncremental, repo, env)
	set.Changes = changes
	set.Samples = len(changes)
	return set
}

// NewRawStallSet is SW-127's intervals. An EMPTY interval slice is a real
// result here — the index ran and stayed silent — so the set is Collected with
// zero samples rather than absent.
func NewRawStallSet(repo string, env RunEnvironment, intervals []StallInterval) RawSampleSet {
	set := newRawSet(RawSeriesStalls, repo, env)
	set.Intervals = intervals
	set.Samples = len(intervals)
	return set
}

// CheckRawCompatibility is the AC-7 gate, run before anything is aggregated.
//
// It refuses three things, all for the same reason: aggregating data whose
// meaning is not established. An unrecognised format version cannot be read at
// all; an unrecognised series has no recomputation; and a run whose files
// disagree about the harness version is two measurements wearing one
// directory's name, which is PRD risk 9 exactly. A warning would not stop any
// of them, so this returns an error and the caller exits.
func CheckRawCompatibility(sets []RawSampleSet) error {
	known := map[string]bool{}
	for _, name := range RawSeriesNames {
		known[name] = true
	}
	versions := map[string][]string{}
	for _, set := range sets {
		if set.FormatVersion != RawFormatVersion {
			return fmt.Errorf("evalreport: raw series %q has format_version %d, want %d: refusing to read a format this build does not define",
				set.Series, set.FormatVersion, RawFormatVersion)
		}
		if !known[set.Series] {
			return fmt.Errorf("evalreport: unknown raw series %q (known: %s)", set.Series, strings.Join(RawSeriesNames, ", "))
		}
		versions[set.HarnessVersion] = append(versions[set.HarnessVersion], set.Series)
	}
	if len(versions) > 1 {
		names := make([]string, 0, len(versions))
		for v, series := range versions {
			sort.Strings(series)
			names = append(names, fmt.Sprintf("%s (%s)", v, strings.Join(series, ", ")))
		}
		sort.Strings(names)
		return fmt.Errorf("evalreport: this run mixes harness_version values — %s: an old and a new methodology are not one measurement and are not aggregated together (PRD risk 9)",
			strings.Join(names, "; "))
	}
	return nil
}

// RawFileName is the file one series is written to. Derived from the series so
// an exporter cannot invent a name the aggregator will not look for; an unknown
// series yields "" and the caller fails rather than writing an orphan.
func RawFileName(series string) string {
	switch series {
	case RawSeriesCold:
		return "cold-index.json"
	case RawSeriesQuery:
		return "query-latency.json"
	case RawSeriesIncremental:
		return "incremental.json"
	case RawSeriesStalls:
		return "progress-stalls.json"
	default:
		return ""
	}
}

// RunDirName is AC-4's convention: <date>-<runner-class>, matching the
// committed 2026-07-15-ubuntu-latest directories. The class is normalised to a
// path-safe slug so the name is predictable from the class without the caller
// knowing the rule; a blank class yields "" and the caller refuses, because a
// trailing-dash directory names nothing.
func RunDirName(date, runnerClass string) string {
	slug := slugify(runnerClass)
	if slug == "" || strings.TrimSpace(date) == "" {
		return ""
	}
	return date + "-" + slug
}

// RunDirPath anchors the convention at the directory the historical evidence
// already lives in, so two runs sit side by side and compare.
func RunDirPath(date, runnerClass string) string {
	name := RunDirName(date, runnerClass)
	if name == "" {
		return ""
	}
	return RunsRoot + "/" + name
}

// slugify lowercases and collapses everything that is not a letter, a digit or
// a dash into single dashes, then trims them from the ends.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// RawFileRef is one raw file as the run index records it: where it is, what is
// in it, and a digest over its bytes so a file edited after the fact is
// detectable rather than merely unlikely.
type RawFileRef struct {
	Series    string `json:"series"`
	File      string `json:"file"`
	Digest    string `json:"sha256"`
	Collected bool   `json:"collected"`
	Samples   int    `json:"samples"`
}

// RunIndex is the run directory's table of contents: the versions it was
// produced under, the environment it ran in, the published report, and the raw
// files every published number must be reproducible from.
type RunIndex struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`

	Date        string `json:"date"`
	RunnerClass string `json:"runner_class"`
	Repo        string `json:"repo,omitempty"`

	// Report is the published aggregate report's filename inside this
	// directory. It is what the aggregator reads its "published" numbers from.
	Report string `json:"report"`
	// Raw lists every series file, including the ones that were not collected —
	// an absent series must be visible as absent (AC-5), not simply missing
	// from a list nobody counts.
	Raw []RawFileRef `json:"raw"`

	Environment RunEnvironment `json:"environment"`
	Notes       string         `json:"notes,omitempty"`
}

// RunIndexNotes explains the directory to a reader who has only the files.
const RunIndexNotes = "SW-128 raw-sample export: `report` is the published aggregate; `raw/` holds the individual measurements it was " +
	"derived from, in a format that carries samples and membership lists only — no percentile travels in a raw file, so " +
	"reproducing the report from it is a real check rather than a comparison of a number with itself. Run " +
	"`go run ./cmd/eval -aggregate <this directory>` to recompute every published metric from `raw/` and diff it against " +
	"`report`: a discrepancy exits non-zero, and so does a run whose raw data or environment is incomplete. " +
	"`format_version` versions the file shape and `harness_version` the measurement method — a directory whose raw files " +
	"disagree about either is refused rather than averaged (PRD risk 9)."

// WriteRunDir writes a complete run directory: the run index, the environment,
// the published report and one file per series.
//
// Every series in RawSeriesNames gets a reference in the index whether or not
// it was collected, because "this run has no freshness data" is a fact a reader
// must be able to read off the index rather than infer from a file that is not
// there.
func WriteRunDir(dir string, index RunIndex, report FullRunReport, sets map[string]RawSampleSet) error {
	if err := os.MkdirAll(filepath.Join(dir, RawDir), 0o755); err != nil {
		return fmt.Errorf("evalreport: create run dir: %w", err)
	}
	index.FormatVersion = RawFormatVersion
	index.HarnessVersion = HarnessVersion
	index.ScorerVersion = ScorerVersion
	if index.Notes == "" {
		index.Notes = RunIndexNotes
	}
	if index.Report == "" {
		index.Report = "report.json"
	}

	index.Raw = index.Raw[:0]
	for _, series := range RawSeriesNames {
		name := RawFileName(series)
		set, ok := sets[series]
		if !ok {
			// Not collected. The reference is still written so the absence is
			// stated rather than inferred from a missing file.
			index.Raw = append(index.Raw, RawFileRef{Series: series, File: RawDir + "/" + name})
			continue
		}
		digest, err := writeJSONFile(filepath.Join(dir, RawDir, name), set)
		if err != nil {
			return err
		}
		index.Raw = append(index.Raw, RawFileRef{
			Series:    series,
			File:      RawDir + "/" + name,
			Digest:    digest,
			Collected: set.Collected,
			Samples:   set.Samples,
		})
	}

	if err := WriteFullRunJSON(report, filepath.Join(dir, index.Report)); err != nil {
		return err
	}
	if _, err := writeJSONFile(filepath.Join(dir, EnvironmentFile), index.Environment); err != nil {
		return err
	}
	if _, err := writeJSONFile(filepath.Join(dir, RunIndexFile), index); err != nil {
		return err
	}
	return nil
}

// writeJSONFile writes v as stable indented JSON and returns the SHA-256 of the
// bytes it wrote, so the index's digest is over exactly what is on disk.
func writeJSONFile(path string, v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evalreport: marshal %s: %w", filepath.Base(path), err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("evalreport: write %s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ReadRunDir loads a run directory back: the index, the published report and
// every raw file the index lists as collected.
//
// A listed-but-uncollected series is simply absent from the returned map, which
// is how the aggregator sees "no raw data for this series" and reports its
// metrics UNKNOWN. A listed-and-collected file that will not load is an ERROR,
// not an absence: the index says it is there, so failing to read it is a broken
// artifact rather than a measurement that was never taken.
func ReadRunDir(dir string) (RunIndex, FullRunReport, map[string]RawSampleSet, error) {
	var index RunIndex
	var report FullRunReport
	sets := map[string]RawSampleSet{}

	raw, err := os.ReadFile(filepath.Join(dir, RunIndexFile))
	if err != nil {
		return index, report, nil, fmt.Errorf("evalreport: read %s: %w", RunIndexFile, err)
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return index, report, nil, fmt.Errorf("evalreport: parse %s: %w", RunIndexFile, err)
	}
	if index.FormatVersion != RawFormatVersion {
		return index, report, nil, fmt.Errorf("evalreport: run index has format_version %d, want %d: refusing to read a format this build does not define",
			index.FormatVersion, RawFormatVersion)
	}

	reportName := index.Report
	if reportName == "" {
		reportName = "report.json"
	}
	reportBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(reportName)))
	if err != nil {
		return index, report, nil, fmt.Errorf("evalreport: read published report %s: %w", reportName, err)
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		return index, report, nil, fmt.Errorf("evalreport: parse published report %s: %w", reportName, err)
	}

	for _, ref := range index.Raw {
		if !ref.Collected {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(ref.File))
		b, err := os.ReadFile(path)
		if err != nil {
			return index, report, nil, fmt.Errorf("evalreport: read raw series %s: %w", ref.Series, err)
		}
		if ref.Digest != "" {
			sum := sha256.Sum256(b)
			if got := hex.EncodeToString(sum[:]); got != ref.Digest {
				return index, report, nil, fmt.Errorf("evalreport: raw series %s does not match the digest in %s (have %s, want %s): the raw data changed after the run",
					ref.Series, RunIndexFile, got, ref.Digest)
			}
		}
		var set RawSampleSet
		if err := json.Unmarshal(b, &set); err != nil {
			return index, report, nil, fmt.Errorf("evalreport: parse raw series %s: %w", ref.Series, err)
		}
		sets[ref.Series] = set
	}
	return index, report, sets, nil
}

// OrderedRawSets returns the loaded sets in RawSeriesNames order, so a
// compatibility check and any summary read them deterministically.
func OrderedRawSets(sets map[string]RawSampleSet) []RawSampleSet {
	out := make([]RawSampleSet, 0, len(sets))
	for _, series := range RawSeriesNames {
		if set, ok := sets[series]; ok {
			out = append(out, set)
		}
	}
	return out
}
