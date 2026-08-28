package main

import (
	"encoding/json"

	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/scorecard"
	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/surfaces/mcp"
)

// GateResult captures the measured scorecard plus every blocking condition.
type GateResult struct {
	Scorecard   scorecard.Result
	Report      evalreport.Report // the eval scorecard report the gate consumed
	UX          *evalreport.UXMetrics
	Removed     []string // MCP tools present in the baseline but missing live
	Regressions []evalreport.Regression
	// Warnings are non-blocking observations (e.g. an invariant that cannot
	// be OBSERVED on this platform but is verified in CI).
	Warnings []string
	Errors   []string
	Pass     bool

	// Context is the execution context the policy was applied in. It is
	// recorded on the result so the verdict can never be read without the
	// question "blocking WHERE?" already answered.
	Context Context
	// Gates is every constituent gate's four-state answer and what this
	// context did about it, sorted by name. This is the record the policy
	// acted on; FormatVerdict renders it before anything else.
	Gates []GateOutcome
}

// Unverified names every gate that reported UNVERIFIED in this run, blocking
// or not.
func (r GateResult) Unverified() []string { return unverifiedGateNames(r.Gates) }

// Runner executes one hard constituent gate. The returned score is
// informational only — the 9/10 verdict comes from the MEASURED eval scorecard
// report, never from runner pass/fail averaging.
//
// The error says WHICH of the four states this run reached, and it is the only
// thing a Runner decides: nil is PASS, *UnverifiedError is UNVERIFIED,
// *GateError is ERROR, anything else is FAIL. Whether a state blocks is not a
// Runner's business — see classifyGate and Context.Blocks in policy.go.
type Runner interface {
	Run() (float64, error)
}

// EvalReportFn produces the measured eval scorecard report (cmd/eval
// -manifest ... -tier 1). Injectable for tests.
type EvalReportFn func() (evalreport.Report, error)

// UXFn produces the measured web-suite UX metrics. Injectable for tests.
type UXFn func() (evalreport.UXMetrics, error)

// Run executes the release gate:
//
//  1. every hard gate (see requiredGates) must report a state this context
//     allows;
//  2. the eval scorecard report supplies the MEASURED area scores;
//  3. the web suite supplies the measured ux score;
//  4. the final scorecard is recomputed from those inputs.
//
// The release is blocked when any hard gate BLOCKS in ctx (see Context.Blocks
// in policy.go — the four-state policy this gate applies), an MCP tool was
// removed against the baseline, the report carries a Tier-1 regression, any
// area is below the 80 floor, or the overall score is below 90.
//
// ctx is the execution context and it changes exactly one thing: whether an
// UNVERIFIED gate blocks. It is passed in rather than sniffed from the
// environment so that the policy is exercised by tests in both contexts
// without either of them having to fake a CI runner.
func Run(ctx Context, gates map[string]Runner, evalFn EvalReportFn, uxFn UXFn, baselinePath string) (GateResult, error) {
	var res GateResult
	res.Context = ctx

	// Every constituent gate is classified into one of four states and the
	// policy in policy.go decides what this context does about it. Nothing
	// here re-decides: a blocking outcome becomes an error, a non-blocking
	// non-PASS outcome becomes a warning, and the reason is carried verbatim.
	res.Gates = evaluateGates(ctx, gates)
	for _, out := range res.Gates {
		switch {
		case out.State == StatePass:
		case out.Blocking:
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %s", out.Name, out.State, out.Detail))
		default:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s (not blocking in context=%s): %s",
				out.Name, out.State, ctx, out.Detail))
		}
	}

	report, err := evalFn()
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("eval report: %v", err))
	}
	res.Report = report
	res.Regressions = report.RegressionsVsBaseline

	scores := map[string]float64{}
	for area, ar := range report.Scorecard.Breakdown {
		scores[area] = ar.Score
	}

	ux, err := uxFn()
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("ux measurement: %v", err))
	} else {
		res.UX = &ux
		scores[scorecard.AreaUX] = ux.Score
		if report.AreaProvenance == nil {
			report.AreaProvenance = map[string]string{}
		}
		report.AreaProvenance[scorecard.AreaUX] = "measured"
		res.Report.AreaProvenance = report.AreaProvenance
	}

	// The eval report warned about every area it had to carry; the gate has
	// since measured some of them (ux). Drop the now-stale carry warnings so
	// the published document never contradicts its own breakdown.
	kept := res.Report.PerfWarnings[:0:0]
	for _, w := range res.Report.PerfWarnings {
		stale := false
		for area, prov := range res.Report.AreaProvenance {
			if prov == "measured" && strings.HasPrefix(w, "area "+area+" carried") {
				stale = true
				break
			}
		}
		if !stale {
			kept = append(kept, w)
		}
	}
	res.Report.PerfWarnings = kept

	removed, err := checkToolBaseline(baselinePath)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("tool baseline check: %v", err))
	}
	res.Removed = removed

	// An incomplete score set (failed eval run) must not panic the gate; fill
	// missing areas with zero so the calculation names them as floored.
	for _, area := range []string{
		scorecard.AreaAgentMCP, scorecard.AreaSignal, scorecard.AreaPerformance,
		scorecard.AreaSetupTrust, scorecard.AreaEvaluation, scorecard.AreaUX,
	} {
		if _, ok := scores[area]; !ok {
			scores[area] = 0
		}
	}
	final, err := scorecard.Calculate(scores)
	if err != nil {
		return GateResult{}, fmt.Errorf("scorecard calculation: %w", err)
	}
	res.Scorecard = final

	res.Pass = final.Pass &&
		len(res.Errors) == 0 &&
		len(res.Removed) == 0 &&
		len(res.Regressions) == 0
	return res, nil
}

func checkToolBaseline(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var baseline []string
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, err
	}
	current := mcp.ToolNames()
	set := make(map[string]bool, len(current))
	for _, c := range current {
		set[c] = true
	}
	var removed []string
	for _, b := range baseline {
		if !set[b] {
			removed = append(removed, b)
		}
	}
	return removed, nil
}

// PublishRefusedError reports that release evidence was NOT written because
// writing it would have published a PASS verdict over a measurement that was
// never taken.
type PublishRefusedError struct{ Gates []string }

func (e *PublishRefusedError) Error() string {
	return fmt.Sprintf(
		"refusing to publish release evidence: %s unverified. "+
			"A published scorecard outlives the run that produced it and is read as the "+
			"record of what was measured; one that cannot honestly say PASS must not be "+
			"written saying it",
		strings.Join(e.Gates, ", "))
}

// Publish writes the scorecard evidence to docs/: the full measured eval
// report with the gate's recomputed scorecard and ux metrics embedded.
//
// It REFUSES, returning *PublishRefusedError, while any gate is UNVERIFIED
// (AC-4). Whether an unverified measurement blocks the run is
// context-dependent; whether it may be laundered into a published PASS is not.
// On a pull request the gate itself passes and this refusal is not a failure —
// see main.go — but the artifact is still not written, because the artifact
// makes a claim the run cannot support.
//
// Publishing is also verified: both files must exist on the way out. That
// assertion used to live in .github/workflows/release-gate.yml, where it could
// not distinguish "publish was refused" from "publish silently wrote nothing".
func Publish(result GateResult, docsDir, version, commit string) error {
	if unverified := result.Unverified(); len(unverified) > 0 {
		return &PublishRefusedError{Gates: unverified}
	}
	report := result.Report
	header := evalreport.NewHeader(version, commit)
	// The eval report carries the richer provenance (resolved SHA, corpus
	// version); keep it when the gate's own build info is weaker.
	header.CorpusVersion = result.Report.Header.CorpusVersion
	if (commit == "" || commit == "unknown") && result.Report.Header.Commit != "" {
		header.Commit = result.Report.Header.Commit
	}
	report.Header = header
	report.Scorecard = result.Scorecard
	report.UXMetrics = result.UX
	report.Target = 90.0
	report.SelfReported = true
	jsonPath := filepath.Join(docsDir, "release-scorecard.json")
	mdPath := filepath.Join(docsDir, "release-scorecard.md")
	if err := evalreport.WriteJSON(report, jsonPath); err != nil {
		return err
	}
	if err := evalreport.WriteMarkdown(report, mdPath); err != nil {
		return err
	}
	for _, path := range []string{jsonPath, mdPath} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("published evidence missing after publish: %w", err)
		}
	}
	return nil
}

// FormatVerdict returns a human-readable summary.
//
// The gate table and the UNVERIFIED banner come FIRST, immediately under the
// verdict line, because AC-2 is about prominence: an unverified measurement
// that a reader has to go looking for at the bottom of a 16-minute log is not
// meaningfully different from one that was never reported. Everything a reader
// needs to know that a measurement is missing — and that it did not block —
// is in the first few lines.
func FormatVerdict(result GateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Release gate: %s  [context=%s]\n",
		map[bool]string{true: "PASS", false: "FAIL"}[result.Pass], result.Context)

	if len(result.Gates) > 0 {
		b.WriteString("Constituent gates (four states; only UNVERIFIED depends on the context):\n")
		for _, g := range result.Gates {
			line := fmt.Sprintf("  %-13s %-10s %s", g.Name, g.State, gateDisposition(result.Context, g))
			b.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	if unverified := result.Unverified(); len(unverified) > 0 {
		fmt.Fprintf(&b, "\n!! UNVERIFIED: %s could not be measured on this run.\n", strings.Join(unverified, ", "))
		b.WriteString("   This is NOT evidence of health. No measurement was taken.\n")
		if result.Context.Blocks(StateUnverified) {
			b.WriteString("   context=" + string(result.Context) + ": this BLOCKS. On the release line a missing\n" +
				"   measurement is not an approval, and no release evidence is published.\n")
		} else {
			b.WriteString("   context=" + string(result.Context) + ": this does NOT block — a measurement that could\n" +
				"   not be taken is not a reason to refuse a change. The same run on the\n" +
				"   release line would be refused, and no release evidence is published.\n")
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Overall: %.1f/100 (pass needs >= 90 overall, every area >= 80)\n", result.Scorecard.Overall)
	var keys []string
	for k := range result.Scorecard.Breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := result.Scorecard.Breakdown[k]
		prov := result.Report.AreaProvenance[k]
		if prov == "" {
			prov = "unknown"
		}
		fmt.Fprintf(&b, "  %s: %.1f/100 (weight %d, below_floor=%v, %s)\n", k, v.Score, v.Weight, v.BelowFloor, prov)
	}
	for _, r := range result.Regressions {
		fmt.Fprintf(&b, "  tier-1 regression: %s (%s \u2192 %s)\n", r.ScenarioID, r.Before, r.After)
	}
	for _, r := range result.Removed {
		fmt.Fprintf(&b, "  removed tool: %s\n", r)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(&b, "  warning: %s\n", w)
	}
	for _, e := range result.Errors {
		fmt.Fprintf(&b, "  error: %s\n", e)
	}
	return b.String()
}

// gateDisposition says what the policy did with one gate, in the reader's
// words rather than a boolean.
func gateDisposition(ctx Context, g GateOutcome) string {
	switch {
	case g.State == StatePass:
		return ""
	case g.Blocking:
		return "BLOCKS \u2014 " + g.Detail
	default:
		return fmt.Sprintf("not blocking in context=%s (blocks in context=%s) \u2014 %s",
			ctx, ContextRelease, g.Detail)
	}
}

// WriteStepSummary appends the verdict to GitHub's check summary when running
// under Actions, so an UNVERIFIED gate is visible on the checks page itself
// rather than only to whoever expands the step log (AC-2).
//
// It is a no-op off CI, and a failure to write it is never allowed to change a
// verdict — reporting the result and deciding the result are different jobs.
func WriteStepSummary(result GateResult) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Release gate: %s (context=%s)\n\n",
		map[bool]string{true: "PASS", false: "FAIL"}[result.Pass], result.Context)
	if unverified := result.Unverified(); len(unverified) > 0 {
		verb := "does not block on a pull request"
		if result.Context.Blocks(StateUnverified) {
			verb = "BLOCKS on the release line"
		}
		fmt.Fprintf(&b, "> **UNVERIFIED — %s could not be measured.** This %s. "+
			"It is not evidence of health, and no release evidence is published while it stands.\n\n",
			strings.Join(unverified, ", "), verb)
	}
	b.WriteString("| gate | state | disposition |\n|---|---|---|\n")
	for _, g := range result.Gates {
		disposition := "allowed"
		if g.Blocking {
			disposition = "**BLOCKS**"
		} else if g.State != StatePass {
			disposition = "not blocking in context=" + string(result.Context)
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", g.Name, g.State, disposition)
	}
	fmt.Fprintf(&b, "\n```\n%s```\n", FormatVerdict(result))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}
