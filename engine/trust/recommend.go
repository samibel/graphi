package trust

// This file is the deterministic recommendation builder (PRD §29 FR-15):
// recommendations are derived ONLY from finding codes and the snapshot state,
// through the fixed-string tables below — no free-text assembly, no LLM, no
// automatic destructive action. The output is bounded by construction:
// deduplication caps it at the number of distinct fixed strings.

// Canonical recommendation strings. Fixed and table-referenced only — a
// recommendation either is one of these constants or does not exist. The
// contract's canonical ones (PRD §29 examples) are all present: sync, rebuild,
// scoped --target, refusing the automated_change policy, and human review at
// an external boundary.
const (
	// RecommendSync — the graph or snapshot trails the source; re-sync.
	RecommendSync = "run 'graphi sync'"
	// RecommendRebuild — no usable graph/snapshot exists; rebuild from scratch.
	RecommendRebuild = "run 'graphi rebuild'"
	// RecommendScopedTarget — repository-wide noise; narrow the assessment.
	RecommendScopedTarget = "use --target for a scoped assessment"
	// RecommendNoAutomatedChange — evidence is insufficient for autonomous
	// changes (contract doc §3.3 A10: missing evidence never PASS).
	RecommendNoAutomatedChange = "do not use the automated_change policy"
	// RecommendHumanReviewBoundary — the scope touches an external boundary
	// (contract doc §3.3 A9: FAIL or explicit human approval).
	RecommendHumanReviewBoundary = "human review required at external boundary"
	// RecommendReviewSkippedFiles — parse skips leave unindexed source.
	RecommendReviewSkippedFiles = "review skipped files"
	// RecommendInspectDegradedPackages — degraded packages carry no confirmed
	// evidence (the contract doc §2.2 example limitation action).
	RecommendInspectDegradedPackages = "inspect degraded packages"
	// RecommendSupplementEvidence — heuristic-only or unresolved evidence
	// needs a stronger source (PRD §29 "supplement with compiler or tests").
	RecommendSupplementEvidence = "supplement with compiler or tests"
	// RecommendVerifyTarget — the target resolved to nothing.
	RecommendVerifyTarget = "verify the target spelling or run 'graphi sync'"
	// RecommendDisambiguateTarget — the target matched several candidates and
	// is never auto-picked (PRD §27).
	RecommendDisambiguateTarget = "disambiguate the target with a fully qualified name"
)

// stateRecommendations maps a non-CURRENT snapshot state to its fixed
// next-step. STALE re-syncs; INCOMPLETE and UNAVAILABLE need a full pass —
// the recommendation prose itself stays aligned with the status surface
// because both reduce to the same two commands (ADR 0006 D1: trust mints no
// freshness prose of its own beyond these fixed strings).
var stateRecommendations = map[State]string{
	StateStale:       RecommendSync,
	StateIncomplete:  RecommendRebuild,
	StateUnavailable: RecommendRebuild,
}

// Recommendations derives the deterministic next-step list for an assessment:
// first the snapshot state's fixed recommendation (the leading fact), then one
// per finding via the registry's per-code default action, in the finding order
// given — callers pass findings already in canonical order (SortFindings), so
// the recommendation order follows the finding order that produced it.
// Duplicates collapse to their first occurrence; codes outside the registry
// contribute nothing. The result is never nil.
func Recommendations(state State, findings []Finding) []string {
	out := make([]string, 0, len(findings)+1)
	seen := make(map[string]struct{}, len(findings)+1)
	add := func(r string) {
		if _, dup := seen[r]; dup || r == "" {
			return
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	if r, ok := stateRecommendations[state]; ok {
		add(r)
	}
	for _, f := range findings {
		if d, ok := findingDefaults[f.Code]; ok {
			add(d.Action)
		}
	}
	return out
}
