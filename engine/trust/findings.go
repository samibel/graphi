package trust

import (
	"errors"
	"fmt"
)

// This file is the closed v1 finding-code registry (contract doc §3.4): the
// union of every code the three built-in policies bind — the PRD §26 list,
// TARGET_AMBIGUOUS (PRD §27, adopted), and the three codes minted in v1.
// Adding a code is a v1-compatible additive change; removing, renaming, or
// rebinding one requires a policy version bump and a contract document bump
// (contract doc §6). NewFinding is the constructing gate: a code outside this
// registry is rejected, so an off-registry Finding is not constructible
// through the package API (same discipline as model.NewEdge's closed tier
// enum).

// Finding codes — the closed v1 registry, one constant per contract doc §3.4
// row.
const (
	// FindingGraphStale — the graph is not current (drift or generation
	// mismatch). PRD §26; bound in E2, R1, A1.
	FindingGraphStale = "GRAPH_STALE"
	// FindingSnapshotMissing — no trust snapshot is published. PRD §26; bound
	// in E7, A2.
	FindingSnapshotMissing = "SNAPSHOT_MISSING"
	// FindingParseSkippedInScope — parse skips fall inside the assessed scope.
	// PRD §26; bound in E5, R3, A4.
	FindingParseSkippedInScope = "PARSE_SKIPPED_IN_SCOPE"
	// FindingPackageDegraded — a degraded (not type-checked) package touches
	// the assessed scope. PRD §26; bound in R4, A5.
	FindingPackageDegraded = "PACKAGE_DEGRADED"
	// FindingAmbiguousReferenceInScope — ambiguous references inside the
	// assessed scope. PRD §26; bound in E4, R5, A6.
	FindingAmbiguousReferenceInScope = "AMBIGUOUS_REFERENCE_IN_SCOPE"
	// FindingUnresolvedReferenceInScope — unresolved references inside the
	// assessed scope. PRD §26; bound in E4, A7.
	FindingUnresolvedReferenceInScope = "UNRESOLVED_REFERENCE_IN_SCOPE"
	// FindingHeuristicOnlyPath — a critical path rests on heuristic-tier
	// evidence only. PRD §26; bound in R6, A8.
	FindingHeuristicOnlyPath = "HEURISTIC_ONLY_PATH"
	// FindingExternalBoundaryReached — the scope touches an external boundary
	// node. PRD §26; bound in E6, R7, A9.
	FindingExternalBoundaryReached = "EXTERNAL_BOUNDARY_REACHED"
	// FindingTargetNotFound — the requested target resolves to nothing; never
	// treated as an empty healthy scope. PRD §26; bound in R2, A3.
	FindingTargetNotFound = "TARGET_NOT_FOUND"
	// FindingScopeEvidenceUnavailable — the evidence a scope assessment needs
	// does not exist (missing sidecar detail, unsupported scope kind). PRD
	// §26; bound in R8, A10.
	FindingScopeEvidenceUnavailable = "SCOPE_EVIDENCE_UNAVAILABLE"
	// FindingTargetAmbiguous — the requested target matches more than one
	// candidate and is never auto-picked. PRD §27 (adopted); bound in R2, A3.
	FindingTargetAmbiguous = "TARGET_AMBIGUOUS"
	// FindingGraphUnavailable — no graph exists to assess. Minted in v1;
	// bound in E1, R1, A1.
	FindingGraphUnavailable = "GRAPH_UNAVAILABLE"
	// FindingSnapshotStale — a snapshot exists but describes another
	// generation. Minted in v1 (sibling of SNAPSHOT_MISSING); bound in A2.
	FindingSnapshotStale = "SNAPSHOT_STALE"
	// FindingHeuristicEdgesPresent — heuristic-tier edges exist in scope;
	// info-severity visibility so a PASS is never findings-free without an
	// explicit all-checks-passed list. Minted in v1; bound in E3.
	FindingHeuristicEdgesPresent = "HEURISTIC_EDGES_PRESENT"
)

// Evidence dimensions a finding belongs to (PRD §26 Dimension). Working
// vocabulary of this layer, one value per registry default below.
const (
	// DimensionFreshness — graph existence and currency.
	DimensionFreshness = "freshness"
	// DimensionSnapshot — trust-snapshot existence and binding.
	DimensionSnapshot = "snapshot"
	// DimensionCoverage — parse/type-check completeness.
	DimensionCoverage = "coverage"
	// DimensionResolution — reference resolution quality.
	DimensionResolution = "resolution"
	// DimensionEvidence — confidence-tier strength of the evidence itself.
	DimensionEvidence = "evidence"
	// DimensionBoundary — external coverage boundaries.
	DimensionBoundary = "boundary"
	// DimensionTarget — target/scope resolvability.
	DimensionTarget = "target"
)

// ErrUnknownFindingCode is the typed sentinel wrapped by NewFinding when the
// code is not a member of the closed v1 registry.
var ErrUnknownFindingCode = errors.New("trust: unknown finding code")

// findingDefault carries the per-code defaults NewFinding fills: the severity
// the code fires at absent a policy override, the evidence dimension it
// belongs to, and the fixed next-step action string the recommendation builder
// derives from (PRD §26 acceptance: findings carry an action or
// recommendation; PRD §29: recommendations are fixed strings derived from
// finding codes, never assembled free text).
type findingDefault struct {
	Severity  string
	Dimension string
	Action    string
}

// findingDefaults is the closed registry table. Membership here IS the
// registry: NewFinding rejects any code without a row.
var findingDefaults = map[string]findingDefault{
	FindingGraphUnavailable:           {SeverityError, DimensionFreshness, RecommendRebuild},
	FindingGraphStale:                 {SeverityWarning, DimensionFreshness, RecommendSync},
	FindingSnapshotMissing:            {SeverityWarning, DimensionSnapshot, RecommendRebuild},
	FindingSnapshotStale:              {SeverityWarning, DimensionSnapshot, RecommendSync},
	FindingParseSkippedInScope:        {SeverityWarning, DimensionCoverage, RecommendReviewSkippedFiles},
	FindingPackageDegraded:            {SeverityWarning, DimensionCoverage, RecommendInspectDegradedPackages},
	FindingAmbiguousReferenceInScope:  {SeverityWarning, DimensionResolution, RecommendScopedTarget},
	FindingUnresolvedReferenceInScope: {SeverityWarning, DimensionResolution, RecommendSupplementEvidence},
	FindingHeuristicOnlyPath:          {SeverityWarning, DimensionEvidence, RecommendSupplementEvidence},
	FindingHeuristicEdgesPresent:      {SeverityInfo, DimensionEvidence, RecommendScopedTarget},
	FindingExternalBoundaryReached:    {SeverityInfo, DimensionBoundary, RecommendHumanReviewBoundary},
	FindingTargetNotFound:             {SeverityError, DimensionTarget, RecommendVerifyTarget},
	FindingTargetAmbiguous:            {SeverityError, DimensionTarget, RecommendDisambiguateTarget},
	FindingScopeEvidenceUnavailable:   {SeverityWarning, DimensionEvidence, RecommendNoAutomatedChange},
}

// NewFinding constructs a Finding for a registry code, filling the per-code
// default severity and dimension. A code outside the closed v1 registry is
// rejected with ErrUnknownFindingCode — the same construction-gate discipline
// model.NewEdge applies to its closed tier enum. Policies that fire a code at
// a non-default severity (e.g. E6's INFO/WARN escalation) adjust the returned
// value's Severity within the closed severity set.
func NewFinding(code string, scope ScopeRef, observed, threshold, message string) (Finding, error) {
	d, ok := findingDefaults[code]
	if !ok {
		return Finding{}, fmt.Errorf("%w: %q is not in the closed v1 registry (contract doc §3.4)", ErrUnknownFindingCode, code)
	}
	return Finding{
		Code:      code,
		Severity:  d.Severity,
		Dimension: d.Dimension,
		Scope:     scope,
		Observed:  observed,
		Threshold: threshold,
		Message:   message,
	}, nil
}
