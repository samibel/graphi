package evalreport

// SW-124 (P0-C1): the cold-run SERIES payload.
//
// One `-full-run` measures one cold index. FR-8 requires at least ten per
// reference scenario and reports p50 AND p95 — and a distribution cannot be
// derived from a single sample. This file is the schema that repetition needs:
// every individual run is kept (ColdRunSample), the aggregates are derived from
// those samples by one exported function (RecomputeColdAggregates), and a run
// that aborted is counted rather than dropped.
//
// The rule the schema enforces structurally: NOTHING published here is a number
// a reader has to take on trust. Every aggregate is recomputable from `runs`
// with RecomputeColdAggregates, which is the groundwork SW-128's aggregator
// builds on — and the only percentile implementation in the tree, so a p50 and
// a p95 can never disagree about what "nearest rank" means.
//
// Status values follow PRD §8.2: UNKNOWN is not a PASS. A gate that was not
// exercised, or was exercised on something other than the frozen candidate,
// reads UNKNOWN and stays visible.

import "sort"

// Measurement status values. UNKNOWN is deliberately a first-class value: PRD
// §8.2 requires an unmeasured gate to stay visible rather than be assumed.
const (
	StatusPass    = "PASS"
	StatusFail    = "FAIL"
	StatusUnknown = "UNKNOWN"
)

// Cold-run lifecycle. A run either produced a usable cold-index measurement or
// it did not; there is no third state, and an aborted run never leaves the
// report (AC-5).
const (
	ColdRunCompleted = "completed"
	ColdRunAborted   = "aborted"
)

// ColdRunMinimum is FR-8's minimum run count per reference scenario. Fewer
// completed runs than this does not FAIL the gates — it makes them UNKNOWN,
// because a distribution over nine samples is not the distribution the PRD
// asked for.
const ColdRunMinimum = 10

// Page-cache states recorded per run. "Cold" is produced and verified, never
// assumed (AC-1), so the state the protocol actually reached is recorded
// verbatim rather than inferred from the fact that a protocol was requested.
const (
	PageCacheDropped     = "dropped"
	PageCacheNotDropped  = "not_dropped"
	PageCacheDropFailed  = "drop_failed"
	PageCacheUnsupported = "unsupported"
)

// ColdState is the per-run evidence that the run really was cold. Coldness is
// two independent claims — a store that did not exist, and a page cache in a
// defined state — and both are recorded as observations rather than as the
// intention that produced them.
type ColdState struct {
	// StorePath and MetaPath are the artifacts that must NOT pre-exist. They
	// are recorded so a reader can see which paths the claim is about.
	StorePath string `json:"store_path,omitempty"`
	MetaPath  string `json:"meta_path,omitempty"`
	// StorePreexisting/MetaPreexisting are the observations. Either being true
	// means the run reused state and is not a cold run.
	StorePreexisting bool `json:"store_preexisting"`
	MetaPreexisting  bool `json:"meta_preexisting"`
	// PageCache is the observed page-cache state (see the PageCache* consts).
	PageCache string `json:"page_cache"`
	// PageCacheCommand is the exact argv used to reach it, so the protocol is
	// auditable rather than described.
	PageCacheCommand []string `json:"page_cache_command,omitempty"`
	PageCacheError   string   `json:"page_cache_error,omitempty"`
	// RequiredProtocol is the runner class's DECLARED cache_state from the
	// SW-123 contract. Recording it beside the observation is what lets a
	// reader see a run that did not meet its own class's protocol.
	RequiredProtocol string `json:"required_protocol,omitempty"`
	// DropRequired is true when the declared protocol obliges this run to drop
	// the page cache (the reference class does; the comparison class declares
	// its cache state uncontrolled).
	DropRequired bool `json:"drop_required"`
	// Verified is the conjunction: no pre-existing state AND, where the class
	// requires it, a page cache that was actually dropped.
	Verified bool   `json:"verified"`
	Reason   string `json:"reason,omitempty"`
}

// CgroupLimits is the cgroup v2 memory limit observed by the measured process
// itself. It is captured on every Linux run, not only under the OOM check: the
// limit a measurement ran under is part of what the measurement means, and
// SW-124's OOM gate needs it read back from INSIDE the constrained process
// (reading the intended limit from the launcher would verify nothing).
type CgroupLimits struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	// MemoryMax and MemorySwapMax are recorded verbatim, including the literal
	// "max", because normalizing them would hide the difference between "no
	// limit" and a limit that happens to be large.
	MemoryMax     string `json:"memory_max,omitempty"`
	MemorySwapMax string `json:"memory_swap_max,omitempty"`
	// OOMKill is the cgroup's `memory.events` oom_kill counter, read by the
	// measured process itself after its work finished. OOMKillCollected says
	// whether it could be read at all — an uncollected counter is not a zero,
	// and the OOM gate refuses to assert the absence of a signal it never saw.
	OOMKill          int    `json:"oom_kill"`
	OOMKillCollected bool   `json:"oom_kill_collected"`
	Error            string `json:"error,omitempty"`
}

// ColdRunSample is ONE cold run. Aborted runs are samples too — with Status
// aborted, an Error, and no metrics — so the count of attempted runs always
// reconciles with the count of measurements (AC-5).
type ColdRunSample struct {
	Run    int    `json:"run"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// Commit is the graphi revision the MEASURING binary was built from, per
	// run, so a series assembled from mixed revisions cannot hide it (AC-8).
	Commit string `json:"commit,omitempty"`
	// RunnerClass is stamped per run for the same reason (AC-8).
	RunnerClass string `json:"runner_class,omitempty"`
	// RepoSHA is the pinned corpus checkout actually measured.
	RepoSHA   string `json:"repo_sha,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	CloneMS   int64  `json:"clone_ms,omitempty"`

	Cold  ColdState    `json:"cold"`
	Index IndexMetrics `json:"index"`
	// StablePeakRSSMB is the whole-session MAXRSS (index + stable operation
	// suite), which is the sample the PRD §12.2 peak-RSS gate is read against.
	StablePeakRSSMB int64 `json:"stable_peak_rss_mb"`
	// BytesPerEdge is derived per run rather than from the aggregates, so the
	// ratio belongs to one measurement instead of mixing two distributions.
	BytesPerEdge float64       `json:"bytes_per_edge,omitempty"`
	Cgroup       *CgroupLimits `json:"cgroup,omitempty"`
	// RunPass/RunFailures carry the child run's own verdict. A run whose warm
	// semantic checks failed still yields a valid COLD sample, so its failures
	// are surfaced here rather than silently discarding the measurement.
	RunPass     bool     `json:"run_pass"`
	RunFailures []string `json:"run_failures,omitempty"`
	ReportPath  string   `json:"report_path,omitempty"`
}

// Aggregate is one derived distribution. Every field is recomputable from the
// series' samples by RecomputeColdAggregates (AC-6).
type Aggregate struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit"`
	N      int     `json:"n"`
	Min    float64 `json:"min"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
}

// GateResult is one PRD §12.2 gate read against this series. Measured is only
// meaningful when HasMeasurement is true; Status is never inferred from a
// missing value.
type GateResult struct {
	ID         string  `json:"id"`
	PRDMetric  string  `json:"prd_metric,omitempty"`
	Threshold  float64 `json:"threshold"`
	Unit       string  `json:"unit"`
	Comparison string  `json:"comparison,omitempty"`
	// Aggregate names the series aggregate the measurement came from, so a
	// reader can recompute the gate input as well as the gate.
	Aggregate      string  `json:"aggregate,omitempty"`
	Measured       float64 `json:"measured,omitempty"`
	HasMeasurement bool    `json:"has_measurement"`
	Status         string  `json:"status"`
	Reason         string  `json:"reason,omitempty"`
}

// StopRuleResult is PRD §17's program-wide 4 GB peak-RSS stop rule read against
// this series. It is not a gate and never substitutes for one.
type StopRuleResult struct {
	ID             string  `json:"id"`
	ThresholdGB    float64 `json:"threshold_gb"`
	ObservedPeakGB float64 `json:"observed_peak_gb,omitempty"`
	Triggered      bool    `json:"triggered"`
	Status         string  `json:"status"`
	Reason         string  `json:"reason,omitempty"`
}

// OOMResult is the 8 GB-host gate (PRD §12.2) executed by the SW-123 method.
// Its default value is deliberately the honest one: a zero OOMResult is
// UNKNOWN, so forgetting to run the check can never render as PASS.
type OOMResult struct {
	GateID string `json:"gate_id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Method and Command record how the limit was imposed, verbatim.
	Method  string   `json:"method,omitempty"`
	Command []string `json:"command,omitempty"`
	// RequiredLimitBytes is the contract's exact figure; Observed* are read
	// back from inside the constrained process. A mismatch INVALIDATES the
	// check (UNKNOWN) — it never passes it.
	RequiredLimitBytes    int64  `json:"required_limit_bytes,omitempty"`
	ObservedMemoryMax     string `json:"observed_memory_max,omitempty"`
	ObservedMemorySwapMax string `json:"observed_memory_swap_max,omitempty"`
	LimitVerified         bool   `json:"limit_verified"`
	RunCompleted          bool   `json:"run_completed"`
	ExitCode              int    `json:"exit_code"`
	// FailureSignals lists every observed kill signal (oom_kill counter, a 137
	// / SIGKILL exit, a kernel-log OOM record). Non-empty means FAIL.
	FailureSignals []string       `json:"failure_signals,omitempty"`
	Run            *ColdRunSample `json:"run,omitempty"`
}

// ColdRunSeries is the whole repeated cold-index measurement for one repository.
type ColdRunSeries struct {
	Repo          string `json:"repo"`
	RunsRequested int    `json:"runs_requested"`
	RunsCompleted int    `json:"runs_completed"`
	RunsAborted   int    `json:"runs_aborted"`
	// MinimumRuns is FR-8's requirement, carried in the artifact so a reader
	// does not need the PRD to see whether the sample was large enough.
	MinimumRuns int `json:"minimum_runs"`
	// Sufficient is RunsCompleted >= MinimumRuns. An insufficient series does
	// not FAIL its gates; it makes them UNKNOWN.
	Sufficient bool `json:"sufficient"`

	RunnerClass       string `json:"runner_class"`
	RunnerRole        string `json:"runner_role,omitempty"`
	ReferenceScenario bool   `json:"reference_scenario"`

	// CandidateSHA is the FROZEN candidate cited from the evidence index;
	// MeasuredSHA is what the measuring binary was actually built from.
	// CandidateMatch is the comparison — the whole point of AC-8 is that a run
	// against something other than the candidate is identifiable at a glance.
	CandidateSHA    string `json:"candidate_sha,omitempty"`
	CandidateSource string `json:"candidate_source,omitempty"`
	MeasuredSHA     string `json:"measured_sha,omitempty"`
	CandidateMatch  bool   `json:"candidate_match"`
	// WorktreeDirty is true when the measuring binary was built from a dirty
	// worktree, which disqualifies the numbers as candidate evidence even when
	// the SHAs match.
	WorktreeDirty bool `json:"worktree_dirty"`

	Runs []ColdRunSample `json:"runs"`
	// Aggregates is keyed by metric name; every entry is reproducible from Runs
	// via RecomputeColdAggregates (AC-6).
	Aggregates      map[string]Aggregate `json:"aggregates"`
	AggregateMethod string               `json:"aggregate_method"`

	Gates    []GateResult    `json:"gates,omitempty"`
	StopRule *StopRuleResult `json:"stop_rule,omitempty"`
	OOMCheck OOMResult       `json:"oom_check"`

	Warnings []string `json:"warnings,omitempty"`
	// Status is the series verdict under PRD §8.2 semantics: PASS only when
	// every gate passed, FAIL on any gate failure or triggered stop rule,
	// UNKNOWN whenever something was not measured.
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

// AggregateMethodNote documents the derivation inline, so the artifact explains
// its own arithmetic.
const AggregateMethodNote = "nearest-rank percentile (rank = ceil(p/100 * n), 1-based) over the COMPLETED runs only, ascending; " +
	"aborted runs contribute to runs_aborted and never to a distribution. " +
	"Every value here is reproducible from `runs` with evalreport.RecomputeColdAggregates."

// Cold-series aggregate metric keys. They are constants because SW-128's
// aggregator and the gate mapping both address them by name.
const (
	MetricIndexWallclockMS = "index_wallclock_ms"
	MetricIndexPeakRSSMB   = "index_peak_rss_mb"
	MetricStablePeakRSSMB  = "stable_peak_rss_mb"
	MetricDBSizeBytes      = "db_size_bytes"
	MetricNodes            = "nodes"
	MetricEdges            = "edges"
	MetricBytesPerEdge     = "bytes_per_edge"
)

// PercentileFloat64 returns the nearest-rank percentile p (1..100) of xs.
//
// Nearest rank, not interpolation: rank = ceil(p/100 * n), 1-based. For an even
// n the p50 is therefore the lower of the two middle samples rather than their
// mean — a real sample, never a value that was not observed. This is the same
// definition the pre-existing warm p95 used, and it is the ONLY percentile
// implementation in the tree so p50 and p95 cannot drift apart.
//
// xs is not modified. An empty xs returns 0 — callers record N alongside the
// value so a zero from an empty sample is never mistaken for a measurement.
func PercentileFloat64(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)
	return sorted[percentileRank(len(sorted), p)-1]
}

// PercentileInt64 is PercentileFloat64 over integer samples (latencies in
// microseconds, byte counts), returning an observed sample rather than a
// rounded interpolation.
func PercentileInt64(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := make([]int64, len(xs))
	copy(sorted, xs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[percentileRank(len(sorted), p)-1]
}

// percentileRank is the 1-based nearest rank, clamped into [1, n]. The clamp is
// what makes the single-sample case (n=1, every percentile is that sample) and
// p=100 behave instead of indexing past the slice.
func percentileRank(n, p int) int {
	if p < 1 {
		p = 1
	}
	if p > 100 {
		p = 100
	}
	rank := (p*n + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank
}

// RecomputeColdAggregates derives every published aggregate from the per-run
// samples. The harness calls it to PRODUCE the aggregates and SW-128's
// aggregator calls it to REPRODUCE them; a divergence between a published
// number and its samples is therefore a test failure rather than a discrepancy
// nobody can see (AC-6).
//
// Only completed runs contribute. A metric with no samples is absent from the
// map rather than present with a zero — an absent distribution and a measured
// zero are different claims.
func RecomputeColdAggregates(runs []ColdRunSample) map[string]Aggregate {
	units := []struct {
		metric string
		unit   string
		value  func(ColdRunSample) (float64, bool)
	}{
		{MetricIndexWallclockMS, "ms", func(r ColdRunSample) (float64, bool) {
			return float64(r.Index.WallclockMS), r.Index.WallclockMS > 0
		}},
		{MetricIndexPeakRSSMB, "MB", func(r ColdRunSample) (float64, bool) {
			return float64(r.Index.PeakRSSMB), r.Index.PeakRSSMB > 0
		}},
		{MetricStablePeakRSSMB, "MB", func(r ColdRunSample) (float64, bool) {
			return float64(r.StablePeakRSSMB), r.StablePeakRSSMB > 0
		}},
		{MetricDBSizeBytes, "bytes", func(r ColdRunSample) (float64, bool) {
			return float64(r.Index.DBSizeBytes), r.Index.DBSizeBytes > 0
		}},
		{MetricNodes, "nodes", func(r ColdRunSample) (float64, bool) {
			return float64(r.Index.Nodes), r.Index.Nodes > 0
		}},
		{MetricEdges, "edges", func(r ColdRunSample) (float64, bool) {
			return float64(r.Index.Edges), r.Index.Edges > 0
		}},
		{MetricBytesPerEdge, "bytes/edge", func(r ColdRunSample) (float64, bool) {
			return r.BytesPerEdge, r.BytesPerEdge > 0
		}},
	}

	out := map[string]Aggregate{}
	for _, u := range units {
		var samples []float64
		for _, r := range runs {
			if r.Status != ColdRunCompleted {
				continue
			}
			if v, ok := u.value(r); ok {
				samples = append(samples, v)
			}
		}
		if len(samples) == 0 {
			continue
		}
		sorted := make([]float64, len(samples))
		copy(sorted, samples)
		sort.Float64s(sorted)
		out[u.metric] = Aggregate{
			Metric: u.metric,
			Unit:   u.unit,
			N:      len(sorted),
			Min:    sorted[0],
			P50:    PercentileFloat64(sorted, 50),
			P95:    PercentileFloat64(sorted, 95),
			Max:    sorted[len(sorted)-1],
		}
	}
	return out
}

// BytesPerEdge is the derived storage ratio for one run, or 0 when the run
// produced no edges (a ratio over zero edges is not a small number, it is no
// measurement at all).
func BytesPerEdge(m IndexMetrics) float64 {
	if m.Edges <= 0 || m.DBSizeBytes <= 0 {
		return 0
	}
	return float64(m.DBSizeBytes) / float64(m.Edges)
}
