package retrieval

// The published report for the qrel-blind smoke evaluation.
//
// The renderer checks its OWN output with CheckQrelBlindSmokeReport before
// returning it, so the naming discipline is not a review convention that a
// future edit can drift past: a report that calls this a system-blind
// evaluation, a human panel, or an estimate of general answerability cannot be
// produced at all.

import (
	"fmt"
	"strings"
)

// RenderQrelBlindSmokeReport renders the evaluation report required by AC-11.
//
// Counts are authoritative and are printed as `k/n` with their `1/n`
// resolution before any percentage, per SW-274's display rules. The only
// percentage in the document is the overall pass rate, at one decimal place.
// The Clopper-Pearson endpoints are printed as PROPORTIONS rather than
// percentages: they are the gate's threshold arithmetic, not a human-facing
// estimate of the savings quantity SW-274's one-decimal ceiling governs, and
// the gate itself is decided in exact rational arithmetic rather than from
// these digits.
func RenderQrelBlindSmokeReport(outcome EvaluationOutcome, pre PreRegistration, precondition PreconditionRecord, provenance CandidateCaptureProvenance) (string, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# SW-280 — %s over the sealed cobra-v2 holdout\n\n", QrelBlindSmokeEvaluationName)
	w("Contract: `%s`. Measurement contract: `%s`.\n\n", outcome.ContractVersion, precondition.MeasurementContractVersion)

	w("## What this is, and what it is not\n\n")
	w("This is a **%s**. The raters saw our bundle format and our question set, and were given\n", QrelBlindSmokeEvaluationName)
	w("the query text and the exact serialized `task_context/2` bundle. It is not a system-blind\n")
	w("evaluation, not a human panel, and not an estimate of general answerability.\n\n")
	w("**Blindness here is instruction-enforced, not sandbox-enforced.** Each rater was a subagent with\n")
	w("file tools, running in a checkout that contains the answer key for all of these questions\n")
	w("(`internal/eval/retrieval/testdata/datasets/cobra-v2.json` carries every grade-3 span), and it was\n")
	w("instructed to read exactly one file. Nothing prevented a rater from reading more; an instruction\n")
	w("discouraged it. No tool-call transcript was preserved, so this cannot be checked after the fact.\n")
	w("What can be checked, and was: every committed prompt rebuilds byte-for-byte from the\n")
	w("pre-registered bundle and question and carries no judgement, qrel, expected answer or rubric.\n")
	w("What the behaviour shows, and it points away from peeking: 38 of the 128 primary responses\n")
	w("declined with `INSUFFICIENT`, and the graded outcomes agree with a naive \"was a grade-3 span\n")
	w("inside a retrieved snippet?\" predictor on 54 of the 64 queries. A panel holding the key would\n")
	w("not produce that shape. Treat the count as a smoke reading taken under instructed blindness,\n")
	w("not as a number no rater could have inflated.\n\n")
	w("Its pass count is a separate gate. The pass count does not enter the token estimand or its\n")
	w("interval (`docs/eval/retrieval/methodology.md`, \"Estimand and claim boundary\").\n\n")

	w("## Result\n\n")
	w("| quantity | value |\n|---|---|\n")
	w("| answerable holdout population `N` | %d |\n", outcome.N)
	w("| pre-registered minimum passing count `k` | %d |\n", outcome.K)
	w("| observed pass count (as reviewed) | %d |\n", outcome.PassCount)
	if len(outcome.DisclosedGradingConcerns) > 0 {
		w("| **corrected pass count** (disclosed concerns subtracted) | **%d** |\n", outcome.CorrectedPassCount)
	}
	w("| observed incidence | %d/%d (resolution 1/%d) |\n", outcome.PassCount, outcome.N, outcome.N)
	w("| observed pass rate | %s |\n", renderOneDecimalPercent(outcome.PassCount, outcome.N))
	w("| exact Clopper-Pearson 95%% interval | [%s, %s] |\n", outcome.Interval.LowerBound, outcome.Interval.UpperBound)
	w("| lower bound clears the %s floor | %t |\n", outcome.Interval.Floor, outcome.Interval.MeetsFloor)
	w("| primary disagreements | %d/%d |\n", outcome.DisagreementCount, outcome.N)
	w("| adjudications | %d |\n", outcome.AdjudicationCount)
	w("| missing, empty or refused primary responses | %d |\n", outcome.NonAnsweredCount)
	w("| **RELEASE** | **%s** |\n\n", outcome.Release)
	for _, reason := range outcome.Reasons {
		w("- %s\n", reason)
	}
	w("\n")

	if len(outcome.DisclosedGradingConcerns) > 0 {
		w("## Disclosed grading concerns — counted passes that the bundle-only rule does not support\n\n")
		w("These are **not** re-grades. A silent re-grade is the retry loop this evaluation exists to\n")
		w("exclude, so nothing below changes a grade, a response or a query outcome. Each entry subtracts\n")
		w("one from the corrected count, and the release is decided on whichever count is smaller.\n\n")
		for _, c := range outcome.DisclosedGradingConcerns {
			w("### %s — %s\n\n", c.QueryID, c.Kind)
			w("%s\n\n", c.Summary)
			for _, evidence := range c.Evidence {
				w("- %s\n", evidence)
			}
			w("\n- grades concerned: ")
			for i, sha := range c.GradeSHA256 {
				if i > 0 {
					w(", ")
				}
				w("`%s`", sha)
			}
			w("\n- raised by %s at %s\n\n", c.RaisedBy, c.RaisedAt)
		}
	}

	w("## Capture binding\n\n")
	if outcome.CaptureBinding.Bound {
		w("The rated bytes are bound to the candidate implementation and the indexed checkout this run\n")
		w("names: candidate `%s`, checkout `%s`, both worktrees clean at capture.\n\n",
			outcome.CaptureBinding.CandidateSHA, outcome.CaptureBinding.CheckoutSHA)
	} else {
		w("**The rated bytes are NOT bound to the commits this run names, and that alone forces\n")
		w("`RELEASE: NO`.** Recording a commit is not binding to it:\n\n")
		for _, reason := range outcome.CaptureBinding.Reasons {
			w("- %s\n", reason)
		}
		w("\n")
	}

	w("## How `k` was derived, before any response was opened\n\n")
	d := pre.Derivation
	w("`k` is the smallest integer in `[0, N]` whose two-sided exact Clopper-Pearson 95%% lower bound is\n")
	w("at least `%s`. It was derived by code from `N`, not written into this document.\n\n", d.Floor)
	w("- `N` = %d, read from the sealed dataset (`%s`): %s\n", d.N, d.DatasetSHA256, d.NSource)
	w("- `k` = %d, whose lower bound is %s\n", d.K, d.KInterval.LowerBound)
	if d.KMinusOneInterval != nil {
		w("- `k-1` = %d, whose lower bound is %s — below the floor, which is what fixes `k`\n", d.K-1, d.KMinusOneInterval.LowerBound)
	}
	w("- method: %s\n", d.Method)
	w("- pre-registered at %s, naming precondition record `%s` at commit `%s`\n", pre.RecordedAt, pre.PreconditionSHA256, pre.PreconditionCommit)
	if provenance.CaptureVersion != CandidateCaptureVersion {
		w("- **`precondition_record_commit` above is not the commit that contains the precondition record.**\n")
		w("  This run was captured by `%s`, which copied the record's own `freeze_commit` — the candidate\n", provenance.CaptureVersion)
		w("  commit at freeze time — into a field whose name promises the containing commit. An auditor\n")
		w("  following it will not find the record there. Recover the true commit with\n")
		w("  `git log --diff-filter=A --format=%%H -- <run dir>/precondition-record.json`. The field cannot be\n")
		w("  corrected in place: the pre-registration is content-addressed and all %d responses name that\n", len(outcome.Queries)*2)
		w("  address, so rewriting it would destroy the ordering evidence it exists to provide. `%s`\n", CandidateCaptureVersion)
		w("  resolves the containing commit from git and refuses to pre-register an uncommitted record.\n")
	}
	w("\n")

	w("## Per-stratum counts\n\n")
	w("Counts are authoritative; each stratum states its own `1/n` resolution.\n\n")
	w("| stratum | passed/total | resolution |\n|---|---|---|\n")
	for _, stratum := range outcome.PerStratum {
		w("| %s | %s | %s |\n", stratum.Stratum, stratum.Rendered, stratum.Resolution)
	}
	w("\n")

	w("## Participants\n\n")
	w("| role | id | provider | model | took part in this track | basis |\n|---|---|---|---|---|---|\n")
	for _, p := range outcome.Participants {
		w("| %s | %s | %s | %s | %t | %s |\n", p.Role, p.ID, p.Provider, p.Model, p.ParticipatedInTrack, p.IndependenceBasis)
	}
	w("\n")

	if outcome.AdjudicationCount > 0 {
		w("## Adjudicated queries\n\n")
		w("| query | primary outcomes | adjudicator | final |\n|---|---|---|---|\n")
		for _, q := range outcome.Queries {
			if !q.Adjudicated {
				continue
			}
			primaries := make([]string, 0, len(q.Primary))
			for _, p := range q.Primary {
				primaries = append(primaries, p.RaterID+"="+p.Outcome)
			}
			w("| %s | %s | %s | %s |\n", q.QueryID, strings.Join(primaries, ", "), q.Adjudicator.Outcome, q.Outcome)
		}
		w("\n")
	}

	w("## Frozen inputs and the end-of-run comparison\n\n")
	w("| role | path | frozen sha256 | observed sha256 | matches |\n|---|---|---|---|---|\n")
	for _, c := range outcome.EndOfRunComparison.Comparisons {
		observed := c.Observed
		if c.Error != "" {
			observed = "UNREADABLE: " + c.Error
		}
		w("| %s | `%s` | `%s` | `%s` | %t |\n", c.Role, c.Path, c.Frozen, observed, c.Matches)
	}
	w("\nCompared at %s. All match: %t.\n\n", outcome.EndOfRunComparison.ComparedAt, outcome.EndOfRunComparison.AllMatch)

	w("## Capture provenance\n\n")
	w("- capture instrument: `%s`\n", provenance.CaptureVersion)
	w("- transport: %s\n", provenance.Transport)
	w("- payload boundary: `%s`\n", provenance.Boundary)
	w("- candidate: `%s` at a %d-token budget\n", provenance.MethodVersion, provenance.TokenBudget)
	w("- repository: %s at `%s`\n", provenance.RepoName, provenance.RepoSHA)
	w("- embedder: `%s` (model `%s`, index `%s`, generation `%s`, %d persisted vectors, state %s)\n",
		provenance.EmbedderSelector, provenance.ModelFingerprint, provenance.IndexFingerprint,
		provenance.GenerationID, provenance.PersistedVectors, provenance.SemanticState)
	w("- tokenizer: `%s` (vocabulary `%s`)\n\n", provenance.TokenizerID, provenance.TokenizerVocabSHA)

	w("## Content addresses\n\n")
	w("| query | query text sha256 | bundle sha256 | bundle bytes |\n|---|---|---|---|\n")
	for _, q := range pre.Queries {
		w("| %s | `%s` | `%s` | %d |\n", q.QueryID, q.QueryTextSHA256, q.BundleSHA256, q.BundleByteCount)
	}
	w("\n| query | rater | response sha256 | status | grade sha256 | outcome |\n|---|---|---|---|---|---|\n")
	for _, q := range outcome.Queries {
		for _, p := range q.Primary {
			w("| %s | %s | `%s` | %s | `%s` | %s |\n", q.QueryID, p.RaterID, p.ResponseSHA256, p.Status, p.GradeSHA256, p.Outcome)
		}
		if q.Adjudicator != nil {
			w("| %s | %s (adjudicator) | `%s` | %s | `%s` | %s |\n", q.QueryID, q.Adjudicator.RaterID,
				q.Adjudicator.ResponseSHA256, q.Adjudicator.Status, q.Adjudicator.GradeSHA256, q.Adjudicator.Outcome)
		}
	}
	w("\n## Per-query outcomes\n\n")
	w("| query | stratum | outcome | reason |\n|---|---|---|---|\n")
	for _, q := range outcome.Queries {
		w("| %s | %s | %s | %s |\n", q.QueryID, q.Stratum, q.Outcome, q.Reason)
	}
	w("\n")

	w("## No override\n\n")
	w("There is no flag, environment variable, configuration key or report field that lowers `k`,\n")
	w("waives a query, excludes a query from `N`, retries a graded response or forces a pass. A\n")
	w("missing, empty or refused response is a failure for that rater and a failure for its query,\n")
	w("and is not adjudicated, re-requested or replaced. A pass count below `k` records\n")
	w("`RELEASE: NO` and exits non-zero.\n")

	report := b.String()
	if err := CheckQrelBlindSmokeReport(report); err != nil {
		return "", err
	}
	return report, nil
}

// renderOneDecimalPercent obeys SW-274's display ceiling: at most one decimal
// percentage point, and only ever beside the authoritative count.
func renderOneDecimalPercent(numerator, denominator int) string {
	if denominator == 0 {
		return "undefined"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(numerator)/float64(denominator))
}
