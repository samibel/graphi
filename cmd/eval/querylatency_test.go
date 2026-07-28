package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

func acceptAll(warmExecution, string, error) bool { return true }

// SW-125 AC-6, directly: the timed region covers the operation and nothing
// else. The test inflates the SETUP cost by 25 ms per execution while the
// operation itself costs ~1 ms, and asserts the reported latency is unchanged.
// If argument assembly ever drifts back inside the clock, every sample here
// jumps by 25 ms and this fails.
func TestExecuteWarmOperation_TimesTheOperationNotTheSetup(t *testing.T) {
	const (
		setupCost = 25 * time.Millisecond
		opCost    = 2 * time.Millisecond
	)
	w := warmOperation{
		op: "callers", class: evalreport.QueryClassStructural, executions: 4,
		prepare: func(i int) warmExecution {
			time.Sleep(setupCost) // deliberately expensive harness work
			return warmExecution{args: map[string]string{"symbol": fmt.Sprint(i)}}
		},
		invoke: func(map[string]string) (string, error) {
			time.Sleep(opCost)
			return "found", nil
		},
	}
	ds := executeWarmOperation(w, acceptAll)
	if len(ds) != 4 {
		t.Fatalf("got %d samples, want 4", len(ds))
	}
	for i, d := range ds {
		if d >= setupCost {
			t.Errorf("sample %d = %v, which includes the %v setup: the timed region is not the operation alone", i, d, setupCost)
		}
		if d < opCost {
			t.Errorf("sample %d = %v, below the operation's own %v cost", i, d, opCost)
		}
	}
}

// AC-6, the other half: warmup executions are invoked and then discarded. They
// must not appear in the samples, and they must not be recorded as semantic
// outcomes — an unmeasured invocation in the outcome counts would misstate how
// much of the run was actually asserted.
func TestExecuteWarmOperation_WarmupIsInvokedThenDiscarded(t *testing.T) {
	invocations, observed := 0, 0
	w := warmOperation{
		op: "search", class: evalreport.QueryClassSearch, executions: 3, warmup: 5,
		prepare: func(i int) warmExecution { return warmExecution{args: map[string]string{"query": "x"}} },
		invoke: func(map[string]string) (string, error) {
			invocations++
			return "found", nil
		},
	}
	ds := executeWarmOperation(w, func(warmExecution, string, error) bool {
		observed++
		return true
	})
	if invocations != 8 {
		t.Errorf("the operation was invoked %d times, want 8 (5 warmup + 3 timed)", invocations)
	}
	if observed != 3 {
		t.Errorf("%d executions were observed, want 3: warmups must not enter the semantic outcome counts", observed)
	}
	if len(ds) != 3 {
		t.Errorf("%d samples retained, want 3: warmups must not enter the distribution", len(ds))
	}
}

// A rejected outcome contributes no latency sample — a fast wrong answer is not
// a fast answer. This rule predates SW-125 and must survive the refactor.
func TestExecuteWarmOperation_RejectedOutcomesContributeNoSample(t *testing.T) {
	w := warmOperation{
		op: "definition", executions: 4,
		prepare: func(i int) warmExecution { return warmExecution{allowed: []string{"found"}} },
		invoke: func(map[string]string) (string, error) {
			return "empty", errors.New("boom")
		},
	}
	if ds := executeWarmOperation(w, func(warmExecution, string, error) bool { return false }); len(ds) != 0 {
		t.Fatalf("got %d samples from rejected executions, want 0", len(ds))
	}
}

// AC-8: with no floor requested, the plan is the pre-SW-125 one — the same
// symbol sample size, the same per-operation counts. The PR path is unchanged
// because nothing about it moved.
func TestNewQueryLatencyPlan_DefaultPathKeepsTheHistoricalCounts(t *testing.T) {
	plan := newQueryLatencyPlan(0, nil)
	if plan.requested != 0 {
		t.Errorf("requested = %d, want 0", plan.requested)
	}
	if plan.symbolSample != fullRunSymbolSample || plan.agentSymbols != fullRunAgentToolSample {
		t.Errorf("sample = %d/%d, want the historical %d/%d", plan.symbolSample, plan.agentSymbols, fullRunSymbolSample, fullRunAgentToolSample)
	}
	if len(plan.perOp) != 0 {
		t.Errorf("the default path must plan no per-operation targets, got %v", plan.perOp)
	}
	// The historical counts, restated as the driver now derives them.
	cases := []struct {
		op   string
		want int
	}{
		{"callers", 25}, {"definition", 25}, {"impact", 25},
		{"search", 20 * 3}, {"explain_symbol", 10}, {"related_files", 10}, {"agent_brief", 3},
	}
	for _, tc := range cases {
		if got := plan.executionsFor(tc.op, 25, 10, 3); got != tc.want {
			t.Errorf("default executions for %s = %d, want %d", tc.op, got, tc.want)
		}
	}
	// FR-8's floor is still what sufficiency is read against, so a default run
	// reports itself as undersampled rather than looking like a floor run.
	if plan.minimum != evalreport.QueryExecutionMinimum {
		t.Errorf("minimum = %d, want FR-8's %d even on the default path", plan.minimum, evalreport.QueryExecutionMinimum)
	}
}

// AC-1: with a floor requested, the plan scales the executions AND the symbol
// sample, so the extra executions are spread over a cross-section of the
// repository instead of hammering the same 25 symbols.
func TestNewQueryLatencyPlan_FloorScalesExecutionsAndSample(t *testing.T) {
	rs, err := loadReferenceScenario(repoRoot(t) + "/docs/eval/reference-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	plan := newQueryLatencyPlan(evalreport.QueryExecutionMinimum, &rs)

	if plan.warmup != queryWarmupFloor {
		t.Errorf("warmup = %d, want %d on the floor path", plan.warmup, queryWarmupFloor)
	}
	if plan.symbolSample != querySymbolSampleCap {
		t.Errorf("symbol sample = %d, want the cap %d", plan.symbolSample, querySymbolSampleCap)
	}
	if plan.agentSymbols != plan.symbolSample {
		t.Errorf("agent symbols = %d, want the whole sample %d", plan.agentSymbols, plan.symbolSample)
	}
	for _, class := range queryClasses {
		total := 0
		for _, op := range stableOperationsInClass(class) {
			total += plan.executionsFor(op, plan.symbolSample, plan.agentSymbols, 3)
		}
		if total < evalreport.QueryExecutionMinimum {
			t.Errorf("class %s plans %d executions, below FR-8's floor of %d", class, total, evalreport.QueryExecutionMinimum)
		}
	}
	// A search class with no manifest queries cannot be measured at all; the
	// plan must say zero rather than schedule executions with nothing to run.
	if got := plan.executionsFor("search", plan.symbolSample, plan.agentSymbols, 0); got != 0 {
		t.Errorf("search executions with no manifest queries = %d, want 0", got)
	}
}

// buildQueryLatencySeries is where AC-1..AC-4 and AC-7 land in the artifact.
func TestBuildQueryLatencySeries_PublishesTheFloorTheMappingAndTheSamples(t *testing.T) {
	rs, err := loadReferenceScenario(repoRoot(t) + "/docs/eval/reference-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	plan := newQueryLatencyPlan(evalreport.QueryExecutionMinimum, &rs)

	// A synthetic measurement that clears the floor everywhere.
	perOp := map[string][]time.Duration{}
	warmupOf := map[string]int{}
	for op := range queryClassOf {
		if !isQueryOperation(op) {
			continue
		}
		n := plan.executionsFor(op, plan.symbolSample, plan.agentSymbols, 3)
		for i := range n {
			perOp[op] = append(perOp[op], time.Duration(i+1)*time.Microsecond)
		}
		warmupOf[op] = plan.warmup
	}
	sample := evalreport.QuerySymbolSample{Requested: plan.symbolSample, Returned: 3, SymbolIDs: []string{"a", "b", "c"}}
	sample.Digest = evalreport.SampleDigest(sample.SymbolIDs)

	s := buildQueryLatencySeries("grpc-go", plan, sample, perOp, warmupOf)

	// AC-4: all twelve, each with a class, and index marked lifecycle-only.
	if len(s.Operations) != 12 || len(s.ClassOf) != 12 {
		t.Fatalf("operations=%d class_of=%d, want 12 and 12", len(s.Operations), len(s.ClassOf))
	}
	for _, op := range s.Operations {
		if op.Class != s.ClassOf[op.Operation] {
			t.Errorf("operation %s carries class %q but class_of says %q", op.Operation, op.Class, s.ClassOf[op.Operation])
		}
		if op.Operation == "index" {
			if op.Measured || op.Note == "" || len(op.SamplesUS) != 0 || op.Latency != nil {
				t.Errorf("the lifecycle operation must be unmeasured and explained: %+v", op)
			}
			continue
		}
		if !op.Measured || len(op.SamplesUS) == 0 {
			t.Errorf("operation %s has no retained measurements", op.Operation)
		}
		if op.Warmup != plan.warmup {
			t.Errorf("operation %s records warmup %d, want %d", op.Operation, op.Warmup, plan.warmup)
		}
	}

	// AC-1/AC-2: every class and every gate pool cleared the floor, and says so.
	if !s.Sufficient {
		t.Errorf("series is not sufficient: %v", s.Warnings)
	}
	for _, c := range s.Classes {
		if c.Minimum != evalreport.QueryExecutionMinimum || !c.Sufficient || c.Executions != c.N {
			t.Errorf("class %+v", c)
		}
	}
	if len(s.Pools) != 3 {
		t.Fatalf("pools = %d, want the three §12.2 warm rows", len(s.Pools))
	}
	for _, p := range s.Pools {
		if !p.Sufficient || p.Executions < evalreport.QueryExecutionMinimum {
			t.Errorf("gate pool %s ran %d executions, below the floor", p.GateID, p.Executions)
		}
	}

	// AC-3/AC-7: every published statistic reproduces from the samples alone.
	reproduced := evalreport.RecomputeQueryLatency(*s)
	for _, op := range s.Operations {
		if !op.Measured {
			continue
		}
		if op.Latency == nil {
			t.Fatalf("measured operation %s published no distribution", op.Operation)
		}
		if *op.Latency != reproduced.Operations[op.Operation] {
			t.Errorf("operation %s published %+v, recomputes to %+v", op.Operation, *op.Latency, reproduced.Operations[op.Operation])
		}
		if op.Latency.P50US > op.Latency.P95US {
			t.Errorf("operation %s p50 %d > p95 %d", op.Operation, op.Latency.P50US, op.Latency.P95US)
		}
	}
	for _, c := range s.Classes {
		if c.LatencyStats != reproduced.Classes[c.Class] {
			t.Errorf("class %s published %+v, recomputes to %+v", c.Class, c.LatencyStats, reproduced.Classes[c.Class])
		}
	}
	if s.TimingMethod == "" || s.AggregateMethod == "" || s.Notes == "" {
		t.Error("the artifact must explain its own measurement and arithmetic")
	}
}

// AC-2 from the other side: a series below the floor marks itself undersampled
// and warns, so the state is visible rather than silent.
func TestBuildQueryLatencySeries_UndersampledIsAVisibleState(t *testing.T) {
	plan := newQueryLatencyPlan(0, nil)
	perOp := map[string][]time.Duration{"callers": {time.Millisecond, 2 * time.Millisecond}}
	s := buildQueryLatencySeries("fixture", plan, evalreport.QuerySymbolSample{}, perOp, map[string]int{"callers": 1})

	if s.Sufficient {
		t.Fatal("two executions cannot be a sufficient series")
	}
	for _, c := range s.Classes {
		if c.Sufficient {
			t.Errorf("class %s reads sufficient at n=%d", c.Class, c.N)
		}
	}
	if len(s.Warnings) == 0 {
		t.Error("an undersampled series must warn")
	}
	// The class still publishes what it DID measure — undersampled is not
	// unmeasured, and hiding the number would be its own dishonesty.
	for _, c := range s.Classes {
		if c.Class == evalreport.QueryClassStructural && c.N != 2 {
			t.Errorf("structural n = %d, want 2", c.N)
		}
	}
}
