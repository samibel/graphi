package trust_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// This file is the adversarial false-PASS regression suite: every test here
// started as an attack on the v1 policies — an attempt to obtain PASS (or a
// better-than-contract verdict) without the evidence the contract demands
// (contract doc §3.0 fail closed; PRD §7.5 missing evidence is never a
// positive signal; §1.8 unresolvable or unsupported scopes read UNKNOWN).
// Attacks that found a hole are pinned here as regressions against the fix;
// attacks the code already repelled are pinned so it keeps repelling them.
// None of these inputs appear in the sealed matrix (policy_matrix_test.go):
// they are out-of-domain or laundered inputs, so pinning them is not a policy
// version bump — the rules' behavior over the contract's valid domain is
// unchanged.

// severityByCode returns the severity of the first finding carrying code, or
// "" when the code is absent.
func severityByCode(fs []trust.Finding, code string) string {
	for _, f := range fs {
		if f.Code == code {
			return f.Severity
		}
	}
	return ""
}

// TestFailClosed_StateOutsideClosedSet — attack 1: a State string outside the
// closed §1.6 set (including the zero value — a caller that never ran
// DeriveState) over a perfectly healthy snapshot. A state that is not one of
// the four frozen values is not evidence, so every policy must read UNKNOWN
// with an explaining finding — never PASS, never WARN.
func TestFailClosed_StateOutsideClosedSet(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	states := []trust.State{"", "bogus", "current", "PARTIAL", "CURRENT "}
	for _, st := range states {
		for _, p := range trust.Policies() {
			a := p.Evaluate(snapPure(), st, repo)
			if a.Verdict != trust.VerdictUnknown {
				t.Errorf("%s over state %q = %s, want UNKNOWN (out-of-set state is not evidence)",
					p.Name, st, a.Verdict)
			}
			if len(a.Findings) == 0 {
				t.Errorf("%s over state %q carries no findings; the fail-closed verdict must be explained", p.Name, st)
			}
		}
	}
}

// TestFailClosed_ScopeLaunderingWithoutResolverFindings — attack 2: pass a
// scope the resolver could only have produced together with fail-closed
// findings (empty-ID symbol/package shapes), or a scope kind v1 cannot
// resolve at all (result-set, an off-registry kind, a path-less file scope,
// the zero ScopeRef) — but drop the resolver findings. §1.8: unresolvable or
// unsupported scopes read UNKNOWN; laundering the findings away must not
// improve the verdict for any policy.
func TestFailClosed_ScopeLaunderingWithoutResolverFindings(t *testing.T) {
	scopes := []trust.ScopeRef{
		{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"}, // not-found/ambiguous shape, findings dropped
		{Kind: trust.ScopePackage, Package: "corp/lib"},  // package resolution deferred in v1
		{Kind: trust.ScopeResultSet},                     // semantics left open in v1
		{Kind: "galaxy"},                                 // outside the closed §1.7 set
		{Kind: trust.ScopeFile},                          // file scope without a path
		{},                                               // zero ScopeRef
	}
	for _, scope := range scopes {
		for _, p := range trust.Policies() {
			a := p.Evaluate(snapPure(), trust.StateCurrent, scope)
			if a.Verdict != trust.VerdictUnknown {
				t.Errorf("%s over laundered scope %+v = %s, want UNKNOWN (§1.8)", p.Name, scope, a.Verdict)
			}
			if len(a.Findings) == 0 {
				t.Errorf("%s over laundered scope %+v carries no findings", p.Name, scope)
			}
		}
	}
}

// TestFailClosed_ScopeLaunderingWithResolverFindings — attack 2, honest-input
// variant: with the resolver findings attached, TARGET_NOT_FOUND and
// TARGET_AMBIGUOUS scopes must never read PASS or WARN under any policy —
// UNKNOWN (exploratory, review) or FAIL (automated_change) only.
func TestFailClosed_ScopeLaunderingWithResolverFindings(t *testing.T) {
	notFoundScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"}
	ambiguousScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Dup"}
	cases := []struct {
		name       string
		scope      trust.ScopeRef
		resolution []trust.Finding
	}{
		{"not found", notFoundScope, []trust.Finding{mustFinding(t, trust.FindingTargetNotFound,
			notFoundScope, "0", "1", "no match")}},
		{"ambiguous", ambiguousScope, []trust.Finding{mustFinding(t, trust.FindingTargetAmbiguous,
			ambiguousScope, "7", "1", "7 candidates")}},
	}
	for _, tc := range cases {
		for _, p := range trust.Policies() {
			a := p.Evaluate(snapPure(), trust.StateCurrent, tc.scope, tc.resolution...)
			if a.Verdict == trust.VerdictPass || a.Verdict == trust.VerdictWarn {
				t.Errorf("%s over %s target = %s, want UNKNOWN or FAIL", p.Name, tc.name, a.Verdict)
			}
			if p.Name == trust.PolicyNameAutomatedChange && a.Verdict != trust.VerdictFail {
				t.Errorf("automated_change over %s target = %s, want FAIL (contract §3.3 A3)", tc.name, a.Verdict)
			}
		}
	}
}

// TestFailClosed_InconsistentFactSections — attack 1 (boundary values): a
// snapshot whose count field claims zero while its own bounded sample or
// per-reason tally witnesses the defect. The sample IS evidence; a count that
// contradicts it must not read clean (contract §3.0 fail closed; PRD §26
// messages carry no unsupported claims — and "no parse skips" over a snapshot
// listing a skipped path would be one).
func TestFailClosed_InconsistentFactSections(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	verdictOf := func(t *testing.T, name string, snap trust.Snapshot) map[string]trust.Assessment {
		t.Helper()
		out := map[string]trust.Assessment{}
		for _, p := range trust.Policies() {
			out[p.Name] = p.Evaluate(snap, trust.StateCurrent, repo)
		}
		return out
	}

	// Parse sample contradicts the zero count.
	parseSample := snapWith(func(s *trust.Snapshot) {
		s.Parse = trust.ParseFacts{Skipped: 0, Paths: []string{"a/hidden.go"}}
	})
	// Per-reason tally contradicts the zero count.
	parseReason := snapWith(func(s *trust.Snapshot) {
		s.Parse = trust.ParseFacts{Skipped: 0, ByReason: []trust.ReasonCount{{Reason: "parse_error", Count: 2}}}
	})
	for name, snap := range map[string]trust.Snapshot{"paths sample": parseSample, "by-reason tally": parseReason} {
		got := verdictOf(t, name, snap)
		if v := got[trust.PolicyNameExploratory].Verdict; v != trust.VerdictWarn {
			t.Errorf("exploratory over contradicted parse count (%s) = %s, want WARN (E5)", name, v)
		}
		if v := got[trust.PolicyNameReview].Verdict; v != trust.VerdictFail {
			t.Errorf("review over contradicted parse count (%s) = %s, want FAIL (R3)", name, v)
		}
		if v := got[trust.PolicyNameAutomatedChange].Verdict; v != trust.VerdictFail {
			t.Errorf("automated_change over contradicted parse count (%s) = %s, want FAIL (A4)", name, v)
		}
	}

	// Degraded-unit sample contradicts the zero count. exploratory binds no
	// degraded rule (contract §3.1), so only review/automated are pinned.
	degradedSample := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{
			UnitsTotal: 3, UnitsDegraded: 0,
			DegradedUnits: []trust.DegradedUnit{{Dir: "a", Name: "a", Reason: "load_error"}},
		}
	})
	got := verdictOf(t, "degraded sample", degradedSample)
	if v := got[trust.PolicyNameReview].Verdict; v != trust.VerdictWarn {
		t.Errorf("review over contradicted degraded count = %s, want WARN (R4 partial)", v)
	}
	if v := got[trust.PolicyNameAutomatedChange].Verdict; v != trust.VerdictFail {
		t.Errorf("automated_change over contradicted degraded count = %s, want FAIL (A5)", v)
	}

	// Degraded count exceeding the unit total: at least total type-evidence
	// loss — R4's graded outcome must read FAIL, never the milder WARN.
	degradedOverflow := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 5}
	})
	got = verdictOf(t, "degraded overflow", degradedOverflow)
	if v := got[trust.PolicyNameReview].Verdict; v != trust.VerdictFail {
		t.Errorf("review over degraded>total = %s, want FAIL (R4 total loss, fail closed)", v)
	}
	if v := got[trust.PolicyNameAutomatedChange].Verdict; v != trust.VerdictFail {
		t.Errorf("automated_change over degraded>total = %s, want FAIL (A5)", v)
	}
}

// TestFailClosed_ZeroPolicyExplained — attack 3: the zero Policy value (an
// exported struct, so constructible outside the registry) judges nothing.
// Its verdict must be UNKNOWN AND explained — PRD §26 forbids a verdict with
// neither findings nor an all-checks-passed list.
func TestFailClosed_ZeroPolicyExplained(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	a := trust.Policy{}.Evaluate(snapPure(), trust.StateCurrent, repo)
	if a.Verdict != trust.VerdictUnknown {
		t.Errorf("zero Policy verdict = %s, want UNKNOWN", a.Verdict)
	}
	if len(a.Findings) == 0 && len(a.ChecksPassed) == 0 {
		t.Error("zero Policy verdict carries neither findings nor an all-checks-passed list (PRD §26)")
	}
}

// TestFailClosed_AutomatedChangeMinimalCounts — attack 1 (0 vs 1 boundary):
// a single unit of any blocking defect must flip automated_change from PASS
// to FAIL; the all-zero baseline stays PASS with the explicit checks list.
func TestFailClosed_AutomatedChangeMinimalCounts(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	p, err := trust.PolicyByName(trust.PolicyNameAutomatedChange)
	if err != nil {
		t.Fatalf("PolicyByName: %v", err)
	}
	baseline := p.Evaluate(snapPure(), trust.StateCurrent, repo)
	if baseline.Verdict != trust.VerdictPass || len(baseline.ChecksPassed) == 0 {
		t.Fatalf("baseline = %s with %d checks, want PASS with the explicit list",
			baseline.Verdict, len(baseline.ChecksPassed))
	}
	single := map[string]func(*trust.Snapshot){
		"one parse skip":     func(s *trust.Snapshot) { s.Parse.Skipped = 1 },
		"one degraded unit":  func(s *trust.Snapshot) { s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 2, UnitsDegraded: 1} },
		"one ambiguous ref":  func(s *trust.Snapshot) { s.Link.Ambiguous = 1 },
		"one unresolved ref": func(s *trust.Snapshot) { s.Link.Skipped = 1 },
		"one external edge":  func(s *trust.Snapshot) { s.External.Edges = 1 },
		"heuristic-only, 1 edge": func(s *trust.Snapshot) {
			s.Graph.EdgesByTier = trust.TierCounts{Heuristic: 1}
		},
	}
	for name, mut := range single {
		a := p.Evaluate(snapWith(mut), trust.StateCurrent, repo)
		if a.Verdict != trust.VerdictFail {
			t.Errorf("automated_change with %s = %s, want FAIL", name, a.Verdict)
		}
	}
}

// TestSeverityBindings_ContractTables — attack 5: the rule-by-rule audit of
// the §3.1–§3.3 tables as executable pins. Every FAIL-bound rule must fire
// its finding at error severity with verdict FAIL; every WARN-bound rule at
// warning with WARN; the exploratory visibility rules at info without moving
// the verdict. A rule the contract binds to FAIL firing as WARN (severity
// laundering) trips these.
func TestSeverityBindings_ContractTables(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	parse := snapWith(func(s *trust.Snapshot) { s.Parse.Skipped = 2 })
	partialDegraded := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 1}
	})
	totalDegraded := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 4}
	})
	ambiguous := snapWith(func(s *trust.Snapshot) { s.Link.Ambiguous = 2 })
	unresolved := snapWith(func(s *trust.Snapshot) { s.Link.Skipped = 2 })
	external := snapWith(func(s *trust.Snapshot) { s.External = trust.ExternalFacts{Nodes: 1, Edges: 2} })
	heuristicOnly := snapWith(func(s *trust.Snapshot) {
		s.Graph.EdgesByTier = trust.TierCounts{Heuristic: 9}
	})

	cases := []struct {
		rule     string
		policy   string
		snap     trust.Snapshot
		st       trust.State
		code     string
		severity string
		verdict  trust.Verdict
	}{
		{"E2 stale", trust.PolicyNameExploratory, snapPure(), trust.StateStale, trust.FindingGraphStale, trust.SeverityWarning, trust.VerdictWarn},
		{"R1 stale", trust.PolicyNameReview, snapPure(), trust.StateStale, trust.FindingGraphStale, trust.SeverityError, trust.VerdictFail},
		{"A1 stale", trust.PolicyNameAutomatedChange, snapPure(), trust.StateStale, trust.FindingGraphStale, trust.SeverityError, trust.VerdictFail},
		{"A2 stale snapshot", trust.PolicyNameAutomatedChange, snapPure(), trust.StateStale, trust.FindingSnapshotStale, trust.SeverityError, trust.VerdictFail},
		{"E5 parse skips", trust.PolicyNameExploratory, parse, trust.StateCurrent, trust.FindingParseSkippedInScope, trust.SeverityWarning, trust.VerdictWarn},
		{"R3 parse skips", trust.PolicyNameReview, parse, trust.StateCurrent, trust.FindingParseSkippedInScope, trust.SeverityError, trust.VerdictFail},
		{"A4 parse skips", trust.PolicyNameAutomatedChange, parse, trust.StateCurrent, trust.FindingParseSkippedInScope, trust.SeverityError, trust.VerdictFail},
		{"R4 partial degraded", trust.PolicyNameReview, partialDegraded, trust.StateCurrent, trust.FindingPackageDegraded, trust.SeverityWarning, trust.VerdictWarn},
		{"R4 total degraded", trust.PolicyNameReview, totalDegraded, trust.StateCurrent, trust.FindingPackageDegraded, trust.SeverityError, trust.VerdictFail},
		{"A5 degraded", trust.PolicyNameAutomatedChange, partialDegraded, trust.StateCurrent, trust.FindingPackageDegraded, trust.SeverityError, trust.VerdictFail},
		{"E4 ambiguous visible", trust.PolicyNameExploratory, ambiguous, trust.StateCurrent, trust.FindingAmbiguousReferenceInScope, trust.SeverityInfo, trust.VerdictPass},
		{"R5 ambiguous", trust.PolicyNameReview, ambiguous, trust.StateCurrent, trust.FindingAmbiguousReferenceInScope, trust.SeverityWarning, trust.VerdictWarn},
		{"A6 ambiguous", trust.PolicyNameAutomatedChange, ambiguous, trust.StateCurrent, trust.FindingAmbiguousReferenceInScope, trust.SeverityError, trust.VerdictFail},
		{"E4 unresolved visible", trust.PolicyNameExploratory, unresolved, trust.StateCurrent, trust.FindingUnresolvedReferenceInScope, trust.SeverityInfo, trust.VerdictPass},
		{"A7 unresolved", trust.PolicyNameAutomatedChange, unresolved, trust.StateCurrent, trust.FindingUnresolvedReferenceInScope, trust.SeverityError, trust.VerdictFail},
		{"R6 heuristic-only", trust.PolicyNameReview, heuristicOnly, trust.StateCurrent, trust.FindingHeuristicOnlyPath, trust.SeverityWarning, trust.VerdictWarn},
		{"A8 heuristic-only", trust.PolicyNameAutomatedChange, heuristicOnly, trust.StateCurrent, trust.FindingHeuristicOnlyPath, trust.SeverityError, trust.VerdictFail},
		{"E6 external visible", trust.PolicyNameExploratory, external, trust.StateCurrent, trust.FindingExternalBoundaryReached, trust.SeverityInfo, trust.VerdictPass},
		{"R7 external", trust.PolicyNameReview, external, trust.StateCurrent, trust.FindingExternalBoundaryReached, trust.SeverityWarning, trust.VerdictWarn},
		{"A9 external", trust.PolicyNameAutomatedChange, external, trust.StateCurrent, trust.FindingExternalBoundaryReached, trust.SeverityError, trust.VerdictFail},
	}
	for _, tc := range cases {
		p, err := trust.PolicyByName(tc.policy)
		if err != nil {
			t.Fatalf("PolicyByName(%s): %v", tc.policy, err)
		}
		a := p.Evaluate(tc.snap, tc.st, repo)
		if a.Verdict != tc.verdict {
			t.Errorf("%s/%s: verdict = %s, want %s", tc.rule, tc.policy, a.Verdict, tc.verdict)
		}
		if got := severityByCode(a.Findings, tc.code); got != tc.severity {
			t.Errorf("%s/%s: finding %s severity = %q, want %q (severity laundering)",
				tc.rule, tc.policy, tc.code, got, tc.severity)
		}
	}
}

// TestExploratoryFloor — attack 6: UNAVAILABLE and INCOMPLETE must never read
// better than UNKNOWN under exploratory (E1/E7 — missing or unsettled
// evidence), regardless of how healthy the snapshot facts look.
func TestExploratoryFloor(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	p, err := trust.PolicyByName(trust.PolicyNameExploratory)
	if err != nil {
		t.Fatalf("PolicyByName: %v", err)
	}
	for _, st := range []trust.State{trust.StateUnavailable, trust.StateIncomplete} {
		for name, snap := range map[string]trust.Snapshot{"zero": {}, "pure": snapPure()} {
			a := p.Evaluate(snap, st, repo)
			if a.Verdict != trust.VerdictUnknown {
				t.Errorf("exploratory over %s snapshot in %s = %s, want UNKNOWN", name, st, a.Verdict)
			}
		}
	}
}

// TestScopeFacts_MalformedReadsAsAbsent — attack 7: hand the evaluator scope
// facts that CLAIM availability but carry no closed-vocabulary evidence — the
// zero File (an "available" claim witnessing nothing), an out-of-domain parse
// status, an out-of-domain package state. None of that is evidence, so every
// such shape must read byte-identically to absent facts (fail closed:
// SCOPE_EVIDENCE_UNAVAILABLE fires, no scope clause runs) — malformed facts
// can never improve a verdict or fabricate a judged one. Facts handed in at
// repository scope are equally ignored: the snapshot IS that scope's
// evidence.
func TestScopeFacts_MalformedReadsAsAbsent(t *testing.T) {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	malformed := map[string]trust.ScopeFacts{
		"available with zero file":    {Available: true},
		"out-of-domain parse status":  {Available: true, File: trust.ScopeFileFacts{ParseStatus: "PARSED"}},
		"out-of-domain package state": {Available: true, File: trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed}, Package: trust.ScopePackageFacts{State: "wrecked"}},
	}
	for _, scope := range []trust.ScopeRef{fileScope, repo} {
		for _, p := range trust.Policies() {
			base, err := trust.EncodeAssessment(p.Evaluate(snapPure(), trust.StateCurrent, scope))
			if err != nil {
				t.Fatalf("EncodeAssessment: %v", err)
			}
			for name, facts := range malformed {
				got, err := trust.EncodeAssessment(p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, scope, facts))
				if err != nil {
					t.Fatalf("EncodeAssessment(%s): %v", name, err)
				}
				if !bytes.Equal(base, got) {
					t.Errorf("%s over %s scope: malformed scope facts (%s) changed the assessment:\nwithout: %s\nwith:    %s",
						p.Name, scope.Kind, name, base, got)
				}
			}
		}
	}
	// Well-formed facts at repository scope are ignored too — even a
	// defect-laden row must not add repository-scope findings.
	dirty := trust.ScopeFacts{
		Available: true,
		File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "oversize", Ambiguous: 3},
	}
	for _, p := range trust.Policies() {
		base, err := trust.EncodeAssessment(p.Evaluate(snapPure(), trust.StateCurrent, repo))
		if err != nil {
			t.Fatalf("EncodeAssessment: %v", err)
		}
		got, err := trust.EncodeAssessment(p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, repo, dirty))
		if err != nil {
			t.Fatalf("EncodeAssessment: %v", err)
		}
		if !bytes.Equal(base, got) {
			t.Errorf("%s: scope facts at repository scope changed the assessment", p.Name)
		}
	}
}

// TestScopeFacts_SeverityBindings — attack 5 extended to the scope clauses:
// the same rule-by-rule audit as TestSeverityBindings_ContractTables, now
// over a resolved file scope with its evidence row. Every clause must fire
// the same code at the same per-policy severity and verdict as its contract
// table row — including the external-boundary clause (E6 info visibility, R7
// WARN, A9 FAIL) that the sealed scoped matrix does not cover.
func TestScopeFacts_SeverityBindings(t *testing.T) {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	parsed := func(mut func(*trust.ScopeFacts)) trust.ScopeFacts {
		sf := trust.ScopeFacts{Available: true, File: trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed}}
		if mut != nil {
			mut(&sf)
		}
		return sf
	}
	skippedFile := trust.ScopeFacts{Available: true, File: trust.ScopeFileFacts{
		ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "oversize"}}
	ambiguous := parsed(func(sf *trust.ScopeFacts) { sf.File.Ambiguous = 2 })
	unresolved := parsed(func(sf *trust.ScopeFacts) { sf.File.Skipped = 2 })
	external := parsed(func(sf *trust.ScopeFacts) { sf.File.ResolvedExternal = 2 })
	degraded := parsed(func(sf *trust.ScopeFacts) {
		sf.Package = trust.ScopePackageFacts{State: trust.ScopePackageStateDegraded, DegradedReason: "load_error"}
	})

	cases := []struct {
		rule     string
		policy   string
		facts    trust.ScopeFacts
		code     string
		severity string
		verdict  trust.Verdict
	}{
		{"E5 parse skip in scope", trust.PolicyNameExploratory, skippedFile, trust.FindingParseSkippedInScope, trust.SeverityWarning, trust.VerdictWarn},
		{"R3 parse skip in scope", trust.PolicyNameReview, skippedFile, trust.FindingParseSkippedInScope, trust.SeverityError, trust.VerdictFail},
		{"A4 parse skip in scope", trust.PolicyNameAutomatedChange, skippedFile, trust.FindingParseSkippedInScope, trust.SeverityError, trust.VerdictFail},
		{"E4 ambiguous visible in scope", trust.PolicyNameExploratory, ambiguous, trust.FindingAmbiguousReferenceInScope, trust.SeverityInfo, trust.VerdictPass},
		{"R5 ambiguous in scope", trust.PolicyNameReview, ambiguous, trust.FindingAmbiguousReferenceInScope, trust.SeverityWarning, trust.VerdictWarn},
		{"A6 ambiguous in scope", trust.PolicyNameAutomatedChange, ambiguous, trust.FindingAmbiguousReferenceInScope, trust.SeverityError, trust.VerdictFail},
		{"E4 unresolved visible in scope", trust.PolicyNameExploratory, unresolved, trust.FindingUnresolvedReferenceInScope, trust.SeverityInfo, trust.VerdictPass},
		{"A7 unresolved in scope", trust.PolicyNameAutomatedChange, unresolved, trust.FindingUnresolvedReferenceInScope, trust.SeverityError, trust.VerdictFail},
		{"E6 external visible in scope", trust.PolicyNameExploratory, external, trust.FindingExternalBoundaryReached, trust.SeverityInfo, trust.VerdictPass},
		{"R7 external in scope", trust.PolicyNameReview, external, trust.FindingExternalBoundaryReached, trust.SeverityWarning, trust.VerdictWarn},
		{"A9 external in scope", trust.PolicyNameAutomatedChange, external, trust.FindingExternalBoundaryReached, trust.SeverityError, trust.VerdictFail},
		{"R4 degraded in scope", trust.PolicyNameReview, degraded, trust.FindingPackageDegraded, trust.SeverityError, trust.VerdictFail},
		{"A5 degraded in scope", trust.PolicyNameAutomatedChange, degraded, trust.FindingPackageDegraded, trust.SeverityError, trust.VerdictFail},
	}
	for _, tc := range cases {
		p, err := trust.PolicyByName(tc.policy)
		if err != nil {
			t.Fatalf("PolicyByName(%s): %v", tc.policy, err)
		}
		a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, tc.facts)
		if a.Verdict != tc.verdict {
			t.Errorf("%s/%s: verdict = %s, want %s", tc.rule, tc.policy, a.Verdict, tc.verdict)
		}
		if got := severityByCode(a.Findings, tc.code); got != tc.severity {
			t.Errorf("%s/%s: finding %s severity = %q, want %q (severity laundering)",
				tc.rule, tc.policy, tc.code, got, tc.severity)
		}
	}
}

// TestScopeFacts_SkippedFileCountersNotVouched — attack 8: a skipped file's
// zero resolution counters are absence of analysis, not evidence of absence.
// The counter checks must NOT appear in checks_passed (an unparsed file's
// references were never analyzed — vouching for them would be an unsupported
// claim, PRD §26); the parse-skip finding explains the gap. And an
// INCONSISTENT row — skipped yet carrying a nonzero counter — still fires the
// counter clause: the row witnesses the defect (fail closed over
// inconsistent facts).
func TestScopeFacts_SkippedFileCountersNotVouched(t *testing.T) {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	skippedFile := trust.ScopeFacts{Available: true, File: trust.ScopeFileFacts{
		ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "parse_error"}}
	counterChecks := []string{
		"no ambiguous references in scope",
		"no unresolved references in scope",
		"no external boundaries in scope",
	}
	for _, p := range trust.Policies() {
		a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, skippedFile)
		for _, check := range counterChecks {
			for _, got := range a.ChecksPassed {
				if got == check {
					t.Errorf("%s: %q claimed passed over an unparsed file (unsupported claim)", p.Name, check)
				}
			}
		}
		if severityByCode(a.Findings, trust.FindingParseSkippedInScope) == "" {
			t.Errorf("%s: the parse-skip finding explaining the silent counter checks is missing", p.Name)
		}
	}

	inconsistent := skippedFile
	inconsistent.File.Ambiguous = 3
	p, err := trust.PolicyByName(trust.PolicyNameAutomatedChange)
	if err != nil {
		t.Fatalf("PolicyByName: %v", err)
	}
	a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, inconsistent)
	if severityByCode(a.Findings, trust.FindingAmbiguousReferenceInScope) != trust.SeverityError {
		t.Error("a witnessed nonzero counter on a skipped-file row did not fire (inconsistent facts must fail closed)")
	}
	if a.Verdict != trust.VerdictFail {
		t.Errorf("automated_change over inconsistent skipped row = %s, want FAIL", a.Verdict)
	}
}

// TestScopeFacts_DegradedScopedGrading pins R4's "nach Schwere" grading at
// single-package granularity (the scoped face of TestReviewDegradedGrading):
// a degraded or never-attempted package with NO remaining confirmed evidence
// is total type-evidence loss → review FAIL; remaining confirmed coverage
// (PRD §22's "partial confirmed coverage") → review WARN. A5 stays strict:
// automated_change FAILs both. checked_with_errors never fires — the PRD §22
// pin that type errors alone are not degradation holds in scope too.
func TestScopeFacts_DegradedScopedGrading(t *testing.T) {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	withPkg := func(pkg trust.ScopePackageFacts) trust.ScopeFacts {
		return trust.ScopeFacts{
			Available: true,
			File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed},
			Package:   pkg,
		}
	}
	review, err := trust.PolicyByName(trust.PolicyNameReview)
	if err != nil {
		t.Fatalf("PolicyByName(review): %v", err)
	}
	automated, err := trust.PolicyByName(trust.PolicyNameAutomatedChange)
	if err != nil {
		t.Fatalf("PolicyByName(automated_change): %v", err)
	}

	total := withPkg(trust.ScopePackageFacts{State: trust.ScopePackageStateDegraded, DegradedReason: "load_error"})
	partial := withPkg(trust.ScopePackageFacts{State: trust.ScopePackageStateDegraded, DegradedReason: "load_error", ConfirmedEdges: 7})
	skippedPkg := withPkg(trust.ScopePackageFacts{State: trust.ScopePackageStateSkipped})
	checkedWithErrors := withPkg(trust.ScopePackageFacts{State: trust.ScopePackageStateCheckedWithErrors, TypeErrors: 17})

	if a := review.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, total); a.Verdict != trust.VerdictFail {
		t.Errorf("review over total scoped degradation = %s, want FAIL", a.Verdict)
	}
	if a := review.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, partial); a.Verdict != trust.VerdictWarn {
		t.Errorf("review over partial confirmed coverage = %s, want WARN", a.Verdict)
	}
	if a := review.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, skippedPkg); a.Verdict != trust.VerdictFail {
		t.Errorf("review over never-attempted package = %s, want FAIL", a.Verdict)
	}
	if a := review.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, checkedWithErrors); a.Verdict != trust.VerdictPass {
		t.Errorf("review over checked_with_errors = %s, want PASS (PRD §22: type errors alone are never degradation)", a.Verdict)
	}
	for name, facts := range map[string]trust.ScopeFacts{"total": total, "partial": partial, "skipped": skippedPkg} {
		if a := automated.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, facts); a.Verdict != trust.VerdictFail {
			t.Errorf("automated_change over %s scoped degradation = %s, want FAIL (A5 is ungraded)", name, a.Verdict)
		}
	}
	if a := automated.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, checkedWithErrors); a.Verdict != trust.VerdictPass {
		t.Errorf("automated_change over checked_with_errors = %s, want PASS (PRD §22 pin)", a.Verdict)
	}
}

// TestScopeFacts_CleanSymbolScope — the symbol-scope twin of sealed case S1:
// a RESOLVED symbol scope (ID present) with its owning file's clean evidence
// row reaches PASS under every policy, without SCOPE_EVIDENCE_UNAVAILABLE and
// with the explicit checks list. An UNRESOLVED symbol shape with the same
// facts attached must NOT improve: the facts describe a file, but the scope
// itself never resolved — UNKNOWN stands (§1.8; scope laundering with a
// borrowed evidence row).
func TestScopeFacts_CleanSymbolScope(t *testing.T) {
	resolved := trust.ScopeRef{Kind: trust.ScopeSymbol, ID: "node-1", Path: "a/alpha.go", Symbol: "pkg.Alpha"}
	unresolved := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Alpha"}
	clean := trust.ScopeFacts{Available: true, File: trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed}}
	for _, p := range trust.Policies() {
		a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, resolved, clean)
		if a.Verdict != trust.VerdictPass {
			t.Errorf("%s over clean resolved symbol scope = %s, want PASS", p.Name, a.Verdict)
		}
		if severityByCode(a.Findings, trust.FindingScopeEvidenceUnavailable) != "" {
			t.Errorf("%s: SCOPE_EVIDENCE_UNAVAILABLE fired although the owning file's evidence was present", p.Name)
		}
		if len(a.ChecksPassed) == 0 {
			t.Errorf("%s: PASS without the explicit all-checks-passed list", p.Name)
		}

		b := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, unresolved, clean)
		if b.Verdict != trust.VerdictUnknown {
			t.Errorf("%s over unresolved symbol scope with borrowed facts = %s, want UNKNOWN (§1.8)", p.Name, b.Verdict)
		}
	}
}

// TestDeterminism_AdversarialInputs — attack 4: identical adversarial inputs
// must produce byte-identical canonical assessments, including out-of-domain
// states and laundered scopes (the fail-closed paths are as deterministic as
// the happy path).
func TestDeterminism_AdversarialInputs(t *testing.T) {
	launderedScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"}
	worst := snapWith(func(s *trust.Snapshot) {
		s.Parse.Skipped = 3
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 1}
		s.Link.Ambiguous = 2
		s.Link.Skipped = 5
		s.External = trust.ExternalFacts{Nodes: 3, Edges: 7}
		s.Graph.EdgesByTier = trust.TierCounts{Heuristic: 20}
	})
	cases := []struct {
		name  string
		snap  trust.Snapshot
		st    trust.State
		scope trust.ScopeRef
	}{
		{"worst-case current repo", worst, trust.StateCurrent, trust.ScopeRef{Kind: trust.ScopeRepository}},
		{"out-of-set state", snapPure(), trust.State("bogus"), trust.ScopeRef{Kind: trust.ScopeRepository}},
		{"laundered scope", snapPure(), trust.StateCurrent, launderedScope},
	}
	for _, tc := range cases {
		for _, p := range trust.Policies() {
			a := p.Evaluate(tc.snap, tc.st, tc.scope)
			// A second, independently constructed policy value must agree.
			q, err := trust.PolicyByName(p.Name)
			if err != nil {
				t.Fatalf("PolicyByName(%s): %v", p.Name, err)
			}
			b := q.Evaluate(tc.snap, tc.st, tc.scope)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%s/%s: Evaluate differs across identical runs", tc.name, p.Name)
			}
			ba, err := trust.EncodeAssessment(a)
			if err != nil {
				t.Fatalf("EncodeAssessment: %v", err)
			}
			bb, err := trust.EncodeAssessment(b)
			if err != nil {
				t.Fatalf("EncodeAssessment: %v", err)
			}
			if !bytes.Equal(ba, bb) {
				t.Errorf("%s/%s: canonical assessment bytes differ across identical runs", tc.name, p.Name)
			}
		}
	}
}
