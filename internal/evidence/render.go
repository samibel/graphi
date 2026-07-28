package evidence

import (
	"fmt"
	"strings"
)

// statusBadge maps a gate status to its legend glyph. UNKNOWN and STALE are
// deliberately given distinct, un-green glyphs so neither can be mistaken for a
// pass.
func statusBadge(status string) string {
	switch status {
	case StatusPass:
		return "✅ PASS"
	case StatusFail:
		return "❌ FAIL"
	case StatusUnknown:
		return "❔ UNKNOWN"
	case StatusStale:
		return "⚠️ STALE"
	default:
		return status
	}
}

// RenderMarkdown renders the gate dashboard from the index. It is a PURE function
// of idx (no live data, no maps in the output path), so the checked-in
// docs/rc/evidence-index.md is exactly RenderMarkdown(Load(yaml)); a freshness
// test and the -check staleness compare assert that equality, so the .md can never
// drift from the .yaml. Rows render in source (plan) order.
func RenderMarkdown(idx Index) string {
	var b strings.Builder
	b.WriteString("# graphi — evidence index & gate dashboard\n\n")
	b.WriteString("<!-- GENERATED FILE — do not edit by hand.\n")
	b.WriteString("     Source of truth: docs/rc/evidence-index.yaml\n")
	b.WriteString("     Regenerate:      go run ./cmd/evidence -generate\n")
	b.WriteString("     Honesty check:   go run ./cmd/evidence -check\n")
	b.WriteString("                      (a PASS row must carry an Evidence URI AND a SHA/Digest;\n")
	b.WriteString("                       a gate with no explicit status defaults to UNKNOWN). -->\n\n")

	b.WriteString("One row per gate, drawn from the execution plan " +
		"([`docs/plan/2026-07-graphi-9of10-execution-plan.md`](../plan/2026-07-graphi-9of10-execution-plan.md), " +
		"§6 WP0–WP10 and the §5 milestone exit gates). The columns are the plan §7 dashboard columns.\n\n")
	b.WriteString("**The rule this instrument enforces (plan §6 WP0):** every 9/10 claim must trace to a " +
		"versioned raw artifact — *kein manuell gepflegtes „grün“ ohne Beleg*. A row may read **PASS** " +
		"only if it carries both an Evidence URI and a SHA/Digest; `go run ./cmd/evidence -check` fails " +
		"otherwise. A gate with no proven evidence reads **UNKNOWN**, and per plan §2.4 **UNKNOWN counts " +
		"as not passed** — never blank, never omitted, never inferred from a green CI job. Most rows are " +
		"UNKNOWN today, and that is the point: the gap is the artifact.\n\n")

	// Candidate block — cited from the freeze record named in the source, not
	// restated. Where that record says the release digest is UNKNOWN, this says
	// UNKNOWN too; the two must never disagree. The record is named by the data,
	// not hard-coded here, so a candidate move is a one-line source edit.
	b.WriteString("**Candidate** — cited from ")
	b.WriteString(fmt.Sprintf("[`%s`](%s):\n\n", idx.Candidate.Source, docsRelative(idx.Candidate.Source)))
	b.WriteString(fmt.Sprintf("- Candidate SHA: `%s`\n", mdInline(idx.Candidate.SHA)))
	b.WriteString(fmt.Sprintf("- Published release digest: **%s**", mdInline(idx.Candidate.ReleaseDigest)))
	if strings.EqualFold(idx.Candidate.ReleaseDigest, StatusUnknown) {
		b.WriteString(" — no published release, tag or attestation is bound to the candidate. " +
			"UNKNOWN counts as not passed (plan §2.4); do not borrow an adjacent commit's digest.")
	}
	b.WriteString("\n\n")

	b.WriteString("**Status legend:** ✅ PASS · ❌ FAIL · ❔ UNKNOWN · ⚠️ STALE. A blank cell (—) means " +
		"the plan names no value for that column. **STALE** (PRD §12) means the row was stated or " +
		"measured against a *superseded* candidate: like UNKNOWN it counts as not passed, and unlike " +
		"UNKNOWN it says so out loud instead of letting the row quietly inherit the new candidate. A " +
		"STALE row must name what superseded it; `-check` rejects one that does not.\n\n")

	b.WriteString("| Gate | Threshold | Current | PASS/FAIL/UNKNOWN/STALE | Evidence URI | SHA/Digest | Owner | Next action | Due date |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, g := range idx.Gates {
		gateCell := fmt.Sprintf("**%s** — %s (%s)", mdCell(g.ID), mdCell(g.Gate), mdCell(g.Section))
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			gateCell,
			mdCell(g.Threshold),
			mdCell(g.Current),
			statusBadge(g.Status),
			mdCell(g.EvidenceURI),
			mdCell(g.SHA),
			mdCell(g.Owner),
			mdCell(g.NextAction),
			mdCell(g.Due),
		)
	}
	return b.String()
}

// mdCell renders an empty value as an em dash and escapes pipes so a cell cannot
// break the table.
func mdCell(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}

// mdInline renders an empty value as UNKNOWN (never blank) for inline prose.
func mdInline(s string) string {
	if strings.TrimSpace(s) == "" {
		return StatusUnknown
	}
	return s
}

// docsRelative turns a repo-relative docs path into a link relative to the
// rendered dashboard, which lives at docs/rc/. A path outside docs/ is left
// alone rather than guessed at.
func docsRelative(p string) string {
	if rest, ok := strings.CutPrefix(p, "docs/"); ok {
		return "../" + rest
	}
	return p
}
