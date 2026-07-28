package main

// SW-125 (P0-C2): the operation → query-class mapping, and the execution plan
// that makes FR-8's floor reachable.
//
// WHY A TABLE AND NOT A CONVENTION. The pre-SW-125 harness attached a class to
// an operation at the call site, as a string literal passed to timeOp. That is
// a mapping inferred from where the code happens to live: adding a stable
// operation and forgetting its timeOp call produced a report that simply did
// not mention it, and no test could tell the difference between "measured and
// fast" and "never measured". queryClassOf is the mapping stated once, keyed by
// the frozen 12 (surfaces/mcp.StableOperations), and a drift test fails the
// build when the two sets diverge — which is what AC-4 asks for.
//
// WHY `index` IS IN THE TABLE. It is one of the 12 and it is NOT a query: it
// is the ingest lifecycle operation, its cost is the cold-index wallclock
// SW-124 measures, and giving it a query-latency percentile would invent a
// number. So it maps to the `lifecycle` class, is excluded from every floor
// and every gate, and says so in the artifact. Silence would have been the
// same omission with better manners.

import (
	"sort"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/surfaces/mcp"
)

// queryClassOf is the EXPLICIT operation → query-class mapping over the frozen
// 12 stable operations. The three query classes match the pools the SW-123
// contract's §12.2 gates are declared over; `lifecycle` holds the one stable
// operation that is not a query.
//
// It must cover exactly mcp.StableOperations — no more (this harness does not
// get to invent an operation) and no fewer (an unmapped stable operation would
// silently drop out of the measurement). TestQueryClassOf_CoversExactlyTheFrozenTwelve
// pins that.
var queryClassOf = map[string]string{
	// structural — selective reads over the graph.
	scenario.OpDefinition:   evalreport.QueryClassStructural,
	scenario.OpReferences:   evalreport.QueryClassStructural,
	scenario.OpCallers:      evalreport.QueryClassStructural,
	scenario.OpCallees:      evalreport.QueryClassStructural,
	scenario.OpNeighborhood: evalreport.QueryClassStructural,
	scenario.OpImpact:       evalreport.QueryClassStructural,

	// search — the FTS/ranking path.
	scenario.OpSearch: evalreport.QueryClassSearch,

	// agent_tools — the agent-context envelopes (PRD "Agent Context").
	scenario.OpExplainSymbol: evalreport.QueryClassAgentTools,
	scenario.OpChangeRisk:    evalreport.QueryClassAgentTools,
	scenario.OpRelatedFiles:  evalreport.QueryClassAgentTools,
	scenario.OpAgentBrief:    evalreport.QueryClassAgentTools,

	// lifecycle — not a query class; see LifecycleOperationNote.
	scenario.OpIndex: evalreport.QueryClassLifecycle,
}

// queryClasses is the set of classes FR-8's floor applies to, in a stable
// order. `lifecycle` is deliberately absent: a floor on the number of index
// runs would be SW-124's ten-run count wearing this story's clothes.
var queryClasses = []string{
	evalreport.QueryClassAgentTools,
	evalreport.QueryClassSearch,
	evalreport.QueryClassStructural,
}

// stableOperationsInClass returns the stable operations mapped to class, sorted.
func stableOperationsInClass(class string) []string {
	var ops []string
	for op, c := range queryClassOf {
		if c == class {
			ops = append(ops, op)
		}
	}
	sort.Strings(ops)
	return ops
}

// isQueryOperation reports whether op is a stable operation that carries a
// query-latency measurement (i.e. everything except the lifecycle one).
func isQueryOperation(op string) bool {
	class, ok := queryClassOf[op]
	return ok && class != evalreport.QueryClassLifecycle
}

// frozenStableOperations is the taxonomy's own list, copied so a caller cannot
// mutate the source of truth.
func frozenStableOperations() []string {
	out := make([]string, len(mcp.StableOperations))
	copy(out, mcp.StableOperations)
	return out
}

// queryPool is one set of operations a floor is read over: a query class, or
// the operation list a single §12.2 gate is declared against.
type queryPool struct {
	// id is the class name or the gate id, and is what the report keys the
	// pool by.
	id string
	// gate is empty for a class pool and the gate id for a gate pool.
	gate       string
	class      string
	operations []string
}

// queryExecutionPlan is how many TIMED executions each operation gets, so that
// every pool clears the minimum.
//
// The arithmetic is deliberately simple and stated in the artifact: an
// operation's target is the LARGEST per-operation share any pool containing it
// demands, i.e. max over pools P ∋ op of ceil(minimum / |P|). Taking the max
// rather than the sum is what stops the plan from over-measuring an operation
// that appears in both its class pool and a gate pool — it appears in both, it
// is not executed twice.
//
// Concretely, at minimum=1000 with the SW-123 contract: the structural class
// (6 ops) demands 167 each, while the caller_callee_impact gate pool (3 ops)
// demands 334 of its three. callers/callees/impact therefore run 334 timed
// executions each and definition/references/neighborhood 167 — the gated pool
// reaches 1002 and the class reaches 1503, so BOTH floors are met and neither
// is met by accident.
//
// This matters because the class floor alone is not enough: 1000 structural
// executions spread evenly over six operations would leave the gated three
// with ~500, and a gate read over 500 executions is not FR-8's measurement
// however green the class looks.
func queryExecutionPlan(minimum int, pools []queryPool) map[string]int {
	plan := map[string]int{}
	if minimum <= 0 {
		return plan
	}
	for _, p := range pools {
		if len(p.operations) == 0 {
			continue
		}
		share := ceilDiv(minimum, len(p.operations))
		for _, op := range p.operations {
			if share > plan[op] {
				plan[op] = share
			}
		}
	}
	return plan
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// classPools is the per-class pool set: every query class with its stable
// operations. It is the floor FR-8 states literally.
func classPools() []queryPool {
	pools := make([]queryPool, 0, len(queryClasses))
	for _, class := range queryClasses {
		pools = append(pools, queryPool{
			id: class, class: class, operations: stableOperationsInClass(class),
		})
	}
	return pools
}

// gatePools reads the §12.2 gates this story owns out of the contract and
// turns each one's declared operation list into a pool.
//
// Operations the contract names but this harness does not measure (a stable
// operation moved to lifecycle, or a name that drifted) are reported rather
// than dropped: a gate whose pool silently lost an operation would still
// render a percentile, over the wrong set.
func gatePools(rs referenceScenario) ([]queryPool, []string) {
	var pools []queryPool
	var problems []string
	for _, g := range rs.Gates {
		if g.MeasuredBy != queryLatencyStory {
			continue
		}
		if len(g.Operations) == 0 {
			problems = append(problems, "gate "+g.ID+" declares no operations, so its query-latency pool is undefined")
			continue
		}
		pool := queryPool{id: g.ID, gate: g.ID}
		classes := map[string]bool{}
		for _, op := range g.Operations {
			if !isQueryOperation(op) {
				problems = append(problems, "gate "+g.ID+" names operation "+op+", which is not a measured query operation")
				continue
			}
			classes[queryClassOf[op]] = true
			pool.operations = append(pool.operations, op)
		}
		sort.Strings(pool.operations)
		if len(classes) == 1 {
			for c := range classes {
				pool.class = c
			}
		}
		if len(pool.operations) > 0 {
			pools = append(pools, pool)
		}
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].id < pools[j].id })
	return pools, problems
}
