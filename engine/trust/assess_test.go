package trust_test

import (
	"bytes"
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// registryCodes is the closed v1 finding-code registry (contract doc §3.4),
// one entry per exported constant. Updating the registry updates this list.
var registryCodes = []string{
	trust.FindingGraphStale,
	trust.FindingSnapshotMissing,
	trust.FindingParseSkippedInScope,
	trust.FindingPackageDegraded,
	trust.FindingAmbiguousReferenceInScope,
	trust.FindingUnresolvedReferenceInScope,
	trust.FindingHeuristicOnlyPath,
	trust.FindingExternalBoundaryReached,
	trust.FindingTargetNotFound,
	trust.FindingScopeEvidenceUnavailable,
	trust.FindingTargetAmbiguous,
	trust.FindingGraphUnavailable,
	trust.FindingSnapshotStale,
	trust.FindingHeuristicEdgesPresent,
}

func mustFinding(t *testing.T, code string, scope trust.ScopeRef, observed, threshold, message string) trust.Finding {
	t.Helper()
	f, err := trust.NewFinding(code, scope, observed, threshold, message)
	if err != nil {
		t.Fatalf("NewFinding(%s): %v", code, err)
	}
	return f
}

func TestNewFindingRegistry(t *testing.T) {
	codeForm := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	validSeverity := map[string]bool{
		trust.SeverityInfo: true, trust.SeverityWarning: true, trust.SeverityError: true,
	}
	scope := trust.ScopeRef{Kind: trust.ScopeRepository}
	for _, code := range registryCodes {
		f, err := trust.NewFinding(code, scope, "1", "0", "msg")
		if err != nil {
			t.Errorf("NewFinding(%s) rejected a registry code: %v", code, err)
			continue
		}
		if !codeForm.MatchString(f.Code) {
			t.Errorf("code %q is not SCREAMING_SNAKE_CASE ASCII", f.Code)
		}
		if !validSeverity[f.Severity] {
			t.Errorf("NewFinding(%s) default severity %q outside {info,warning,error}", code, f.Severity)
		}
		if f.Dimension == "" {
			t.Errorf("NewFinding(%s) filled no default dimension", code)
		}
		if f.Scope != scope || f.Observed != "1" || f.Threshold != "0" || f.Message != "msg" {
			t.Errorf("NewFinding(%s) did not carry the caller fields verbatim: %+v", code, f)
		}
	}
	if len(registryCodes) != 14 {
		t.Errorf("registry has %d codes, contract doc §3.4 freezes 14", len(registryCodes))
	}
	for _, bad := range []string{"NOT_A_CODE", "", "graph_stale", "GRAPH_STALE "} {
		if _, err := trust.NewFinding(bad, scope, "", "", ""); !errors.Is(err, trust.ErrUnknownFindingCode) {
			t.Errorf("NewFinding(%q) err = %v, want ErrUnknownFindingCode", bad, err)
		}
	}
}

func TestSortFindings(t *testing.T) {
	fileA := trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/a.go"}
	fileB := trust.ScopeRef{Kind: trust.ScopeFile, Path: "b/b.go"}
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}

	// Canonical order: severity rank descending (error > warning > info),
	// then code ascending, then scope.
	want := []trust.Finding{
		mustFinding(t, trust.FindingTargetNotFound, repo, "0", "1", "m"),            // error
		mustFinding(t, trust.FindingAmbiguousReferenceInScope, repo, "2", "0", "m"), // warning, code asc
		mustFinding(t, trust.FindingGraphStale, fileA, "1", "0", "m"),               // warning, scope tiebreak
		mustFinding(t, trust.FindingGraphStale, fileB, "1", "0", "m"),
		mustFinding(t, trust.FindingExternalBoundaryReached, fileA, "1", "0", "m"), // info, code asc
		mustFinding(t, trust.FindingHeuristicEdgesPresent, repo, "3", "0", "m"),
	}

	got := []trust.Finding{want[3], want[5], want[0], want[4], want[2], want[1]}
	trust.SortFindings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortFindings order:\n got %+v\nwant %+v", got, want)
	}
}

func buildAssessment(t *testing.T, reversed bool) trust.Assessment {
	t.Helper()
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	findings := []trust.Finding{
		mustFinding(t, trust.FindingTargetNotFound, repo, "0", "1", "not found"),
		mustFinding(t, trust.FindingGraphStale, repo, "1", "0", "stale"),
		mustFinding(t, trust.FindingHeuristicEdgesPresent, repo, "9", "0", "heuristic edges"),
	}
	limitations := []trust.Limitation{
		{Code: trust.LimitationParseSkipped, Severity: trust.SeverityWarning, Count: 3, Action: trust.RecommendReviewSkippedFiles},
		{Code: trust.LimitationCrossRepositoryUnavailable, Severity: trust.SeverityInfo, Action: trust.ActionCrossRepository},
	}
	if reversed {
		for i, j := 0, len(findings)-1; i < j; i, j = i+1, j-1 {
			findings[i], findings[j] = findings[j], findings[i]
		}
		for i, j := 0, len(limitations)-1; i < j; i, j = i+1, j-1 {
			limitations[i], limitations[j] = limitations[j], limitations[i]
		}
	}
	return trust.Assessment{
		SchemaVersion:   trust.AssessmentSchemaVersion,
		Policy:          trust.PolicyRef{Name: "review", Version: 1},
		Scope:           repo,
		SnapshotState:   trust.StateCurrent,
		Verdict:         trust.VerdictWarn,
		Findings:        findings,
		Limitations:     limitations,
		Recommendations: []string{trust.RecommendSync, trust.RecommendScopedTarget},
	}
}

func TestEncodeAssessmentDeterminism(t *testing.T) {
	a, err := trust.EncodeAssessment(buildAssessment(t, false))
	if err != nil {
		t.Fatalf("EncodeAssessment: %v", err)
	}
	b, err := trust.EncodeAssessment(buildAssessment(t, true))
	if err != nil {
		t.Fatalf("EncodeAssessment: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("EncodeAssessment not byte-identical across input orderings:\n%s\n%s", a, b)
	}
	if bytes.HasSuffix(a, []byte("\n")) {
		t.Error("EncodeAssessment output must not carry a trailing newline")
	}
	// The canonical bytes list findings sorted: the error-severity finding
	// first, the info one last; recommendations keep producer order.
	iNotFound := bytes.Index(a, []byte(trust.FindingTargetNotFound))
	iStale := bytes.Index(a, []byte(`"GRAPH_STALE"`))
	iHeuristic := bytes.Index(a, []byte(trust.FindingHeuristicEdgesPresent))
	if iNotFound < 0 || iStale < 0 || iHeuristic < 0 || !(iNotFound < iStale && iStale < iHeuristic) {
		t.Errorf("findings not canonically sorted in encoded form: %s", a)
	}
	iSync := bytes.Index(a, []byte(trust.RecommendSync))
	iScoped := bytes.Index(a, []byte(trust.RecommendScopedTarget))
	if iSync < 0 || iScoped < 0 || iSync > iScoped {
		t.Errorf("recommendations did not keep producer order: %s", a)
	}
}

func TestEncodeAssessmentDoesNotMutateCaller(t *testing.T) {
	in := buildAssessment(t, true) // findings deliberately out of canonical order
	before := make([]trust.Finding, len(in.Findings))
	copy(before, in.Findings)
	if _, err := trust.EncodeAssessment(in); err != nil {
		t.Fatalf("EncodeAssessment: %v", err)
	}
	if !reflect.DeepEqual(in.Findings, before) {
		t.Errorf("EncodeAssessment mutated the caller's findings slice")
	}
}

func TestEncodeAssessmentNormalizesNilSlices(t *testing.T) {
	enc, err := trust.EncodeAssessment(trust.Assessment{SchemaVersion: trust.AssessmentSchemaVersion})
	if err != nil {
		t.Fatalf("EncodeAssessment: %v", err)
	}
	if bytes.Contains(enc, []byte("null")) {
		t.Fatalf("EncodeAssessment emitted null for an empty list: %s", enc)
	}
	for _, field := range []string{"findings", "limitations", "recommendations"} {
		if !bytes.Contains(enc, []byte(`"`+field+`":[]`)) {
			t.Errorf("missing empty-array field %q in %s", field, enc)
		}
	}
	// Presence rule: policy and scope are present with zero values, never
	// omitted (contract doc §2.3).
	for _, field := range []string{`"policy":{"id":"","name":"","version":0}`, `"scope":`, `"snapshot_state":""`, `"verdict":""`} {
		if !bytes.Contains(enc, []byte(field)) {
			t.Errorf("missing always-present field %s in %s", field, enc)
		}
	}
}

func TestRecommendations(t *testing.T) {
	repo := trust.ScopeRef{Kind: trust.ScopeRepository}
	cases := []struct {
		name     string
		state    trust.State
		findings []trust.Finding
		want     []string
	}{
		{
			name:  "state and finding recommendations dedupe",
			state: trust.StateStale,
			findings: []trust.Finding{
				mustFinding(t, trust.FindingGraphStale, repo, "1", "0", "m"),
				mustFinding(t, trust.FindingSnapshotStale, repo, "1", "0", "m"),
				mustFinding(t, trust.FindingExternalBoundaryReached, repo, "1", "0", "m"),
				mustFinding(t, trust.FindingGraphStale, repo, "2", "0", "m"),
			},
			want: []string{trust.RecommendSync, trust.RecommendHumanReviewBoundary},
		},
		{
			name:  "unavailable collapses to rebuild",
			state: trust.StateUnavailable,
			findings: []trust.Finding{
				mustFinding(t, trust.FindingGraphUnavailable, repo, "0", "1", "m"),
				mustFinding(t, trust.FindingSnapshotMissing, repo, "0", "1", "m"),
			},
			want: []string{trust.RecommendRebuild},
		},
		{
			name:  "finding order drives recommendation order",
			state: trust.StateCurrent,
			findings: []trust.Finding{
				mustFinding(t, trust.FindingTargetAmbiguous, repo, "2", "1", "m"),
				mustFinding(t, trust.FindingGraphStale, repo, "1", "0", "m"),
			},
			want: []string{trust.RecommendDisambiguateTarget, trust.RecommendSync},
		},
		{
			name:     "off-registry code contributes nothing",
			state:    trust.StateCurrent,
			findings: []trust.Finding{{Code: "BOGUS_CODE", Severity: trust.SeverityError}},
			want:     []string{},
		},
		{
			name:     "current state with no findings",
			state:    trust.StateCurrent,
			findings: nil,
			want:     []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trust.Recommendations(tc.state, tc.findings)
			if got == nil {
				t.Fatal("Recommendations returned nil, want non-nil")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Recommendations = %q, want %q", got, tc.want)
			}
			again := trust.Recommendations(tc.state, tc.findings)
			if !reflect.DeepEqual(got, again) {
				t.Errorf("Recommendations not deterministic: %q vs %q", got, again)
			}
		})
	}
}

func TestLimitationsFromSnapshot(t *testing.T) {
	s := trust.Snapshot{
		Parse:          trust.ParseFacts{Skipped: 3},
		TypeResolution: trust.TypeResolutionFacts{UnitsDegraded: 2},
		Link:           trust.LinkFacts{Ambiguous: 4},
		External:       trust.ExternalFacts{Nodes: 2, Edges: 9},
	}
	want := []trust.Limitation{
		{Code: trust.LimitationAmbiguousReferences, Severity: trust.SeverityWarning, Count: 4, Action: trust.RecommendScopedTarget},
		{Code: trust.LimitationParseSkipped, Severity: trust.SeverityWarning, Count: 3, Action: trust.RecommendReviewSkippedFiles},
		{Code: trust.LimitationTypecheckDegraded, Severity: trust.SeverityWarning, Count: 2, Action: trust.RecommendInspectDegradedPackages},
		{Code: trust.LimitationCrossRepositoryUnavailable, Severity: trust.SeverityInfo, Action: trust.ActionCrossRepository},
		{Code: trust.LimitationDependencyInternalsUnknown, Severity: trust.SeverityInfo, Action: trust.ActionDependencyInternals},
		{Code: trust.LimitationExternalNotNavigable, Severity: trust.SeverityInfo, Count: 9, Action: trust.RecommendHumanReviewBoundary},
	}
	got := trust.LimitationsFromSnapshot(s)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LimitationsFromSnapshot:\n got %+v\nwant %+v", got, want)
	}

	standing := trust.LimitationsFromSnapshot(trust.Snapshot{})
	wantStanding := []trust.Limitation{
		{Code: trust.LimitationCrossRepositoryUnavailable, Severity: trust.SeverityInfo, Action: trust.ActionCrossRepository},
		{Code: trust.LimitationDependencyInternalsUnknown, Severity: trust.SeverityInfo, Action: trust.ActionDependencyInternals},
	}
	if !reflect.DeepEqual(standing, wantStanding) {
		t.Errorf("standing limitations:\n got %+v\nwant %+v", standing, wantStanding)
	}
}
