// Command eval runs the token-parity eval harness with the CI-gated per-
// capability coverage matrix (story SW-012). It loads the frozen labeled eval
// set, measures graphi-vs-baseline token ratios per case with the deterministic
// offline tokenizer, emits a version-stamped report, enforces the per-capability
// coverage matrix and drift gate, and (in -claim-validate mode) gates the public
// "~50x fewer tokens" claim on the measured aggregate — resolving open question
// OQ4 with evidence rather than assertion. Hermetic: zero non-loopback network,
// no telemetry, CGo-disabled, deterministic byte-identical re-runs.
//
// Usage:
//
//	go run ./cmd/eval [-claim-validate] [-threshold 50]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/samibel/graphi/internal/eval"
)

func main() {
	claimValidate := flag.Bool("claim-validate", false, "gate the public ~50x claim on the measured aggregate (exit non-zero if held back)")
	threshold := flag.Float64("threshold", eval.DefaultClaimThreshold, "claim threshold (default ~50x)")
	flag.Parse()

	ds, err := eval.LoadDataset()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: load dataset: %v\n", err)
		os.Exit(2)
	}
	rep, err := eval.Run(ds, *claimValidate, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: run: %v\n", err)
		os.Exit(2)
	}

	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		fmt.Fprintf(os.Stderr, "eval: encode report: %v\n", err)
		os.Exit(2)
	}

	if !rep.Pass {
		fmt.Fprintf(os.Stderr, "%s FAILED:\n", rep.Name)
		for _, v := range rep.Violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", v)
		}
		os.Exit(1)
	}
	verdict := "held back"
	if rep.ClaimSupported {
		verdict = "supported"
	}
	fmt.Fprintf(os.Stderr, "%s PASS (aggregate=%.2fx, claim %s, threshold=%.0fx)\n",
		rep.Name, rep.AggregateRatio, verdict, rep.ClaimThreshold)
}
