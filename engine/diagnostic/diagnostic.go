// Package diagnostic derives diagnostic records directly from graphi's canonical
// code graph — unresolved references / dangling edges left by prior edits and
// dead (unreferenced) symbols — without invoking any external compiler or build
// step. Each finding is a typed record (severity, code, message, symbol_id,
// file, line) carrying zero or more typed, machine-applicable code-actions
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
)

// CodeAction is a typed, structured remediation attached to a diagnostic. It
// references existing symbol ids only — never free text — so a caller can apply
// it mechanically. TargetSymbol is the symbol the action operates on.
type CodeAction struct {
	Kind         ActionKind   `json:"kind"`
	TargetSymbol model.NodeId `json:"target_symbol"`
}

// Diagnostic is a single typed finding. Its identity fields (Symbol, File, Line,
// Column, Code) are carried verbatim from the graph; Message is human-readable.
// Actions is always non-nil (possibly empty) so the wire shape is stable.
type Diagnostic struct {
	Severity Severity     `json:"severity"`
	Code     string       `json:"code"`
	Message  string       `json:"message"`
	Symbol   model.NodeId `json:"symbol_id"`
	File     string       `json:"file"`
	Line     int          `json:"line"`
	Column   int          `json:"column"`
	Actions  []CodeAction `json:"actions"`
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
