package coverage

// SW-218: the README SIZE BUDGET — a SECOND ASSERTION on SW-216's readme gate,
// not a second instrument.
//
// WHY HERE (AC-6). `context/standards.md` warns: "don't extend one measurement
// instrument to serve another." That warning is about measurement DOMAINS — a
// coverage instrument must not quietly start reporting, say, latency, because
// then a green run means two unrelated things and neither is legible. This is
// not that. SW-216 already stood up an instrument whose subject is exactly one
// artifact: `readme.md`. It READS that file once, per `-check` run, from
// `ReadmePath`, and reports on properties of it. The two properties this file
// adds — total source lines and spine length — are properties OF THE SAME
// ARTIFACT, measured from THE SAME BYTES that `CheckReadmeClaims` is handed,
// in THE SAME run, reported through THE SAME `-check` exit path. Standing up a
// sixth binary to run `wc -l` on a file this gate already has in memory would
// be the actual duplication, and would give the readme two gates that can
// disagree about which readme they read.
//
// It is a SEPARATE FILE and a SEPARATE NAMED CHECK ("readme-budget") on
// purpose. SW-216's scope note is a load-bearing promise — it says a green
// `readme-claims` run means "the capability NUMBERS match the manifest" and
// explicitly NOT "readme.md is verified". Folding a size assertion under that
// name would widen a note whose whole value is that it is narrow. Two named
// checks, one instrument, one document, one read.
//
// WHAT IT ASSERTS
//
//	(1) CEILING — readme.md is at most ReadmeCeiling source lines, counted the
//	    way `wc -l` counts them (newline bytes). Set by the decision record
//	    memory/decisions/2026-08-26-readme-line-target-retired-for-a-composition
//	    -contract.md, which retired the spec's ~205-line target and replaced it
//	    with a composition contract plus this ceiling.
//
//	(2) SPINE — the spine is at most ReadmeSpineBudget source lines. This is
//	    SW-212 AC-7, which has been true and UNGATED since SW-212 shipped, and
//	    was carried in backlog.md as SW-212 review minor m3: the spine is met at
//	    exactly 55 with ZERO headroom, so the next added spine line breaks it
//	    silently because nothing watches.
//
// WHAT "THE SPINE" IS, AND WHY IT IS NOT THE HEADING AC-5 NAMES. SW-218 AC-5
// cites the budget as "<= 55 lines up to `## Measured, not asserted`". That
// phrasing is SW-212's own, and it was a LOCATOR, not the definition: SW-212's
// verification record fixes the spine as "readme.md:1-55 (`## Measured, not
// asserted` starts at :57)" — i.e. everything above the FIRST section that
// follows the quick start. SW-214 later inserted `## What an answer looks like`
// into that slot, so `## Measured, not asserted` is now the THIRD H2 at :141
// and a literal reading would budget 140 lines at 55 and fail on a document
// nobody broke. Gating a heading NAME would also mean a rename silently
// disables the gate. So the boundary is STRUCTURAL: the spine is every line
// before the SECOND `## ` heading, trailing blanks trimmed. On the document
// this shipped against that is `:1-55` — byte-identically the range SW-212
// measured, and still exactly 55.
//
// FAILS CLOSED. A readme with fewer than two `## ` headings, or one that does
// not end in a newline (so `wc -l` and this counter would disagree), is an
// ERROR, not a skip. A size gate that silently passes on a document it could
// not parse is the failure mode SW-216 wrote its own fail-closed rule against.

import (
	"fmt"
	"strings"
)

// ReadmeCeiling is the maximum source-line count for readme.md, per the
// 2026-08-26 composition-contract decision record. `wc -l` semantics.
const ReadmeCeiling = 400

// ReadmeSpineBudget is SW-212 AC-7's maximum source-line count for the spine.
const ReadmeSpineBudget = 55

// readmeH2Prefix opens a level-2 heading. The second one ends the spine.
const readmeH2Prefix = "## "

// ReadmeBudgetReport is the outcome of CheckReadmeBudget.
type ReadmeBudgetReport struct {
	// Lines is readme.md's source-line count, as `wc -l` counts it.
	Lines int
	// Spine is the source-line count of the spine (see the file comment).
	Spine int
	// SpineBoundary is the heading text that ends the spine, so the output
	// names the structural boundary it actually used rather than asserting it.
	SpineBoundary string
	// Violations, empty on pass, name each breach in document order.
	Violations []string
}

// Pass reports whether readme.md is within both budgets.
func (r ReadmeBudgetReport) Pass() bool { return len(r.Violations) == 0 }

// CheckReadmeBudget measures readme.md's total and spine source lines against
// the two budgets. It takes the SAME bytes CheckReadmeClaims is given, so the
// two assertions can never disagree about which document they read.
//
// The error return is the fail-closed path: a document this cannot delimit is
// an error, never a quiet pass.
func CheckReadmeBudget(src string) (ReadmeBudgetReport, error) {
	if src == "" {
		return ReadmeBudgetReport{}, fmt.Errorf("coverage: %s is empty (readme-budget fails closed — an empty readme is an error, not a skip)", ReadmePath)
	}
	if !strings.HasSuffix(src, "\n") {
		return ReadmeBudgetReport{}, fmt.Errorf("coverage: %s does not end in a newline, so `wc -l` and this counter would disagree about its length (readme-budget fails closed)", ReadmePath)
	}
	// `wc -l` counts newline bytes; with the trailing newline asserted above,
	// that is exactly the number of lines this slice holds.
	lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	total := len(lines)

	spine, boundary, err := readmeSpine(lines)
	if err != nil {
		return ReadmeBudgetReport{}, err
	}

	rep := ReadmeBudgetReport{Lines: total, Spine: spine, SpineBoundary: boundary}
	if total > ReadmeCeiling {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"%s is %d source lines; the ceiling is %d (%d over)", ReadmePath, total, ReadmeCeiling, total-ReadmeCeiling))
	}
	if spine > ReadmeSpineBudget {
		rep.Violations = append(rep.Violations, fmt.Sprintf(
			"the spine (%s:1-%d, everything above %q) is %d source lines; SW-212 AC-7's budget is %d (%d over)",
			ReadmePath, spine, boundary, spine, ReadmeSpineBudget, spine-ReadmeSpineBudget))
	}
	return rep, nil
}

// readmeSpine returns the spine's source-line count and the heading that ends
// it. The spine is every line before the SECOND `## ` heading, with trailing
// blank lines trimmed — the structural form of SW-212's `readme.md:1-55`.
func readmeSpine(lines []string) (int, string, error) {
	seen := 0
	for i, l := range lines {
		if !strings.HasPrefix(l, readmeH2Prefix) {
			continue
		}
		seen++
		if seen < 2 {
			continue
		}
		n := i
		for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
			n--
		}
		return n, strings.TrimSpace(strings.TrimPrefix(l, readmeH2Prefix)), nil
	}
	return 0, "", fmt.Errorf(
		"coverage: %s has %d `## ` headings, so the spine has no structural end (readme-budget fails closed — see internal/coverage/readmebudget.go for why the boundary is the SECOND H2 and not a heading name)",
		ReadmePath, seen)
}

// Format renders the report for `go run ./cmd/coverage -check`, in the shape
// SW-216's readme-claims output uses.
func (r ReadmeBudgetReport) Format() string {
	var b strings.Builder
	if r.Pass() {
		fmt.Fprintf(&b, "readme-budget check PASS — %s is %d/%d source lines, spine %d/%d (spine = :1-%d, everything above %q).\n",
			ReadmePath, r.Lines, ReadmeCeiling, r.Spine, ReadmeSpineBudget, r.Spine, r.SpineBoundary)
		b.WriteString(readmeBudgetScopeNote())
		return b.String()
	}
	fmt.Fprintf(&b, "readme-budget check FAILED — %s is over budget:\n\n", ReadmePath)
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "    - %s\n", v)
	}
	fmt.Fprintf(&b, "\n  Cut source lines from %s (or, for the spine, from :1-%d), then re-run:\n  go run ./cmd/coverage -check\n", ReadmePath, r.Spine)
	b.WriteString(readmeBudgetScopeNote())
	return b.String()
}

// readmeBudgetScopeNote states the limit of this assertion out loud, for the
// same reason SW-216's does: a green size check says nothing whatever about
// whether the document is any good.
func readmeBudgetScopeNote() string {
	return fmt.Sprintf(
		"  SCOPE (SW-218 AC-5): SIZE only — total source lines (ceiling %d, the 2026-08-26\n"+
			"  composition-contract decision record) and spine source lines (budget %d, SW-212\n"+
			"  AC-7). It measures LENGTH, not content: a green readme-budget run does not mean\n"+
			"  readme.md is well-composed, accurate or complete.\n",
		ReadmeCeiling, ReadmeSpineBudget)
}
