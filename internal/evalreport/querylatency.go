package evalreport

// SW-125 (P0-C2): the query-latency payload.
//
// The warm half of `-full-run` already reported a p95 per operation class. Two
// things were missing against FR-8, and both are schema problems before they
// are harness problems:
//
//   - the SAMPLE COUNT was chosen by wall-clock pragmatism. FR-8 requires at
//     least 1000 executions per query class, and nothing recorded whether that
//     floor was met — so a p95 over 30 executions and a p95 over 1000 landed in
//     the same field and looked identical to every consumer;
//   - only p95 existed, while PRD §12.2 gates on p50 as well.
//
// The rule this file enforces structurally is SW-124's rule applied to
// latency: nothing published here is a number a reader has to take on trust.
// Every executed operation keeps its individual measurement (SamplesUS), every
// class, pool and operation statistic is derived from those samples by one
// exported function (RecomputeQueryLatency), and a class that did not reach the
// floor is UNDERSAMPLED — a visible state that makes its gates UNKNOWN, never
// a silent PASS (PRD §8.2).
//
// Percentiles come from PercentileInt64 in coldrun.go. There is deliberately
// one nearest-rank implementation in the tree: a p50 and a p95 that disagreed
// about even sample counts would surface as an unexplainable gate result
// rather than as a test failure.

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// QueryExecutionMinimum is FR-8's floor: at least 1000 executions per query
// class. Falling short does not FAIL a gate — it makes it UNKNOWN, because a
// distribution over fewer executions is not the distribution the PRD asked
// for. It is carried in the artifact so a reader does not need the PRD to see
// whether the sample was large enough.
const QueryExecutionMinimum = 1000

// Query classes. These are the pools a latency is reported over. `lifecycle`
// is not a query class: it exists so the frozen 12 stable operations can each
// carry an EXPLICIT class rather than have one inferred from its name, and
// `index` — the ingest lifecycle operation — is the only member.
const (
	QueryClassStructural = "structural"
	QueryClassSearch     = "search"
	QueryClassAgentTools = "agent_tools"
	QueryClassLifecycle  = "lifecycle"
)

// LifecycleOperationNote says, in the artifact, why the one lifecycle
// operation has no latency distribution — so "index has no p95" reads as a
// recorded decision rather than as a gap.
const LifecycleOperationNote = "lifecycle-only: `index` is the ingest lifecycle operation, not a query. Its cost is the " +
	"cold-index wallclock measured by SW-124, so it carries no query-latency samples, no 1000-execution floor and no " +
	"PRD §12.2 query gate. It is listed here so all twelve stable operations are visibly accounted for."

// LatencyStats is one latency distribution, in MICROSECONDS. Microseconds
// because the selective-read stable ops are routinely sub-millisecond on real
// repositories and a 0 ms value cannot ratchet.
//
// N is always published beside the percentiles: a percentile without its
// sample count is not interpretable, and it is what makes the FR-8 floor
// checkable rather than asserted.
type LatencyStats struct {
	N     int   `json:"n"`
	MinUS int64 `json:"min_us"`
	P50US int64 `json:"p50_us"`
	P95US int64 `json:"p95_us"`
	MaxUS int64 `json:"max_us"`
}

// LatencyStatsFrom derives the distribution of samples. An empty sample yields
// the zero value, whose N is 0 — callers read N, never a zero percentile, to
// decide whether anything was measured.
func LatencyStatsFrom(samples []int64) LatencyStats {
	if len(samples) == 0 {
		return LatencyStats{}
	}
	sorted := make([]int64, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return LatencyStats{
		N:     len(sorted),
		MinUS: sorted[0],
		P50US: PercentileInt64(sorted, 50),
		P95US: PercentileInt64(sorted, 95),
		MaxUS: sorted[len(sorted)-1],
	}
}

// QueryOpLatency is ONE stable operation's latency evidence. All twelve appear,
// including the lifecycle one, so the operation → class mapping is readable off
// the artifact instead of being reconstructed from operation names (AC-4).
type QueryOpLatency struct {
	Operation string `json:"operation"`
	Class     string `json:"class"`
	// Measured is false for the lifecycle operation only. It is explicit so an
	// entry with no samples can never be confused with an operation the
	// harness forgot to exercise.
	Measured bool `json:"measured"`
	// Gated names the PRD §12.2 gates that read this operation, or is empty
	// when the operation is measured but carries no gate (the contract's
	// `measured_not_gated` list).
	Gated []string `json:"gated_by,omitempty"`
	// Warmup is the number of UNTIMED executions run before the timed ones, so
	// the "warm up, then purely timed iterations" separation is a recorded
	// property of the measurement rather than a claim about the code (AC-6).
	Warmup int `json:"warmup_executions"`
	// Latency is a POINTER and absent for an unmeasured operation. An embedded
	// value would render the lifecycle operation as `p95_us: 0`, which is
	// exactly the conflation coldrun.go's aggregation rule exists to remove: an
	// absent distribution and a measured zero are different claims, and a
	// sub-microsecond operation really can report a zero.
	Latency *LatencyStats `json:"latency,omitempty"`
	// SamplesUS retains every individual timed measurement so p50 and p95 can
	// be recomputed rather than believed (AC-7). This is also the raw data
	// SW-128's export and aggregator build on.
	SamplesUS []int64 `json:"samples_us,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// QueryClassLatency is one query class pooled across its operations. Executions
// is what FR-8's floor is read against.
type QueryClassLatency struct {
	Class      string   `json:"class"`
	Operations []string `json:"operations"`
	Executions int      `json:"executions"`
	Minimum    int      `json:"minimum"`
	// Sufficient is Executions >= Minimum. An undersampled class does not FAIL
	// its gates; it makes them UNKNOWN (AC-2).
	Sufficient bool `json:"sufficient"`
	LatencyStats
}

// QueryPoolLatency is the operation subset ONE PRD §12.2 gate is read over.
// It exists because a gate's pool is not always its whole class:
// `caller_callee_impact_p95` names three of the six structural operations, so
// a class that cleared the floor says nothing about whether the gated subset
// did. Pools are declared by the SW-123 contract, never inferred here.
type QueryPoolLatency struct {
	GateID     string   `json:"gate_id"`
	Class      string   `json:"class,omitempty"`
	Operations []string `json:"operations"`
	Executions int      `json:"executions"`
	Minimum    int      `json:"minimum"`
	Sufficient bool     `json:"sufficient"`
	LatencyStats
}

// QuerySymbolSample records the degree-stratified symbol sample the structural
// and agent-context operations were driven through.
//
// PRD §16 requires two consecutive green runs, and two runs over different
// symbol samples are not comparable — the second would be measuring a different
// question. So the sample is deterministic and REPRODUCIBLE FROM THE REPORT
// (AC-5): the ordered node ids are published verbatim, with a digest over them
// so a drift between two runs is one string comparison rather than a diff.
type QuerySymbolSample struct {
	Requested int `json:"requested"`
	Returned  int `json:"returned"`
	// Method states the ordering that makes the sample deterministic, so the
	// claim can be checked against the implementation.
	Method string `json:"method"`
	// Digest is SampleDigest over SymbolIDs. It is an equality check between two
	// runs — the ids themselves are published beside it, so the digest never has
	// to be trusted on its own.
	Digest    string   `json:"digest"`
	SymbolIDs []string `json:"symbol_ids"`
	// AgentSymbols is how many of the sampled symbols the agent-context
	// operations were driven through (they are the first AgentSymbols entries
	// of SymbolIDs, so that subset is reproducible too).
	AgentSymbols int `json:"agent_symbols"`
}

// QueryLatencySeries is the whole query-latency measurement for one warm
// session over one repository.
type QueryLatencySeries struct {
	Repo string `json:"repo"`
	// Minimum is FR-8's per-class execution floor, carried in the artifact.
	Minimum int `json:"minimum_executions_per_class"`
	// Requested is the floor this invocation ASKED for (the -query-executions
	// flag). It is 0 on the default path, which runs the historical fixed
	// sample counts and is therefore expected to be undersampled — visibly.
	Requested int `json:"requested_executions_per_class"`
	// TotalExecutions is every timed execution across all classes.
	TotalExecutions int `json:"total_executions"`
	// ClassOf is the EXPLICIT operation → class mapping for all twelve stable
	// operations (AC-4). Nothing here is derived from an operation's name.
	ClassOf map[string]string `json:"class_of"`
	// StableOperations is the frozen 12, in the taxonomy's own order, so a
	// reader can see the coverage claim is over the whole set.
	StableOperations []string `json:"stable_operations"`

	Operations []QueryOpLatency    `json:"operations"`
	Classes    []QueryClassLatency `json:"classes"`
	Pools      []QueryPoolLatency  `json:"pools,omitempty"`

	Sample QuerySymbolSample `json:"symbol_sample"`

	// Sufficient is true only when EVERY query class reached the floor.
	Sufficient bool `json:"sufficient"`

	Gates    []GateResult `json:"gates,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Status   string       `json:"status"`

	// TimingMethod and AggregateMethod make the artifact explain its own
	// measurement and its own arithmetic.
	TimingMethod    string `json:"timing_method"`
	AggregateMethod string `json:"aggregate_method"`
	Notes           string `json:"notes,omitempty"`
}

// QueryTimingMethodNote is AC-6 stated in the artifact: what is inside the
// timed region and what is deliberately outside it.
const QueryTimingMethodNote = "The timed region contains the operation invocation and NOTHING else. Clone, index, store open, engine " +
	"construction and the symbol sample all happen before any measurement; per-execution argument assembly (choosing the " +
	"symbol or query for execution i, building the argument map) happens in an UNTIMED prepare step; and each operation " +
	"runs warmup_executions untimed invocations before the first timed one. Warmup measurements are discarded, never " +
	"pooled. Latency is time.Since around the invocation, recorded in microseconds."

// QueryAggregateMethodNote documents the derivation inline.
const QueryAggregateMethodNote = "nearest-rank percentile (rank = ceil(p/100 * n), 1-based) over the retained per-execution samples, " +
	"ascending — the same implementation the cold series uses, so p50 and p95 cannot disagree about even sample counts. " +
	"A class statistic pools its operations' samples; a pool statistic pools exactly the operations the SW-123 contract " +
	"assigns to that gate. Every value here is reproducible from `operations[].samples_us` with " +
	"evalreport.RecomputeQueryLatency."

// QueryLatencyNotes explains the artifact to a reader who has only the JSON.
const QueryLatencyNotes = "SW-125 query-latency measurement: warm operations over ONE open store in the session model, driven through " +
	"a deterministic degree-stratified symbol sample that is published verbatim so two runs are comparable. FR-8's floor is " +
	"minimum_executions_per_class; a class below it is marked sufficient=false and every gate read over it is UNKNOWN, " +
	"never PASS (PRD §8.2). No gate is read at all unless the run is the reference scenario on the reference class and " +
	"from the frozen candidate."

// QueryLatencyRecomputation is RecomputeQueryLatency's result: the same three
// statistic sets the series publishes, derived from nothing but the retained
// samples.
type QueryLatencyRecomputation struct {
	Operations map[string]LatencyStats
	Classes    map[string]LatencyStats
	Pools      map[string]LatencyStats
}

// RecomputeQueryLatency derives every published statistic from the retained
// per-execution samples. The harness calls it to PRODUCE the statistics and a
// consumer (SW-128's aggregator, and the tests here) calls it to REPRODUCE
// them, so a divergence between a published percentile and its samples is a
// test failure rather than a discrepancy nobody can see (AC-7).
//
// It reads only `Operations[].SamplesUS` plus the class and pool membership
// lists. Operations with no retained samples contribute nothing, which is how
// the lifecycle operation stays out of every distribution without a special
// case here.
func RecomputeQueryLatency(s QueryLatencySeries) QueryLatencyRecomputation {
	samples := make(map[string][]int64, len(s.Operations))
	out := QueryLatencyRecomputation{
		Operations: make(map[string]LatencyStats, len(s.Operations)),
		Classes:    make(map[string]LatencyStats, len(s.Classes)),
		Pools:      make(map[string]LatencyStats, len(s.Pools)),
	}
	for _, op := range s.Operations {
		samples[op.Operation] = op.SamplesUS
		if len(op.SamplesUS) == 0 {
			continue
		}
		out.Operations[op.Operation] = LatencyStatsFrom(op.SamplesUS)
	}
	pool := func(ops []string) []int64 {
		var pooled []int64
		for _, op := range ops {
			pooled = append(pooled, samples[op]...)
		}
		return pooled
	}
	for _, c := range s.Classes {
		out.Classes[c.Class] = LatencyStatsFrom(pool(c.Operations))
	}
	for _, p := range s.Pools {
		out.Pools[p.GateID] = LatencyStatsFrom(pool(p.Operations))
	}
	return out
}

// SampleDigest is the reproducibility check for an ordered symbol sample:
// SHA-256 over the length-prefixed ids ("<len>:<id>" each), hex-encoded.
//
// Order-sensitive on purpose. Two runs that sampled the same symbols in a
// different order did not measure the same question — the structural loop
// walks the sample in order and the agent-context loop takes its prefix — so a
// set-equal-but-reordered sample must read as a difference, not as a match.
//
// Length-prefixed rather than joined by a separator so the encoding is
// unambiguous: with a plain separator, ["a", "b"] and ["a<sep>b"] would digest
// identically, and a digest that cannot distinguish two samples is not a
// reproducibility check.
func SampleDigest(ids []string) string {
	h := sha256.New()
	for _, id := range ids {
		_, _ = h.Write([]byte(strconv.Itoa(len(id))))
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(id))
	}
	return hex.EncodeToString(h.Sum(nil))
}
