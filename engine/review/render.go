package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
)

// WriterVersion identifies the render/gate LOGIC version, echoed in every
// PublishResult so a stored/audited comment can be tied to the writer that
// produced it (determinism is an integrity property — this output is a contract
// SW-043 pins).
const WriterVersion = "pr-comment/1"

// PublishSchemaVersion versions the PublishResult JSON shape. Bump when the shape
// changes so SW-043 can pin a known contract.
const PublishSchemaVersion = 1

// MarkerVersion is the version segment of the hidden sticky marker. Bumping it is
// an EXPLICIT contract break (old comments are no longer matched and a fresh
// sticky comment is created) — never change StickyMarker silently.
const MarkerVersion = "v1"

// StickyMarker is the stable, hidden HTML-comment marker that gives the sticky PR
// comment its identity. PR hosts (GitHub/GitLab) preserve HTML comments verbatim
// in rendered Markdown, so the same marker can be located on a re-run and the
// comment updated IN PLACE (one comment per PR, idempotent). It is a single
// documented constant so re-runs are deterministic; the version segment makes any
// future change an explicit, auditable bump (Contract C2).
const StickyMarker = "<!-- graphi-pr-review:" + MarkerVersion + " -->"

// ProvenanceSummary mirrors analysis.ProvenanceSummary: the redaction level at
// which sensitive taint source/sink/step provenance is NOT rendered. The public
// PR comment defaults to this so a world-readable comment never leaks sensitive
// code paths (Security S2).
const ProvenanceSummary = analysis.ProvenanceSummary

// renderConfig is the DOCUMENTED, fixed-by-default render model. It is hashed into
// the PublishResult ConfigHash (together with the gate config) so any change is
// auditable and reproducible — mirroring the SW-039 weightsHash / SW-040
// signalConfig / SW-041 questionConfig discipline.
type renderConfig struct {
	// Redact, when true, renders only non-sensitive (summary-level) provenance in
	// the public comment body. Default true (fail-safe for a public comment).
	Redact bool `json:"redact"`
	// MaxQuestions bounds the number of reviewer questions rendered so the comment
	// stays token-bounded for a huge PR. 0 means the documented default.
	MaxQuestions int `json:"max_questions"`
}

// defaultRenderConfig is the fixed render model for WriterVersion "pr-comment/1".
var defaultRenderConfig = renderConfig{Redact: true, MaxQuestions: 50}

// hash returns the deterministic fingerprint of the render model.
func (c renderConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// FindingsBundle is the consumed input to the publisher: the three sibling
// REPORTS (never the analyzers) plus the PR identity. It is produced once by the
// findingsSource seam (consume, never recompute).
type FindingsBundle struct {
	// PR identifies the pull request the comment is posted to. Free-form host
	// reference (e.g. "owner/repo#42"); rendered in the header for humans.
	PR string `json:"pr"`
	// Risk is the SW-039 per-region risk report.
	Risk analysis.RiskReport `json:"risk"`
	// Signals is the SW-040 per-region hub/bridge/surprise report.
	Signals analysis.SignalReport `json:"signals"`
	// Questions is the SW-041 reviewer-question report.
	Questions analysis.QuestionReport `json:"questions"`
}

// Render assembles the consumed bundle into ONE deterministic Markdown body. The
// body always begins with the hidden StickyMarker (the comment identity) and
// contains a header plus risk, signals, and questions sections; when gate is
// non-nil its verdict section is rendered too. It is PURE string assembly: it
// imports no network/exec packages and performs no I/O (AC3, Security S4).
//
// Determinism: the consumed reports are already canonically sorted by their own
// serializers; Render copies that order verbatim and never iterates a map, so
// byte-identical input yields a byte-identical body.
func Render(bundle FindingsBundle, cfg renderConfig, gate *GateDecision) string {
	var b strings.Builder

	// 1. Hidden sticky marker FIRST — the comment identity. Must be present and
	// stable so a re-run can find-and-update this exact comment.
	b.WriteString(StickyMarker)
	b.WriteByte('\n')

	// 2. Header.
	b.WriteString("## graphi PR review\n\n")
	if pr := strings.TrimSpace(bundle.PR); pr != "" {
		b.WriteString("PR: `")
		b.WriteString(pr)
		b.WriteString("`\n\n")
	}

	// 3. Gate verdict section (only when a gate decision was computed). Rendering
	// the verdict in the body makes a BLOCK reason visible on the PR itself. The
	// body NEVER contains a host token — only the verdict + evidence (Security S1).
	if gate != nil {
		renderGateSection(&b, *gate)
	}

	// 4. Risk section.
	renderRiskSection(&b, bundle.Risk, cfg.Redact)

	// 5. Signals section.
	renderSignalsSection(&b, bundle.Signals)

	// 6. Questions section.
	renderQuestionsSection(&b, bundle.Questions, cfg.MaxQuestions)

	// 7. Footer: writer/schema provenance (auditable, deterministic).
	b.WriteString("\n---\n")
	b.WriteString(fmt.Sprintf("_generated by %s (schema %d, identity %d)_\n",
		WriterVersion, PublishSchemaVersion, model.IdentitySchemaVersion))

	return b.String()
}

// renderGateSection renders the merge-gate verdict and, on BLOCK, its
// evidence-linked reason.
func renderGateSection(b *strings.Builder, gate GateDecision) {
	b.WriteString("### Merge gate: ")
	b.WriteString(gate.Verdict)
	b.WriteByte('\n')
	if reason := strings.TrimSpace(gate.Reason); reason != "" {
		b.WriteString("\n")
		b.WriteString(reason)
		b.WriteByte('\n')
	}
	for _, ev := range gate.Evidence {
		b.WriteString("- ")
		b.WriteString(gateEvidenceLine(ev))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// renderRiskSection renders the SW-039 per-region risk records. redact gates the
// publisher's OWN defense-in-depth redaction of sensitive taint provenance — it
// is applied regardless of the upstream record's provenance level so a misconfigured
// caller (e.g. provenance=full) can never leak source/sink paths into the public
// comment (Security S2).
func renderRiskSection(b *strings.Builder, rep analysis.RiskReport, redact bool) {
	b.WriteString("### Risk\n\n")
	resolved := 0
	for _, rec := range rep.Regions {
		if rec.Degraded {
			continue
		}
		resolved++
		b.WriteString(fmt.Sprintf("- `%s` — score **%s** (confidence: %s)\n",
			string(rec.Region), rec.Score, rec.Confidence))
		for _, ev := range rec.Evidence {
			b.WriteString("  - ")
			b.WriteString(riskEvidenceLine(ev, redact))
			b.WriteByte('\n')
		}
	}
	if degraded := countDegradedRisk(rep); degraded > 0 {
		b.WriteString(fmt.Sprintf("- _%d changed region(s) could not be resolved to the indexed graph (degraded)._\n", degraded))
	}
	if resolved == 0 && countDegradedRisk(rep) == 0 {
		b.WriteString("- _No scored regions._\n")
	}
	b.WriteByte('\n')
}

// renderSignalsSection renders the SW-040 hub/bridge/surprise annotations.
func renderSignalsSection(b *strings.Builder, rep analysis.SignalReport) {
	b.WriteString("### Signals\n\n")
	any := false
	for _, rec := range rep.Regions {
		if rec.Degraded || len(rec.Signals) == 0 {
			continue
		}
		any = true
		b.WriteString(fmt.Sprintf("- `%s`\n", string(rec.Region)))
		for _, s := range rec.Signals {
			b.WriteString("  - ")
			b.WriteString(signalLine(s))
			b.WriteByte('\n')
		}
	}
	if !any {
		b.WriteString("- _No hub/bridge/surprise signals._\n")
	}
	b.WriteByte('\n')
}

// renderQuestionsSection renders the SW-041 reviewer questions (bounded).
func renderQuestionsSection(b *strings.Builder, rep analysis.QuestionReport, max int) {
	b.WriteString("### Reviewer questions\n\n")
	if max <= 0 {
		max = defaultRenderConfig.MaxQuestions
	}
	if len(rep.Questions) == 0 {
		b.WriteString("- _No questions generated._\n\n")
		return
	}
	n := 0
	for _, q := range rep.Questions {
		if n >= max {
			b.WriteString(fmt.Sprintf("- _… %d more question(s) omitted (bound %d)._\n", len(rep.Questions)-n, max))
			break
		}
		n++
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(q.Text))
		if q.Region != "" {
			b.WriteString(fmt.Sprintf(" _(`%s`)_", string(q.Region)))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// riskEvidenceLine renders one risk EvidenceItem as a compact bullet. When redact
// is true (the default for a public comment), the sensitive taint Source/Sink are
// NEVER rendered, independent of the upstream record's provenance level — this is
// the publisher's own defense-in-depth guard so a caller that asked the scorer for
// "full" provenance still cannot leak source/sink paths into a world-readable
// comment (Security S2). When redact is false, the caller has explicitly opted
// into verbatim provenance. (Steps are not rendered in either mode — only the
// Kind/Reason/Source/Sink summary is shown.)
func riskEvidenceLine(ev analysis.EvidenceItem, redact bool) string {
	parts := []string{ev.Kind}
	if r := strings.TrimSpace(ev.Reason); r != "" {
		parts = append(parts, r)
	}
	if !redact && (ev.Source != "" || ev.Sink != "") {
		parts = append(parts, fmt.Sprintf("path %s → %s", ev.Source, ev.Sink))
	}
	return strings.Join(parts, ": ")
}

// signalLine renders one SW-040 SignalFlag.
func signalLine(s analysis.SignalFlag) string {
	parts := []string{s.Kind}
	if s.Detail != "" {
		parts = append(parts, s.Detail)
	}
	if r := strings.TrimSpace(s.Reason); r != "" {
		parts = append(parts, r)
	}
	return strings.Join(parts, ": ")
}

// countDegradedRisk counts degraded (unresolved) risk records.
func countDegradedRisk(rep analysis.RiskReport) int {
	n := 0
	for _, rec := range rep.Regions {
		if rec.Degraded {
			n++
		}
	}
	return n
}
