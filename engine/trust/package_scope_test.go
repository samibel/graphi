package trust_test

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// Package assessment (PRD §22, contract §5 "package scope" leaves-open item).
//
// v1 could not answer package questions: ResolveScope had no way to tell a
// real package from a typo, so every package-looking target read TARGET_NOT
// _FOUND and all three policies abstained. The evidence to answer them was
// already being persisted per generation (engine/ingest trust_package_evidence)
// — what was missing was a way for the pure resolver to CONSULT it without
// importing the sidecar.
//
// The optional PackageLookup port closes that: absent, resolution is
// byte-identical to v1 (fail closed, deferred); present, a confirmed key
// resolves and its row is judged by the same rules that already grade package
// state. These tests pin both halves, because the fail-closed default is what
// makes the feature safe to add.

// stubPackageLookup confirms exactly the keys it was given.
type stubPackageLookup struct {
	known map[string]bool
	err   error
	asked []string
}

func (s *stubPackageLookup) LookupPackage(_ context.Context, key string) (bool, error) {
	s.asked = append(s.asked, key)
	if s.err != nil {
		return false, s.err
	}
	return s.known[key], nil
}

// TestResolveScope_PackageConfirmedByLookup is the feature: a key the lookup
// knows resolves to a package scope carrying its identity, with no findings.
func TestResolveScope_PackageConfirmedByLookup(t *testing.T) {
	store, _ := seedScopeStore(t)
	lookup := &stubPackageLookup{known: map[string]bool{"internal/service": true}}

	scope, findings, err := trust.ResolveScope(context.Background(), store, "internal/service", lookup)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if scope.Kind != trust.ScopePackage {
		t.Errorf("kind = %q, want package", scope.Kind)
	}
	if scope.ID != "internal/service" || scope.Package != "internal/service" {
		t.Errorf("scope = %+v, want ID and Package both internal/service", scope)
	}
	if len(findings) != 0 {
		t.Errorf("a confirmed package must carry no findings, got %v", findingCodes(findings))
	}
}

// TestResolveScope_PackageWithoutSlashResolves removes an arbitrary v1
// heuristic. v1 guessed "package" from a "/" in the raw string, so a
// top-level package directory could never be one. With a real lookup the guess
// is unnecessary: what the evidence knows is a package, is a package.
func TestResolveScope_PackageWithoutSlashResolves(t *testing.T) {
	store, _ := seedScopeStore(t)
	lookup := &stubPackageLookup{known: map[string]bool{"util": true}}

	scope, findings, err := trust.ResolveScope(context.Background(), store, "util", lookup)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if scope.Kind != trust.ScopePackage || scope.ID != "util" {
		t.Errorf("scope = %+v, want a resolved package scope for util", scope)
	}
	if len(findings) != 0 {
		t.Errorf("unexpected findings: %v", findingCodes(findings))
	}
}

// TestResolveScope_UnknownPackageStaysNotFound is the fail-closed half. A
// lookup that does NOT know the key must leave the v1 outcome intact —
// consulting the evidence may only ever CONFIRM a target, never invent one.
func TestResolveScope_UnknownPackageStaysNotFound(t *testing.T) {
	store, _ := seedScopeStore(t)
	lookup := &stubPackageLookup{known: map[string]bool{"internal/service": true}}

	scope, findings, err := trust.ResolveScope(context.Background(), store, "internal/typo", lookup)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if scope.ID != "" {
		t.Errorf("an unknown package must stay unresolved, got ID %q", scope.ID)
	}
	codes := findingCodes(findings)
	if len(codes) == 0 || codes[0] != trust.FindingTargetNotFound {
		t.Errorf("findings = %v, want TARGET_NOT_FOUND first", codes)
	}
}

// TestResolveScope_LookupErrorFailsClosed pins the error direction. A sidecar
// that cannot be read is missing evidence, not permission to resolve: the
// target stays unresolved rather than being confirmed on a failed check.
func TestResolveScope_LookupErrorFailsClosed(t *testing.T) {
	store, _ := seedScopeStore(t)
	lookup := &stubPackageLookup{err: errors.New("sidecar unreadable")}

	scope, findings, err := trust.ResolveScope(context.Background(), store, "internal/service", lookup)
	if err != nil {
		t.Fatalf("a lookup failure is not an operational error of resolution: %v", err)
	}
	if scope.ID != "" {
		t.Errorf("a failed lookup must not resolve the target, got ID %q", scope.ID)
	}
	if codes := findingCodes(findings); len(codes) == 0 || codes[0] != trust.FindingTargetNotFound {
		t.Errorf("findings = %v, want TARGET_NOT_FOUND", codes)
	}
}

// TestResolveScope_WithoutLookupIsUnchanged pins the compatibility contract:
// callers that supply no lookup get exactly the v1 behaviour, so adding the
// port cannot alter any existing surface by itself.
func TestResolveScope_WithoutLookupIsUnchanged(t *testing.T) {
	store, _ := seedScopeStore(t)

	scope, findings, err := trust.ResolveScope(context.Background(), store, "internal/service")
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if scope.Kind != trust.ScopePackage || scope.ID != "" {
		t.Errorf("scope = %+v, want the v1 unresolved package shape", scope)
	}
	codes := findingCodes(findings)
	if len(codes) != 2 || codes[0] != trust.FindingTargetNotFound || codes[1] != trust.FindingScopeEvidenceUnavailable {
		t.Errorf("findings = %v, want [TARGET_NOT_FOUND SCOPE_EVIDENCE_UNAVAILABLE]", codes)
	}
}

// TestPackageScopeFacts_AreUsableWithoutAFileClaim is the evaluation half.
// v1's wellFormed demanded a file parse status, so a package row alone read as
// no evidence at all. A package scope has no single file, and rejecting its own
// row would make the whole feature unreachable.
func TestPackageScopeFacts_AreUsableWithoutAFileClaim(t *testing.T) {
	p, err := trust.PolicyByID(trust.PolicyIDReview)
	if err != nil {
		t.Fatalf("PolicyByID: %v", err)
	}
	scope := trust.ScopeRef{Kind: trust.ScopePackage, ID: "internal/service", Package: "internal/service"}
	facts := trust.ScopeFacts{
		Available: true,
		Package:   trust.ScopePackageFacts{State: trust.ScopePackageStateChecked, ConfirmedEdges: 12},
	}
	a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, scope, facts)
	if a.Verdict != trust.VerdictPass {
		t.Errorf("verdict = %q, want PASS for a cleanly checked package\nfindings: %v", a.Verdict, findingCodes(a.Findings))
	}
	for _, f := range a.Findings {
		if f.Code == trust.FindingScopeEvidenceUnavailable {
			t.Error("SCOPE_EVIDENCE_UNAVAILABLE fired although the package row IS the scope's evidence")
		}
	}
}

// TestPackageScopeFacts_DegradedBlocks pins that the existing grading applies
// unchanged at package scope: a degraded package is a coverage gap review
// warns about and automated change refuses.
func TestPackageScopeFacts_DegradedBlocks(t *testing.T) {
	scope := trust.ScopeRef{Kind: trust.ScopePackage, ID: "internal/service", Package: "internal/service"}
	facts := trust.ScopeFacts{
		Available: true,
		Package: trust.ScopePackageFacts{
			State: trust.ScopePackageStateDegraded, DegradedReason: "multiple package clauses in directory",
		},
	}
	for _, tc := range []struct {
		policy string
		want   trust.Verdict
	}{
		{trust.PolicyIDReview, trust.VerdictFail},
		{trust.PolicyIDAutomatedChange, trust.VerdictFail},
	} {
		p, err := trust.PolicyByID(tc.policy)
		if err != nil {
			t.Fatalf("PolicyByID(%s): %v", tc.policy, err)
		}
		a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, scope, facts)
		if a.Verdict != tc.want {
			t.Errorf("%s: verdict = %q, want %q\nfindings: %v", tc.policy, a.Verdict, tc.want, findingCodes(a.Findings))
		}
	}
}

// TestPackageScopeFacts_SkippedFilesAreNotSilentlyClean is the red gate of this
// feature. A package row carries its own skipped-file count, and a package
// whose files were skipped must never read as having no parse gaps — that is
// the "filtered emptiness" failure in a different costume.
func TestPackageScopeFacts_SkippedFilesAreNotSilentlyClean(t *testing.T) {
	scope := trust.ScopeRef{Kind: trust.ScopePackage, ID: "internal/service", Package: "internal/service"}
	facts := trust.ScopeFacts{
		Available: true,
		Package: trust.ScopePackageFacts{
			State: trust.ScopePackageStateChecked, ConfirmedEdges: 3, SkippedFiles: 2,
		},
	}
	for _, tc := range []struct {
		policy string
		want   trust.Verdict
	}{
		{trust.PolicyIDExploratory, trust.VerdictWarn},
		{trust.PolicyIDReview, trust.VerdictFail},
		{trust.PolicyIDAutomatedChange, trust.VerdictFail},
	} {
		p, err := trust.PolicyByID(tc.policy)
		if err != nil {
			t.Fatalf("PolicyByID(%s): %v", tc.policy, err)
		}
		a := p.EvaluateWithScopeFacts(snapPure(), trust.StateCurrent, scope, facts)
		if a.Verdict != tc.want {
			t.Errorf("%s: verdict = %q, want %q\nfindings: %v", tc.policy, a.Verdict, tc.want, findingCodes(a.Findings))
		}
		found := false
		for _, f := range a.Findings {
			if f.Code == trust.FindingParseSkippedInScope {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: a package with skipped files carries no PARSE_SKIPPED_IN_SCOPE finding: %v", tc.policy, findingCodes(a.Findings))
		}
	}
}
