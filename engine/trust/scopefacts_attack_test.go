package trust_test

// Adversarial pins for the ScopeFacts input extension (scopefacts.go), written
// to PROVE a fabricated- or contradictory-facts laundering path. All attacks
// were held by the production code; each is pinned here as a regression gate:
//
//   - Malformed facts (Available=true outside the closed vocabularies —
//     including the contradictory all-zero shape, whose empty parse status is
//     no witness) must be byte-identical to the factless call over EVERY
//     sealed situation: out-of-vocabulary facts can never improve a verdict or
//     fabricate a judged one.
//   - Facts handed in at repository scope are ignored wholesale — the
//     snapshot IS that scope's evidence — again to the byte.
//   - Clean well-formed facts attached to an UNRESOLVED or v1-unsupported
//     scope (empty-ID symbol, package, result-set, out-of-set kind) must not
//     launder the scope toward PASS, and must leave the evaluation
//     byte-identical to the factless call.
//   - A contradictory skipped-file row (skipped parse status with nonzero
//     resolution counters) fires BOTH the parse-skip clause and the counter
//     clauses — fail closed over inconsistent facts — and the zero-counter
//     checks of a skipped row are never recorded as passed (absence of
//     analysis is not evidence of absence).

import (
	"bytes"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// malformedScopeFacts enumerates Available=true shapes outside the closed
// vocabularies — none of them is evidence (scopefacts.go wellFormed).
func malformedScopeFacts() map[string]trust.ScopeFacts {
	return map[string]trust.ScopeFacts{
		// The contradictory-zeros fabrication: "available" with a zero File
		// claim. A real row always carries a parse status; this one witnesses
		// nothing and must read exactly like absent facts.
		"available with contradictory zeros": {Available: true},
		"parse status outside the closed set": {
			Available: true,
			File:      trust.ScopeFileFacts{ParseStatus: "PARSED"},
		},
		"package state outside the closed set": {
			Available: true,
			File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed},
			Package:   trust.ScopePackageFacts{State: "checked!"},
		},
		"clean-looking counters without a parse status": {
			Available: true,
			File:      trust.ScopeFileFacts{ResolvedDerived: 3},
		},
	}
}

// cleanScopeFileFacts is the well-formed, fully clean facts value an attacker
// would fabricate to make a scope look decidable and healthy.
func cleanScopeFileFacts() trust.ScopeFacts {
	return trust.ScopeFacts{
		Available: true,
		File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusParsed},
		Package:   trust.ScopePackageFacts{State: trust.ScopePackageStateChecked},
	}
}

// TestScopeFactsMalformed_ByteIdenticalRedGate — red gate: over EVERY sealed
// matrix situation and every policy, every malformed Available=true shape
// evaluates byte-identical to the factless call. This strengthens the
// absent-facts byte-identity sweep (policy_matrix_test.go) to the fail-closed
// wellFormed boundary: fabricated facts outside the closed vocabularies are no
// evidence at all.
func TestScopeFactsMalformed_ByteIdenticalRedGate(t *testing.T) {
	for _, tc := range sealedMatrix(t) {
		for _, p := range trust.Policies() {
			base, err := trust.EncodeAssessment(p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...))
			if err != nil {
				t.Fatalf("%s/%s: EncodeAssessment: %v", tc.name, p.Name, err)
			}
			for variant, facts := range malformedScopeFacts() {
				got, err := trust.EncodeAssessment(p.EvaluateWithScopeFacts(tc.snap, tc.st, tc.scope, facts, tc.resolution...))
				if err != nil {
					t.Fatalf("%s/%s: EncodeAssessment(%s): %v", tc.name, p.Name, variant, err)
				}
				if !bytes.Equal(base, got) {
					t.Errorf("%s/%s: malformed scope facts (%s) changed the canonical assessment:\nwithout: %s\nwith:    %s",
						tc.name, p.Name, variant, base, got)
				}
			}
		}
	}
}

// TestScopeFactsAtRepositoryScope_IgnoredWholesale — facts handed in at
// repository scope (well-formed clean AND well-formed dirty) are ignored to
// the byte: the snapshot is that scope's evidence, and a fabricated row can
// neither improve nor poison the repository assessment.
func TestScopeFactsAtRepositoryScope_IgnoredWholesale(t *testing.T) {
	dirty := trust.ScopeFacts{
		Available: true,
		File: trust.ScopeFileFacts{
			ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "oversize",
			ResolvedExternal: 9, Skipped: 9, Ambiguous: 9,
		},
		Package: trust.ScopePackageFacts{State: trust.ScopePackageStateDegraded, DegradedReason: "load_error"},
	}
	variants := map[string]trust.ScopeFacts{"clean": cleanScopeFileFacts(), "dirty": dirty}
	for _, tc := range sealedMatrix(t) {
		if tc.scope.Kind != trust.ScopeRepository {
			continue
		}
		for _, p := range trust.Policies() {
			base, err := trust.EncodeAssessment(p.Evaluate(tc.snap, tc.st, tc.scope, tc.resolution...))
			if err != nil {
				t.Fatalf("%s/%s: EncodeAssessment: %v", tc.name, p.Name, err)
			}
			for variant, facts := range variants {
				got, err := trust.EncodeAssessment(p.EvaluateWithScopeFacts(tc.snap, tc.st, tc.scope, facts, tc.resolution...))
				if err != nil {
					t.Fatalf("%s/%s: EncodeAssessment(%s): %v", tc.name, p.Name, variant, err)
				}
				if !bytes.Equal(base, got) {
					t.Errorf("%s/%s: repository-scope facts (%s) changed the canonical assessment:\nwithout: %s\nwith:    %s",
						tc.name, p.Name, variant, base, got)
				}
			}
		}
	}
}

// TestScopeFacts_UnresolvedScopeNeverLaundered — the laundering attack: clean,
// well-formed facts attached to a scope the resolver never resolved must not
// move the scope toward PASS. Every policy must keep a non-PASS verdict with
// the explaining SCOPE_EVIDENCE_UNAVAILABLE finding, and the evaluation must
// be byte-identical to the factless call (fabricated facts on an unresolved
// scope are completely inert).
func TestScopeFacts_UnresolvedScopeNeverLaundered(t *testing.T) {
	notFoundScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"}
	resNotFound := []trust.Finding{mustFinding(t, trust.FindingTargetNotFound, notFoundScope,
		"0", "1", `target "pkg.Missing" matches no symbol qualified name and no indexed file path`)}
	ambiguousScope := trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Dup"}
	resAmbiguous := []trust.Finding{mustFinding(t, trust.FindingTargetAmbiguous, ambiguousScope,
		"7", "1", `target "pkg.Dup" matches 7 symbols`)}

	cases := []struct {
		name       string
		scope      trust.ScopeRef
		resolution []trust.Finding
	}{
		{"empty-ID symbol scope without resolver findings", trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.X"}, nil},
		{"not-found scope with resolver findings", notFoundScope, resNotFound},
		{"ambiguous scope with resolver findings", ambiguousScope, resAmbiguous},
		{"package scope (v1 deferred)", trust.ScopeRef{Kind: trust.ScopePackage, Package: "a/b"}, nil},
		{"result-set scope (v1 open)", trust.ScopeRef{Kind: trust.ScopeResultSet}, nil},
		{"kind outside the closed set", trust.ScopeRef{Kind: "universe"}, nil},
		{"path-less file scope", trust.ScopeRef{Kind: trust.ScopeFile}, nil},
	}
	facts := cleanScopeFileFacts()
	for _, tc := range cases {
		for _, p := range trust.Policies() {
			a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, tc.scope, facts, tc.resolution...)
			if a.Verdict == trust.VerdictPass {
				t.Errorf("%s/%s: fabricated clean facts laundered the unresolved scope to PASS", tc.name, p.Name)
			}
			hasUnavailable := false
			for _, f := range a.Findings {
				if f.Code == trust.FindingScopeEvidenceUnavailable {
					hasUnavailable = true
				}
			}
			if !hasUnavailable {
				t.Errorf("%s/%s: SCOPE_EVIDENCE_UNAVAILABLE missing; findings = %v",
					tc.name, p.Name, findingCodes(a.Findings))
			}
			base, err := trust.EncodeAssessment(p.Evaluate(snapPure(), trust.StateCurrent, tc.scope, tc.resolution...))
			if err != nil {
				t.Fatalf("%s/%s: EncodeAssessment: %v", tc.name, p.Name, err)
			}
			got, err := trust.EncodeAssessment(a)
			if err != nil {
				t.Fatalf("%s/%s: EncodeAssessment(with facts): %v", tc.name, p.Name, err)
			}
			if !bytes.Equal(base, got) {
				t.Errorf("%s/%s: facts on an unresolved scope changed the canonical assessment:\nwithout: %s\nwith:    %s",
					tc.name, p.Name, base, got)
			}
		}
	}
}

// TestScopeFacts_ContradictorySkippedRowFailsClosed — a skipped-file row whose
// resolution counters are nonzero contradicts itself (an unparsed file was
// never analyzed). Fail closed means BOTH signals fire: the parse-skip clause
// and every nonzero counter clause, and the automated-change verdict is FAIL.
// The zero counters of a skipped row must never be recorded as passed checks.
func TestScopeFacts_ContradictorySkippedRowFailsClosed(t *testing.T) {
	fileScope := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"}
	contradictory := trust.ScopeFacts{
		Available: true,
		File: trust.ScopeFileFacts{
			ParseStatus: trust.ScopeParseStatusSkipped,
			ParseReason: "oversize",
			Ambiguous:   2,
			Skipped:     3,
		},
	}
	p, err := trust.PolicyByName(trust.PolicyNameAutomatedChange)
	if err != nil {
		t.Fatalf("PolicyByName: %v", err)
	}
	a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, contradictory)
	if a.Verdict != trust.VerdictFail {
		t.Errorf("verdict = %s, want FAIL over the contradictory skipped row", a.Verdict)
	}
	codes := map[string]bool{}
	for _, f := range a.Findings {
		codes[f.Code] = true
	}
	for _, want := range []string{
		trust.FindingParseSkippedInScope,
		trust.FindingAmbiguousReferenceInScope,
		trust.FindingUnresolvedReferenceInScope,
	} {
		if !codes[want] {
			t.Errorf("finding %s missing — the contradictory row must fire every witnessed defect: %v",
				want, findingCodes(a.Findings))
		}
	}

	// A skipped row with ZERO counters: the counter checks could not run and
	// must be neither fired nor recorded as passed.
	skippedClean := trust.ScopeFacts{
		Available: true,
		File:      trust.ScopeFileFacts{ParseStatus: trust.ScopeParseStatusSkipped, ParseReason: "oversize"},
	}
	a = p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, fileScope, skippedClean)
	if a.Verdict != trust.VerdictFail {
		t.Errorf("verdict = %s, want FAIL (A4: parse skip in scope)", a.Verdict)
	}
	for _, check := range a.ChecksPassed {
		switch check {
		case "no ambiguous references in scope", "no unresolved references in scope", "no external boundaries in scope":
			t.Errorf("check %q recorded as passed on an unparsed file — absence of analysis is not evidence of absence", check)
		}
	}
}
