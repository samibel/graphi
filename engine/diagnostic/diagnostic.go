// Package diagnostic derives diagnostic records directly from graphi's canonical
// code graph — unresolved references / dangling edges left by prior edits and
// dead (unreferenced) symbols — without invoking any external compiler or build
// step. Each finding is a typed record (severity, code, message, symbol_id,
// file, line, confidence) carrying zero or more typed, machine-applicable code-actions
// expressed as structured edits over existing symbol ids — never free-text.
//
// Results are returned as one canonical typed envelope (Result) with an explicit
// Outcome marker: Reported (≥1 diagnostic), Clean (resolved, zero diagnostics —
// never a bare nil), or Unavailable (no analyzer could run). Analyzers that
// cannot run on a given graph degrade to a recorded "unavailable" entry rather
// than erroring, so the engine never reaches out to satisfy a request.
//
// The package lives strictly in the engine layer (cmd→surfaces→engine→core): it
// imports core/model and the read-only engine/query.Reader only, and performs no
// I/O, no network egress, and no CGo. Per-surface wiring (CLI/MCP/HTTP/daemon) is
// owned by SW-094.
package diagnostic

import "github.com/samibel/graphi/core/model"

// Severity is the closed set of diagnostic severities, ordered (see
// severityRank) so the canonical comparator can sort most-severe-first.
type Severity string

const (
	// SeverityError — a graph inconsistency the agent must act on (e.g. an edge
	// whose target no longer exists).
	SeverityError Severity = "error"
	// SeverityWarning — a likely problem that is not provably breaking (e.g. a
	// symbol with no inbound references).
	SeverityWarning Severity = "warning"
	// SeverityInfo — informational signal.
	SeverityInfo Severity = "info"
)

// Outcome is the explicit resolution status of a diagnostics request. It mirrors
// query.Outcome: it distinguishes the non-error terminal states so callers never
// have to guess from a nil/empty slice, and it is represented identically across
// every surface.
type Outcome string

const (
	// OutcomeReported — analyzers ran and produced at least one diagnostic.
	OutcomeReported Outcome = "reported"
	// OutcomeClean — analyzers ran and produced zero diagnostics. Distinct from
	// Unavailable: the graph was analyzed and found consistent.
	OutcomeClean Outcome = "clean"
	// OutcomeUnavailable — no requested analyzer could run (e.g. all were
	// unknown). The graceful-skip terminal state; never an error.
	OutcomeUnavailable Outcome = "unavailable"
)

// ActionKind is the closed set of typed, machine-applicable code-actions. Each
// is a structured edit over existing symbol ids, applied without re-parsing.
type ActionKind string

const (
	// ActionSafeDeleteSymbol — remove a symbol that has no live inbound
	// references (hand-off to the SW-093 safe_delete refactor, which re-checks
	// safety before applying).
	ActionSafeDeleteSymbol ActionKind = "safe_delete_symbol"
	// ActionInspectReference — point an agent at the evidence (references to
	// review). Read-only / non-mutating.
	ActionInspectReference ActionKind = "inspect_reference"
	// ActionAddSuppression — offer to record a suppression rather than delete.
	// Read-only / non-mutating, preview-only for heuristic findings.
	ActionAddSuppression ActionKind = "add_suppression"
)

// Confidence is the diagnostic confidence level used by the filter pipeline.
// It mirrors the graph model's provenance tiers: heuristic maps to the model's
// heuristic tier; exact maps to derived and confirmed tiers.
type Confidence string

const (
	// ConfidenceHeuristic — produced by a best-effort/heuristic signal.
	ConfidenceHeuristic Confidence = "heuristic"
	// ConfidenceExact — produced by derived or confirmed analysis.
	ConfidenceExact Confidence = "exact"
)

// confidenceRank assigns a stable order to Confidence for comparator use.
// Exact (0) is the higher bar; heuristic (1) is lower.
func confidenceRank(c Confidence) int {
	switch c {
	case ConfidenceExact:
		return 0
	case ConfidenceHeuristic:
		return 1
	default:
		return 2
	}
}

// SuppressionCategory tags why a finding was withheld from default output.
// Suppressed findings are still visible behind --all and are counted in Summary.
type SuppressionCategory string

const (
	SuppressionTestCode                 SuppressionCategory = "test_code"
	SuppressionGenerated                SuppressionCategory = "generated"
	SuppressionFrameworkEntrypoint      SuppressionCategory = "framework_entrypoint"
	SuppressionPublicAPINoEvidence      SuppressionCategory = "public_api_no_evidence"
	SuppressionAggregatedExternalImport SuppressionCategory = "aggregated_external_import"
	SuppressionConfiguredPath           SuppressionCategory = "configured_path"
)

// CodeAction is a typed, structured remediation attached to a diagnostic. It
// references existing symbol ids only — never free text — so a caller can apply
// it mechanically. TargetSymbol is the symbol the action operates on.
// Preview marks an action that should not be applied automatically.
type CodeAction struct {
	Kind         ActionKind   `json:"kind"`
	TargetSymbol model.NodeId `json:"target_symbol"`
	Preview      bool         `json:"preview,omitempty"`
}

// Diagnostic is a single typed finding. Its identity fields (Symbol, File, Line,
// Column, Code) are carried verbatim from the graph; Message is human-readable.
// Actions is always non-nil (possibly empty) so the wire shape is stable.
// Confidence records the provenance tier of the finding.
// Suppression records why a finding was withheld from default output; it is
// empty for findings that are shown by default.
type Diagnostic struct {
	Severity     Severity     `json:"severity"`
	Code         string       `json:"code"`
	Reason       ReasonCode   `json:"reason"`
	Message      string       `json:"message"`
	Symbol       model.NodeId `json:"symbol_id"`
	TargetSymbol model.NodeId `json:"target_symbol_id,omitempty"`
	File         string       `json:"file"`
	Line         int          `json:"line"`
	Column       int          `json:"column"`
	Actions      []CodeAction `json:"actions"`
	Confidence   Confidence   `json:"confidence"`
	// Evidence carries "path:line" citations backing the finding: the edge
	// evidence for unresolved references, the definition site for dead symbols.
	// Every default-visible diagnostic has at least one entry.
	Evidence        []string            `json:"evidence,omitempty"`
	Suppression     SuppressionCategory `json:"suppression,omitempty"`
	OccurrenceCount int                 `json:"occurrence_count,omitempty"`
}

// Summary is the honesty block added to Result. It is a value type so the
// zero/empty case serializes as a well-formed present block rather than null.
// Fields are populated incrementally by C1–C5: C1 sets TotalAnalyzed/Shown/
// TotalWithheld; C2 adds SuppressedByCategory; C3 adds DedupCollapsed.
type Summary struct {
	TotalAnalyzed        int            `json:"total_analyzed"`
	Shown                int            `json:"shown"`
	TotalWithheld        int            `json:"total_withheld"`
	SuppressedByCategory map[string]int `json:"suppressed_by_category,omitempty"`
	DedupCollapsed       int            `json:"dedup_collapsed,omitempty"`
}

// Result is the canonical, surface-agnostic diagnostics envelope. Diagnostics is
// always materialized-then-sorted by the canonical comparator before the Result
// is returned, so the value is deterministic regardless of map-iteration order.
// Unavailable lists, in canonical order, the analyzer kinds that were requested
// but could not run — the typed graceful-skip signal — while still returning the
// diagnostics the other analyzers produced.
type Result struct {
	Outcome     Outcome      `json:"outcome"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Unavailable []string     `json:"unavailable"`
	Summary     Summary      `json:"summary"`
}

// DiagnoseOptions carries the flag surface and filtering configuration.
// All fields are optional; the zero value means "use the default high-confidence
// diagnostic output with no severity floor, default suppression config, and no
// JSON flag."
type DiagnoseOptions struct {
	// All disables every default filter: confidence gate, severity floor, and
	// suppression taxonomy.
	All bool
	// ConfidenceThreshold overrides the default ConfidenceExact threshold.
	// Valid values are the product tiers "confirmed", "derived", "heuristic"
	// (confirmed/derived map onto the exact gate) or the internal "exact";
	// empty means default (exact).
	ConfidenceThreshold string
	// SeverityThreshold sets a severity floor. Valid values are "error",
	// "warning", "info"; empty means no floor (show all that pass confidence gate).
	SeverityThreshold string
	// JSON is a rendering hint for surfaces; the engine result is typed, so
	// surfaces decide encoding. It is recorded here for transparency.
	JSON bool
	// ExplainSuppressed keeps suppressed findings visible in otherwise-default
	// output, each tagged with its suppression category, so callers can audit
	// WHY findings were withheld without disabling the confidence gate the way
	// --all does. Counts remain in Summary.SuppressedByCategory either way.
	ExplainSuppressed bool
	// SuppressionConfig overrides the default built-in suppression patterns.
	SuppressionConfig SuppressionConfig
}

// ConfidenceThresholdOf resolves the option string to a Confidence value. The
// product-level tier names are accepted: "heuristic" opens the gate to
// heuristic findings; "confirmed" and "derived" (like the internal "exact")
// keep the default exact gate. An empty or unknown value returns the default
// ConfidenceExact.
func ConfidenceThresholdOf(s string) Confidence {
	switch s {
	case string(ConfidenceHeuristic):
		return ConfidenceHeuristic
	case string(ConfidenceExact), "confirmed", "derived":
		return ConfidenceExact
	default:
		return ConfidenceExact
	}
}

// SeverityThresholdOf resolves the option string to a Severity value. An empty
// value returns SeverityInfo (the lowest floor, effectively off). Unknown values
// default to SeverityInfo.
func SeverityThresholdOf(s string) Severity {
	switch s {
	case string(SeverityError):
		return SeverityError
	case string(SeverityWarning):
		return SeverityWarning
	case string(SeverityInfo):
		return SeverityInfo
	default:
		return SeverityInfo
	}
}

// severityRank assigns a stable total order to the closed Severity enum so the
// comparator can sort most-severe-first: error (0) < warning (1) < info (2) <
// unknown (3).
func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}
