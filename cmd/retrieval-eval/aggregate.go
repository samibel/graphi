package main

// `-aggregate <run-dir>` — reproduce every published number from the raw
// data, and say so with an exit code nobody has to interpret (mirrors
// cmd/eval/aggregate.go):
//
//	0  every published metric reproduced, environment captured — publishable
//	1  DISCREPANCY: a published number does not follow from its raw samples
//	2  usage, I/O, or an artifact this build cannot read
//	3  INCOMPLETE: nothing contradicted, but something is unmeasured or
//	   undocumented, so the aggregate must not be published

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// AggregateFile is what the aggregator writes into the run directory.
const AggregateFile = "aggregate.json"

func runAggregate(dir, outPath string, w io.Writer) int {
	if strings.TrimSpace(dir) == "" {
		fmt.Fprintln(w, "retrieval-eval: -aggregate needs a run directory")
		return retrieval.ExitUsage
	}
	run, err := retrieval.ReadRunDir(dir)
	if err != nil {
		fmt.Fprintf(w, "retrieval-eval: %v\n", err)
		return retrieval.ExitUsage
	}
	result := retrieval.Reproduce(run)

	target := outPath
	if target == "" {
		target = filepath.Join(dir, AggregateFile)
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "retrieval-eval: %v\n", err)
		return retrieval.ExitUsage
	}
	if err := os.WriteFile(target, append(raw, '\n'), 0o644); err != nil {
		fmt.Fprintf(w, "retrieval-eval: write %s: %v\n", target, err)
		return retrieval.ExitUsage
	}
	fmt.Fprintf(w, "retrieval-eval: wrote the aggregate reproduction to %s\n", target)
	fmt.Fprintf(w, "retrieval-eval: aggregate %s (harness %s, scorer %s): %d metric(s) checked, %d reproduced, %d discrepant, %d unknown\n",
		result.Status, result.HarnessVersion, result.ScorerVersion, result.Checked, result.Reproduced, result.Discrepant, result.Unknown)

	switch result.ExitCode() {
	case retrieval.ExitDiscrepancy:
		fmt.Fprintf(w, "retrieval-eval: FAIL - %d published metric(s) do not follow from the raw data:\n", result.Discrepant)
		for _, d := range result.Discrepancies {
			fmt.Fprintf(w, "retrieval-eval:   %s\n", d)
		}
	case retrieval.ExitIncomplete:
		fmt.Fprintf(w, "retrieval-eval: INCOMPLETE - this run is not publishable (%d metric(s) UNKNOWN", result.Unknown)
		if len(result.MissingRaw) > 0 {
			fmt.Fprintf(w, "; no raw data: %s", strings.Join(result.MissingRaw, ", "))
		}
		if len(result.MissingEnvironment) > 0 {
			fmt.Fprintf(w, "; environment not captured: %s", strings.Join(result.MissingEnvironment, ", "))
		}
		fmt.Fprintln(w, ")")
	default:
		fmt.Fprintf(w, "retrieval-eval: PASS - all %d published metric(s) reproduced from the raw data\n", result.Reproduced)
	}
	return result.ExitCode()
}
