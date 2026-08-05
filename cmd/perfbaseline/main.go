// Command perfbaseline records and compares the ARCH-P0 performance baseline for
// the P2 architecture refactor.
//
//	go run ./cmd/perfbaseline -record -commit <sha> \
//	    -out docs/rc/perf-baseline.json          # measure and write the baseline
//	go run ./cmd/perfbaseline -diff a.json,b.json # compare a later run against it
//
// It measures full index, warm query per operation, the trust report, and the
// canonical release binary size, reporting median AND p95 for every timing.
//
// This is NOT a release gate — bench/bench-budget.yml stays the gate for the
// metrics it owns. This artifact exists so the architecture phases can answer
// "did inserting an application layer cost anything" with a measurement instead
// of an opinion. Timings are only comparable against another run on the same
// machine; -diff warns when that assumption is broken.
//
// Exit codes: 0 = ok / inside budget, 1 = a metric regressed beyond budget,
// 2 = internal error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/perfbaseline"
)

func main() {
	var (
		record  = flag.Bool("record", false, "measure and print a baseline")
		diff    = flag.String("diff", "", "compare two recorded baselines: baseline.json,candidate.json")
		out     = flag.String("out", "", "with -record: write the JSON artifact to this path")
		commit  = flag.String("commit", "", "with -record: the revision being measured (required)")
		samples = flag.Int("samples", perfbaseline.DefaultSamples, "recorded samples per metric")
		fixture = flag.String("fixture", "", "workload directory (default: bench/fixture)")
		skipBin = flag.Bool("skip-binary-size", false, "skip the canonical release build (fast, but leaves the size metric unrecorded)")
	)
	flag.Parse()

	switch {
	case *record && *diff == "":
		runRecord(*commit, *out, *fixture, *samples, *skipBin)
	case !*record && *diff != "":
		runDiff(*diff)
	default:
		fmt.Fprintln(os.Stderr, "perfbaseline: exactly one of -record or -diff is required")
		os.Exit(2)
	}
}

func runRecord(commit, out, fixture string, samples int, skipBin bool) {
	if commit == "" {
		fmt.Fprintln(os.Stderr, "perfbaseline: -commit is required (a measurement that cannot name what it measured cannot be compared)")
		os.Exit(2)
	}
	report, err := perfbaseline.Measure(context.Background(), perfbaseline.Config{
		FixtureDir:     fixture,
		Samples:        samples,
		Commit:         commit,
		SkipBinarySize: skipBin,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(perfbaseline.Summary(report))

	if out == "" {
		return
	}
	rendered, err := perfbaseline.Render(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline: %v\n", err)
		os.Exit(2)
	}
	path := out
	if !filepath.IsAbs(path) {
		root, rerr := perfbaseline.ModuleRoot()
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "perfbaseline: %v\n", rerr)
			os.Exit(2)
		}
		path = filepath.Join(root, filepath.FromSlash(out))
	}
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline: write %s: %v\n", path, err)
		os.Exit(2)
	}
	fmt.Printf("wrote %s\n", out)
}

func runDiff(spec string) {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "perfbaseline: -diff needs exactly two paths: baseline.json,candidate.json")
		os.Exit(2)
	}
	baseline, err := loadReport(strings.TrimSpace(parts[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline: %v\n", err)
		os.Exit(2)
	}
	candidate, err := loadReport(strings.TrimSpace(parts[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "perfbaseline: %v\n", err)
		os.Exit(2)
	}
	report := perfbaseline.Diff(baseline, candidate)
	fmt.Print(report.Format())
	if !report.Pass() {
		os.Exit(1)
	}
}

func loadReport(path string) (perfbaseline.Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return perfbaseline.Report{}, fmt.Errorf("read %s: %w", path, err)
	}
	return perfbaseline.Parse(raw)
}
