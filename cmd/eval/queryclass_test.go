package main

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/surfaces/mcp"
)

// SW-125 AC-4: the operation → query-class mapping is EXPLICIT, and it covers
// exactly the frozen 12. This is the drift gate: adding a stable operation
// without giving it a class, or naming an operation here that is not stable,
// fails the build instead of producing a report that quietly omits it.
func TestQueryClassOf_CoversExactlyTheFrozenTwelve(t *testing.T) {
	frozen := append([]string(nil), mcp.StableOperations...)
	slices.Sort(frozen)

	mapped := make([]string, 0, len(queryClassOf))
	for op := range queryClassOf {
		mapped = append(mapped, op)
	}
	slices.Sort(mapped)

	if !reflect.DeepEqual(mapped, frozen) {
		t.Fatalf("queryClassOf covers %v, want exactly the frozen 12 %v", mapped, frozen)
	}
	if len(frozen) != 12 {
		t.Fatalf("the stable set has %d operations; the 12-op freeze moved and this harness must be revisited", len(frozen))
	}
	for op, class := range queryClassOf {
		switch class {
		case evalreport.QueryClassStructural, evalreport.QueryClassSearch,
			evalreport.QueryClassAgentTools, evalreport.QueryClassLifecycle:
		default:
			t.Errorf("operation %s maps to unknown class %q", op, class)
		}
	}
}

// AC-4: `index`'s lifecycle-only role is declared, not implied. It carries no
// query-latency measurement, is absent from every query class, and is
// therefore absent from every floor and every gate pool.
func TestQueryClassOf_IndexIsLifecycleOnly(t *testing.T) {
	if got := queryClassOf["index"]; got != evalreport.QueryClassLifecycle {
		t.Fatalf("index maps to %q, want %q", got, evalreport.QueryClassLifecycle)
	}
	if isQueryOperation("index") {
		t.Error("index must not be treated as a measured query operation")
	}
	if slices.Contains(queryClasses, evalreport.QueryClassLifecycle) {
		t.Error("the lifecycle class must not carry FR-8's per-class execution floor")
	}
	for _, class := range queryClasses {
		if slices.Contains(stableOperationsInClass(class), "index") {
			t.Errorf("index appears in query class %q", class)
		}
	}
	// Every other stable operation IS measured — "index is special" must not
	// become "several operations are quietly special".
	for _, op := range mcp.StableOperations {
		if op == "index" {
			continue
		}
		if !isQueryOperation(op) {
			t.Errorf("stable operation %s carries no query-latency measurement", op)
		}
	}
}

// The three query classes partition the eleven measured operations: no
// operation is measured twice into one class pool, and none is dropped.
func TestStableOperationsInClass_PartitionsTheMeasuredOperations(t *testing.T) {
	seen := map[string]int{}
	for _, class := range queryClasses {
		for _, op := range stableOperationsInClass(class) {
			seen[op]++
		}
	}
	if len(seen) != 11 {
		t.Fatalf("the query classes cover %d operations, want 11 (the frozen 12 minus lifecycle-only index)", len(seen))
	}
	for op, n := range seen {
		if n != 1 {
			t.Errorf("operation %s appears in %d classes", op, n)
		}
	}
	if got := stableOperationsInClass(evalreport.QueryClassStructural); !reflect.DeepEqual(
		got, []string{"callees", "callers", "definition", "impact", "neighborhood", "references"}) {
		t.Errorf("structural class = %v", got)
	}
}

// AC-1: the plan is what makes the floor REACHABLE. The class floor alone is
// not enough — 1000 structural executions spread over six operations leave the
// gated three with ~500 — so an operation's target is the largest per-operation
// share any pool containing it demands.
func TestQueryExecutionPlan_EveryPoolClearsTheFloor(t *testing.T) {
	pools := append(classPools(), queryPool{
		id: "caller_callee_impact_p95", gate: "caller_callee_impact_p95",
		class:      evalreport.QueryClassStructural,
		operations: []string{"callees", "callers", "impact"},
	})
	plan := queryExecutionPlan(evalreport.QueryExecutionMinimum, pools)

	for _, p := range pools {
		total := 0
		for _, op := range p.operations {
			total += plan[op]
		}
		if total < evalreport.QueryExecutionMinimum {
			t.Errorf("pool %s plans %d executions over %v, below the floor of %d",
				p.id, total, p.operations, evalreport.QueryExecutionMinimum)
		}
	}
	// The gated three take the larger share; the ungated three take the class
	// share. Taking the MAX rather than the sum is what stops the plan from
	// double-counting an operation that sits in both pools.
	if plan["callers"] != 334 || plan["definition"] != 167 {
		t.Errorf("callers=%d definition=%d, want 334 and 167 (ceil(1000/3) and ceil(1000/6))", plan["callers"], plan["definition"])
	}
	if plan["search"] != evalreport.QueryExecutionMinimum {
		t.Errorf("search=%d, want %d (a single-operation class takes the whole floor)", plan["search"], evalreport.QueryExecutionMinimum)
	}
	if plan["agent_brief"] != 250 {
		t.Errorf("agent_brief=%d, want 250 (ceil(1000/4))", plan["agent_brief"])
	}
	if _, planned := plan["index"]; planned {
		t.Error("the lifecycle operation must not be given an execution target")
	}
	if got := queryExecutionPlan(0, pools); len(got) != 0 {
		t.Errorf("a zero minimum must plan nothing, got %v", got)
	}
}

// The gate pools come from the SW-123 contract, by name — the harness never
// infers which operations a §12.2 row is read over.
func TestGatePools_ComeFromTheContract(t *testing.T) {
	rs, err := loadReferenceScenario(filepath.Join(repoRoot(t), "docs", "eval", "reference-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	pools, problems := gatePools(rs)
	if len(problems) != 0 {
		t.Fatalf("the checked-in contract produced pool problems: %v", problems)
	}
	got := map[string][]string{}
	for _, p := range pools {
		got[p.gate] = p.operations
	}
	want := map[string][]string{
		"agent_context_p95":        {"agent_brief", "change_risk", "explain_symbol", "related_files"},
		"caller_callee_impact_p95": {"callees", "callers", "impact"},
		"warm_search_p95":          {"search"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gate pools = %v, want %v", got, want)
	}
	// Each of the three §12.2 warm rows is a pool: a gate the harness cannot
	// pool is a gate that would read UNKNOWN forever without saying why.
	for _, g := range rs.Gates {
		if g.MeasuredBy != queryLatencyStory {
			continue
		}
		if _, ok := want[g.ID]; !ok {
			t.Errorf("contract gate %s is assigned to %s but has no pool", g.ID, queryLatencyStory)
		}
	}
}

// A contract that names an operation this harness does not measure is
// REPORTED, not silently pooled over the remainder: a gate whose pool quietly
// lost an operation still renders a percentile, over the wrong set.
func TestGatePools_UnmeasurableOperationsAreReported(t *testing.T) {
	rs := referenceScenario{Gates: []gateMapping{
		{ID: "warm_search_p95", MeasuredBy: queryLatencyStory, Operations: []string{"search", "index", "teleport"}},
		{ID: "agent_context_p95", MeasuredBy: queryLatencyStory},
		{ID: "cold_index_p50", MeasuredBy: "SW-124", Operations: []string{"search"}},
	}}
	pools, problems := gatePools(rs)
	if len(pools) != 1 || pools[0].gate != "warm_search_p95" || !reflect.DeepEqual(pools[0].operations, []string{"search"}) {
		t.Fatalf("pools = %+v, want only warm_search_p95 over [search]", pools)
	}
	if len(problems) != 3 {
		t.Fatalf("problems = %v, want three (index, teleport, and the gate with no operations)", problems)
	}
}
