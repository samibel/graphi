package trust_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// This file is the sealed policy matrix: 20 fixture situations × the three
// built-in policies = 60 cases, each pinning the exact verdict and the exact
// canonically-sorted finding-code list, written from contract doc §3 (and the
// decisions of record in policy.go where the contract is silent) before the
// implementation was matched to it. Fixtures are pure Snapshot/State/ScopeRef
// (plus resolver findings for target cases) — Evaluate is pure, no stores.

// snapPure is the baseline healthy snapshot: a confirmed-only graph with no
// skips, no degradation, no ambiguity, no external surface.
func snapPure() trust.Snapshot {
	return trust.Snapshot{
		SchemaVersion:   trust.SnapshotSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		Graph: trust.GraphFacts{
			NodesTotal: 10, EdgesTotal: 20,
			EdgesByTier: trust.TierCounts{Confirmed: 20},
		},
	}
}

// snapWith derives a fixture snapshot from the pure baseline.
func snapWith(mut func(*trust.Snapshot)) trust.Snapshot {
	s := snapPure()
	mut(&s)
	return s
}

// want is one sealed expectation: the exact verdict and the exact finding
// codes in canonical (sorted) order.
type want struct {
	verdict trust.Verdict
	codes   []string
}

type sealedCase struct {
	name       string
	snap       trust.Snapshot
	st         trust.State
	scope      trust.ScopeRef
	resolution []trust.Finding
	expect     map[string]want // keyed by policy name
}

// sealedMatrix builds the 20 fixture situations. SEALED — a change here is a
// policy version bump (contract §3 versioning rule).
func sealedMatrix(t *testing.T) []sealedCase {
	t.Helper()
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	symbolScope := trust.ScopeRef{
		Kind: trust.ScopeSymbol, ID: "node-1", Path: "a/alpha.go", Symbol: "pkg.Alpha",
	}
	notFoundScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"}
	ambiguousScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Dup"}

	// Resolver findings exactly as ResolveScope shapes them (scope.go).
	resNotFound := []trust.Finding{mustFinding(t, trust.FindingTargetNotFound, notFoundScope,
		"0", "1", `target "pkg.Missing" matches no symbol qualified name and no indexed file path`)}
	resAmbiguous := []trust.Finding{mustFinding(t, trust.FindingTargetAmbiguous, ambiguousScope,
		"7", "1", `target "pkg.Dup" matches 7 symbols`)}

	unavailable := map[string]want{
		// E1+E7 / R1 / A1+A2: no usable evidence, UNKNOWN (never PASS).
		trust.PolicyNameExploratory: {trust.VerdictUnknown,
			[]string{trust.FindingGraphUnavailable, trust.FindingSnapshotMissing}},
		trust.PolicyNameReview: {trust.VerdictUnknown,
			[]string{trust.FindingGraphUnavailable}},
		trust.PolicyNameAutomatedChange: {trust.VerdictUnknown,
			[]string{trust.FindingGraphUnavailable, trust.FindingSnapshotMissing}},
	}
	incomplete := map[string]want{
		// E7 / R1 / A1+A2: unsettled evidence — no released snapshot, graph
		// not settled — UNKNOWN.
		trust.PolicyNameExploratory: {trust.VerdictUnknown,
			[]string{trust.FindingSnapshotMissing}},
		trust.PolicyNameReview: {trust.VerdictUnknown,
			[]string{trust.FindingGraphStale}},
		trust.PolicyNameAutomatedChange: {trust.VerdictUnknown,
			[]string{trust.FindingGraphStale, trust.FindingSnapshotMissing}},
	}
	stale := map[string]want{
		// E2 WARN (drift grading deferred); R1 FAIL; A1+A2 FAIL.
		trust.PolicyNameExploratory: {trust.VerdictWarn,
			[]string{trust.FindingGraphStale}},
		trust.PolicyNameReview: {trust.VerdictFail,
			[]string{trust.FindingGraphStale}},
		trust.PolicyNameAutomatedChange: {trust.VerdictFail,
			[]string{trust.FindingGraphStale, trust.FindingSnapshotStale}},
	}

	return []sealedCase{
		{
			name: "01 current pure-confirmed graph",
			snap: snapPure(), st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, nil},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictPass, nil},
			},
		},
		{
			name: "02 mixed tiers current",
			snap: snapWith(func(s *trust.Snapshot) {
				s.Graph.EdgesByTier = trust.TierCounts{Confirmed: 10, Derived: 5, Heuristic: 5}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				// E3 visibility only; mixed tiers are not "rein heuristic",
				// so R6/A8 hold.
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingHeuristicEdgesPresent}},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictPass, nil},
			},
		},
		{
			name: "03 stale by drift",
			snap: snapPure(), st: trust.StateStale, scope: repo,
			expect: stale,
		},
		{
			name: "04 unavailable missing graph",
			snap: trust.Snapshot{}, st: trust.StateUnavailable, scope: repo,
			expect: unavailable,
		},
		{
			name: "05 unavailable missing snapshot",
			snap: trust.Snapshot{}, st: trust.StateUnavailable, scope: repo,
			expect: unavailable,
		},
		{
			name: "06 unavailable corrupt digest",
			snap: trust.Snapshot{}, st: trust.StateUnavailable, scope: repo,
			expect: unavailable,
		},
		{
			name: "07 parse skips present",
			snap: snapWith(func(s *trust.Snapshot) {
				s.Parse = trust.ParseFacts{Skipped: 3, Paths: []string{"a/skipped.go"}}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictWarn, []string{trust.FindingParseSkippedInScope}},
				trust.PolicyNameReview:          {trust.VerdictFail, []string{trust.FindingParseSkippedInScope}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingParseSkippedInScope}},
			},
		},
		{
			name: "08 degraded package present",
			snap: snapWith(func(s *trust.Snapshot) {
				s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 1}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				// exploratory binds no degraded rule; the TYPECHECK_DEGRADED
				// limitation still carries it.
				trust.PolicyNameExploratory:     {trust.VerdictPass, nil},
				trust.PolicyNameReview:          {trust.VerdictWarn, []string{trust.FindingPackageDegraded}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingPackageDegraded}},
			},
		},
		{
			name: "09 ambiguous references present",
			snap: snapWith(func(s *trust.Snapshot) { s.Link.Ambiguous = 2 }),
			st:   trust.StateCurrent, scope: repo,
			expect: map[string]want{
				// E4 makes them visible (info) without blocking exploration.
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingAmbiguousReferenceInScope}},
				trust.PolicyNameReview:          {trust.VerdictWarn, []string{trust.FindingAmbiguousReferenceInScope}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingAmbiguousReferenceInScope}},
			},
		},
		{
			name: "10 unresolved references present",
			snap: snapWith(func(s *trust.Snapshot) { s.Link.Skipped = 5 }),
			st:   trust.StateCurrent, scope: repo,
			expect: map[string]want{
				// review binds no unresolved rule (contract §3.2).
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingUnresolvedReferenceInScope}},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingUnresolvedReferenceInScope}},
			},
		},
		{
			name: "11 external boundaries present",
			snap: snapWith(func(s *trust.Snapshot) {
				s.External = trust.ExternalFacts{Nodes: 3, Edges: 7}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingExternalBoundaryReached}},
				trust.PolicyNameReview:          {trust.VerdictWarn, []string{trust.FindingExternalBoundaryReached}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingExternalBoundaryReached}},
			},
		},
		{
			name: "12 incomplete running ingest",
			snap: snapPure(), st: trust.StateIncomplete, scope: repo,
			expect: incomplete,
		},
		{
			name: "13 incomplete aborted ingest",
			snap: snapPure(), st: trust.StateIncomplete, scope: repo,
			expect: incomplete,
		},
		{
			name: "14 stale generation mismatch",
			snap: snapPure(), st: trust.StateStale, scope: repo,
			expect: stale,
		},
		{
			name: "15 symbol scope current (scope evidence deferred)",
			snap: snapPure(), st: trust.StateCurrent, scope: symbolScope,
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictWarn, []string{trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameReview:          {trust.VerdictUnknown, []string{trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameAutomatedChange: {trust.VerdictUnknown, []string{trust.FindingScopeEvidenceUnavailable}},
			},
		},
		{
			name: "16 target not found scope",
			snap: snapPure(), st: trust.StateCurrent, scope: notFoundScope,
			resolution: resNotFound,
			expect: map[string]want{
				trust.PolicyNameExploratory: {trust.VerdictUnknown,
					[]string{trust.FindingTargetNotFound, trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameReview: {trust.VerdictUnknown,
					[]string{trust.FindingTargetNotFound, trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail,
					[]string{trust.FindingTargetNotFound, trust.FindingScopeEvidenceUnavailable}},
			},
		},
		{
			name: "17 target ambiguous scope",
			snap: snapPure(), st: trust.StateCurrent, scope: ambiguousScope,
			resolution: resAmbiguous,
			expect: map[string]want{
				trust.PolicyNameExploratory: {trust.VerdictUnknown,
					[]string{trust.FindingTargetAmbiguous, trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameReview: {trust.VerdictUnknown,
					[]string{trust.FindingTargetAmbiguous, trust.FindingScopeEvidenceUnavailable}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail,
					[]string{trust.FindingTargetAmbiguous, trust.FindingScopeEvidenceUnavailable}},
			},
		},
		{
			name: "18 heuristic-only graph",
			snap: snapWith(func(s *trust.Snapshot) {
				s.Graph.EdgesByTier = trust.TierCounts{Heuristic: 20}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingHeuristicEdgesPresent}},
				trust.PolicyNameReview:          {trust.VerdictWarn, []string{trust.FindingHeuristicOnlyPath}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingHeuristicOnlyPath}},
			},
		},
		{
			name: "19 zero-edge empty graph current",
			snap: snapWith(func(s *trust.Snapshot) {
				s.Graph = trust.GraphFacts{}
			}),
			st: trust.StateCurrent, scope: repo,
			expect: map[string]want{
				// Empty is not missing: the facts are complete and judged.
				trust.PolicyNameExploratory:     {trust.VerdictPass, nil},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictPass, nil},
			},
		},
		{
			name: "20 combined worst case",
			snap: snapWith(func(s *trust.Snapshot) {
				s.Parse.Skipped = 3
				s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 1}
				s.Link.Ambiguous = 2
			}),
			st: trust.StateStale, scope: repo,
			expect: map[string]want{
				trust.PolicyNameExploratory: {trust.VerdictWarn, []string{
					trust.FindingGraphStale, trust.FindingParseSkippedInScope,
					trust.FindingAmbiguousReferenceInScope,
				}},
				trust.PolicyNameReview: {trust.VerdictFail, []string{
					trust.FindingGraphStale, trust.FindingParseSkippedInScope,
					trust.FindingAmbiguousReferenceInScope, trust.FindingPackageDegraded,
				}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{
					trust.FindingAmbiguousReferenceInScope, trust.FindingGraphStale,
					trust.FindingPackageDegraded, trust.FindingParseSkippedInScope,
					trust.FindingSnapshotStale,
				}},
			},
		},
	}
}

// TestSealedPolicyMatrix pins verdict and sorted finding codes for all 60
// cases, and verifies every assessment is wired: limitations equal the
// snapshot-derived list, recommendations equal the deterministic builder over
// the sorted findings, and evaluation is deterministic.
// SEALED — a change here is a policy version bump (contract §3 versioning
// rule).
func TestSealedPolicyMatrix(t *testing.T) {
	for _, tc := range sealedMatrix(t) {
		for _, p := range trust.Policies() {
			exp, ok := tc.expect[p.Name]
			if !ok {
				t.Fatalf("%s: no sealed expectation for policy %s", tc.name, p.Name)
			}
			t.Run(tc.name+"/"+p.Name, func(t *testing.T) {
				a := p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...)
				if a.Verdict != exp.verdict {
					t.Errorf("verdict = %s, want %s", a.Verdict, exp.verdict)
				}
				wantCodes := exp.codes
				if wantCodes == nil {
					wantCodes = []string{}
				}
				if got := findingCodes(a.Findings); !reflect.DeepEqual(got, wantCodes) {
					t.Errorf("finding codes = %v, want %v", got, wantCodes)
				}
				if a.Policy.Name != p.Name || a.Policy.Version != p.Version {
					t.Errorf("policy ref = %+v, want %s v%d", a.Policy, p.Name, p.Version)
				}
				if a.Scope != tc.scope || a.SnapshotState != tc.st {
					t.Errorf("scope/state = %+v/%s, want %+v/%s", a.Scope, a.SnapshotState, tc.scope, tc.st)
				}
				// Wiring: limitations and recommendations always attached.
				if wantLim := trust.LimitationsFromSnapshot(tc.snap); !reflect.DeepEqual(a.Limitations, wantLim) {
					t.Errorf("limitations = %+v, want %+v", a.Limitations, wantLim)
				}
				if wantRec := trust.Recommendations(tc.st, a.Findings); !reflect.DeepEqual(a.Recommendations, wantRec) {
					t.Errorf("recommendations = %q, want %q", a.Recommendations, wantRec)
				}
				if a.Findings == nil || a.Limitations == nil || a.Recommendations == nil || a.ChecksPassed == nil {
					t.Error("assessment carries a nil list; want empty slices, never null")
				}
				again := p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...)
				if !reflect.DeepEqual(a, again) {
					t.Error("Evaluate is not deterministic over identical inputs")
				}
			})
		}
	}
}

// TestNoFalsePass_MissingEvidence — red gate: every snapshot state other than
// CURRENT, under every policy, never yields PASS (contract §3.0 fail closed;
// PRD §25.3 A10), regardless of how healthy the snapshot facts look.
func TestNoFalsePass_MissingEvidence(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	states := []trust.State{trust.StateStale, trust.StateIncomplete, trust.StateUnavailable}
	snaps := map[string]trust.Snapshot{"zero": {}, "pure": snapPure()}
	for _, st := range states {
		for snapName, snap := range snaps {
			for _, p := range trust.Policies() {
				a := p.Evaluate(snap, st, repo)
				if a.Verdict == trust.VerdictPass {
					t.Errorf("%s over %s snapshot in state %s = PASS; missing evidence must never PASS", p.Name, snapName, st)
				}
				if len(a.Findings) == 0 {
					t.Errorf("%s over %s snapshot in state %s carries no findings", p.Name, snapName, st)
				}
			}
		}
	}
}

// TestNoFalsePass_AutomatedChange — red gate: any nonzero parse-skip,
// degraded-unit, ambiguous, or unresolved count in the snapshot never yields
// PASS under automated-change-v1, even on a CURRENT state (contract §3.3
// A4–A7; the task's hard floor).
func TestNoFalsePass_AutomatedChange(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	p, err := trust.PolicyByName(trust.PolicyNameAutomatedChange)
	if err != nil {
		t.Fatalf("PolicyByName(automated_change): %v", err)
	}
	counts := []int{0, 1, 3}
	for _, skips := range counts {
		for _, degraded := range counts {
			for _, ambiguous := range counts {
				for _, unresolved := range counts {
					if skips == 0 && degraded == 0 && ambiguous == 0 && unresolved == 0 {
						continue
					}
					snap := snapWith(func(s *trust.Snapshot) {
						s.Parse.Skipped = skips
						s.TypeResolution.UnitsTotal = degraded + 1 // partial degradation is enough
						s.TypeResolution.UnitsDegraded = degraded
						s.Link.Ambiguous = ambiguous
						s.Link.Skipped = unresolved
					})
					a := p.Evaluate(snap, trust.StateCurrent, repo)
					if a.Verdict == trust.VerdictPass {
						t.Errorf("automated_change PASS with skips=%d degraded=%d ambiguous=%d unresolved=%d",
							skips, degraded, ambiguous, unresolved)
					}
					if a.Verdict != trust.VerdictFail {
						t.Errorf("automated_change = %s with skips=%d degraded=%d ambiguous=%d unresolved=%d, want FAIL (contract §3.3)",
							a.Verdict, skips, degraded, ambiguous, unresolved)
					}
				}
			}
		}
	}
}

// TestVerdictAlwaysExplained — red gate: every matrix case, under every
// policy, carries findings or the explicit all-checks-passed list; a PASS
// verdict always carries the list (PRD §26: "kein Verdict ohne Findings oder
// explizite 'all checks passed'-Liste").
func TestVerdictAlwaysExplained(t *testing.T) {
	for _, tc := range sealedMatrix(t) {
		for _, p := range trust.Policies() {
			a := p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...)
			if len(a.Findings) == 0 && len(a.ChecksPassed) == 0 {
				t.Errorf("%s/%s: verdict %s with neither findings nor an all-checks-passed list", tc.name, p.Name, a.Verdict)
			}
			if a.Verdict == trust.VerdictPass && len(a.ChecksPassed) == 0 {
				t.Errorf("%s/%s: PASS without the explicit all-checks-passed list", tc.name, p.Name)
			}
		}
	}
}

// TestPoliciesRegistry pins the registry surface: the three built-ins at
// version 1 in fixed order, and the typed ErrPolicyUnknown for anything else
// (contract §4 ErrTrustPolicyUnknown).
func TestPoliciesRegistry(t *testing.T) {
	wantNames := []string{
		trust.PolicyNameExploratory, trust.PolicyNameReview, trust.PolicyNameAutomatedChange,
	}
	ps := trust.Policies()
	if len(ps) != len(wantNames) {
		t.Fatalf("Policies() returned %d policies, want %d", len(ps), len(wantNames))
	}
	for i, p := range ps {
		if p.Name != wantNames[i] {
			t.Errorf("Policies()[%d].Name = %q, want %q", i, p.Name, wantNames[i])
		}
		if p.Version != 1 {
			t.Errorf("policy %s version = %d, want 1", p.Name, p.Version)
		}
		got, err := trust.PolicyByName(p.Name)
		if err != nil || got.Name != p.Name || got.Version != p.Version {
			t.Errorf("PolicyByName(%q) = (%+v, %v), want the built-in", p.Name, got, err)
		}
	}
	for _, bad := range []string{"", "exploratory-v1", "Review", "automated-change", "yolo"} {
		if _, err := trust.PolicyByName(bad); !errors.Is(err, trust.ErrPolicyUnknown) {
			t.Errorf("PolicyByName(%q) err = %v, want ErrPolicyUnknown", bad, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Scope-facts sealed additions.
//
// ScopeFacts (scopefacts.go) EXTEND THE EVALUATION INPUT, not the rules: the
// v1 policies always contained the contract §3 scope clauses (parse skip in
// scope, degraded package in scope, ambiguity in scope, unresolved in scope,
// external boundary in scope) — v1 merely could not evaluate them for lack of
// scope evidence and fell to SCOPE_EVIDENCE_UNAVAILABLE, which the original
// 20 situations above seal. With facts present the same rules judge the same
// codes at the same per-policy severities against the scope's own evidence
// row. Because no rule and no threshold changed, this is NO policy version
// bump (contract §3.0 bumps on rule/threshold changes, not input extensions):
// the original sealed situations are untouched, and
// TestScopeFactsAbsent_ByteIdenticalRedGate proves the factless call is
// byte-identical over every one of them. The cases below are SEALED exactly
// like the matrix above — a change here is a policy version bump.
// ---------------------------------------------------------------------------

// sealedScopedCase is one sealed scope-facts expectation: a fixture snapshot,
// state, resolved target scope, its evidence facts, and the exact verdict +
// canonically sorted finding codes per policy.
type sealedScopedCase struct {
	name   string
	snap   trust.Snapshot
	st     trust.State
	scope  trust.ScopeRef
	facts  trust.ScopeFacts
	expect map[string]want
}

// sealedScopeFactsMatrix builds the sealed scope-facts situations, all over a
// resolved file scope with a CURRENT pure snapshot so the fired code is
// attributable to the scope facts alone. SEALED — a change here is a policy
// version bump (contract §3 versioning rule).
func sealedScopeFactsMatrix() []sealedScopedCase {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	parsedFile := func(mut func(*trust.ScopeFacts)) trust.ScopeFacts {
		sf := trust.ScopeFacts{
			Available: true,
			File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed},
		}
		if mut != nil {
			mut(&sf)
		}
		return sf
	}

	return []sealedScopedCase{
		{
			// The task's hard floor from the other side: clean scope facts do
			// NOT fire SCOPE_EVIDENCE_UNAVAILABLE, and the scope may reach
			// PASS — with the checks recorded in checks_passed (asserted by
			// the runner).
			name: "S1 clean file scope facts",
			snap: snapPure(), st: trust.StateCurrent, scope: fileScope,
			facts: parsedFile(nil),
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, nil},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictPass, nil},
			},
		},
		{
			// E5 WARN / R3 FAIL / A4 FAIL — the contract tables' parse-skip
			// clauses, now decidable in scope.
			name: "S2 parse skip in scope",
			snap: snapPure(), st: trust.StateCurrent, scope: fileScope,
			facts: trust.ScopeFacts{
				Available: true,
				File: trust.ScopeFileFacts{
					ParseStatus: trust.ScopeParseStatusSkipped,
					ParseReason: "parse_timeout",
				},
			},
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictWarn, []string{trust.FindingParseSkippedInScope}},
				trust.PolicyNameReview:          {trust.VerdictFail, []string{trust.FindingParseSkippedInScope}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingParseSkippedInScope}},
			},
		},
		{
			// R4 FAIL (total type-evidence loss for the scope's package — the
			// R4-style grading already sealed at repository scope) / A5 FAIL;
			// exploratory binds no degraded rule (contract §3.1, sealed case
			// 08).
			name: "S3 degraded package in scope",
			snap: snapPure(), st: trust.StateCurrent, scope: fileScope,
			facts: parsedFile(func(sf *trust.ScopeFacts) {
				sf.Package = trust.ScopePackageFacts{
					State:          trust.ScopePackageStateDegraded,
					DegradedReason: "load_error",
				}
			}),
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, nil},
				trust.PolicyNameReview:          {trust.VerdictFail, []string{trust.FindingPackageDegraded}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingPackageDegraded}},
			},
		},
		{
			// E4 visibility (info, non-blocking) / R5 WARN / A6 FAIL.
			name: "S4 ambiguity in scope",
			snap: snapPure(), st: trust.StateCurrent, scope: fileScope,
			facts: parsedFile(func(sf *trust.ScopeFacts) { sf.File.Ambiguous = 2 }),
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingAmbiguousReferenceInScope}},
				trust.PolicyNameReview:          {trust.VerdictWarn, []string{trust.FindingAmbiguousReferenceInScope}},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingAmbiguousReferenceInScope}},
			},
		},
		{
			// E4 visibility / A7 FAIL; review binds no unresolved rule
			// (contract §3.2, sealed case 10).
			name: "S5 unresolved in scope",
			snap: snapPure(), st: trust.StateCurrent, scope: fileScope,
			facts: parsedFile(func(sf *trust.ScopeFacts) { sf.File.Skipped = 4 }),
			expect: map[string]want{
				trust.PolicyNameExploratory:     {trust.VerdictPass, []string{trust.FindingUnresolvedReferenceInScope}},
				trust.PolicyNameReview:          {trust.VerdictPass, nil},
				trust.PolicyNameAutomatedChange: {trust.VerdictFail, []string{trust.FindingUnresolvedReferenceInScope}},
			},
		},
	}
}

// TestSealedPolicyMatrix_ScopeFacts pins verdict and sorted finding codes for
// the sealed scope-facts situations under all three policies, with the same
// wiring assertions as the factless matrix. Additionally: no case may fire
// SCOPE_EVIDENCE_UNAVAILABLE (the facts ARE the scope evidence), and a PASS
// carries the explicit all-checks-passed list.
// SEALED — a change here is a policy version bump (contract §3 versioning
// rule).
func TestSealedPolicyMatrix_ScopeFacts(t *testing.T) {
	for _, tc := range sealedScopeFactsMatrix() {
		for _, p := range trust.Policies() {
			exp, ok := tc.expect[p.Name]
			if !ok {
				t.Fatalf("%s: no sealed expectation for policy %s", tc.name, p.Name)
			}
			t.Run(tc.name+"/"+p.Name, func(t *testing.T) {
				a := p.EvaluateWithScopeFacts(tc.snap, tc.st, tc.scope, tc.facts)
				if a.Verdict != exp.verdict {
					t.Errorf("verdict = %s, want %s", a.Verdict, exp.verdict)
				}
				wantCodes := exp.codes
				if wantCodes == nil {
					wantCodes = []string{}
				}
				if got := findingCodes(a.Findings); !reflect.DeepEqual(got, wantCodes) {
					t.Errorf("finding codes = %v, want %v", got, wantCodes)
				}
				for _, f := range a.Findings {
					if f.Code == trust.FindingScopeEvidenceUnavailable {
						t.Error("SCOPE_EVIDENCE_UNAVAILABLE fired although scope facts were present")
					}
				}
				if a.Policy.Name != p.Name || a.Policy.Version != 1 {
					t.Errorf("policy ref = %+v, want %s v1 (scope facts are an input extension, never a version bump)", a.Policy, p.Name)
				}
				if a.Scope != tc.scope || a.SnapshotState != tc.st {
					t.Errorf("scope/state = %+v/%s, want %+v/%s", a.Scope, a.SnapshotState, tc.scope, tc.st)
				}
				if wantLim := trust.LimitationsFromSnapshot(tc.snap); !reflect.DeepEqual(a.Limitations, wantLim) {
					t.Errorf("limitations = %+v, want %+v", a.Limitations, wantLim)
				}
				if wantRec := trust.Recommendations(tc.st, a.Findings); !reflect.DeepEqual(a.Recommendations, wantRec) {
					t.Errorf("recommendations = %q, want %q", a.Recommendations, wantRec)
				}
				if a.Verdict == trust.VerdictPass && len(a.ChecksPassed) == 0 {
					t.Error("PASS without the explicit all-checks-passed list (PRD §26)")
				}
				if a.Findings == nil || a.Limitations == nil || a.Recommendations == nil || a.ChecksPassed == nil {
					t.Error("assessment carries a nil list; want empty slices, never null")
				}
				again := p.EvaluateWithScopeFacts(tc.snap, tc.st, tc.scope, tc.facts)
				if !reflect.DeepEqual(a, again) {
					t.Error("EvaluateWithScopeFacts is not deterministic over identical inputs")
				}
			})
		}
	}
}

// TestScopeFactsAbsent_ByteIdenticalRedGate — red gate: over EVERY sealed
// matrix situation and every policy, Evaluate without facts and
// EvaluateWithScopeFacts with absent facts (Available=false) produce
// byte-identical canonical assessments — both for the zero ScopeFacts and for
// an Available=false value carrying nonzero fields (unavailable facts are
// ignored wholesale, never partially read). This is the executable form of
// the no-version-bump reasoning: the input extension leaves the sealed v1
// behavior untouched to the byte.
func TestScopeFactsAbsent_ByteIdenticalRedGate(t *testing.T) {
	absent := map[string]trust.ScopeFacts{
		"zero value": {},
		"unavailable with nonzero fields": {
			Available: false,
			File: trust.ScopeFileFacts{
				ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "oversize",
				ResolvedExternal: 9, Skipped: 9, Ambiguous: 9,
			},
			Package: trust.ScopePackageFacts{
				State: trust.ScopePackageStateDegraded, DegradedReason: "load_error", TypeErrors: 9,
			},
		},
	}
	for _, tc := range sealedMatrix(t) {
		for _, p := range trust.Policies() {
			base, err := trust.EncodeAssessment(p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...))
			if err != nil {
				t.Fatalf("%s/%s: EncodeAssessment: %v", tc.name, p.Name, err)
			}
			for variant, facts := range absent {
				got, err := trust.EncodeAssessment(p.EvaluateWithScopeFacts(tc.snap, tc.st, tc.scope, facts, tc.resolution...))
				if err != nil {
					t.Fatalf("%s/%s: EncodeAssessment(%s): %v", tc.name, p.Name, variant, err)
				}
				if !bytes.Equal(base, got) {
					t.Errorf("%s/%s: absent scope facts (%s) changed the canonical assessment:\nwithout: %s\nwith:    %s",
						tc.name, p.Name, variant, base, got)
				}
			}
		}
	}
}

// TestReviewDegradedGrading pins R4's "WARN oder FAIL nach Schwere" grading:
// partial degradation WARN, total type-evidence loss FAIL (the only
// non-arbitrary v1 grading — policy.go decision of record).
func TestReviewDegradedGrading(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	p, err := trust.PolicyByName(trust.PolicyNameReview)
	if err != nil {
		t.Fatalf("PolicyByName(review): %v", err)
	}
	partial := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 3}
	})
	if a := p.Evaluate(partial, trust.StateCurrent, repo); a.Verdict != trust.VerdictWarn {
		t.Errorf("review over partial degradation = %s, want WARN", a.Verdict)
	}
	total := snapWith(func(s *trust.Snapshot) {
		s.TypeResolution = trust.TypeResolutionFacts{UnitsTotal: 4, UnitsDegraded: 4}
	})
	if a := p.Evaluate(total, trust.StateCurrent, repo); a.Verdict != trust.VerdictFail {
		t.Errorf("review over total degradation = %s, want FAIL", a.Verdict)
	}
}
