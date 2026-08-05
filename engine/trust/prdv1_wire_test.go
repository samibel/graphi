package trust

import (
	"errors"
	"testing"
)

// This file pins the three wire values PRD v1.0 §6 fixes and the July PRD did
// not. It is the regression face of the realignment recorded in
// docs/plan/2026-08-graphi-p1-prd-v1-delta.md §A: each test below failed before
// that change and would fail again if a later edit reverted the value.
//
// The values are machine contracts. An agent branches on them; a script greps
// them. Renaming one is a breaking change to the Labs trust surface and needs a
// delta row plus a contract-document version bump (contract doc §6) — not a
// quiet edit here.

// TestPRDv1_VerdictWireValues pins the closed verdict set. UNVERIFIED replaced
// UNKNOWN (delta §A1): it names the absence of *verifiable* evidence, which is
// what the fail-closed policies actually detect, rather than mere ignorance.
func TestPRDv1_VerdictWireValues(t *testing.T) {
	for _, tc := range []struct {
		got  Verdict
		want string
	}{
		{VerdictPass, "PASS"},
		{VerdictWarn, "WARN"},
		{VerdictFail, "FAIL"},
		{VerdictUnverified, "UNVERIFIED"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("verdict wire value = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestPRDv1_NoUnknownVerdictSurvives guards the rename itself. "UNKNOWN" was
// the shipped v0.8.0 value; no verdict may carry it again, or a client written
// against either contract would silently see the other one's vocabulary.
//
// Note the deliberate narrowness: this pins the *verdict* enum only. State
// still has its own values (contract doc §1.6) and the two enums never mix in
// one field (§1.8), so this test must not be widened into a repo-wide string
// ban.
func TestPRDv1_NoUnknownVerdictSurvives(t *testing.T) {
	for _, v := range []Verdict{VerdictPass, VerdictWarn, VerdictFail, VerdictUnverified} {
		if string(v) == "UNKNOWN" {
			t.Errorf("verdict %v still carries the pre-PRD-v1.0 wire value UNKNOWN", v)
		}
	}
}

// TestPRDv1_PolicyIDs pins the canonical versioned identifiers from PRD v1.0
// §6. The identifier is derived from name and version rather than stored, so a
// policy whose rules change bumps its version and its identifier together — a
// stored identifier could drift from the rules it names.
func TestPRDv1_PolicyIDs(t *testing.T) {
	want := []string{"exploratory-v1", "review-v1", "automated-change-v1"}
	got := make([]string, 0, 3)
	for _, p := range Policies() {
		got = append(got, p.ID())
	}
	if len(got) != len(want) {
		t.Fatalf("Policies() returned %d policies, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("policy %d id = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestPRDv1_PolicyIDConstantsMatchDerived keeps the two spellings of the same
// identifier from drifting. The constants exist for readable call sites; ID()
// is the authority. A version bump that mints a new policy version but forgets
// its constant would otherwise leave the constant advertising a token
// PolicyByID rejects — a broken surface with green unit tests.
func TestPRDv1_PolicyIDConstantsMatchDerived(t *testing.T) {
	for _, tc := range []struct {
		constant string
		policy   Policy
	}{
		{PolicyIDExploratory, PolicyExploratory()},
		{PolicyIDReview, PolicyReview()},
		{PolicyIDAutomatedChange, PolicyAutomatedChange()},
	} {
		if tc.constant != tc.policy.ID() {
			t.Errorf("constant %q != derived %q — a version bump lost its constant", tc.constant, tc.policy.ID())
		}
	}
}

// TestPRDv1_PolicyByIDResolvesCanonicalTokens is the input half: the tokens PRD
// v1.0 §6 names are exactly what the surfaces accept.
func TestPRDv1_PolicyByIDResolvesCanonicalTokens(t *testing.T) {
	for _, id := range []string{"exploratory-v1", "review-v1", "automated-change-v1"} {
		p, err := PolicyByID(id)
		if err != nil {
			t.Errorf("PolicyByID(%q) failed: %v", id, err)
			continue
		}
		if p.ID() != id {
			t.Errorf("PolicyByID(%q) resolved to %q", id, p.ID())
		}
	}
}

// TestPRDv1_BarePolicyNamesRejected is the fail-closed half, and the one that
// makes the CHANGELOG's "breaking" honest. v0.8.0 accepted the bare names; PRD
// v1.0 replaced them. Accepting both would leave the old contract half-alive
// and let a caller written against it keep working while believing it had
// selected a policy this binary no longer defines under that spelling.
//
// "automated_change" is listed explicitly: it is the one token whose *name*
// component also changed (underscore → hyphen), so it must not resolve by
// accident through some later normalization pass.
func TestPRDv1_BarePolicyNamesRejected(t *testing.T) {
	for _, id := range []string{"exploratory", "review", "automated_change", "automated-change", "automated_change-v1"} {
		if _, err := PolicyByID(id); !errors.Is(err, ErrPolicyUnknown) {
			t.Errorf("PolicyByID(%q) = %v, want ErrPolicyUnknown", id, err)
		}
	}
}

// TestPRDv1_AssessmentCarriesPolicyID pins that the identifier reaches the
// wire. PRD v1.0 §5 shows the policy as a single `review-v1` string; the
// shipped document decomposes it into name and version. Emitting `id` beside
// them means an agent can match the PRD's identifier literally instead of
// reassembling it — and, because it is derived, the three fields cannot
// disagree.
func TestPRDv1_AssessmentCarriesPolicyID(t *testing.T) {
	p, err := PolicyByID("review-v1")
	if err != nil {
		t.Fatalf("PolicyByID: %v", err)
	}
	a := p.Evaluate(Snapshot{}, StateUnavailable, ScopeRef{Kind: ScopeRepository})
	if a.Policy.ID != "review-v1" {
		t.Errorf("assessment policy id = %q, want %q", a.Policy.ID, "review-v1")
	}
	if a.Policy.Name != "review" || a.Policy.Version != 1 {
		t.Errorf("assessment policy name/version = %q/%d, want review/1", a.Policy.Name, a.Policy.Version)
	}
	// A policy asked about an unavailable snapshot must abstain, not pass —
	// the property the identifier rename must not disturb.
	if a.Verdict != VerdictUnverified {
		t.Errorf("verdict on UNAVAILABLE snapshot = %q, want UNVERIFIED", a.Verdict)
	}
}
