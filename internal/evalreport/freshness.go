package evalreport

// SW-126 (P0-C3): the freshness and incremental-update payload.
//
// The repository had a partial answer in the wrong place. `cmd/eval/perf.go`
// measured `incremental_update` as a SINGLE median over a copied tier-1
// fixture: one change, one change class, no sequence, no distribution — and
// freshness, the interval from a file changing to a query answering the new
// state, was not measured anywhere. FR-8 asks for at least 100 incremental
// changes with p50 AND p95 for both.
//
// Like the cold series and the query-latency series, this is a schema problem
// before it is a harness problem, and it enforces the same three rules
// structurally:
//
//   - EVERY INDIVIDUAL MEASUREMENT IS RETAINED (AC-7). `Changes` holds one
//     sample per attempted change; every published percentile is derived from
//     those samples by RecomputeIncremental, which is also what a consumer
//     (SW-128's aggregator, and the tests here) calls to reproduce them. A
//     published number that disagrees with its samples is a test failure, not a
//     discrepancy nobody can see.
//   - A FAILED CHANGE IS COUNTED, NEVER DROPPED (AC-6). A change that errored or
//     never converged stays in `Changes` with its stage and its error, is
//     counted in `Failed`, and degrades the series verdict. What it cannot do is
//     contribute a latency it does not have — so the presence of each
//     measurement is an explicit boolean rather than a zero that has to be
//     interpreted.
//   - UNKNOWN IS NOT A PASS (PRD §8.2). Fewer than IncrementalChangeMinimum
//     completed changes, or a change class the sequence never exercised, makes
//     the freshness gate UNKNOWN rather than letting a percentile over the wrong
//     sequence read as FR-8's measurement.
//
// Percentiles come from PercentileInt64 in coldrun.go — the one nearest-rank
// implementation in the tree, so update p50 and freshness p95 cannot disagree
// about what a rank means.

import "sort"

// IncrementalChangeMinimum is FR-8's floor: at least 100 incremental changes
// measured against the reference scenario. Falling short does not FAIL the
// freshness gate — it makes it UNKNOWN, because a distribution over fewer
// changes is not the distribution the PRD asked for.
const IncrementalChangeMinimum = 100

// Change classes. AC-2 names them: the sequence must cover adding, modifying
// and deleting a file, plus at least one change that touches cross-package
// edges. They are constants because the coverage check, the harness and the
// artifact all address them by name.
const (
	ChangeClassAdd          = "add"
	ChangeClassModify       = "modify"
	ChangeClassDelete       = "delete"
	ChangeClassCrossPackage = "cross_package"
)

// RequiredChangeClasses is AC-2 as data: the classes a sequence must exercise
// before it is the sequence FR-8 asked for. Order is fixed so the artifact's
// coverage table is stable.
var RequiredChangeClasses = []string{
	ChangeClassAdd,
	ChangeClassModify,
	ChangeClassDelete,
	ChangeClassCrossPackage,
}

// Change lifecycle. A change either produced a usable measurement or it did
// not; there is no third state, and a failed change never leaves the report.
const (
	ChangeCompleted = "completed"
	ChangeFailed    = "failed"
)

// The stage a failed change failed at. Recording the stage is what makes AC-6
// readable: "the edit could not be written" and "the sync errored" and "the
// graph never answered the new state" are three different defects, and a bare
// `failed` would hide which one happened.
const (
	ChangeStageApply     = "apply"
	ChangeStageUpdate    = "update"
	ChangeStageConverge  = "converge"
	ChangeStageUnchanged = ""
)

// ChangeSample is ONE attempted incremental change.
//
// UpdateMeasured and FreshnessMeasured are explicit rather than inferred from a
// non-zero duration, and they are independent: a change whose incremental
// update completed but whose result never became answerable HAS an update
// latency and has NO freshness. Collapsing the two would either discard a real
// measurement or invent one.
type ChangeSample struct {
	Step  int    `json:"step"`
	Class string `json:"class"`
	// Path is the repo-relative POSIX path the change targeted.
	Path string `json:"path"`
	// Symbol is the identifier the change introduces or removes; Expect states
	// what "the new state" means for this step, in words, so the convergence
	// criterion is readable off the artifact rather than off the harness.
	Symbol string `json:"symbol,omitempty"`
	Expect string `json:"expect,omitempty"`

	Status      string `json:"status"`
	FailedStage string `json:"failed_stage,omitempty"`
	Error       string `json:"error,omitempty"`

	// UpdateUS is the incremental ingest call, in microseconds.
	UpdateUS       int64 `json:"update_us"`
	UpdateMeasured bool  `json:"update_measured"`
	// FreshnessUS is the interval from the file change landing on disk to the
	// first query that answered the new state. It therefore CONTAINS UpdateUS —
	// see FreshnessDefinitionNote.
	FreshnessUS       int64 `json:"freshness_us"`
	FreshnessMeasured bool  `json:"freshness_measured"`
	// Probes is how many convergence probes were issued. 1 is the synchronous
	// case (the update returned and the very first query answered the new
	// state); more than 1 means the harness waited, and that wait is inside the
	// freshness figure.
	Probes int `json:"probes"`
}

// ChangeClassCoverage is one class's presence in the sequence. Required marks
// the AC-2 classes, so a coverage table with a zero on a required row is
// self-explaining.
type ChangeClassCoverage struct {
	Class    string `json:"class"`
	Steps    int    `json:"steps"`
	Required bool   `json:"required"`
}

// ChangeClassLatency is one class's two distributions, so a regression is
// attributable to a change class instead of only to the pooled total.
type ChangeClassLatency struct {
	Class     string       `json:"class"`
	Changes   int          `json:"changes"`
	Update    LatencyStats `json:"update"`
	Freshness LatencyStats `json:"freshness"`
}

// CrossPackageEvidence is why the cross-package class really is cross-package.
//
// AC-2 asks for "at least one change that touches cross-package edges". A
// harness that merely labelled a step `cross_package` would satisfy the letter
// of that and prove nothing, so the target files are chosen from the GRAPH —
// symbols with inbound edges from other directories — and the count that made
// them qualify is published beside them.
type CrossPackageEvidence struct {
	// Satisfied is false when no qualifying target was found. It is not an
	// error: a single-package repository genuinely has no cross-package edges,
	// and saying so is better than relabelling an in-package change.
	Satisfied bool   `json:"satisfied"`
	Reason    string `json:"reason,omitempty"`
	Method    string `json:"method,omitempty"`
	// Targets are the chosen files with their qualifying evidence.
	Targets []CrossPackageTarget `json:"targets,omitempty"`
	// Examined is how many candidate symbols were inspected to find them.
	Examined int `json:"examined"`
}

// CrossPackageTarget is one qualifying file and the evidence that qualified it.
type CrossPackageTarget struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	// InboundFromOtherDirs is the number of neighbours (callers and referencing
	// symbols) whose source file lives in a different directory.
	InboundFromOtherDirs int `json:"inbound_from_other_dirs"`
	// ExampleSources are a few of those neighbours' paths, so the count can be
	// spot-checked rather than believed.
	ExampleSources []string `json:"example_sources,omitempty"`
}

// ChangeSequenceInfo describes the change sequence as a reproducible artifact.
//
// PRD §16 wants two consecutive green runs, and two runs over different change
// sequences are not two runs of the same measurement. So the sequence is
// DEFINED, not random (AC-1): the method is stated, and a digest over the
// ordered steps makes a drift between two runs one string comparison.
type ChangeSequenceInfo struct {
	Steps  int    `json:"steps"`
	Cycle  int    `json:"cycle_length"`
	Method string `json:"method"`
	// Digest is SampleDigest over each step's "class|path|symbol" descriptor, in
	// order. Order-sensitive on purpose: the same steps in a different order are
	// a different sequence, because the tree each step sees depends on the ones
	// before it.
	Digest string `json:"digest"`
	// SourceFiles is how many indexed files the rotation draws from.
	SourceFiles  int                  `json:"source_files"`
	CrossPackage CrossPackageEvidence `json:"cross_package"`
}

// IncrementalSeries is the whole freshness and incremental-update measurement
// for one repository.
type IncrementalSeries struct {
	Repo string `json:"repo"`
	// Requested is the number of changes this invocation asked for; Minimum is
	// FR-8's floor, carried in the artifact so a reader does not need the PRD to
	// see whether the sample was large enough.
	Requested int `json:"changes_requested"`
	Minimum   int `json:"minimum_changes"`
	Completed int `json:"changes_completed"`
	Failed    int `json:"changes_failed"`
	// Sufficient is Completed >= Minimum. ClassesCovered is AC-2: every required
	// change class was exercised. They are separate booleans because they are
	// separate claims — 200 changes that never deleted a file are plentiful and
	// still not the sequence FR-8 described.
	Sufficient     bool `json:"sufficient"`
	ClassesCovered bool `json:"classes_covered"`

	Sequence ChangeSequenceInfo    `json:"sequence"`
	Coverage []ChangeClassCoverage `json:"coverage"`
	Changes  []ChangeSample        `json:"changes"`

	// Update and Freshness are the pooled distributions over every change that
	// produced the respective measurement (AC-3, AC-4).
	Update    LatencyStats         `json:"update"`
	Freshness LatencyStats         `json:"freshness"`
	PerClass  []ChangeClassLatency `json:"per_class"`

	Gates    []GateResult `json:"gates,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Status   string       `json:"status"`

	// FreshnessDefinition, TimingMethod and AggregateMethod make the artifact
	// explain its own measurement and its own arithmetic (AC-4).
	FreshnessDefinition string `json:"freshness_definition"`
	TimingMethod        string `json:"timing_method"`
	AggregateMethod     string `json:"aggregate_method"`
	ScopeLimitation     string `json:"scope_limitation"`
	Notes               string `json:"notes,omitempty"`
}

// FreshnessDefinitionNote is AC-4's "the report documents when fresh is
// considered reached", stated in the artifact rather than in a story nobody
// reading the JSON has.
const FreshnessDefinitionNote = "FRESH is reached at the first query that answers the NEW state. The clock starts the instant the file " +
	"change is durably on disk (the write or unlink has returned) and stops at the first convergence probe that observes " +
	"the change: for an added, modified or cross-package change, the symbol the change introduces is answerable by a " +
	"search; for a delete, the symbol the removed file defined is no longer answerable. Freshness therefore CONTAINS the " +
	"incremental update — update_us is the ingest call, freshness_us is change-to-answer — and freshness_us >= update_us " +
	"holds for every completed change."

// IncrementalScopeLimitation is the honest boundary of what this measurement
// covers. It matters: a reader who assumed watch latency was included would
// read the freshness gate as a stronger claim than it is.
const IncrementalScopeLimitation = "The harness drives the incremental update EXPLICITLY, in the `graphi sync` shape: change the file, run the " +
	"incremental ingest, query. It therefore EXCLUDES filesystem-watch detection latency — the interval between a write " +
	"and engine/watch noticing it — which this story does not measure and does not alter (AC-8). A daemon-driven " +
	"freshness figure would be this one plus watch detection, never less."

// IncrementalTimingMethodNote states what is inside each clock and what is not.
const IncrementalTimingMethodNote = "Two clocks per change, both around the operation and nothing else. update_us is time.Since around the " +
	"incremental ingest call for the changed path — building the edit content, writing it and choosing the next step all " +
	"happen outside it. freshness_us starts immediately after the file mutation returns and stops at the probe that " +
	"observed the new state, so it contains the update plus any waiting the harness did. No warmup: an incremental " +
	"update is a cold-by-nature event and repeating it to warm it up would measure something a user never experiences."

// IncrementalAggregateMethodNote documents the derivation inline.
const IncrementalAggregateMethodNote = "nearest-rank percentile (rank = ceil(p/100 * n), 1-based) over the retained per-change samples, ascending — the " +
	"same implementation the cold series and the query-latency series use. A change contributes to the update " +
	"distribution when update_measured is true and to the freshness distribution when freshness_measured is true; the two " +
	"counts differ exactly by the changes whose update completed but which never converged. Failed changes are counted in " +
	"changes_failed and retained in `changes`, never removed. Every value here is reproducible from `changes` with " +
	"evalreport.RecomputeIncremental."

// IncrementalNotes explains the artifact to a reader who has only the JSON.
const IncrementalNotes = "SW-126 freshness and incremental measurement: a DEFINED, reproducible change sequence — not random mutation — " +
	"executed against a pinned corpus repository, reporting incremental-update and freshness p50 and p95 with every " +
	"individual measurement retained. The tier-1 fixture path in cmd/eval/perf.go is a PR-gate smoke check over a copied " +
	"fixture and is NOT this measurement; it is never P0 evidence. No gate is read unless the run is the reference " +
	"scenario on the reference class and from the frozen candidate, with at least minimum_changes completed changes and " +
	"every required change class exercised. Otherwise it is UNKNOWN, which is not a PASS (PRD §8.2)."

// IncrementalRecomputation is RecomputeIncremental's result: the same statistic
// sets the series publishes, derived from nothing but the retained samples.
type IncrementalRecomputation struct {
	Update    LatencyStats
	Freshness LatencyStats
	Classes   map[string]ChangeClassLatency
}

// RecomputeIncremental derives every published statistic from the retained
// per-change samples. The harness calls it to PRODUCE the statistics and a
// consumer calls it to REPRODUCE them (AC-7).
//
// It reads only the per-change samples and their explicit measured flags, so a
// failed change contributes nothing to a distribution while still being present
// in the input — which is precisely the AC-6 property: retained, counted, and
// not silently averaged in as a zero.
func RecomputeIncremental(changes []ChangeSample) IncrementalRecomputation {
	var update, freshness []int64
	classUpdate := map[string][]int64{}
	classFreshness := map[string][]int64{}
	classChanges := map[string]int{}
	for _, c := range changes {
		classChanges[c.Class]++
		if c.UpdateMeasured {
			update = append(update, c.UpdateUS)
			classUpdate[c.Class] = append(classUpdate[c.Class], c.UpdateUS)
		}
		if c.FreshnessMeasured {
			freshness = append(freshness, c.FreshnessUS)
			classFreshness[c.Class] = append(classFreshness[c.Class], c.FreshnessUS)
		}
	}
	out := IncrementalRecomputation{
		Update:    LatencyStatsFrom(update),
		Freshness: LatencyStatsFrom(freshness),
		Classes:   make(map[string]ChangeClassLatency, len(classChanges)),
	}
	for class, n := range classChanges {
		out.Classes[class] = ChangeClassLatency{
			Class:     class,
			Changes:   n,
			Update:    LatencyStatsFrom(classUpdate[class]),
			Freshness: LatencyStatsFrom(classFreshness[class]),
		}
	}
	return out
}

// ChangeClassCoverageOf counts each class's steps in the sequence and marks the
// AC-2 required ones. The returned rows are in RequiredChangeClasses order
// first, then any additional class sorted, so the table is stable across runs.
func ChangeClassCoverageOf(changes []ChangeSample) []ChangeClassCoverage {
	counts := map[string]int{}
	for _, c := range changes {
		counts[c.Class]++
	}
	out := make([]ChangeClassCoverage, 0, len(counts))
	seen := map[string]bool{}
	for _, class := range RequiredChangeClasses {
		out = append(out, ChangeClassCoverage{Class: class, Steps: counts[class], Required: true})
		seen[class] = true
	}
	var extra []string
	for class := range counts {
		if !seen[class] {
			extra = append(extra, class)
		}
	}
	sort.Strings(extra)
	for _, class := range extra {
		out = append(out, ChangeClassCoverage{Class: class, Steps: counts[class]})
	}
	return out
}

// AllRequiredClassesCovered reports whether every AC-2 class was exercised by a
// COMPLETED change. A class whose only step failed is not covered: the sequence
// attempted it, the measurement does not contain it, and reporting otherwise
// would let a broken class hide behind the coverage table.
func AllRequiredClassesCovered(changes []ChangeSample) bool {
	completed := map[string]int{}
	for _, c := range changes {
		if c.Status == ChangeCompleted {
			completed[c.Class]++
		}
	}
	for _, class := range RequiredChangeClasses {
		if completed[class] == 0 {
			return false
		}
	}
	return true
}
