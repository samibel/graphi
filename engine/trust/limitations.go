package trust

// This file builds the Limitation list an assessment attaches: the counted
// coverage limits the snapshot facts document (PRD §11.2) plus the standing
// structural boundary limits every assessment carries (PRD §23 FR-9, contract
// doc §2.5). Limitation codes are wire vocabulary, not the closed finding
// registry — adding one is additive, removing or renaming one is breaking
// (contract doc §2.5).

// Limitation codes emitted by the snapshot-derived builder.
const (
	// LimitationParseSkipped — files the parser skipped are absent from the
	// graph (counted from ParseFacts).
	LimitationParseSkipped = "PARSE_SKIPPED"
	// LimitationTypecheckDegraded — packages without type-check evidence
	// (counted from TypeResolutionFacts; the contract doc §2.2 example code).
	LimitationTypecheckDegraded = "TYPECHECK_DEGRADED"
	// LimitationAmbiguousReferences — references the linker could not resolve
	// to a single candidate (counted from LinkFacts).
	LimitationAmbiguousReferences = "AMBIGUOUS_REFERENCES"
	// LimitationExternalNotNavigable — edges terminate at external boundary
	// nodes; structural queries do not navigate into them (PRD §23).
	LimitationExternalNotNavigable = "EXTERNAL_NOT_NAVIGABLE"
	// LimitationCrossRepositoryUnavailable — standing: cross-repository
	// coverage does not exist (PRD §23).
	LimitationCrossRepositoryUnavailable = "CROSS_REPOSITORY_UNAVAILABLE"
	// LimitationDependencyInternalsUnknown — standing: nothing is claimed
	// about library internals (PRD §23).
	LimitationDependencyInternalsUnknown = "DEPENDENCY_INTERNALS_UNKNOWN"
	// LimitationDynamicRuntimeUnknown — dynamic runtime behavior is outside
	// structural evidence. Part of the §2.5 minimum vocabulary; declared for
	// completeness, not emitted by this builder (no fact backs a count yet).
	LimitationDynamicRuntimeUnknown = "DYNAMIC_RUNTIME_UNKNOWN"
)

// Fixed action strings for the standing boundary limitations. Like the
// recommendation constants these are the only prose this layer mints.
const (
	// ActionCrossRepository — the fixed action for CROSS_REPOSITORY_UNAVAILABLE.
	ActionCrossRepository = "treat cross-repository references as out of scope"
	// ActionDependencyInternals — the fixed action for DEPENDENCY_INTERNALS_UNKNOWN.
	ActionDependencyInternals = "do not assume dependency internals"
)

// LimitationsFromSnapshot derives the bounded, deterministic limitation list
// from one snapshot's facts. Counted limitations appear only when their count
// is positive: parse skips, degraded type units, ambiguous references, and the
// external-boundary edge count ("external" is a boundary, never called
// "unresolved" — PRD §23 acceptance). The two standing boundary limitations
// are always present with count 0: they hold for every graph regardless of
// facts (contract doc §2.5, PRD §23). The result is canonically sorted
// (severity rank, then code — the same order EncodeAssessment enforces), never
// nil, and bounded by construction at six entries.
func LimitationsFromSnapshot(s Snapshot) []Limitation {
	out := []Limitation{}
	if s.Parse.Skipped > 0 {
		out = append(out, Limitation{
			Code: LimitationParseSkipped, Severity: SeverityWarning,
			Count: s.Parse.Skipped, Action: RecommendReviewSkippedFiles,
		})
	}
	if s.TypeResolution.UnitsDegraded > 0 {
		out = append(out, Limitation{
			Code: LimitationTypecheckDegraded, Severity: SeverityWarning,
			Count: s.TypeResolution.UnitsDegraded, Action: RecommendInspectDegradedPackages,
		})
	}
	if s.Link.Ambiguous > 0 {
		out = append(out, Limitation{
			Code: LimitationAmbiguousReferences, Severity: SeverityWarning,
			Count: s.Link.Ambiguous, Action: RecommendScopedTarget,
		})
	}
	if s.External.Edges > 0 {
		out = append(out, Limitation{
			Code: LimitationExternalNotNavigable, Severity: SeverityInfo,
			Count: s.External.Edges, Action: RecommendHumanReviewBoundary,
		})
	}
	out = append(out,
		Limitation{Code: LimitationCrossRepositoryUnavailable, Severity: SeverityInfo, Action: ActionCrossRepository},
		Limitation{Code: LimitationDependencyInternalsUnknown, Severity: SeverityInfo, Action: ActionDependencyInternals},
	)
	sortLimitations(out)
	return out
}
