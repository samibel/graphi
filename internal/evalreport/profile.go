package evalreport

// SW-129 (P0-C6): profiles as a BY-PRODUCT of a missed gate.
//
// FR-8 carries two acceptance criteria that only work together — "a missed
// performance gate produces profiles" and "no optimisation without a profile" —
// and PRD §8.5 turns the pair into a process rule. Without automation the
// realistic sequence is: optimise first, justify second. This file is the
// artifact half of the mechanism: what a profile set IS, which gate it answers
// for, and where it sits relative to the run's raw data.
//
// Two design facts a reader has to know, and both are stated in the artifact
// rather than only here:
//
//  1. THE PROFILES COME FROM A DIAGNOSTIC RE-EXECUTION, NOT FROM THE MEASURED
//     RUN. AC-4 forbids profiling the normal path — a harness that profiles
//     everything distorts the numbers it exists to establish — so the profiler
//     is started only after a gate has already been read as missed, and it
//     re-runs the affected scenario on the same machine, the same checkout and
//     the same binary. That localises cost; it does not reproduce the exact
//     timings of the run that failed, and claiming otherwise would be a
//     different kind of dishonesty from the one this program removes.
//
//  2. GO HAS NO FILE-I/O PROFILE. The `io` artifact is the runtime BLOCK
//     profile — goroutine blocking on channels and locks, which is where an
//     ingest worker pool's waiting is observable — published together with the
//     process's real I/O counters. Naming the block profile "the I/O profile"
//     without saying so would be exactly the quiet over-claim FR-8 is written
//     against.
//
// UNKNOWN is not a miss. An unmeasured gate has no measurement to explain, and
// a profile of a scenario that was never gated answers no question.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProfileFormatVersion versions the profile-set envelope: the field names and
// their meaning. It is separate from RawFormatVersion because profiles are a
// separate artifact with a separate reader.
const ProfileFormatVersion = 1

// ProfileDir is where profile sets live inside a run directory, and
// ProfileIndexFile is the profile root's own table of contents — written even
// when the profiles could not be placed inside a run directory, so a set is
// never a directory of unexplained files.
const (
	ProfileDir       = "profiles"
	ProfileIndexFile = "profiles.json"
)

// The four profiles FR-8 asks for. They are constants because the capture, the
// file naming and every reader address them by name.
const (
	ProfileCPU    = "cpu"
	ProfileHeap   = "heap"
	ProfileAllocs = "allocs"
	ProfileIO     = "io"
)

// ProfileKinds is every profile a set contains, in the order the story lists
// them.
var ProfileKinds = []string{ProfileCPU, ProfileHeap, ProfileAllocs, ProfileIO}

// ProfileMechanism says, per kind, exactly which runtime mechanism produced the
// file. It travels INTO the artifact: a profile whose provenance is only in the
// harness source is a file a reader has to trust rather than read.
var ProfileMechanism = map[string]string{
	ProfileCPU: "runtime/pprof CPU profile (pprof.StartCPUProfile) covering the re-executed scenario and nothing else",
	ProfileHeap: "runtime/pprof \"heap\" profile — live objects, written after an explicit runtime.GC() at the end of the " +
		"scenario, so retained memory is what it shows",
	ProfileAllocs: "runtime/pprof \"allocs\" profile — total allocations sampled at runtime.MemProfileRate (the default rate; " +
		"the harness does not raise it, so this is a sample and not a census)",
	ProfileIO: "runtime/pprof \"block\" profile at rate 1. Go has NO file-I/O profile: the runtime does not attribute " +
		"blocking syscalls to a pprof profile, so this shows goroutine blocking on channels and locks — where an ingest " +
		"worker pool's waiting on I/O becomes observable — and the run's real I/O counters are published beside it in " +
		"`io_counters` rather than being inferred from it",
}

// ProfileTriggerMissedGate is the only trigger this story defines. It is a
// field rather than an assumption so a later trigger (a scheduled profile, an
// operator request) cannot be mistaken for a gate failure after the fact.
const ProfileTriggerMissedGate = "missed_gate"

// ProfileMethodNote is the method statement carried by every set.
const ProfileMethodNote = "The measured run was NOT profiled: profiling is off on the normal path, because a harness that profiles " +
	"every run distorts the numbers it exists to establish (FR-8 / SW-129 AC-4). These profiles come from a DIAGNOSTIC " +
	"RE-EXECUTION of the scenario whose gate was missed — same machine, same checkout, same binary, immediately after the " +
	"gate was read — so they localise where the cost is, and they are not a replay of the exact execution that missed the " +
	"gate. Read them with `go tool pprof <file>`."

// ProfileIndexNotes explains the directory to a reader who has only the files.
const ProfileIndexNotes = "SW-129 profile sets: one directory per scenario whose gate was missed, each holding the CPU, heap, " +
	"allocation and I/O profiles of a diagnostic RE-RUN of that scenario (the measured run itself is never profiled). " +
	"Every set names the gate it answers for, with the threshold and the measured value, so a fix can cite the profile of " +
	"the run that motivated it (PRD §8.5). Open any file with `go tool pprof`."

// ProfileFileName is the file one kind is written to. Derived from the kind so
// a capture cannot invent a name a reader will not look for; an unknown kind
// yields "" and the caller refuses rather than writing an orphan.
func ProfileFileName(kind string) string {
	switch kind {
	case ProfileCPU, ProfileHeap, ProfileAllocs, ProfileIO:
		return kind + ".pprof"
	default:
		return ""
	}
}

// MissedGate is one threshold this run did not meet, carried with the series
// whose scenario has to be re-executed to explain it.
type MissedGate struct {
	Series         string  `json:"series"`
	ID             string  `json:"id"`
	PRDMetric      string  `json:"prd_metric,omitempty"`
	Threshold      float64 `json:"threshold"`
	Unit           string  `json:"unit,omitempty"`
	Measured       float64 `json:"measured,omitempty"`
	HasMeasurement bool    `json:"has_measurement"`
	Status         string  `json:"status"`
	Reason         string  `json:"reason,omitempty"`
}

// MissedGates is every gate this report read as FAIL, plus PRD §17's stop rule
// when it triggered, in series order.
//
// FAIL only. An UNKNOWN gate is unmeasured, not exceeded — profiling a scenario
// that produced no number would generate a file that explains nothing and would
// make "a profile exists" stop meaning "a threshold was missed".
func MissedGates(r FullRunReport) []MissedGate {
	var out []MissedGate
	add := func(series string, gates []GateResult) {
		for _, g := range gates {
			if g.Status != StatusFail {
				continue
			}
			out = append(out, MissedGate{
				Series: series, ID: g.ID, PRDMetric: g.PRDMetric,
				Threshold: g.Threshold, Unit: g.Unit,
				Measured: g.Measured, HasMeasurement: g.HasMeasurement,
				Status: g.Status, Reason: g.Reason,
			})
		}
	}

	if s := r.ColdSeries; s != nil {
		add(RawSeriesCold, s.Gates)
		if rule := s.StopRule; rule != nil && rule.Triggered {
			// §17 is not a §12.2 gate, but it is a performance threshold that was
			// exceeded — and a 9 GB peak is the single most profile-worthy thing a
			// run can report.
			out = append(out, MissedGate{
				Series: RawSeriesCold, ID: rule.ID, PRDMetric: "peak_rss",
				Threshold: rule.ThresholdGB, Unit: "GB",
				Measured: rule.ObservedPeakGB, HasMeasurement: rule.ObservedPeakGB > 0,
				Status: StatusFail, Reason: rule.Reason,
			})
		}
	}
	// A blown BUDGET is a missed performance gate too. The §12.2 rows are the
	// PRD's gates; the checked-in per-repo ceilings are the ones the matrix jobs
	// enforce, and a run that exceeded one is exactly as much in need of a
	// profile. Leaving them out would make the automation apply only to the
	// reference scenario, which is not what FR-8 says.
	for _, check := range r.Repo.BudgetChecks {
		if check.Pass {
			continue
		}
		series, _ := BudgetSeries(check.Name)
		out = append(out, MissedGate{
			Series: series, ID: "budget." + check.Name,
			Threshold: check.Budget, Unit: check.Unit,
			Measured: check.Measured, HasMeasurement: true, Status: StatusFail,
			Reason: fmt.Sprintf("%.3f %s exceeds the budget of %.3f %s", check.Measured, check.Unit, check.Budget, check.Unit),
		})
	}
	if s := r.Repo.QueryLatency; s != nil {
		add(RawSeriesQuery, s.Gates)
	}
	if s := r.Repo.Incremental; s != nil {
		add(RawSeriesIncremental, s.Gates)
	}
	if s := r.Repo.Stalls; s != nil {
		add(RawSeriesStalls, s.Gates)
	}
	return out
}

// BudgetSeries maps a per-repo budget metric to the scenario that produces it,
// so a blown ceiling profiles the work behind it rather than something adjacent.
// The second return says whether the mapping is KNOWN or a fallback.
//
// The fallback is the cold index: it is the one scenario every full run
// performs, so a profile of it is never a profile of work that did not happen.
// It is nevertheless a fallback, and a guard test over the budget evaluator's
// own metric names keeps it unreachable — a new budget metric fails that test
// rather than quietly inheriting an attribution nobody chose.
func BudgetSeries(metric string) (string, bool) {
	switch {
	case strings.HasPrefix(metric, "warm_p95_us"), strings.HasPrefix(metric, "warm_p50_us"):
		return RawSeriesQuery, true
	case metric == "index_wallclock_ms", metric == "peak_rss_mb", metric == "stable_peak_rss_mb", metric == "db_size_mb":
		return RawSeriesCold, true
	default:
		return RawSeriesCold, false
	}
}

// MissedGatesBySeries groups the misses by the scenario that has to be
// re-executed, in RawSeriesNames order so two runs profile in the same order.
func MissedGatesBySeries(missed []MissedGate) []string {
	seen := map[string]bool{}
	for _, m := range missed {
		seen[m.Series] = true
	}
	var out []string
	for _, series := range RawSeriesNames {
		if seen[series] {
			out = append(out, series)
		}
	}
	return out
}

// ProfileGateRef is the gate a set answers for, with the numbers that make the
// profile actionable: what was required and what was measured.
type ProfileGateRef struct {
	ID             string  `json:"id"`
	PRDMetric      string  `json:"prd_metric,omitempty"`
	Threshold      float64 `json:"threshold"`
	Unit           string  `json:"unit,omitempty"`
	Measured       float64 `json:"measured,omitempty"`
	HasMeasurement bool    `json:"has_measurement"`
	Status         string  `json:"status"`
	Reason         string  `json:"reason,omitempty"`
}

// GateRefsOf turns the misses of one series into the set's gate references.
func GateRefsOf(missed []MissedGate, series string) []ProfileGateRef {
	var out []ProfileGateRef
	for _, m := range missed {
		if m.Series != series {
			continue
		}
		out = append(out, ProfileGateRef{
			ID: m.ID, PRDMetric: m.PRDMetric, Threshold: m.Threshold, Unit: m.Unit,
			Measured: m.Measured, HasMeasurement: m.HasMeasurement, Status: m.Status, Reason: m.Reason,
		})
	}
	return out
}

// ProfileArtifact is one profile file. Written is separate from Error because
// the two answer different questions — "is there a file to open" and "why is
// there not" — and a set that lost one of four profiles is still worth the
// three it has.
type ProfileArtifact struct {
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Mechanism string `json:"mechanism,omitempty"`
	Digest    string `json:"sha256,omitempty"`
	Bytes     int64  `json:"bytes"`
	Written   bool   `json:"written"`
	Error     string `json:"error,omitempty"`
}

// ProfileIOCounters is what the operating system says the profiled scenario
// actually did to the disk. It is published because the `io` profile is a block
// profile and cannot answer this question; inferring I/O volume from blocking
// would be a guess wearing a measurement's clothes.
type ProfileIOCounters struct {
	// BlockInputOps and BlockOutputOps are getrusage ru_inblock / ru_oublock
	// deltas across the profiled scenario. Available says whether the platform
	// reported them at all — a zero on a platform that does not count them is
	// not "no I/O".
	BlockInputOps  int64  `json:"block_input_ops"`
	BlockOutputOps int64  `json:"block_output_ops"`
	Available      bool   `json:"available"`
	Source         string `json:"source,omitempty"`
	Note           string `json:"note,omitempty"`
}

// ProfileSet is everything produced for ONE scenario whose gate was missed.
type ProfileSet struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`

	Series string `json:"series"`
	Repo   string `json:"repo,omitempty"`
	// Trigger is why this set exists. See ProfileTriggerMissedGate.
	Trigger string `json:"trigger"`
	// Gates are the misses this set answers for — AC-2's association, in the
	// artifact rather than in a naming convention.
	Gates []ProfileGateRef `json:"gates"`
	// Scenario describes the workload that was RE-EXECUTED under the profiler,
	// in the words of the harness that ran it.
	Scenario string `json:"scenario"`
	// Dir is where the files are, relative to the profile root.
	Dir string `json:"dir"`

	StartedAt  string `json:"started_at,omitempty"`
	DurationMS int64  `json:"duration_ms"`

	Artifacts  []ProfileArtifact  `json:"artifacts"`
	IOCounters *ProfileIOCounters `json:"io_counters,omitempty"`

	// Complete is true only when every kind in ProfileKinds was written. It is
	// the field AC-5 hangs on: an incomplete set must not leave a run green.
	Complete bool   `json:"complete"`
	Error    string `json:"error,omitempty"`

	Method string `json:"method"`
}

// NewProfileSet stamps the fields every set shares, so a new capture path
// cannot be added without them.
func NewProfileSet(series, repo, scenario, dir string, gates []ProfileGateRef) ProfileSet {
	return ProfileSet{
		FormatVersion:  ProfileFormatVersion,
		HarnessVersion: HarnessVersion,
		Series:         series,
		Repo:           repo,
		Trigger:        ProfileTriggerMissedGate,
		Gates:          gates,
		Scenario:       scenario,
		Dir:            dir,
		Method:         ProfileMethodNote,
	}
}

// ProfileIndex is the profile root's table of contents.
type ProfileIndex struct {
	FormatVersion  int          `json:"format_version"`
	HarnessVersion string       `json:"harness_version"`
	Trigger        string       `json:"trigger"`
	Sets           []ProfileSet `json:"sets"`
	Notes          string       `json:"notes"`
}

// NewProfileIndex assembles the index and stamps every set's shared fields, so
// a set assembled by hand in a test still round-trips like a real one.
func NewProfileIndex(sets []ProfileSet) ProfileIndex {
	out := make([]ProfileSet, 0, len(sets))
	for _, set := range sets {
		if set.FormatVersion == 0 {
			set.FormatVersion = ProfileFormatVersion
		}
		if set.HarnessVersion == "" {
			set.HarnessVersion = HarnessVersion
		}
		if set.Trigger == "" {
			set.Trigger = ProfileTriggerMissedGate
		}
		if set.Method == "" {
			set.Method = ProfileMethodNote
		}
		out = append(out, set)
	}
	return ProfileIndex{
		FormatVersion:  ProfileFormatVersion,
		HarnessVersion: HarnessVersion,
		Trigger:        ProfileTriggerMissedGate,
		Sets:           out,
		Notes:          ProfileIndexNotes,
	}
}

// WriteProfileIndex writes the index into the profile root.
func WriteProfileIndex(root string, index ProfileIndex) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("evalreport: create profile root: %w", err)
	}
	_, err := writeJSONFile(filepath.Join(root, ProfileIndexFile), index)
	return err
}

// ReadProfileIndex loads a profile root's index back.
func ReadProfileIndex(root string) (ProfileIndex, error) {
	var index ProfileIndex
	raw, err := os.ReadFile(filepath.Join(root, ProfileIndexFile))
	if err != nil {
		return index, fmt.Errorf("evalreport: read %s: %w", ProfileIndexFile, err)
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return index, fmt.Errorf("evalreport: parse %s: %w", ProfileIndexFile, err)
	}
	if index.FormatVersion != ProfileFormatVersion {
		return index, fmt.Errorf("evalreport: profile index has format_version %d, want %d: refusing to read a format this build does not define",
			index.FormatVersion, ProfileFormatVersion)
	}
	return index, nil
}

// IncompleteProfileSets names every set that did not produce the profiles it
// was asked for, with the reason. AC-5: profile generation failing is a fact
// the caller must be able to ask for rather than re-derive from four booleans.
func IncompleteProfileSets(sets []ProfileSet) []string {
	var out []string
	for _, set := range sets {
		if set.Complete {
			continue
		}
		reasons := []string{}
		if set.Error != "" {
			reasons = append(reasons, set.Error)
		}
		for _, a := range set.Artifacts {
			if a.Written {
				continue
			}
			reason := a.Error
			if reason == "" {
				reason = "not written"
			}
			reasons = append(reasons, a.Kind+": "+reason)
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "incomplete, with no reason recorded")
		}
		out = append(out, fmt.Sprintf("%s: %s", set.Series, strings.Join(reasons, "; ")))
	}
	return out
}

// ProfileSetRef is how a run index points at a set: where it is, which gates it
// answers for, and whether it is whole — without restating the set.
type ProfileSetRef struct {
	Series   string   `json:"series"`
	Dir      string   `json:"dir"`
	Gates    []string `json:"gates"`
	Written  int      `json:"profiles_written"`
	Expected int      `json:"profiles_expected"`
	Complete bool     `json:"complete"`
	Error    string   `json:"error,omitempty"`
}

// ProfileRefs summarises sets for the run index.
func ProfileRefs(sets []ProfileSet) []ProfileSetRef {
	var out []ProfileSetRef
	for _, set := range sets {
		ref := ProfileSetRef{
			Series: set.Series, Dir: set.Dir,
			Expected: len(ProfileKinds), Complete: set.Complete, Error: set.Error,
		}
		for _, g := range set.Gates {
			ref.Gates = append(ref.Gates, g.ID)
		}
		for _, a := range set.Artifacts {
			if a.Written {
				ref.Written++
			}
		}
		out = append(out, ref)
	}
	return out
}

// ProfileDigest is the SHA-256 of a written profile, so a file that changed
// after the run is detectable rather than merely unlikely — the same rule the
// raw sample files follow.
func ProfileDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
