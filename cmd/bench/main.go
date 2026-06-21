// Command bench runs the budget-gated benchmark suite for graphi (story SW-010).
// It measures cold-start P95, full-index time, freshness lag, and static binary
// size against the pinned budgets in bench/bench-budget.yml, emits a
// machine-readable report (metrics + gate), and exits non-zero when any metric
// exceeds its budget so CI fails loudly naming the regressed metric.
//
// Usage:
//
//	go run ./cmd/bench [-budget bench/bench-budget.yml] [-samples 15] [-binary ./cmd/graphi/]
//
// Re-pinning an intentional perf change requires only editing bench-budget.yml
// (baseline + version stamp) — no code change.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/samibel/graphi/internal/bench"
)

func main() {
	budgetPath := flag.String("budget", "bench/bench-budget.yml", "path to bench-budget.yml")
	samples := flag.Int("samples", 15, "measurement samples (after warmup)")
	binaryTarget := flag.String("binary", "./cmd/graphi/", "build target whose binary size is measured")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall bench timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	man, err := bench.LoadManifest(*budgetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench: load manifest: %v\n", err)
		os.Exit(2)
	}

	metrics, err := bench.Run(ctx, bench.HarnessConfig{
		Samples:      *samples,
		BinaryTarget: *binaryTarget,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench: run: %v\n", err)
		os.Exit(2)
	}
	metrics.BaselineVersion = man.BaselineVersion

	// Enforce the frozen-workload invariant: a changed fixture must be re-pinned
	// in the manifest (digest + version stamp). This keeps runs comparable across
	// time and makes re-pinning a manifest-only, auditable edit.
	if man.FixtureDigest != "" && man.FixtureDigest != metrics.FixtureDigest {
		fmt.Fprintf(os.Stderr,
			"bench: fixture digest mismatch — measured %s != pinned %s\n"+
				"re-pin fixture_digest and bump baseline_version in %s (no code change)\n",
			metrics.FixtureDigest, man.FixtureDigest, *budgetPath)
		os.Exit(1)
	}

	report := struct {
		Metrics bench.Metrics    `json:"metrics"`
		Gate    bench.GateReport `json:"gate"`
	}{
		Metrics: metrics,
		Gate:    bench.Gate(metrics.Map(), man),
	}

	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "bench: encode report: %v\n", err)
		os.Exit(2)
	}

	if !report.Gate.Pass {
		fmt.Fprint(os.Stderr, report.Gate.FormatFailure())
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "benchmark gate PASS (baseline_version=%s)\n", man.BaselineVersion)
}
