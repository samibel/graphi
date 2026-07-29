package main

// SW-125 (P0-C2): the query-latency harness — FR-8's "at least 1000 executions
// per query class", p50 as well as p95, and a symbol sample two runs can share.
//
// THREE THINGS THIS FILE OWNS.
//
//  1. THE TIMED REGION (AC-6). warmOperation splits an execution into an
//     UNTIMED prepare and a TIMED invoke. Choosing the symbol for execution i
//     and building its argument map is harness cost, not operation cost, so it
//     sits outside time.Since — and because the split is structural rather than
//     a comment, a test can inflate prepare and show the reported latency
//     unchanged. Each operation also runs warmup executions that are invoked
//     and discarded, so "warm up, then purely timed iterations" is a property
//     of the driver instead of a habit of whoever wrote the loop.
//
//  2. THE EXECUTION PLAN. How many timed executions each operation needs so
//     that every class AND every gate pool clears the floor lives in
//     queryclass.go; this file turns that into the per-operation counts the
//     driver runs, and records the plan in the artifact.
//
//  3. THE SAMPLE'S REPRODUCIBILITY (AC-5). PRD §16 wants two consecutive green
//     runs. Two runs over different symbol samples are not two runs of the same
//     measurement, so the ordered sample is published verbatim with a digest.
//     The determinism itself is the store's: DegreeStratifiedSymbols orders
//     candidates by degree DESC then node id ASC and picks one per quantile
//     bucket, which is a total order over a fixed graph. This harness must not
//     re-introduce nondeterminism on top of it, which is why every loop here
//     walks a sorted slice and never a map.

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/evalreport"
)

// queryLatencyStory is the contract's `measured_by` value for the gates this
// harness answers. It is a constant so the gate selection cannot drift from
// the story that owns it.
const queryLatencyStory = "SW-125"

// Warmup execution counts. Warmups are invoked and discarded: they populate the
// store's page cache, the statement cache and the allocator pools for that
// operation so the first TIMED execution is not measuring one-time setup.
//
// The floor path uses more because it is the one whose numbers a §12.2 gate is
// read from; the default path keeps one, which is the untimed assertion pass
// the search loop already performed before SW-125 generalised it to every
// operation.
const (
	queryWarmupDefault = 1
	queryWarmupFloor   = 10
)

// querySymbolSampleCap bounds the degree-stratified sample on the floor path.
//
// 25 symbols (the default) driven 334 times each would measure 25 symbols very
// thoroughly and the repository not at all; an unbounded sample would make the
// artifact's published symbol list enormous for no gain. 250 is the point where
// each structural operation still cycles the whole sample at least once at the
// 1000-execution floor, so every published symbol is actually measured.
const querySymbolSampleCap = 250

// warmExecution is ONE prepared execution: the arguments, and what its outcome
// must be for the measurement to count. Requirement and allowed travel with the
// execution rather than with the operation because the search operation's
// promise is per manifest query — `expect_nonempty` binds some queries and not
// others.
type warmExecution struct {
	args        map[string]string
	requirement string
	allowed     []string
}

// warmOperation is one stable operation as the latency harness executes it.
//
// The prepare/invoke split is the AC-6 boundary and the reason this type
// exists at all: everything the harness does per execution that is not the
// operation happens in prepare, and invoke is the only thing the clock sees.
type warmOperation struct {
	op    string
	class string
	// prepare builds execution i. UNTIMED.
	prepare func(i int) warmExecution
	// invoke performs the operation and returns its outcome. TIMED — and
	// nothing else is.
	invoke func(args map[string]string) (string, error)
	// executions is the number of TIMED executions; warmup the number of
	// untimed ones that precede them.
	executions int
	warmup     int
}

// executeWarmOperation runs w's warmups untimed, then its timed executions, and
// returns one duration per execution whose outcome was accepted.
//
// observe reports whether the execution counted; a rejected outcome (a failed
// semantic requirement, or an error) contributes no latency sample, because a
// fast wrong answer is not a fast answer. That rule predates SW-125 and is
// preserved verbatim.
func executeWarmOperation(w warmOperation, observe func(x warmExecution, outcome string, err error) bool) []time.Duration {
	for i := range w.warmup {
		x := w.prepare(i)
		// Deliberately unrecorded and untimed. A warmup that errors will be
		// reported by the timed executions that follow it; recording it here
		// would put an unmeasured invocation into the semantic outcome counts.
		_, _ = w.invoke(x.args)
	}
	out := make([]time.Duration, 0, w.executions)
	for i := range w.executions {
		x := w.prepare(i) // UNTIMED: symbol selection, argument assembly.
		start := time.Now()
		outcome, err := w.invoke(x.args) // TIMED: the operation, and only it.
		d := time.Since(start)
		if !observe(x, outcome, err) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// queryLatencyPlan is what this invocation intends to measure, resolved before
// anything is measured so it can be recorded in the artifact and asserted in a
// test.
type queryLatencyPlan struct {
	// minimum is FR-8's floor, always evalreport.QueryExecutionMinimum. It is
	// what sufficiency is read against even on the default path — which is how
	// the default path reports itself as undersampled instead of looking
	// identical to a floor run.
	minimum int
	// requested is the -query-executions value: 0 on the default path, which
	// keeps the historical fixed sample counts unchanged (AC-8).
	requested    int
	symbolSample int
	agentSymbols int
	warmup       int
	// perOp is the timed execution count per operation on the floor path, and
	// empty on the default path (where the counts are the historical ones,
	// derived from the sample size and the manifest's search queries).
	perOp    map[string]int
	pools    []queryPool
	problems []string
}

// newQueryLatencyPlan resolves the plan. rs is the SW-123 contract when one was
// supplied; without it there are no gate pools, only the class floors — the
// harness still measures, it just has nothing to read the numbers against.
func newQueryLatencyPlan(requested int, rs *referenceScenario) queryLatencyPlan {
	plan := queryLatencyPlan{
		minimum:      evalreport.QueryExecutionMinimum,
		requested:    requested,
		symbolSample: fullRunSymbolSample,
		agentSymbols: fullRunAgentToolSample,
		warmup:       queryWarmupDefault,
		pools:        classPools(),
	}
	if rs != nil {
		gates, problems := gatePools(*rs)
		plan.pools = append(plan.pools, gates...)
		plan.problems = problems
	}
	sort.Slice(plan.pools, func(i, j int) bool { return plan.pools[i].id < plan.pools[j].id })
	if requested <= 0 {
		return plan
	}
	plan.perOp = queryExecutionPlan(requested, plan.pools)
	plan.warmup = queryWarmupFloor
	// The symbol sample scales with the largest per-operation target so the
	// measured symbols are a cross-section of the repository rather than the
	// same 25 symbols answered hundreds of times.
	largest := 0
	for _, n := range plan.perOp {
		if n > largest {
			largest = n
		}
	}
	plan.symbolSample = max(fullRunSymbolSample, min(largest, querySymbolSampleCap))
	plan.agentSymbols = plan.symbolSample
	return plan
}

// executionsFor is the timed execution count for op. On the floor path it comes
// from the plan; on the default path it is the historical count, which is
// derived from what there is to measure (one execution per sampled symbol, the
// fixed search iterations per manifest query) rather than from a target.
func (p queryLatencyPlan) executionsFor(op string, symbols, agentSymbols, searchQueries int) int {
	if n, ok := p.perOp[op]; ok {
		if op == scenario.OpSearch && searchQueries == 0 {
			return 0
		}
		return n
	}
	switch queryClassOf[op] {
	case evalreport.QueryClassSearch:
		return fullRunSearchIters * searchQueries
	case evalreport.QueryClassAgentTools:
		if op == scenario.OpAgentBrief {
			return min(fullRunBriefIters, symbols)
		}
		return agentSymbols
	case evalreport.QueryClassStructural:
		return symbols
	}
	return 0
}

// buildQueryLatencySeries assembles the artifact from the retained samples.
// Every statistic it publishes is produced by evalreport.RecomputeQueryLatency,
// which is the same function a consumer uses to reproduce them — so a published
// percentile that disagrees with its samples is a test failure, not a
// discrepancy nobody can see (AC-7).
func buildQueryLatencySeries(repo string, plan queryLatencyPlan, sample evalreport.QuerySymbolSample, perOp map[string][]time.Duration, warmupOf map[string]int) *evalreport.QueryLatencySeries {
	series := &evalreport.QueryLatencySeries{
		Repo:             repo,
		Minimum:          plan.minimum,
		Requested:        plan.requested,
		ClassOf:          map[string]string{},
		StableOperations: frozenStableOperations(),
		Sample:           sample,
		TimingMethod:     evalreport.QueryTimingMethodNote,
		AggregateMethod:  evalreport.QueryAggregateMethodNote,
		Notes:            evalreport.QueryLatencyNotes,
		Warnings:         append([]string(nil), plan.problems...),
	}

	// Which gates read which operation, so an operation's entry says whether a
	// §12.2 row depends on it (the contract's `measured_not_gated` list is the
	// complement, and this is that list made mechanical).
	gatedBy := map[string][]string{}
	for _, pool := range plan.pools {
		if pool.gate == "" {
			continue
		}
		for _, op := range pool.operations {
			gatedBy[op] = append(gatedBy[op], pool.gate)
		}
	}

	for _, op := range series.StableOperations {
		class, mapped := queryClassOf[op]
		if !mapped {
			// Unreachable while the drift test is green; recorded rather than
			// skipped so a future 13th operation is visible instead of absent.
			series.Warnings = append(series.Warnings, "stable operation "+op+" has no declared query class")
			continue
		}
		series.ClassOf[op] = class
		entry := evalreport.QueryOpLatency{
			Operation: op,
			Class:     class,
			Measured:  isQueryOperation(op),
			Gated:     gatedBy[op],
			Warmup:    warmupOf[op],
			SamplesUS: durationsToMicroseconds(perOp[op]),
		}
		if !entry.Measured {
			entry.Note = evalreport.LifecycleOperationNote
			entry.Warmup = 0
		}
		series.Operations = append(series.Operations, entry)
		series.TotalExecutions += len(entry.SamplesUS)
	}

	for _, class := range queryClasses {
		series.Classes = append(series.Classes, evalreport.QueryClassLatency{
			Class:      class,
			Operations: stableOperationsInClass(class),
			Minimum:    plan.minimum,
		})
	}
	for _, pool := range plan.pools {
		if pool.gate == "" {
			continue
		}
		series.Pools = append(series.Pools, evalreport.QueryPoolLatency{
			GateID:     pool.gate,
			Class:      pool.class,
			Operations: pool.operations,
			Minimum:    plan.minimum,
		})
	}

	// One derivation, used to produce and to reproduce.
	recomputed := evalreport.RecomputeQueryLatency(*series)
	for i := range series.Operations {
		stats, measured := recomputed.Operations[series.Operations[i].Operation]
		if !measured {
			// No samples, no distribution. Publishing a zero-valued one would
			// make "never executed" render identically to "answered in under a
			// microsecond".
			continue
		}
		series.Operations[i].Latency = &stats
	}
	series.Sufficient = true
	for i := range series.Classes {
		stats := recomputed.Classes[series.Classes[i].Class]
		series.Classes[i].LatencyStats = stats
		series.Classes[i].Executions = stats.N
		series.Classes[i].Sufficient = stats.N >= series.Classes[i].Minimum
		if !series.Classes[i].Sufficient {
			series.Sufficient = false
			series.Warnings = append(series.Warnings, undersampledWarning("class", series.Classes[i].Class, stats.N, series.Classes[i].Minimum))
		}
	}
	for i := range series.Pools {
		stats := recomputed.Pools[series.Pools[i].GateID]
		series.Pools[i].LatencyStats = stats
		series.Pools[i].Executions = stats.N
		series.Pools[i].Sufficient = stats.N >= series.Pools[i].Minimum
		if !series.Pools[i].Sufficient {
			series.Warnings = append(series.Warnings, undersampledWarning("gate pool", series.Pools[i].GateID, stats.N, series.Pools[i].Minimum))
		}
	}
	return series
}

func undersampledWarning(kind, id string, got, want int) string {
	return kind + " " + id + " ran " + strconv.Itoa(got) + " timed executions, below FR-8's floor of " + strconv.Itoa(want) +
		": every gate read over it is UNKNOWN, not PASS"
}

// querySampleMethod is the sample's determinism claim, stated in the artifact
// beside the sample itself so it can be checked rather than believed.
const querySampleMethod = "core/graphstore DegreeSamplePort.DegreeStratifiedSymbols over the indexed store: candidates are the " +
	"function and method nodes, ordered by incident degree DESC then node id ASC, one picked per quantile bucket. That is a " +
	"total order over a fixed graph, so the same graph yields the same ordered sample; the structural operations walk this " +
	"list in order and the agent-context operations take its first agent_symbols entries."

// buildWarmOperations declares every measured stable operation once: its class,
// how execution i's arguments are chosen (untimed), and how it is invoked
// (timed). The argument sets and the semantic requirements are the pre-SW-125
// ones verbatim — this story changes how many times an operation runs and what
// is inside the clock, not what is asked of it.
//
// `index` is absent by construction: it is the lifecycle operation, its
// semantic outcome is recorded by the caller from the ingest inventory, and
// giving it a latency distribution would invent a number (AC-4).
func buildWarmOperations(ctx context.Context, eng *scenario.FixtureEngine, e corpus.Entry, symbolIDs []string, agentSymbols int) []warmOperation {
	symbolAt := func(i int) string { return symbolIDs[i%len(symbolIDs)] }
	agentAt := func(i int) string {
		if agentSymbols <= 0 {
			return symbolAt(i)
		}
		return symbolIDs[i%agentSymbols]
	}
	rendered := func(op string) func(map[string]string) (string, error) {
		return func(args map[string]string) (string, error) {
			lines, _, err := eng.Invoke(op, args)
			return renderedOutcome(lines), err
		}
	}
	envelope := func(op string) func(map[string]string) (string, error) {
		return func(args map[string]string) (string, error) {
			result, err := eng.InvokeContract(ctx, op, args)
			return contractOutcome(result, err)
		}
	}

	ops := make([]warmOperation, 0, len(queryClassOf))

	for _, op := range []string{scenario.OpDefinition, scenario.OpCallers, scenario.OpCallees, scenario.OpReferences} {
		allowed := []string{"found", "empty"}
		requirement := "resolved symbol; found or legitimately empty"
		if op == scenario.OpDefinition {
			allowed = []string{"found"}
			requirement = "resolved symbol with at least one definition"
		}
		ops = append(ops, warmOperation{
			op: op, class: evalreport.QueryClassStructural,
			prepare: func(i int) warmExecution {
				return warmExecution{args: map[string]string{"symbol": symbolAt(i)}, requirement: requirement, allowed: allowed}
			},
			invoke: rendered(op),
		})
	}
	ops = append(ops, warmOperation{
		op: scenario.OpNeighborhood, class: evalreport.QueryClassStructural,
		prepare: func(i int) warmExecution {
			return warmExecution{
				args:        map[string]string{"symbol": symbolAt(i), "depth": "1"},
				requirement: "resolved symbol; bounded neighborhood outcome",
				allowed:     []string{"found", "empty"},
			}
		},
		invoke: rendered(scenario.OpNeighborhood),
	})
	ops = append(ops, warmOperation{
		op: scenario.OpImpact, class: evalreport.QueryClassStructural,
		prepare: func(i int) warmExecution {
			return warmExecution{
				args:        map[string]string{"symbol": symbolAt(i), "direction": "reverse", "max_nodes": "256"},
				requirement: "resolved symbol; bounded impact outcome",
				allowed:     []string{"found", "empty"},
			}
		},
		invoke: rendered(scenario.OpImpact),
	})

	if len(e.Searches) > 0 {
		searches := e.Searches
		ops = append(ops, warmOperation{
			op: scenario.OpSearch, class: evalreport.QueryClassSearch,
			// Round-robin over the manifest's queries rather than one block per
			// query: at the FR-8 floor a per-query block would let the last
			// query's warmed FTS state dominate the tail of the distribution.
			prepare: func(i int) warmExecution {
				s := searches[i%len(searches)]
				x := warmExecution{
					args:        map[string]string{"query": s.Query},
					requirement: "valid search outcome",
					allowed:     []string{"found", "empty"},
				}
				if s.ExpectNonEmpty {
					x.requirement = "manifest-promised non-empty search"
					x.allowed = []string{"found"}
				}
				return x
			},
			invoke: rendered(scenario.OpSearch),
		})
	}

	// SW-136 (D1): "partial" is countable for ALL FOUR members of the
	// agent_context_p95 pool, not only agent_brief. Before this correction these
	// three declared allowed=["found"] (and ["found","empty"] for related_files)
	// while agent_brief — same pool, same gate, same percentile — declared
	// ["found","partial"], so one number was reported over observations admitted
	// under two incompatible rules (docs/decisions/2026-07-p0-candidate-decision.md
	// §2.1, §3.1). It cost exactly 25 of FR-8's 1000 executions in both published
	// runs, on two CPU families, leaving gate 9 permanently UNKNOWN.
	//
	// This does not weaken the rule above ("a fast wrong answer is not a fast
	// answer"). A "partial" IS a resolved target with a valid envelope: the
	// operation answered and truthfully declared its own truncation
	// (engine/agenttools/shape/shape.go:171). It is designed, documented,
	// GA-frozen behaviour — corpus/hero/hero-17-explain-symbol-partial.yaml
	// asserts it — so discarding it was discarding a correct observation, and
	// systematically the slowest ones: the answers large enough to truncate are
	// the ones with the most items to resolve, rank and assemble. not_found,
	// ambiguous and error remain uncountable.
	for _, op := range []string{scenario.OpExplainSymbol, scenario.OpChangeRisk, scenario.OpRelatedFiles} {
		allowed := []string{"found", "partial"}
		requirement := "resolved target with a valid found or partial envelope"
		if op == scenario.OpRelatedFiles {
			allowed = []string{"found", "empty", "partial"}
			requirement = "resolved target; found, partial, or legitimately empty related-file set"
		}
		ops = append(ops, warmOperation{
			op: op, class: evalreport.QueryClassAgentTools,
			prepare: func(i int) warmExecution {
				return warmExecution{args: map[string]string{"symbol": agentAt(i)}, requirement: requirement, allowed: allowed}
			},
			invoke: envelope(op),
		})
	}
	ops = append(ops, warmOperation{
		op: scenario.OpAgentBrief, class: evalreport.QueryClassAgentTools,
		prepare: func(i int) warmExecution {
			return warmExecution{
				args:        map[string]string{"topic": symbolAt(i)},
				requirement: "resolved topic with a valid found/partial project brief",
				allowed:     []string{"found", "partial"},
			}
		},
		invoke: envelope(scenario.OpAgentBrief),
	})
	return ops
}

func durationsToMicroseconds(ds []time.Duration) []int64 {
	if len(ds) == 0 {
		return nil
	}
	out := make([]int64, len(ds))
	for i, d := range ds {
		out[i] = d.Microseconds()
	}
	return out
}
