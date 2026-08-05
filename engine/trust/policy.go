package trust

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/samibel/graphi/core/model"
)

// This file is the policy specification v1 (contract doc §3, PRD §25): the
// three built-in policies exploratory-v1, review-v1, automated-change-v1 as
// static, versioned rule sets, and the pure evaluator that turns
// Facts + Scope + Policy into a Verdict (contract doc §3.0). Everything the
// contract prohibits stays impossible by construction: rules are Go code in
// closed tables — no user-supplied expression, no dynamic scripting, no LLM
// verdict, and any rule or threshold change is a Version bump on the policy it
// touches (§3.0 versioning rule). Every rule is bound to a closed-registry
// finding code (the §3.1–§3.3 tables), every fired rule appends its Finding,
// and the verdict is the worst outcome across fired rules.
//
// Decisions of record where contract v1 is genuinely silent, all resolved in
// the strictly-safer direction:
//
//   - Verdict combining order. The contract closes the verdict set (§1.5) but
//     never orders it. Combining uses PASS < WARN < UNKNOWN < FAIL: UNKNOWN
//     outranks WARN because a judged degradation must never mask missing
//     evidence, and FAIL outranks UNKNOWN so a definite violation is never
//     masked by an indefinite one — §27 and A10 both list FAIL as the
//     stricter of the two blocking outcomes, and PRD §15's exit codes block
//     on both.
//   - UNAVAILABLE vs verdict. §1.6 UNAVAILABLE means no usable graph or
//     snapshot facts exist, so the state rules map it to UNKNOWN ("the facts
//     required for a judgment are missing"), never PASS and never a judged
//     FAIL — there are no facts to judge. E1's and R1's/A1's verdicts are
//     unstated in the tables; UNKNOWN follows §7.5/§1.8 (missing evidence maps
//     to UNKNOWN, never a positive signal) and E7's explicit UNKNOWN.
//   - INCOMPLETE vs verdict. No table row names INCOMPLETE. A running or
//     aborted ingest means the facts are unsettled — PRD §18 releases a
//     snapshot only after a successful ingest — so INCOMPLETE reads as
//     missing/unsettled evidence: SNAPSHOT_MISSING (E7/A2) and, for the
//     graph-currency rules, GRAPH_STALE observed INCOMPLETE (R1/A1 bind
//     GRAPH_STALE for every "not current" case), verdict UNKNOWN.
//   - STALE vs verdict. "Graph muss current sein" (R1/A1) is violated by a
//     definite fact, so review and automated-change map STALE to FAIL —
//     consistent with R3, which already FAILs review for the lesser gap of a
//     single parse skip. exploratory's E2 grades "WARN oder FAIL nach Drift",
//     but drift magnitude is a freshness fact the pure evaluator does not
//     receive (the snapshot carries no drift counts), so v1 fixes E2 at WARN —
//     inside the contract's stated range, never PASS — and grading by drift
//     is deferred to a policy version that receives drift facts.
//   - Scope evidence. Detail (non-repository) scope evidence is deferred in
//     v1 (contract doc §5), so every non-repository scope fires
//     SCOPE_EVIDENCE_UNAVAILABLE: verdict UNKNOWN for review (R8, explicit)
//     and automated-change (A10 allows FAIL or UNKNOWN; UNKNOWN is chosen
//     because absent evidence is absence, not a judged violation), WARN for
//     exploratory (no E-table row exists; WARN keeps the repository facts
//     usable for exploration while never reading PASS). The repository-scoped
//     evidence rules do not run against a non-repository scope — the snapshot
//     carries repository facts only, and claiming them as scope-local
//     evidence would be an unsupported claim (PRD §26).
//   - Target resolution. R2/A3 judge resolvability, but ambiguous and
//     not-found targets are indistinguishable from the ScopeRef alone (both
//     keep the asked kind with an empty ID — scope.go), so Evaluate accepts
//     the resolver's findings as its optional trailing input and adopts them
//     verbatim (a policy never alters or hides facts, §3.0). Verdicts: review
//     maps both target codes to UNKNOWN (§1.8: unresolvable scopes read
//     UNKNOWN); automated-change maps both to FAIL (§27 lets the policy pick
//     the stricter of UNKNOWN/FAIL; A3 is a hard precondition); exploratory
//     has no table row and follows §1.8's UNKNOWN.
//   - "rein heuristic kritischer Pfad" (R6/A8). v1 has no per-path analysis
//     (contract doc §5), so the repository-scope proxy is a graph whose edge
//     evidence is heuristic-tier only (no derived, no confirmed edges) — the
//     only reading the code HEURISTIC_ONLY_PATH supports without claiming
//     path knowledge that does not exist. Firing on any heuristic edge would
//     assert "heuristic only" over a mixed graph, an unsupported claim.
//   - "degraded → WARN oder FAIL nach Schwere" (R4). v1 defines no numeric
//     severity threshold (a silent threshold would violate §3.0), so the only
//     non-arbitrary grading is used: every unit degraded (total type-evidence
//     loss) is FAIL, partial degradation is WARN.
//   - "externe Boundaries erzeugen INFO/WARN" (E6). Ungraded in v1 for the
//     same no-silent-threshold reason: INFO, the visibility floor — external
//     boundaries exist for every real repository, and R7/A9 carry the strict
//     treatments. A9's "FAIL oder explizite Human Approval" has no approval
//     channel in v1, so an external boundary FAILs automated-change with the
//     human-review recommendation attached.
//   - Scope facts (input extension, NO policy version bump). The v1 rules
//     above already contain the contract §3 scope clauses (parse skip in
//     scope, degraded package in scope, ambiguity in scope, unresolved in
//     scope, external boundary in scope); v1 could not evaluate them for lack
//     of scope evidence and fell to SCOPE_EVIDENCE_UNAVAILABLE. The optional
//     ScopeFacts input (EvaluateWithScopeFacts, scopefacts.go) extends the
//     INPUT, not the rules: absent, every evaluation is byte-identical to the
//     factless call (the sealed matrix and every false-pass pin hold
//     unchanged, and the byte-identity red gate in policy_matrix_test.go
//     sweeps it); present and well-formed on a resolved non-repository scope,
//     the same rules judge the same codes at the same per-policy severities
//     against the scope's own evidence row, and the scope-evidence check
//     passes instead of firing. Because no rule and no threshold changed, the
//     policy versions stay at 1 (§3.0 bumps on rule/threshold changes, not on
//     input extensions). In-scope grading decisions, resolved strictly-safer:
//     an unparsed (skipped) file's zero resolution counters are absence of
//     analysis, not evidence of absence — the counter checks stay silent
//     there and the parse-skip finding explains the gap, while a nonzero
//     counter on such a row still fires (the row witnesses the defect); a
//     package claim of degraded or skipped carries no authoritative type
//     evidence — R4's "nach Schwere" grading at single-package granularity
//     mirrors the repository grading with the row's own facts (partial
//     confirmed coverage remains → WARN, total loss → FAIL; A5 stays strict
//     FAIL) — and an absent package claim ("", the v1 file-evidence shape)
//     lets the degraded check neither pass nor fire, since file evidence
//     alone cannot support a "no degraded package" claim; facts at repository
//     scope are ignored entirely (the snapshot IS that scope's evidence); and
//     malformed facts (Available without the closed vocabularies) are no
//     evidence and read exactly like absent facts.
//   - Out-of-domain inputs. The enums are closed (§1.5–§1.7), but Evaluate is
//     a pure exported function and cannot stop a caller from handing it a
//     State or ScopeRef.Kind outside them, a scope shape the resolver only
//     produces together with fail-closed findings (empty-ID symbol/package)
//     without those findings, or a snapshot whose count fields contradict
//     their own bounded samples. None of that is evidence, so all of it fails
//     closed: an out-of-set state reads like UNAVAILABLE (UNKNOWN, explained
//     by the state rules); a visibly unresolved or v1-unsupported scope with
//     no resolver findings reads UNKNOWN (§1.8, explained by the scope-
//     evidence rule); and the parse/degraded rules judge the strongest signal
//     their fact section carries (count, sample, per-reason tally), so a
//     zeroed count never launders a defect the snapshot itself documents.
//     These are fail-closed floors under inputs outside the contract's valid
//     domain, not rule changes within it — the sealed matrix is untouched and
//     no version bumps.
//
// PRD §26's explainability rule ("kein Verdict ohne Findings oder explizite
// „all checks passed“-Liste") is structural: every check that ran and held
// lands in Assessment.ChecksPassed, so a findings-free PASS always carries the
// explicit list.

// Policy names — the unversioned name component of the wire `policy.name`
// field. The token a surface accepts is the *versioned* identifier built from
// these by Policy.ID (contract doc §2.1 as amended; PRD v1.0 §6).
//
// PolicyNameAutomatedChange changed spelling with PRD v1.0: v0.8.0 shipped the
// snake_case "automated_change", but the identifier PRD v1.0 §6 fixes is
// "automated-change-v1", and the identifier is derived rather than stored.
// Kebab-case here is what makes the derivation produce the specified token
// (delta doc §A2).
const (
	// PolicyNameExploratory names exploratory-v1.
	PolicyNameExploratory = "exploratory"
	// PolicyNameReview names review-v1.
	PolicyNameReview = "review"
	// PolicyNameAutomatedChange names automated-change-v1.
	PolicyNameAutomatedChange = "automated-change"
)

// Canonical versioned policy identifiers (PRD v1.0 §6) — the tokens the
// surfaces accept and the value of the wire `policy.id` field.
//
// Each constant names ONE version of one policy. A rules or threshold change
// bumps that policy's Version (contract doc §3.0) and therefore mints a *new*
// identifier and a new constant beside these; it never re-points an existing
// one, because callers pinned to `review-v1` asked for those rules, not for
// whatever review happens to mean later.
//
// These are declared for readability at call sites; Policy.ID derives the same
// string from Name and Version, and `prdv1_wire_test.go` pins the two forms
// equal so a version bump that forgets a constant fails loudly instead of
// advertising a token the resolver rejects.
const (
	PolicyIDExploratory     = PolicyNameExploratory + "-v1"
	PolicyIDReview          = PolicyNameReview + "-v1"
	PolicyIDAutomatedChange = PolicyNameAutomatedChange + "-v1"
)

// ErrPolicyUnknown is the typed sentinel PolicyByName wraps for a name outside
// the built-in registry (the package face of the contract doc §4 class
// ErrTrustPolicyUnknown).
var ErrPolicyUnknown = errors.New("trust: unknown trust policy")

// Policy is one versioned built-in rule set (PRD §11.4): a name, the version
// that any rule or threshold change bumps (contract doc §3.0), and the static
// rules themselves. The rules are unexported: the three constructors below and
// the Policies/PolicyByName registry are the only sources of a usable Policy,
// so a rule set outside contract v1 is not constructible through the package
// API.
type Policy struct {
	Name    string
	Version int
	rules   []policyRule
}

// ruleInput is the fact set one evaluation hands every rule: the snapshot
// facts, the derived snapshot state, the scope under assessment, the resolver
// findings for that scope (empty when the scope resolved cleanly), and the
// optional target-scope evidence (zero when the caller has none — scopefacts.go).
type ruleInput struct {
	snap       Snapshot
	st         State
	scope      ScopeRef
	resolution []Finding
	scopeFacts ScopeFacts
}

// ruleResult is one rule's outcome. Exactly one of three shapes:
// passed=true — the check ran and held (one ChecksPassed entry);
// passed=false with findings — the rule fired, findings explain it and
// verdict is its contribution; passed=false without findings — the check
// could not run (missing evidence) or its finding is already adopted from the
// resolver: it claims neither pass nor failure, and any verdict it carries
// still combines (the finding that explains the gap lives in another rule's
// output).
type ruleResult struct {
	passed   bool
	findings []Finding
	verdict  Verdict
}

// policyRule is one static rule: the contract §3 table row it implements, the
// fixed passed-check gloss for the all-checks-passed list, and the pure check.
type policyRule struct {
	id    string
	check string
	eval  func(in ruleInput) ruleResult
}

// verdictRank orders verdicts for worst-of combining: PASS < WARN < UNKNOWN <
// FAIL (decision of record in the file comment).
func verdictRank(v Verdict) int {
	switch v {
	case VerdictFail:
		return 3
	case VerdictUnverified:
		return 2
	case VerdictWarn:
		return 1
	default:
		return 0
	}
}

// worseVerdict returns the worse of two verdicts under verdictRank.
func worseVerdict(a, b Verdict) Verdict {
	if verdictRank(b) > verdictRank(a) {
		return b
	}
	return a
}

// policyFinding builds a Finding for a registry code and adjusts its severity
// within the closed set when the rule fires it at a non-default level
// (severity "" keeps the registry default). Codes here are compile-time
// registry constants, so NewFinding cannot fail; a failure is a programming
// error and panics rather than silently dropping an explanation.
func policyFinding(code, severity string, scope ScopeRef, observed, threshold, message string) Finding {
	f, err := NewFinding(code, scope, observed, threshold, message)
	if err != nil {
		panic(fmt.Sprintf("trust: policy rule fired non-registry code: %v", err))
	}
	if severity != "" {
		f.Severity = severity
	}
	return f
}

// passed and skipped are the two finding-free rule outcomes.
func passed() ruleResult  { return ruleResult{passed: true} }
func skipped() ruleResult { return ruleResult{} }

func fired(v Verdict, fs ...Finding) ruleResult {
	return ruleResult{findings: fs, verdict: v}
}

// stateKnown reports membership in the closed §1.6 snapshot-state set. Any
// other string — including the zero State of a caller that never derived one
// — is not evidence: the state-gated rules fail closed on it and never pass
// (contract doc §3.0 fail closed; the closed-set discipline of §1.6).
func stateKnown(st State) bool {
	switch st {
	case StateCurrent, StateStale, StateIncomplete, StateUnavailable:
		return true
	}
	return false
}

// scopeResolved reports whether a ScopeRef is a shape the v1 resolver
// produces WITHOUT attaching findings (scope.go): the repository default, a
// file scope carrying its path, or a symbol scope carrying its node ID.
// Package scopes (resolution deferred in v1), result-set scopes (semantics
// left open), empty-ID symbol shapes (the not-found/ambiguous outcomes), and
// any kind outside the closed §1.7 set are never resolved in v1.
func scopeResolved(s ScopeRef) bool {
	switch s.Kind {
	case ScopeRepository:
		return true
	case ScopeFile:
		return s.Path != ""
	case ScopeSymbol:
		return s.ID != ""
	case ScopePackage:
		// Resolved only when ResolveScope CONFIRMED the key against real
		// package evidence; the deferred v1 shape leaves ID empty and stays
		// unresolved, exactly as before.
		return s.ID != ""
	default:
		return false
	}
}

// heuristicOnly reports a graph whose edge evidence is heuristic-tier only —
// the v1 repository-scope proxy for R6/A8 (see the file comment).
func heuristicOnly(s Snapshot) bool {
	t := s.Graph.EdgesByTier
	return t.Confirmed == 0 && t.Derived == 0 && t.Heuristic > 0
}

// parseSkipEvidence returns the strongest parse-skip signal the fact section
// carries: the count, the bounded path sample, and the per-reason tallies all
// witness skips, and a count that contradicts its own sample never reads
// clean (fail closed over inconsistent facts; PRD §26 — "no parse skips" over
// a snapshot listing a skipped path would be an unsupported claim).
func parseSkipEvidence(p ParseFacts) int {
	n := p.Skipped
	if len(p.Paths) > n {
		n = len(p.Paths)
	}
	byReason := 0
	for _, r := range p.ByReason {
		if r.Count > 0 {
			byReason += r.Count
		}
	}
	if byReason > n {
		n = byReason
	}
	return n
}

// degradedEvidence returns the strongest degraded-unit signal the fact
// section carries: the count or the bounded unit sample, whichever witnesses
// more (fail closed over inconsistent facts, same rationale as
// parseSkipEvidence).
func degradedEvidence(tr TypeResolutionFacts) int {
	n := tr.UnitsDegraded
	if len(tr.DegradedUnits) > n {
		n = len(tr.DegradedUnits)
	}
	return n
}

// resolutionHas reports whether the resolver findings carry the given code.
func resolutionHas(resolution []Finding, code string) bool {
	for _, f := range resolution {
		if f.Code == code {
			return true
		}
	}
	return false
}

// Shared rule constructors. Each implements one contract table row (or one
// documented v1 gap) for the policies that bind it; per-policy severity and
// verdict parameters are the tables' stated outcomes. Severity convention:
// a FAIL-mapping rule fires at error, a WARN-mapping rule at warning, a
// visibility rule at info; UNKNOWN-mapping rules keep the registry default —
// absence of evidence is not a judged violation.

// ruleGraphAvailable — E1 "Graph muss vorhanden sein". UNAVAILABLE means no
// usable graph evidence exists (§1.6); verdict UNKNOWN (file comment). A state
// outside the closed §1.6 set is equally not evidence of a graph and fails
// closed the same way.
func ruleGraphAvailable(id string) policyRule {
	return policyRule{id: id, check: "graph available", eval: func(in ruleInput) ruleResult {
		switch {
		case in.st == StateUnavailable:
			return fired(VerdictUnverified, policyFinding(FindingGraphUnavailable, "", in.scope,
				string(in.st), string(StateCurrent),
				"no usable graph or trust snapshot evidence: snapshot state is UNAVAILABLE"))
		case !stateKnown(in.st):
			return fired(VerdictUnverified, policyFinding(FindingGraphUnavailable, "", in.scope,
				string(in.st), string(StateCurrent),
				"snapshot state is outside the closed set; no usable evidence (fail closed)"))
		default:
			return passed()
		}
	}}
}

// ruleGraphNotStale — E2 "stale Graph → WARN oder FAIL nach Drift". Drift
// magnitude is not an input of the pure evaluator, so v1 fixes the outcome at
// WARN (file comment). UNAVAILABLE/INCOMPLETE are not this rule's states —
// E1/E7 explain them — so it neither passes nor fires there.
func ruleGraphNotStale(id string) policyRule {
	return policyRule{id: id, check: "graph current", eval: func(in ruleInput) ruleResult {
		switch in.st {
		case StateStale:
			return fired(VerdictWarn, policyFinding(FindingGraphStale, SeverityWarning, in.scope,
				string(in.st), string(StateCurrent),
				"the graph is not current: snapshot state is STALE"))
		case StateCurrent:
			return passed()
		default:
			return skipped()
		}
	}}
}

// ruleGraphCurrent — R1/A1 "Graph muss current sein". STALE is a definite
// violation (FAIL, severity error); INCOMPLETE is unsettled evidence
// (GRAPH_STALE observed INCOMPLETE, verdict UNKNOWN); UNAVAILABLE is absent
// evidence (GRAPH_UNAVAILABLE, verdict UNKNOWN); a state outside the closed
// §1.6 set is equally absent evidence and never reads current. See the file
// comment.
func ruleGraphCurrent(id string) policyRule {
	return policyRule{id: id, check: "graph current", eval: func(in ruleInput) ruleResult {
		switch in.st {
		case StateCurrent:
			return passed()
		case StateUnavailable:
			return fired(VerdictUnverified, policyFinding(FindingGraphUnavailable, "", in.scope,
				string(in.st), string(StateCurrent),
				"no usable graph or trust snapshot evidence: snapshot state is UNAVAILABLE"))
		case StateIncomplete:
			return fired(VerdictUnverified, policyFinding(FindingGraphStale, "", in.scope,
				string(in.st), string(StateCurrent),
				"the graph is not settled: snapshot state is INCOMPLETE (running or aborted ingest)"))
		case StateStale:
			return fired(VerdictFail, policyFinding(FindingGraphStale, SeverityError, in.scope,
				string(in.st), string(StateCurrent),
				"the graph is not current: snapshot state is STALE"))
		default:
			return fired(VerdictUnverified, policyFinding(FindingGraphUnavailable, "", in.scope,
				string(in.st), string(StateCurrent),
				"snapshot state is outside the closed set; no usable evidence (fail closed)"))
		}
	}}
}

// ruleSnapshotPublished — E7 "fehlender Snapshot → UNKNOWN". Under
// UNAVAILABLE no usable snapshot exists; under INCOMPLETE none is released
// (PRD §18: a snapshot is released only after a successful ingest). Under
// CURRENT and STALE a published snapshot exists — the currency rules judge
// the stale one — so the check passes. A state outside the closed §1.6 set
// cannot establish that a snapshot was released: fail closed.
func ruleSnapshotPublished(id string) policyRule {
	return policyRule{id: id, check: "trust snapshot published", eval: func(in ruleInput) ruleResult {
		switch in.st {
		case StateCurrent, StateStale:
			return passed()
		case StateUnavailable:
			return fired(VerdictUnverified, policyFinding(FindingSnapshotMissing, "", in.scope,
				string(in.st), string(StateCurrent),
				"no usable trust snapshot: snapshot state is UNAVAILABLE"))
		case StateIncomplete:
			return fired(VerdictUnverified, policyFinding(FindingSnapshotMissing, "", in.scope,
				string(in.st), string(StateCurrent),
				"no released trust snapshot: snapshot state is INCOMPLETE (a snapshot is released only after a successful ingest)"))
		default:
			return fired(VerdictUnverified, policyFinding(FindingSnapshotMissing, "", in.scope,
				string(in.st), string(StateCurrent),
				"snapshot state is outside the closed set; cannot establish a released trust snapshot (fail closed)"))
		}
	}}
}

// ruleSnapshotCurrent — A2 "Snapshot current". Adds to ruleSnapshotPublished
// the STALE case: a snapshot that cannot be considered current is
// SNAPSHOT_STALE and FAILs an automated change (severity error).
func ruleSnapshotCurrent(id string) policyRule {
	base := ruleSnapshotPublished(id)
	return policyRule{id: id, check: "trust snapshot current", eval: func(in ruleInput) ruleResult {
		if in.st == StateStale {
			return fired(VerdictFail, policyFinding(FindingSnapshotStale, SeverityError, in.scope,
				string(in.st), string(StateCurrent),
				"the trust snapshot cannot be considered current: snapshot state is STALE"))
		}
		return base.eval(in)
	}}
}

// ruleTargetResolved — R2 "Target muss auflösbar sein", A3 "Target
// eindeutig", and the exploratory §1.8 fallback. The resolver's findings are
// adopted verbatim (facts are never altered or hidden, §3.0); the verdict is
// the worst mapped outcome — notFound/ambiguous per the policy's table,
// evidence for a SCOPE_EVIDENCE_UNAVAILABLE the resolver attached, and
// evidence again for any code without a mapping (fail closed: an unresolved
// oddity never improves the outcome).
func ruleTargetResolved(id string, notFound, ambiguous, evidence Verdict) policyRule {
	return policyRule{id: id, check: "target resolved", eval: func(in ruleInput) ruleResult {
		if len(in.resolution) == 0 {
			if scopeResolved(in.scope) {
				return passed()
			}
			// The scope is visibly unresolved or unsupported — an empty-ID
			// symbol/package shape the resolver only produces together with
			// fail-closed findings, the v1-deferred result-set kind, a
			// path-less file scope, or a kind outside the closed §1.7 set —
			// yet no resolver findings arrived. The evidence this check needs
			// is missing, so it can neither pass nor claim a specific target
			// code: verdict UNKNOWN (§1.8), findings-free — the scope-evidence
			// rule fires the explaining SCOPE_EVIDENCE_UNAVAILABLE for every
			// non-repository scope.
			return ruleResult{verdict: VerdictUnverified}
		}
		adopted := make([]Finding, len(in.resolution))
		copy(adopted, in.resolution)
		v := VerdictPass
		for _, f := range adopted {
			switch f.Code {
			case FindingTargetNotFound:
				v = worseVerdict(v, notFound)
			case FindingTargetAmbiguous:
				v = worseVerdict(v, ambiguous)
			default:
				v = worseVerdict(v, evidence)
			}
		}
		return fired(v, adopted...)
	}}
}

// ruleScopeEvidence — R8 "fehlende Scope-Evidenz → UNKNOWN", A10's
// scope-evidence face, and the exploratory WARN gap (file comment). A
// non-repository scope without usable scope facts fires; with usable facts
// (the wiring layer read the scope's evidence row under the snapshot
// generation) the check holds and the scope clauses of the evidence rules
// judge the row. When the resolver already attached
// SCOPE_EVIDENCE_UNAVAILABLE (package-looking target), its more specific
// finding — adopted by the target rule — stands as the explanation and this
// rule only contributes the verdict. At repository scope the snapshot IS the
// scope evidence: the check passes when the state gives a usable snapshot and
// stays silent under UNAVAILABLE, where the state rules explain the absence.
func ruleScopeEvidence(id string, verdict Verdict) policyRule {
	return policyRule{id: id, check: "scope evidence available", eval: func(in ruleInput) ruleResult {
		if in.scope.Kind == ScopeRepository {
			// At repository scope the snapshot IS the scope evidence: pass
			// only when the state says a usable snapshot backs it; stay
			// silent under UNAVAILABLE and out-of-set states, where the state
			// rules explain the absence.
			if repoFactsUsable(in) {
				return passed()
			}
			return skipped()
		}
		if scopeFactsUsable(in) {
			return passed()
		}
		if resolutionHas(in.resolution, FindingScopeEvidenceUnavailable) {
			return ruleResult{verdict: verdict}
		}
		return fired(verdict, policyFinding(FindingScopeEvidenceUnavailable, "", in.scope,
			in.scope.Kind, ScopeRepository,
			"scope-level evidence is not collected in v1; only repository-scope facts exist"))
	}}
}

// scopeFactsUsable gates the scope clauses of the evidence rules: they judge
// the target-scope facts only when the assessed scope is a resolved
// non-repository scope (the facts describe the target's own evidence row,
// never the repository — repoFactsUsable covers that side) and the facts are
// well-formed (scopefacts.go). At repository scope any handed-in facts are
// ignored entirely: the snapshot IS that scope's evidence. Malformed or
// absent facts fail closed to the unchanged v1 behavior — the scope clauses
// stay silent and ruleScopeEvidence fires SCOPE_EVIDENCE_UNAVAILABLE.
func scopeFactsUsable(in ruleInput) bool {
	return in.scope.Kind != ScopeRepository && scopeResolved(in.scope) && in.scopeFacts.wellFormed()
}

// scopeCounterOutcome judges one per-file resolution counter of the scope
// facts. A nonzero counter fires (the row witnesses the defect — even on an
// inconsistent skipped-file row, fail closed over inconsistent facts, same
// rationale as parseSkipEvidence). A zero counter passes only for a PARSED
// file: an unparsed file's references were never analyzed, so its zero
// counters are absence of analysis, not evidence of absence — the check stays
// silent and the parse-skip finding explains the gap (the "checks that could
// not run" convention of the file comment).
func scopeCounterOutcome(in ruleInput, n int, verdict Verdict, mk func(n int) Finding) ruleResult {
	if n > 0 {
		return fired(verdict, mk(n))
	}
	if in.scopeFacts.File.ParseStatus != ScopeParseStatusParsed {
		return skipped()
	}
	return passed()
}

// scopeDegradedOutcome judges the scope facts' package claim for the degraded
// rules. An absent claim ("" — the v1 file-evidence shape, package scope
// deferred) can neither pass nor fire: file evidence alone cannot support a
// "no degraded package in scope" claim (PRD §26), and the clean scope may
// still PASS on the checks that did run. checked/checked_with_errors pass —
// the PRD §22 pin: type errors alone are never degradation. degraded and
// skipped (never attempted) carry no authoritative type evidence; graded=true
// applies R4's "nach Schwere" at single-package granularity mirroring the
// repository grading with the row's own facts — partial confirmed coverage
// remains (PRD §22's fourth state) → WARN, total type-evidence loss → FAIL —
// while graded=false is A5's strict FAIL.
func scopeDegradedOutcome(in ruleInput, graded bool) ruleResult {
	p := in.scopeFacts.Package
	switch p.State {
	case "":
		return skipped()
	case ScopePackageStateChecked, ScopePackageStateCheckedWithErrors:
		return passed()
	}
	observed := p.State
	if p.DegradedReason != "" {
		observed += "(" + p.DegradedReason + ")"
	}
	if graded && p.ConfirmedEdges > 0 {
		return fired(VerdictWarn, policyFinding(FindingPackageDegraded, SeverityWarning, in.scope,
			observed, ScopePackageStateChecked,
			"the package owning the assessed scope lacks full type-check evidence; partial confirmed coverage remains"))
	}
	return fired(VerdictFail, policyFinding(FindingPackageDegraded, SeverityError, in.scope,
		observed, ScopePackageStateChecked,
		"the package owning the assessed scope carries no authoritative type-check evidence"))
}

// repoFactsUsable gates the repository-scoped evidence rules: they run only
// against the repository scope (the snapshot carries repository facts only —
// claiming them for a narrower scope would be unsupported) and only when the
// state is a closed-set member other than UNAVAILABLE (an UNAVAILABLE state
// has no snapshot facts, and a state outside the closed set vouches for
// nothing; zero counts under either would be fabricated evidence). Skipped
// checks are explained by the scope-evidence or state findings, never listed
// as passed.
func repoFactsUsable(in ruleInput) bool {
	return in.scope.Kind == ScopeRepository && stateKnown(in.st) && in.st != StateUnavailable
}

// ruleParseSkips — E5 "Parse Skips erzeugen WARN", R3 "Parse Skip im Target
// Scope → FAIL", A4 "kein Parse Skip im Scope". With usable scope facts the
// clause judges the target file's own row (its parse status and recorded
// reason); at repository scope it judges the snapshot's strongest skip signal.
func ruleParseSkips(id, severity string, verdict Verdict) policyRule {
	return policyRule{id: id, check: "no parse skips in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			f := in.scopeFacts.File
			if f.ParseStatus == ScopeParseStatusSkipped {
				observed := f.ParseStatus
				if f.ParseReason != "" {
					observed += "(" + f.ParseReason + ")"
				}
				return fired(verdict, policyFinding(FindingParseSkippedInScope, severity, in.scope,
					observed, ScopeParseStatusParsed,
					"the file in the assessed scope was skipped during parsing and is absent from the evidence"))
			}
			if f.ParseStatus == ScopeParseStatusParsed {
				return passed()
			}
			// No file claim — a package scope, whose evidence is its own row.
			// The row counts the files the parser skipped inside the package,
			// so the SAME clause is answerable at package granularity: a
			// nonzero count fires, a zero count on an otherwise-present row
			// passes. Returning passed() unconditionally here (the shape
			// before package scope existed) would have claimed "no parse skips
			// in scope" from a row that never mentioned files — a clean
			// reading of evidence that was never consulted.
			if p := in.scopeFacts.Package; p.State != "" {
				if p.SkippedFiles > 0 {
					return fired(verdict, policyFinding(FindingParseSkippedInScope, severity, in.scope,
						strconv.Itoa(p.SkippedFiles), "0",
						"files in the assessed package were skipped during parsing and are absent from the evidence"))
				}
				return passed()
			}
			return skipped()
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := parseSkipEvidence(in.snap.Parse); n > 0 {
			return fired(verdict, policyFinding(FindingParseSkippedInScope, severity, in.scope,
				strconv.Itoa(n), "0",
				"files were skipped during parsing; skipped files are absent from the evidence"))
		}
		return passed()
	}}
}

// ruleDegradedGraded — R4 "degraded Package im Target Scope → WARN oder FAIL
// nach Schwere": total type-evidence loss (every unit degraded — or a
// degraded signal at or beyond the unit total, which can only mean at least
// total loss) is FAIL, partial degradation WARN (file comment). With usable
// scope facts the clause judges the scope's package claim instead
// (scopeDegradedOutcome — same partial/total grading over the row's facts).
func ruleDegradedGraded(id string) policyRule {
	return policyRule{id: id, check: "no degraded packages in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			return scopeDegradedOutcome(in, true)
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		tr := in.snap.TypeResolution
		n := degradedEvidence(tr)
		switch {
		case n > 0 && n >= tr.UnitsTotal:
			return fired(VerdictFail, policyFinding(FindingPackageDegraded, SeverityError, in.scope,
				strconv.Itoa(n), "0",
				"every package unit lacks type-check evidence"))
		case n > 0:
			return fired(VerdictWarn, policyFinding(FindingPackageDegraded, SeverityWarning, in.scope,
				strconv.Itoa(n), "0",
				"packages lack type-check evidence in the assessed scope"))
		default:
			return passed()
		}
	}}
}

// ruleDegradedStrict — A5 "kein degraded Package im Scope". With usable scope
// facts the clause judges the scope's package claim (ungraded: any degraded or
// never-attempted package FAILs an automated change).
func ruleDegradedStrict(id string) policyRule {
	return policyRule{id: id, check: "no degraded packages in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			return scopeDegradedOutcome(in, false)
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := degradedEvidence(in.snap.TypeResolution); n > 0 {
			return fired(VerdictFail, policyFinding(FindingPackageDegraded, SeverityError, in.scope,
				strconv.Itoa(n), "0",
				"packages lack type-check evidence in the assessed scope"))
		}
		return passed()
	}}
}

// ruleAmbiguousRefs — E4's ambiguous half, R5 "Ambiguous References im Scope
// → WARN", A6 "keine Ambiguity im Scope". With usable scope facts the clause
// judges the target file's own ambiguous counter (scopeCounterOutcome).
func ruleAmbiguousRefs(id, severity string, verdict Verdict) policyRule {
	return policyRule{id: id, check: "no ambiguous references in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			return scopeCounterOutcome(in, in.scopeFacts.File.Ambiguous, verdict, func(n int) Finding {
				return policyFinding(FindingAmbiguousReferenceInScope, severity, in.scope,
					strconv.Itoa(n), "0",
					"references resolved to more than one candidate in the assessed scope")
			})
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := in.snap.Link.Ambiguous; n > 0 {
			return fired(verdict, policyFinding(FindingAmbiguousReferenceInScope, severity, in.scope,
				strconv.Itoa(n), "0",
				"references resolved to more than one candidate in the assessed scope"))
		}
		return passed()
	}}
}

// ruleUnresolvedRefs — E4's unresolved half and A7 "keine unresolved
// References im relevanten Scope". With usable scope facts the clause judges
// the target file's own skipped counter (scopeCounterOutcome).
func ruleUnresolvedRefs(id, severity string, verdict Verdict) policyRule {
	return policyRule{id: id, check: "no unresolved references in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			return scopeCounterOutcome(in, in.scopeFacts.File.Skipped, verdict, func(n int) Finding {
				return policyFinding(FindingUnresolvedReferenceInScope, severity, in.scope,
					strconv.Itoa(n), "0",
					"references could not be resolved and were skipped in the assessed scope")
			})
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := in.snap.Link.Skipped; n > 0 {
			return fired(verdict, policyFinding(FindingUnresolvedReferenceInScope, severity, in.scope,
				strconv.Itoa(n), "0",
				"references could not be resolved and were skipped in the assessed scope"))
		}
		return passed()
	}}
}

// ruleHeuristicVisible — E3 "heuristische Edges erlaubt": allowed and made
// visible, an info finding that never moves the verdict.
func ruleHeuristicVisible(id string) policyRule {
	return policyRule{id: id, check: "no heuristic-tier edges", eval: func(in ruleInput) ruleResult {
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := in.snap.Graph.EdgesByTier.Heuristic; n > 0 {
			return fired(VerdictPass, policyFinding(FindingHeuristicEdgesPresent, "", in.scope,
				strconv.Itoa(n), "",
				"heuristic-tier edges are present; they are allowed for exploration"))
		}
		return passed()
	}}
}

// ruleHeuristicOnly — R6 "rein heuristic kritischer Pfad → WARN", A8
// "kritische Beziehungen mindestens derived". v1 proxy: a heuristic-only
// graph (file comment).
func ruleHeuristicOnly(id, severity string, verdict Verdict) policyRule {
	return policyRule{id: id, check: "evidence not purely heuristic", eval: func(in ruleInput) ruleResult {
		if !repoFactsUsable(in) {
			return skipped()
		}
		if heuristicOnly(in.snap) {
			return fired(verdict, policyFinding(FindingHeuristicOnlyPath, severity, in.scope,
				string(model.TierHeuristic), string(model.TierDerived),
				"the graph carries heuristic-tier edge evidence only (no derived or confirmed edges)"))
		}
		return passed()
	}}
}

// ruleExternalBoundaries — E6 "externe Boundaries erzeugen INFO/WARN"
// (ungraded in v1: INFO), R7 "externe Boundaries im Pfad → WARN", A9
// "externe Boundary mit möglicher Verhaltensabhängigkeit → FAIL oder
// explizite Human Approval" (no approval channel in v1: FAIL, with the
// human-review recommendation via the code's action). With usable scope facts
// the clause judges the target file's own externally-resolved reference
// counter (scopeCounterOutcome) at the same per-policy severity.
func ruleExternalBoundaries(id, severity string, verdict Verdict, message string) policyRule {
	return policyRule{id: id, check: "no external boundaries in scope", eval: func(in ruleInput) ruleResult {
		if scopeFactsUsable(in) {
			return scopeCounterOutcome(in, in.scopeFacts.File.ResolvedExternal, verdict, func(n int) Finding {
				return policyFinding(FindingExternalBoundaryReached, severity, in.scope,
					strconv.Itoa(n), "", message)
			})
		}
		if !repoFactsUsable(in) {
			return skipped()
		}
		if n := in.snap.External.Edges; n > 0 {
			return fired(verdict, policyFinding(FindingExternalBoundaryReached, severity, in.scope,
				strconv.Itoa(n), "", message))
		}
		return passed()
	}}
}

// PolicyExploratory returns exploratory-v1 (contract doc §3.1): understand a
// codebase, find files, form hypotheses. Rules E1–E7 plus the two documented
// v1 gap rules (scope evidence, target resolution — file comment).
func PolicyExploratory() Policy {
	return Policy{Name: PolicyNameExploratory, Version: 1, rules: []policyRule{
		ruleGraphAvailable("E1"),
		ruleGraphNotStale("E2"),
		ruleHeuristicVisible("E3"),
		ruleAmbiguousRefs("E4", SeverityInfo, VerdictPass),
		ruleUnresolvedRefs("E4", SeverityInfo, VerdictPass),
		ruleParseSkips("E5", SeverityWarning, VerdictWarn),
		ruleExternalBoundaries("E6", SeverityInfo, VerdictPass,
			"edges terminate at external boundary nodes; structural coverage ends there"),
		ruleSnapshotPublished("E7"),
		ruleScopeEvidence("E-scope(v1)", VerdictWarn),
		ruleTargetResolved("E-target(v1)", VerdictUnverified, VerdictUnverified, VerdictWarn),
	}}
}

// PolicyReview returns review-v1 (contract doc §3.2): PR review, change
// impact, risk analysis. Rules R1–R8.
func PolicyReview() Policy {
	return Policy{Name: PolicyNameReview, Version: 1, rules: []policyRule{
		ruleGraphCurrent("R1"),
		ruleTargetResolved("R2", VerdictUnverified, VerdictUnverified, VerdictUnverified),
		ruleParseSkips("R3", SeverityError, VerdictFail),
		ruleDegradedGraded("R4"),
		ruleAmbiguousRefs("R5", SeverityWarning, VerdictWarn),
		ruleHeuristicOnly("R6", SeverityWarning, VerdictWarn),
		ruleExternalBoundaries("R7", SeverityWarning, VerdictWarn,
			"edges terminate at external boundary nodes; review evidence ends there"),
		ruleScopeEvidence("R8", VerdictUnverified),
	}}
}

// PolicyAutomatedChange returns automated-change-v1 (contract doc §3.3):
// preparing an autonomous change, safe delete, inline, automated
// refactorings. Rules A1–A10. The A10 floor — missing evidence never PASS —
// holds by construction: every missing-evidence path above maps to UNKNOWN or
// FAIL, and the red-gate tests sweep it.
func PolicyAutomatedChange() Policy {
	return Policy{Name: PolicyNameAutomatedChange, Version: 1, rules: []policyRule{
		ruleGraphCurrent("A1"),
		ruleSnapshotCurrent("A2"),
		ruleTargetResolved("A3", VerdictFail, VerdictFail, VerdictUnverified),
		ruleParseSkips("A4", SeverityError, VerdictFail),
		ruleDegradedStrict("A5"),
		ruleAmbiguousRefs("A6", SeverityError, VerdictFail),
		ruleUnresolvedRefs("A7", SeverityError, VerdictFail),
		ruleHeuristicOnly("A8", SeverityError, VerdictFail),
		ruleExternalBoundaries("A9", SeverityError, VerdictFail,
			"external boundaries with possible behavioral dependence require explicit human approval; no approval channel exists in v1"),
		ruleScopeEvidence("A10", VerdictUnverified),
	}}
}

// Policies returns the three built-in policies in their fixed surface order.
func Policies() []Policy {
	return []Policy{PolicyExploratory(), PolicyReview(), PolicyAutomatedChange()}
}

// ID is the policy's canonical versioned identifier — "review-v1" — as fixed
// by PRD v1.0 §6. It is the token every surface accepts for `--policy` and the
// value of the wire `policy.id` field.
//
// Derived, never stored: a rules or threshold change bumps Version (contract
// doc §3.0), and the identifier moves with it automatically. A stored
// identifier could name a version whose rules had already changed underneath
// it, which is exactly the silent-policy-change the version discipline exists
// to prevent.
func (p Policy) ID() string {
	return fmt.Sprintf("%s-v%d", p.Name, p.Version)
}

// PolicyIDs returns the canonical identifiers of the built-in policies in
// surface order, for usage messages and input-schema enums. Surfaces must build
// their vocabulary from this rather than repeating string literals, so a
// version bump reaches every prompt at once.
func PolicyIDs() []string {
	ps := Policies()
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID())
	}
	return ids
}

// PolicyByID resolves a canonical versioned policy identifier (contract doc
// §2.1 as amended; PRD v1.0 §6) to its built-in rule set. An unknown
// identifier wraps ErrPolicyUnknown — never a zero Policy, which would
// evaluate fail-closed but unexplained.
//
// Resolution is exact. The bare names v0.8.0 accepted ("review",
// "automated_change") are rejected: accepting both spellings would leave the
// superseded contract half-alive, and a caller written against it would keep
// working while believing it had selected a policy this binary no longer
// defines under that spelling (delta doc §A2).
func PolicyByID(id string) (Policy, error) {
	for _, p := range Policies() {
		if p.ID() == id {
			return p, nil
		}
	}
	return Policy{}, fmt.Errorf("%w: %q is not a built-in policy (want one of %v)", ErrPolicyUnknown, id, PolicyIDs())
}

// Evaluate is the pure policy evaluator: Facts + Scope + Policy → Verdict
// (contract doc §3.0). No I/O, no store — identical inputs always yield an
// identical Assessment (§3.0 acceptance: same facts + same policy → same
// verdict). snap and st come from the reader (Load/Evaluate in read.go),
// scope from ResolveScope, and resolution is ResolveScope's finding list for
// that scope — pass it whenever a target was asked, omit it for the
// repository default (the trailing variadic keeps the plain four-argument
// call shape). Evaluate is the factless call: it evaluates with the zero
// ScopeFacts, so every non-repository scope reads
// SCOPE_EVIDENCE_UNAVAILABLE exactly as in v1 — callers holding target-scope
// evidence use EvaluateWithScopeFacts.
func (p Policy) Evaluate(snap Snapshot, st State, scope ScopeRef, resolution ...Finding) Assessment {
	return p.EvaluateWithScopeFacts(snap, st, scope, ScopeFacts{}, resolution...)
}

// EvaluateWithScopeFacts is Evaluate extended with the optional target-scope
// evidence input (scopefacts.go). The extension bumps NO policy version: the
// rules and thresholds are unchanged — the scope clauses the contract §3
// tables always contained simply become decidable when facts arrive (design
// decision of record in the file comment and scopefacts.go). With
// facts.Available false (or a malformed shape, which is no evidence) the
// result is byte-identical to Evaluate — the red gate in
// policy_matrix_test.go pins this over every sealed case.
//
// Every rule that fires appends its Finding(s); the verdict is the worst
// outcome across fired rules (PASS < WARN < UNKNOWN < FAIL). Findings are
// sorted canonically BEFORE recommendations are derived, so the
// recommendation order follows the finding order. Checks that ran and held
// fill ChecksPassed — the explicit PRD §26 all-checks-passed list — so a
// findings-free PASS is always explained. Every assessment carries the
// snapshot-derived limitations and the deterministic recommendations.
func (p Policy) EvaluateWithScopeFacts(snap Snapshot, st State, scope ScopeRef, facts ScopeFacts, resolution ...Finding) Assessment {
	in := ruleInput{snap: snap, st: st, scope: scope, resolution: resolution, scopeFacts: facts}
	findings := []Finding{}
	checksPassed := []string{}
	verdict := VerdictPass
	for _, r := range p.rules {
		res := r.eval(in)
		if res.passed {
			checksPassed = append(checksPassed, r.check)
			continue
		}
		findings = append(findings, res.findings...)
		verdict = worseVerdict(verdict, res.verdict)
	}
	if len(p.rules) == 0 {
		// A rule-less Policy (the zero value) judged nothing: fail closed to
		// UNKNOWN, never PASS — and explained, because PRD §26 forbids a
		// verdict with neither findings nor an all-checks-passed list.
		// Unreachable via the constructors, but Policy is an exported type, so
		// the zero value is constructible and must not slip through silently.
		verdict = VerdictUnverified
		findings = append(findings, policyFinding(FindingScopeEvidenceUnavailable, "", scope,
			"0", "1",
			"the policy defines no rules; no check ran and no evidence was judged (fail closed)"))
	}
	SortFindings(findings)
	return Assessment{
		SchemaVersion:   AssessmentSchemaVersion,
		Policy:          PolicyRef{ID: p.ID(), Name: p.Name, Version: p.Version},
		Scope:           scope,
		SnapshotState:   st,
		Verdict:         verdict,
		Findings:        findings,
		Limitations:     LimitationsFromSnapshot(snap),
		Recommendations: Recommendations(st, findings),
		ChecksPassed:    checksPassed,
	}
}
