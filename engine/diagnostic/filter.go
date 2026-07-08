package diagnostic

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// filterStage is the pipeline-stage contract: it receives a slice of diagnostics
// and a running tally, and returns the surviving slice. The tally is mutated
// in place so later stages can see counts accumulated by earlier stages.
type filterStage func([]Diagnostic, *tally) []Diagnostic

// tally accumulates counts that feed the Result summary. It is the extension
// seam for C2–C4.
type tally struct {
	TotalAnalyzed        int
	WithheldByConfidence int
	SuppressedByCategory map[string]int
	DedupCollapsed       int
}

// newTally returns a zero tally ready for accumulation.
func newTally() *tally {
	return &tally{
		SuppressedByCategory: map[string]int{},
	}
}

// Summary builds the final summary block from the tally and the shown diagnostics.
func (t *tally) Summary(shown []Diagnostic) Summary {
	suppressedTotal := 0
	for _, c := range t.SuppressedByCategory {
		suppressedTotal += c
	}
	var suppressedByCategory map[string]int
	if len(t.SuppressedByCategory) > 0 {
		suppressedByCategory = t.SuppressedByCategory
	}
	shownCount := 0
	for _, d := range shown {
		if d.OccurrenceCount < 1 {
			shownCount++
		} else {
			shownCount += d.OccurrenceCount
		}
	}
	return Summary{
		TotalAnalyzed:        t.TotalAnalyzed,
		Shown:                shownCount,
		TotalWithheld:        t.TotalAnalyzed - shownCount,
		SuppressedByCategory: suppressedByCategory,
		DedupCollapsed:       t.DedupCollapsed,
	}
}

// DiagnoseWithOptions runs the selected analyzers and applies the configured
// filter pipeline. It is the engine entry point for the flag surface.
func DiagnoseWithOptions(ctx context.Context, r query.Reader, kinds []string, opts DiagnoseOptions) (Result, error) {
	if r == nil {
		return Result{}, fmt.Errorf("diagnostic: nil reader")
	}

	selected, unavailable := resolveKinds(kinds)

	diags := []Diagnostic{}
	ranAny := false
	for _, k := range selected {
		switch k {
		case KindUnresolvedRef:
			found, err := analyzeUnresolvedRefs(ctx, r)
			if err != nil {
				return Result{}, err
			}
			diags = append(diags, found...)
			ranAny = true
		case KindDeadSymbol:
			found, err := analyzeDeadSymbols(ctx, r)
			if err != nil {
				return Result{}, err
			}
			diags = append(diags, found...)
			ranAny = true
		}
	}

	t := newTally()
	// TotalAnalyzed counts underlying FINDINGS, not diagnostic records: an
	// aggregated diagnostic (e.g. an unresolved_reference collapsed by target with
	// OccurrenceCount=N) stands in for N findings. Summing OccurrenceCount here
	// keeps TotalAnalyzed reconciled with Shown (which also sums OccurrenceCount)
	// now that aggregation happens in the analyzer rather than a later stage.
	t.TotalAnalyzed = 0
	for _, d := range diags {
		if d.OccurrenceCount < 1 {
			t.TotalAnalyzed++
		} else {
			t.TotalAnalyzed += d.OccurrenceCount
		}
	}

	// Build the filter pipeline. Confidence gate/severity floor only apply when not
	// --all. Suppression classification (tagging + aggregation) always runs so
	// findings carry their category and --all output shows suppressed findings.
	stages := []filterStage{}
	if !opts.All {
		stages = append(stages, confidenceGate(opts))
		stages = append(stages, severityFloor(opts))
	}
	cfg := opts.SuppressionConfig
	if cfg.GeneratedPathPatterns == nil && cfg.TestPathPatterns == nil && cfg.ConfiguredPathPatterns == nil && cfg.FrameworkSignatures == nil {
		detector := cfg.GeneratedMarkerDetector
		cfg = DefaultSuppressionConfig()
		cfg.GeneratedMarkerDetector = detector
	}
	stages = append(stages, suppressionStage(cfg, isExternalImport))
	stages = append(stages, actionGate())
	stages = append(stages, dedupStage())
	if !opts.All {
		// --explain-suppressed keeps the tagged suppressed findings visible so
		// callers can audit the withholding; counts stay in the summary either way.
		stages = append(stages, showStage(opts.ExplainSuppressed))
	}

	for _, stage := range stages {
		diags = stage(diags, t)
	}

	sortDiagnostics(diags)

	out := Result{
		Diagnostics: diags,
		Unavailable: unavailable,
		Summary:     t.Summary(diags),
	}
	// Reconciliation invariant: every analyzed finding is either shown or explicitly
	// accounted for as withheld. A violation is a programming error.
	if out.Summary.Shown+out.Summary.TotalWithheld != out.Summary.TotalAnalyzed {
		return Result{}, fmt.Errorf("diagnostic: summary reconciliation violated: shown=%d withheld=%d analyzed=%d", out.Summary.Shown, out.Summary.TotalWithheld, out.Summary.TotalAnalyzed)
	}
	switch {
	case !ranAny:
		out.Outcome = OutcomeUnavailable
	case t.TotalAnalyzed == 0:
		out.Outcome = OutcomeClean
	default:
		// Preserve "reported" even if all findings were gated, so callers can
		// distinguish "nothing found" from "findings withheld by threshold".
		out.Outcome = OutcomeReported
	}
	return out, nil
}

// Diagnose preserves the original call signature and uses the default options.
func Diagnose(ctx context.Context, r query.Reader, kinds []string) (Result, error) {
	return DiagnoseWithOptions(ctx, r, kinds, DiagnoseOptions{})
}

// showStage returns a stage that removes suppressed findings from the default
// output. When --all is set, suppressed findings remain visible (with their
// tags). The suppressed counts have already been tallied by suppressionStage.
func showStage(all bool) filterStage {
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		if all {
			return diags
		}
		out := make([]Diagnostic, 0, len(diags))
		for _, d := range diags {
			if d.Suppression == "" {
				out = append(out, d)
			}
		}
		return out
	}
}

// confidenceGate returns a stage that drops diagnostics below the configured
// confidence threshold and increments the withheld count. Default threshold is
// ConfidenceExact.
func confidenceGate(opts DiagnoseOptions) filterStage {
	threshold := ConfidenceThresholdOf(opts.ConfidenceThreshold)
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		out := make([]Diagnostic, 0, len(diags))
		for _, d := range diags {
			if confidenceRank(d.Confidence) <= confidenceRank(threshold) {
				out = append(out, d)
			} else {
				t.WithheldByConfidence++
			}
		}
		return out
	}
}

// severityFloor returns a stage that drops diagnostics below the configured
// severity threshold. The severity floor is independent of the confidence gate
// and is disabled by --all.
func severityFloor(opts DiagnoseOptions) filterStage {
	floor := SeverityThresholdOf(opts.SeverityThreshold)
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		if opts.SeverityThreshold == "" {
			return diags
		}
		out := make([]Diagnostic, 0, len(diags))
		for _, d := range diags {
			if severityRank(d.Severity) <= severityRank(floor) {
				out = append(out, d)
			}
		}
		return out
	}
}

// confidenceFromTier maps the graph model's provenance tier to the diagnostic
// Confidence enum. Heuristic stays heuristic; derived and confirmed become exact.
func confidenceFromTier(tier model.ConfidenceTier) Confidence {
	if tier == model.TierHeuristic {
		return ConfidenceHeuristic
	}
	return ConfidenceExact
}
