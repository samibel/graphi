package main

// SW-128 (P0-C5): `-aggregate <run-dir>` — reproduce every published number
// from the raw data, and say so with an exit code nobody has to interpret.
//
// AC-8 is the reason the codes are spelled out as constants rather than as
// bare integers at each return: an incomplete run must not exit 0, and
// "the report is wrong" must not be indistinguishable from "the report cannot
// be checked". Those are different facts and a CI job reacts to them
// differently — a discrepancy is a defect in the evidence, an incomplete run is
// evidence that is not finished.
//
//	0  every published metric reproduced, environment captured — publishable
//	1  DISCREPANCY: a published number does not follow from its raw samples
//	2  usage, I/O, or an artifact this build cannot read (format/harness version)
//	3  INCOMPLETE: nothing contradicted, but something is unmeasured or
//	   undocumented, so the aggregate must not be published (AC-5)

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// Aggregator exit codes. See the file comment: each names one distinct outcome,
// and no two outcomes share one.
const (
	exitAggregateReproduced  = 0
	exitAggregateDiscrepancy = 1
	exitAggregateUsage       = 2
	exitAggregateIncomplete  = 3
)

// runAggregate reads a run directory, recomputes every published metric from
// its raw samples, writes aggregate.json beside them and returns the exit code.
func runAggregate(dir, outPath string, w io.Writer) int {
	if strings.TrimSpace(dir) == "" {
		fmt.Fprintf(w, "eval: -aggregate needs a run directory (for example %s/2026-07-28-ubuntu-latest)\n", evalreport.RunsRoot)
		return exitAggregateUsage
	}

	index, report, sets, err := evalreport.ReadRunDir(dir)
	if err != nil {
		fmt.Fprintf(w, "eval: %v\n", err)
		return exitAggregateUsage
	}
	// AC-7, before any arithmetic: a directory whose raw files disagree about
	// the harness version is two measurements, and aggregating them would
	// produce a number that describes neither.
	if err := evalreport.CheckRawCompatibility(evalreport.OrderedRawSets(sets)); err != nil {
		fmt.Fprintf(w, "eval: %v\n", err)
		return exitAggregateUsage
	}

	result := evalreport.Reproduce(report, sets, index.Environment)

	target := outPath
	if target == "" {
		target = filepath.Join(dir, evalreport.AggregateFile)
	}
	if err := evalreport.WriteAggregateJSON(result, target); err != nil {
		fmt.Fprintf(w, "eval: %v\n", err)
		return exitAggregateUsage
	}
	fmt.Fprintf(w, "eval: wrote the aggregate reproduction to %s\n", target)
	printAggregateSummary(w, result)

	switch {
	case result.Discrepant > 0:
		fmt.Fprintf(w, "eval: FAIL - %d published metric(s) do not follow from the raw data:\n", result.Discrepant)
		for _, d := range result.Discrepancies {
			fmt.Fprintf(w, "eval:   %s\n", d)
		}
		return exitAggregateDiscrepancy
	case !result.Publishable:
		// AC-5 + AC-8. Nothing was contradicted, so this is not a FAIL — but an
		// aggregate without raw data, or without a documented machine, is not
		// publishable and must not leave a green job behind it.
		fmt.Fprintf(w, "eval: INCOMPLETE - this run is not publishable (%d metric(s) UNKNOWN", result.Unknown)
		if len(result.MissingSeries) > 0 {
			fmt.Fprintf(w, "; no raw data for %s", strings.Join(result.MissingSeries, ", "))
		}
		if len(result.MissingEnvironment) > 0 {
			fmt.Fprintf(w, "; environment not captured: %s", strings.Join(result.MissingEnvironment, ", "))
		}
		fmt.Fprintln(w, ")")
		return exitAggregateIncomplete
	default:
		fmt.Fprintf(w, "eval: PASS - all %d published metric(s) reproduced from the raw data\n", result.Reproduced)
		return exitAggregateReproduced
	}
}

// printAggregateSummary renders the per-series table, so a reader of the job log
// sees which harness reproduced and which could not be checked without opening
// the JSON.
func printAggregateSummary(w io.Writer, r evalreport.AggregateReport) {
	fmt.Fprintf(w, "eval: aggregate %s (harness %s, scorer %s)\n", r.Status, r.HarnessVersion, r.ScorerVersion)
	for _, c := range r.Series {
		if !c.Published {
			fmt.Fprintf(w, "eval:   %-16s not published by this run\n", c.Series)
			continue
		}
		raw := fmt.Sprintf("%d raw sample(s)", c.RawSamples)
		if !c.RawPresent {
			raw = "NO RAW DATA"
		} else if !c.RawCollected {
			raw = "not collected"
		}
		fmt.Fprintf(w, "eval:   %-16s %-7s %d metric(s): %d reproduced, %d discrepant, %d unknown (%s)\n",
			c.Series, c.Status, c.Metrics, c.Reproduced, c.Discrepant, c.Unknown, raw)
	}
	for _, row := range r.Environment.Rows() {
		if row.Known {
			continue
		}
		reason := row.Error
		if reason == "" {
			reason = "not captured"
		}
		fmt.Fprintf(w, "eval:   environment %s = UNKNOWN (%s)\n", row.Field, reason)
	}
}
