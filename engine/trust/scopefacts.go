package trust

// This file is the target-scope evidence input of the policy evaluator: the
// map-free mirror of the persisted per-file / per-package trust evidence rows
// (engine/ingest's trust_file_evidence / trust_package_evidence sidecar
// tables, PRD §14.3/§21/§22), handed to Policy.EvaluateWithScopeFacts as an
// OPTIONAL extension of the evaluation input. The wiring layer reads the rows
// (generation-checked, fail closed) and maps them into these types; this
// package stays I/O-free and never imports engine/ingest, so the closed row
// vocabularies live HERE and engine/ingest aliases them — one source, no
// drift.
//
// Design decision of record — scope facts are an input extension, NOT a rule
// change, so no policy version bump: the v1 policy rules already contain the
// scope clauses (the contract §3 tables bind PARSE_SKIPPED_IN_SCOPE,
// PACKAGE_DEGRADED, AMBIGUOUS_REFERENCE_IN_SCOPE,
// UNRESOLVED_REFERENCE_IN_SCOPE and EXTERNAL_BOUNDARY_REACHED against the
// target scope); v1 merely could not evaluate them for lack of scope evidence
// and fell to SCOPE_EVIDENCE_UNAVAILABLE (UNKNOWN/WARN). With facts absent
// (Available=false — or a shape outside the closed vocabularies below, which
// is no evidence either) every evaluation is byte-identical to the factless
// call: all sealed matrix cases and every false-pass pin hold unchanged. With
// facts present, the SAME rules judge the SAME codes at the SAME per-policy
// severities against the scope's own row. Contract doc §3.0 bumps a policy
// version on rule or threshold changes; nothing here changes either, and the
// sealed matrix plus the byte-identity red gate in policy_matrix_test.go pin
// exactly that.

// Parse-status vocabulary of ScopeFileFacts.ParseStatus (closed set). This is
// the single source of the persisted trust_file_evidence parse_status values;
// engine/ingest aliases these constants for its rows.
const (
	// ScopeParseStatusParsed — the file parsed and its symbols were committed.
	ScopeParseStatusParsed = "parsed"
	// ScopeParseStatusSkipped — the file was skipped fail-closed; ParseReason
	// carries the recorded skip reason.
	ScopeParseStatusSkipped = "skipped"
)

// Package-state vocabulary of ScopePackageFacts.State (closed set, PRD §22).
// Single source of the persisted trust_package_evidence state values;
// engine/ingest aliases these constants for its rows. The PRD §22 pin holds
// at evaluation too: type errors alone are never degradation.
const (
	// ScopePackageStateChecked — the unit type-checked with zero swallowed
	// errors.
	ScopePackageStateChecked = "checked"
	// ScopePackageStateCheckedWithErrors — the unit type-checked; some errors
	// were swallowed (expected with stub imports). Authoritative, not degraded.
	ScopePackageStateCheckedWithErrors = "checked_with_errors"
	// ScopePackageStateDegraded — the unit was not type-checked;
	// DegradedReason names why.
	ScopePackageStateDegraded = "degraded"
	// ScopePackageStateSkipped — reserved by the PRD §22 vocabulary for a unit
	// the resolver never attempted; readers must accept it. Like degraded it
	// carries no authoritative type evidence.
	ScopePackageStateSkipped = "skipped"
)

// ScopeFileFacts mirrors one persisted trust_file_evidence row for the
// assessed scope's file (the target file, or a symbol target's owning file):
// parse coverage plus the linker's per-file resolution counters. JSON tags
// carry the row's column names so the wire scope_evidence object stays
// aligned with the sidecar schema.
type ScopeFileFacts struct {
	ParseStatus       string `json:"parse_status"`
	ParseReason       string `json:"parse_reason"`
	ResolvedDerived   int    `json:"resolved_derived"`
	ResolvedHeuristic int    `json:"resolved_heuristic"`
	ResolvedExternal  int    `json:"resolved_external"`
	Skipped           int    `json:"skipped"`
	Ambiguous         int    `json:"ambiguous"`
}

// ScopePackageFacts mirrors one persisted trust_package_evidence row for the
// scope's owning package. A zero State means the evaluation carries NO
// package claim (the v1 composition fetches file rows only — package scope
// resolution stays deferred); the degraded rules then neither pass nor fire,
// because file evidence alone cannot support a "no degraded package in scope"
// claim (PRD §26: no unsupported claims).
type ScopePackageFacts struct {
	State          string `json:"state"`
	DegradedReason string `json:"degraded_reason"`
	TypeErrors     int    `json:"type_errors"`
	DroppedIntents int    `json:"dropped_intents"`
	ConfirmedEdges int    `json:"confirmed_edges"`
	SkippedFiles   int    `json:"skipped_files"`
}

// ScopeFacts is the optional target-scope evidence of one evaluation.
// Available=false is the factless call: byte-identical to Evaluate without
// facts, fail closed to SCOPE_EVIDENCE_UNAVAILABLE for every non-repository
// scope exactly as before. Available=true asserts the wiring layer read the
// scope's evidence row under the SNAPSHOT generation (stale or missing rows
// must be handed in as absent, never as zero-valued "clean" facts).
type ScopeFacts struct {
	Available bool              `json:"available"`
	File      ScopeFileFacts    `json:"file"`
	Package   ScopePackageFacts `json:"package"`
}

// wellFormed reports whether the facts are usable evidence: Available, a
// file claim inside the closed parse-status set (an "available" claim without
// a recognizable parse status witnesses nothing — a real row always carries
// one), and a package claim that is either absent ("") or inside the closed
// state set. Anything else is not evidence and reads exactly like absent
// facts (fail closed): SCOPE_EVIDENCE_UNAVAILABLE fires and no scope clause
// runs — out-of-vocabulary facts can never improve a verdict or fabricate a
// judged one.
func (sf ScopeFacts) wellFormed() bool {
	if !sf.Available {
		return false
	}
	switch sf.File.ParseStatus {
	case ScopeParseStatusParsed, ScopeParseStatusSkipped:
	default:
		return false
	}
	switch sf.Package.State {
	case "", ScopePackageStateChecked, ScopePackageStateCheckedWithErrors,
		ScopePackageStateDegraded, ScopePackageStateSkipped:
		return true
	}
	return false
}
