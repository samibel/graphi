package main

// SW-134 characterization baseline: the harness half of the P0 gate-9 UNKNOWN.
//
// Two facts are pinned here.
//
//  1. The declared `allowed` sets are asymmetric within a single FR-8 pool:
//     agent_brief counts a "partial" execution, and explain_symbol / change_risk
//     / related_files do not — although "partial" is a designed, GA-frozen
//     outcome of all four (corpus/hero/hero-17-explain-symbol-partial.yaml).
//
//  2. Replaying the PUBLISHED outcome tallies through those same declared sets
//     reproduces the published pool size exactly: 975 of 1000 for the reference
//     scenario, with the missing 25 attributed per operation. A root cause that
//     did not predict the number would not be proven; this one does, from the
//     committed evidence rather than from a re-run.
//
// Neither test needs an index, a corpus clone or the network.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/evalreport"
)

// baselineRunDir is the published P0 baseline this diagnosis analyses.
// docs/eval/p0/partial-outcome-diagnosis.md cites the same directory.
const baselineRunDir = "../../docs/eval/runs/2026-07-28-ubuntu-latest"

// agentContextPool is the FR-8 pool gate 9 reads, per
// docs/eval/reference-scenario.json's agent_context_p95 entry.
var agentContextPool = []string{
	scenario.OpAgentBrief,
	scenario.OpExplainSymbol,
	scenario.OpChangeRisk,
	scenario.OpRelatedFiles,
}

// sw134HistoricalAllowed freezes the agent-context pool's `allowed` sets AS THEY
// WERE when the published baseline was produced — candidate v0.7.0 at 5815db5,
// docs/eval/runs/2026-07-28-ubuntu-latest/. It is transcribed from
// cmd/eval/querylatency.go:430-455 at that SHA and quoted verbatim in
// docs/eval/p0/partial-outcome-diagnosis.md §2.3.
//
// The tests below replay PUBLISHED outcome tallies. Those tallies were produced
// by the instrument as it was, so the rule they must be replayed through is the
// rule that was in force — not whatever the live code declares today. Reading
// the live sets was correct only while the two were identical; SW-136 corrects
// the live rule (D1), so the historical one is pinned here instead. Freezing it
// keeps SW-134's diagnosis independently checkable after the correction rather
// than silently re-interpreting the baseline through a rule it never ran under.
//
// It is a historical record and must never be "fixed" to match the live code.
var sw134HistoricalAllowed = map[string][]string{
	scenario.OpAgentBrief:    {"found", "partial"},
	scenario.OpExplainSymbol: {"found"},
	scenario.OpChangeRisk:    {"found"},
	scenario.OpRelatedFiles:  {"empty", "found"},
}

// declaredAllowed returns each measured operation's `allowed` outcome set as
// buildWarmOperations declares it. prepare touches neither the engine nor the
// store, so a nil engine is enough to read the declarations — the point is the
// declaration, not an invocation.
func declaredAllowed(t *testing.T) map[string][]string {
	t.Helper()
	ops := buildWarmOperations(context.Background(), nil, corpus.Entry{}, []string{"n0", "n1"}, 2)
	out := map[string][]string{}
	for _, w := range ops {
		x := w.prepare(0)
		allowed := append([]string(nil), x.allowed...)
		sort.Strings(allowed)
		out[w.op] = allowed
	}
	return out
}

// The asymmetry, stated as an assertion. This test does NOT claim the sets are
// wrong — SW-135 owns that judgement. It claims they differ, inside one pool,
// for one outcome, which is the fact the diagnosis rests on.
func TestAgentContextPool_DeclaresPartialCountableForAgentBriefOnly(t *testing.T) {
	allowed := declaredAllowed(t)
	for _, op := range agentContextPool {
		if _, ok := allowed[op]; !ok {
			t.Fatalf("%s is not a measured warm operation: the agent_context_p95 pool moved", op)
		}
	}
	want := map[string][]string{
		scenario.OpAgentBrief:    {"found", "partial"},
		scenario.OpExplainSymbol: {"found"},
		scenario.OpChangeRisk:    {"found"},
		scenario.OpRelatedFiles:  {"empty", "found"},
	}
	for op, w := range want {
		if got := allowed[op]; !slices.Equal(got, w) {
			t.Errorf("%s allowed = %v, want %v", op, got, w)
		}
	}
	if slices.Contains(allowed[scenario.OpAgentBrief], "partial") ==
		slices.Contains(allowed[scenario.OpExplainSymbol], "partial") {
		t.Errorf("agent_brief and explain_symbol now agree on partial; the pool's asymmetry is gone")
	}
}

type publishedCheck struct {
	Operation string         `json:"operation"`
	Samples   int            `json:"samples"`
	Outcomes  map[string]int `json:"outcomes"`
}

type publishedReport struct {
	Repo struct {
		Name         string           `json:"name"`
		StableChecks []publishedCheck `json:"stable_checks"`
	} `json:"repo"`
}

func readPublishedReport(t *testing.T, run, repo string) map[string]publishedCheck {
	t.Helper()
	path := filepath.Join(baselineRunDir, run, "query-latency", repo, "report.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published report %s: %v", path, err)
	}
	var r publishedReport
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	out := map[string]publishedCheck{}
	for _, c := range r.Repo.StableChecks {
		out[c.Operation] = c
	}
	return out
}

// rejected returns how many of an operation's published executions the declared
// allowed set refuses, and the outcomes responsible.
func rejected(check publishedCheck, allowed []string) (int, []string) {
	n := 0
	var causes []string
	for outcome, count := range check.Outcomes {
		if slices.Contains(allowed, outcome) {
			continue
		}
		n += count
		causes = append(causes, outcome)
	}
	sort.Strings(causes)
	return n, causes
}

// AC-4, mechanically: 975 = 1000 - 25, and the 25 is 16 + 5 + 4 + 0.
//
// Both sides are historical: the published tallies, and the `allowed` sets that
// were in force when they were produced (sw134HistoricalAllowed). If either
// moves, this fails — which is exactly what a baseline is for. SW-136's
// correction does not touch this arithmetic; the shortfall it explains is a
// closed fact about v0.7.0 at 5815db5, and stays true after the instrument is
// corrected. What the corrected rule does to the same tallies is
// TestPublishedBaseline_CorrectedRuleRecoversAllTwentyFiveExecutions.
func TestPublishedBaseline_PartialAloneExplainsThe25ExecutionShortfall(t *testing.T) {
	allowed := sw134HistoricalAllowed
	const referenceRepo = "grpc-go"

	// Per-operation rejected executions, reference scenario, both runs.
	wantRejected := map[string]int{
		scenario.OpExplainSymbol: 16,
		scenario.OpRelatedFiles:  5,
		scenario.OpChangeRisk:    4,
		scenario.OpAgentBrief:    0,
	}

	for _, run := range []string{"run-a", "run-b"} {
		checks := readPublishedReport(t, run, referenceRepo)
		pooled, planned, shortfall := 0, 0, 0
		for _, op := range agentContextPool {
			c, ok := checks[op]
			if !ok {
				t.Fatalf("%s: %s missing from the published stable_checks", run, op)
			}
			if c.Samples != 250 {
				t.Errorf("%s/%s: %d attempted executions, want 250 (4 ops x 250 = FR-8's 1000)", run, op, c.Samples)
			}
			n, causes := rejected(c, allowed[op])
			if n != wantRejected[op] {
				t.Errorf("%s/%s: %d rejected executions, want %d (outcomes %v)", run, op, n, wantRejected[op], c.Outcomes)
			}
			if n > 0 && !slices.Equal(causes, []string{"partial"}) {
				t.Errorf("%s/%s: rejected outcomes %v — something other than partial is costing executions", run, op, causes)
			}
			planned += c.Samples
			pooled += c.Samples - n
			shortfall += n
		}
		if planned != evalreport.QueryExecutionMinimum {
			t.Errorf("%s: %d executions attempted, want the FR-8 floor of %d", run, planned, evalreport.QueryExecutionMinimum)
		}
		if pooled != 975 {
			t.Errorf("%s: pooled %d executions, want the published 975", run, pooled)
		}
		if shortfall != 25 {
			t.Errorf("%s: shortfall %d, want the published 25", run, shortfall)
		}
		if pooled >= evalreport.QueryExecutionMinimum {
			t.Errorf("%s: pooled %d is not undersampled; gate 9's UNKNOWN would have no cause", run, pooled)
		}
	}
}

// AC-4's determinism half and AC-5 in one: the tallies are byte-identical
// between the two runs (different CPU families) for every pinned repo, and `lo`
// records no partial at all.
func TestPublishedBaseline_TalliesAreRunInvariantAndLoIsClean(t *testing.T) {
	repos := []string{"grpc-go", "lo", "uuid", "cobra", "gin"}
	for _, repo := range repos {
		a := readPublishedReport(t, "run-a", repo)
		b := readPublishedReport(t, "run-b", repo)
		for _, op := range agentContextPool {
			ca, cb := a[op], b[op]
			if ca.Samples != cb.Samples {
				t.Errorf("%s/%s: attempted %d in run-a, %d in run-b", repo, op, ca.Samples, cb.Samples)
			}
			for outcome, na := range ca.Outcomes {
				if nb := cb.Outcomes[outcome]; na != nb {
					t.Errorf("%s/%s/%s: %d in run-a, %d in run-b — the outcome is machine-dependent", repo, op, outcome, na, nb)
				}
			}
			if len(ca.Outcomes) != len(cb.Outcomes) {
				t.Errorf("%s/%s: outcome sets differ between runs (%v vs %v)", repo, op, ca.Outcomes, cb.Outcomes)
			}
			if repo == "lo" && ca.Outcomes["partial"] != 0 {
				t.Errorf("lo/%s: %d partial executions; the passing repository is no longer partial-free", op, ca.Outcomes["partial"])
			}
		}
	}
}

type publishedSample struct {
	Repo struct {
		QueryLatency struct {
			SymbolSample struct {
				Requested    int      `json:"requested"`
				Returned     int      `json:"returned"`
				AgentSymbols int      `json:"agent_symbols"`
				SymbolIDs    []string `json:"symbol_ids"`
			} `json:"symbol_sample"`
		} `json:"query_latency"`
	} `json:"repo"`
}

// An independent prediction of the mechanism, confirmed by the published data.
//
// uuid's degree-stratified sample returned only 137 symbols, but each operation
// still ran 250 timed executions over agentAt(i) = symbolIDs[i%137]. So symbols
// at indices 0..112 are asked twice and 113..136 once. If rejection were an
// execution-level event (timing, runner, load) the counts would be arbitrary;
// if it is a deterministic property of the SYMBOL — which "item count exceeds
// the cap" is — every offending symbol in the doubled prefix costs exactly two
// executions. The prefix is the doubled one because the sample is ordered by
// degree DESC, so the high-degree symbols that can exceed a cap are precisely
// the ones asked twice. Both published counts are even.
func TestPublishedBaseline_UuidRepeatsItsSampleAndRepeatsItsRejections(t *testing.T) {
	path := filepath.Join(baselineRunDir, "run-a", "query-latency", "uuid", "report.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s publishedSample
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	sample := s.Repo.QueryLatency.SymbolSample
	if sample.Returned >= 250 {
		t.Skipf("uuid's sample no longer wraps (%d symbols for 250 executions)", sample.Returned)
	}
	if sample.AgentSymbols != sample.Returned || len(sample.SymbolIDs) != sample.Returned {
		t.Fatalf("sample is inconsistent: returned=%d agent_symbols=%d ids=%d",
			sample.Returned, sample.AgentSymbols, len(sample.SymbolIDs))
	}
	doubled := 250 - sample.Returned // indices 0..doubled-1 are asked twice

	checks := readPublishedReport(t, "run-a", "uuid")
	for _, op := range []string{scenario.OpExplainSymbol, scenario.OpChangeRisk} {
		// Historical sets: this is a statement about the published run, which was
		// taken under the pre-SW-136 rule.
		n, _ := rejected(checks[op], sw134HistoricalAllowed[op])
		if n == 0 {
			t.Fatalf("uuid/%s: no rejected executions; this test has nothing to check", op)
		}
		if n%2 != 0 {
			t.Errorf("uuid/%s: %d rejected executions is odd — with %d symbols asked twice, rejection is no longer a deterministic property of the symbol",
				op, n, doubled)
		}
		if n/2 > doubled {
			t.Errorf("uuid/%s: %d rejections cannot come from %d doubled symbols", op, n, doubled)
		}
	}
}

// AC-11 guard. The undersampled pool's p95 (471.250 ms, run-a) sits inside the
// 500 ms threshold, and the published artifact still reads UNKNOWN. This test
// exists so no later change — including any correction SW-136 might make — can
// quietly turn that number into a verdict without a re-run that fills the pool.
func TestPublishedBaseline_Gate9StaysUnknownDespiteAnUndersampledP95UnderThreshold(t *testing.T) {
	path := filepath.Join(baselineRunDir, "p0-baseline.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Gates []struct {
			ID      string `json:"id"`
			Verdict string `json:"verdict"`
			RunA    struct {
				Status           string `json:"status"`
				PooledExecutions int    `json:"pooled_executions"`
				Required         int    `json:"required"`
			} `json:"run_a"`
		} `json:"gates"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	found := false
	for _, g := range doc.Gates {
		if g.ID != "agent_context_p95" {
			continue
		}
		found = true
		if g.Verdict != "UNKNOWN" {
			t.Errorf("gate 9 verdict = %q, want UNKNOWN: the pool is still %d of %d",
				g.Verdict, g.RunA.PooledExecutions, g.RunA.Required)
		}
		if g.RunA.PooledExecutions != 975 || g.RunA.Required != evalreport.QueryExecutionMinimum {
			t.Errorf("gate 9 run-a pool = %d of %d, want 975 of %d",
				g.RunA.PooledExecutions, g.RunA.Required, evalreport.QueryExecutionMinimum)
		}
	}
	if !found {
		t.Fatalf("agent_context_p95 is absent from the published gate list")
	}
}
