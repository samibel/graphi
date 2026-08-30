package main

// `-derive` — write docs/eval/retrieval-targets.json (AC-7) and
// docs/eval/retrieval-budgets.json (AC-8) from finished reports. Both files
// cite the report they came from by path and sha256, so a reader can check
// the derivation against a checked-in artifact rather than trust it.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// budgetReport is one -budget-<class> report path.
type budgetReport struct {
	class string
	path  string
}

func runDerive(targetsReport string, budgetReports []budgetReport, targetsOut, budgetsOut, date string, w io.Writer) int {
	if targetsOut == "" && budgetsOut == "" {
		fmt.Fprintln(w, "retrieval-eval: -derive needs -targets-out and/or -budgets-out")
		return exitUsage
	}
	if targetsOut != "" {
		if targetsReport == "" {
			fmt.Fprintln(w, "retrieval-eval: -targets-out needs -targets-report <report.json>")
			return exitUsage
		}
		report, from, err := loadReport(targetsReport)
		if err != nil {
			fmt.Fprintf(w, "retrieval-eval: %v\n", err)
			return exitUsage
		}
		targets, err := retrieval.DeriveTargets(report, from, date)
		if err != nil {
			fmt.Fprintf(w, "retrieval-eval: %v\n", err)
			return exitError
		}
		raw, err := retrieval.MarshalTargets(targets)
		if err != nil {
			fmt.Fprintf(w, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := os.WriteFile(targetsOut, raw, 0o644); err != nil {
			fmt.Fprintf(w, "retrieval-eval: write %s: %v\n", targetsOut, err)
			return exitError
		}
		fmt.Fprintf(w, "retrieval-eval: wrote %s from %s\n", targetsOut, targetsReport)
	}
	if budgetsOut != "" {
		if len(budgetReports) == 0 {
			fmt.Fprintln(w, "retrieval-eval: -budgets-out needs at least one of -budget-small, -budget-medium, -budget-large <report.json>")
			return exitUsage
		}
		var measurements []retrieval.FixtureMeasurement
		var cited []string
		for _, br := range budgetReports {
			report, from, err := loadReport(br.path)
			if err != nil {
				fmt.Fprintf(w, "retrieval-eval: %v\n", err)
				return exitUsage
			}
			measurements = append(measurements, retrieval.FixtureMeasurement{Class: br.class, Report: report, DerivedFrom: from})
			cited = append(cited, br.class+"="+br.path)
		}
		budgets, err := retrieval.DeriveBudgets(measurements, date)
		if err != nil {
			fmt.Fprintf(w, "retrieval-eval: %v\n", err)
			return exitError
		}
		raw, err := retrieval.MarshalBudgets(budgets)
		if err != nil {
			fmt.Fprintf(w, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := os.WriteFile(budgetsOut, raw, 0o644); err != nil {
			fmt.Fprintf(w, "retrieval-eval: write %s: %v\n", budgetsOut, err)
			return exitError
		}
		fmt.Fprintf(w, "retrieval-eval: wrote %s from %s\n", budgetsOut, strings.Join(cited, ", "))
	}
	return exitOK
}

// loadReport reads a report, refuses one from another harness version, and
// returns the citation (path + sha256 of the bytes on disk) for derived_from.
func loadReport(path string) (*retrieval.Report, retrieval.DerivedFrom, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, retrieval.DerivedFrom{}, fmt.Errorf("read report: %w", err)
	}
	var r retrieval.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, retrieval.DerivedFrom{}, fmt.Errorf("parse report %s: %w", path, err)
	}
	if err := retrieval.CheckReportVersion(&r); err != nil {
		return nil, retrieval.DerivedFrom{}, err
	}
	return &r, retrieval.DerivedFrom{Report: path, SHA256: retrieval.SHA256Hex(raw)}, nil
}
