package client

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// SW-226 (AX-06) AC-5: the executor seam's latency cost, measured against the
// threshold that was written down FIRST.
//
// The bar and the method are docs/rc/ax06-canary-latency.md §1–§3, committed
// ahead of this file and ahead of any number. This test is the instrument that
// bar judges; it does not get to define it. In particular the two-term gate
// (10 % of legacy p95, or 250 µs absolute, whichever is more permissive) and the
// best-of-three anti-flake provision come from that document — reproduced here
// only so a reader of the test can see what is being asserted.

const (
	// canaryLatencySamples is N from §1.
	canaryLatencySamples = 200
	// canaryLatencyWarmup is the warm-up count from §1.
	canaryLatencyWarmup = 20
	// canaryLatencyRelative is the 10 % relative term from §2.
	canaryLatencyRelative = 0.10
	// canaryLatencyAbsolute is the 250 µs floor from §2.
	canaryLatencyAbsolute = 250 * time.Microsecond
	// canaryLatencyRounds is the best-of-three provision from §1.
	canaryLatencyRounds = 3
)

// canaryLatencyFixture seeds a graph large enough that dead_code does real work
// (the analysis is a whole-graph node + edge pass) while staying hermetic.
func canaryLatencyFixture(t testing.TB) *Direct {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	const symbols = 120

	nodes := make([]model.Node, 0, symbols)
	for i := 0; i < symbols; i++ {
		n, err := model.NewNode("function", fmt.Sprintf("p.F%03d", i), fmt.Sprintf("p/f%03d.go", i), 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	// A sparse call chain: every third symbol is called, so the analysis has
	// both live and dead symbols to score and exclude.
	for i := 3; i < symbols; i += 3 {
		e, err := model.NewEdge(nodes[i-3].ID(), nodes[i].ID(), query.EdgeKindCalls,
			model.TierConfirmed, 1, "chain", []string{"e"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

// canaryLatencySample times N calls in one kill-switch position and returns the
// sorted per-call durations.
func canaryLatencySample(t *testing.T, direct *Direct, mode CanaryMode) []time.Duration {
	t.Helper()
	previous := CanaryModeSetting()
	if err := SetCanaryMode(mode); err != nil {
		t.Fatalf("SetCanaryMode(%q): %v", mode, err)
	}
	defer func() {
		if err := SetCanaryMode(previous); err != nil {
			t.Fatalf("restore canary mode: %v", err)
		}
	}()

	ctx := context.Background()
	call := func() {
		if _, err := DispatchCanary(ctx, direct, &DeadCodeArgs{}); err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
	}
	for i := 0; i < canaryLatencyWarmup; i++ {
		call()
	}
	samples := make([]time.Duration, 0, canaryLatencySamples)
	for i := 0; i < canaryLatencySamples; i++ {
		start := time.Now()
		call()
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// TestAX06_ExecutorSeamLatencyWithinThreshold measures the canary's three
// positions and asserts the executor path against the pre-registered bar.
//
// Run it with -v to read the numbers; they are what §4 of the threshold document
// records.
func TestAX06_ExecutorSeamLatencyWithinThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)

	var (
		lastLegacy, lastExecutor, lastShadow []time.Duration
		lastBudget, lastOverhead             time.Duration
	)
	for round := 1; round <= canaryLatencyRounds; round++ {
		legacy := canaryLatencySample(t, direct, CanaryModeLegacy)
		executor := canaryLatencySample(t, direct, CanaryModeActive)
		shadow := canaryLatencySample(t, direct, CanaryModeShadow)

		legacyP95 := percentile(legacy, 0.95)
		executorP95 := percentile(executor, 0.95)
		overhead := executorP95 - legacyP95
		budget := time.Duration(float64(legacyP95) * canaryLatencyRelative)
		if budget < canaryLatencyAbsolute {
			budget = canaryLatencyAbsolute
		}

		if round == 1 {
			lastLegacy, lastExecutor, lastShadow = legacy, executor, shadow
			lastBudget, lastOverhead = budget, overhead
			t.Logf("AX-06 canary latency, round 1 of at most %d (N=%d after %d warm-up):",
				canaryLatencyRounds, canaryLatencySamples, canaryLatencyWarmup)
			for _, row := range []struct {
				name    string
				samples []time.Duration
			}{
				{"legacy  ", legacy},
				{"executor", executor},
				{"shadow  ", shadow},
			} {
				t.Logf("  %s p50=%v p95=%v", row.name, percentile(row.samples, 0.50), percentile(row.samples, 0.95))
			}
			t.Logf("  executor p95 - legacy p95 = %v; budget max(10%%=%v, 250µs) = %v",
				overhead, time.Duration(float64(legacyP95)*canaryLatencyRelative), budget)
		}
		if overhead <= budget {
			return // §2 met; the anti-flake rounds exist for the other case.
		}
		lastBudget, lastOverhead = budget, overhead
	}

	t.Errorf("the executor seam exceeded its pre-registered latency budget in all %d rounds "+
		"(docs/rc/ax06-canary-latency.md §2): executor p95 - legacy p95 = %v, budget = %v\n"+
		"  legacy   p50=%v p95=%v\n  executor p50=%v p95=%v\n  shadow   p50=%v p95=%v",
		canaryLatencyRounds, lastOverhead, lastBudget,
		percentile(lastLegacy, 0.50), percentile(lastLegacy, 0.95),
		percentile(lastExecutor, 0.50), percentile(lastExecutor, 0.95),
		percentile(lastShadow, 0.50), percentile(lastShadow, 0.95))
}

// BenchmarkCanaryDispatch is the reproducible instrument behind the numbers: it
// times the same call in each kill-switch position so a future change can be
// compared against the recorded baseline with `go test -bench`.
func BenchmarkCanaryDispatch(b *testing.B) {
	direct := canaryLatencyFixture(b)
	ctx := context.Background()
	for _, mode := range CanaryModes() {
		b.Run(string(mode), func(b *testing.B) {
			previous := CanaryModeSetting()
			if err := SetCanaryMode(mode); err != nil {
				b.Fatalf("SetCanaryMode: %v", err)
			}
			b.Cleanup(func() { _ = SetCanaryMode(previous) })
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DispatchCanary(ctx, direct, &DeadCodeArgs{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
