package ingest

import (
	"reflect"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// TestTrustFold_SingleLanguageIsIdentity pins the fold's hard rule: with one
// completed run (today's Go-only registry) the folded facts and evidence are
// EXACTLY the recorded values — same struct bytes, same slice — so the
// published trust snapshot (whose canonical Encode is the digest contract)
// cannot move under the per-language widening.
func TestTrustFold_SingleLanguageIsIdentity(t *testing.T) {
	i := &Ingester{}
	facts := trust.TypeResolutionFacts{
		UnitsTotal: 3, UnitsDegraded: 1, SkippedFiles: 2, DroppedIntents: 4,
		ConfirmedEdges: 5, TypeErrors: 6,
		DegradedUnits: []trust.DegradedUnit{{Dir: "a", Name: "a", Reason: "r", TypeErrors: 6}},
	}
	rows := []PackageEvidence{{PackageKey: "a", State: "ok"}}
	i.recordSemanticRun("go", semanticRun{facts: facts, evidence: rows})

	if got := i.combinedTypeResolutionFacts(); !reflect.DeepEqual(got, facts) {
		t.Fatalf("single-language fold must be the identity:\n got %+v\nwant %+v", got, facts)
	}
	if got := i.combinedPackageEvidence(); !reflect.DeepEqual(got, rows) {
		t.Fatalf("single-language evidence fold must be the identity: %+v", got)
	}
	if !i.semanticEvidenceReady() {
		t.Fatal("a completed run without read failure must be ready")
	}
}

// TestTrustFold_ZeroRunsIsZeroValue pins the skipped-pass behavior: no
// completed run folds to the zero-value facts (nil DegradedUnits — the exact
// value resetTrustSignals used to leave in the single slot) and evidence is
// not ready for a wholesale replace.
func TestTrustFold_ZeroRunsIsZeroValue(t *testing.T) {
	i := &Ingester{}
	i.resetTrustSignals()
	if got := i.combinedTypeResolutionFacts(); !reflect.DeepEqual(got, trust.TypeResolutionFacts{}) {
		t.Fatalf("zero runs must fold to the zero value, got %+v", got)
	}
	if i.combinedPackageEvidence() != nil {
		t.Fatal("zero runs must fold to nil evidence")
	}
	if i.semanticEvidenceReady() {
		t.Fatal("a pass with no completed run must not replace persisted evidence")
	}
}

// TestTrustFold_MultiLanguageSumsAndOrders pins the multi-registrant fold the
// WP-J3 entry criterion demands: counters sum over languages, the degraded
// sample re-merges under (Dir, Name), and evidence rows concatenate in
// sorted-language order — never "last run wins".
func TestTrustFold_MultiLanguageSumsAndOrders(t *testing.T) {
	i := &Ingester{}
	i.recordSemanticRun("kotlin", semanticRun{
		facts: trust.TypeResolutionFacts{
			UnitsTotal: 2, ConfirmedEdges: 10, TypeErrors: 1,
			DegradedUnits: []trust.DegradedUnit{{Dir: "z", Name: "z", Reason: "k"}},
		},
		evidence: []PackageEvidence{{PackageKey: "z"}},
	})
	i.recordSemanticRun("java", semanticRun{
		facts: trust.TypeResolutionFacts{
			UnitsTotal: 3, UnitsDegraded: 1, SkippedFiles: 1, DroppedIntents: 2,
			ConfirmedEdges: 20, TypeErrors: 4,
			DegradedUnits: []trust.DegradedUnit{{Dir: "a", Name: "a", Reason: "j"}},
		},
		evidence: []PackageEvidence{{PackageKey: "a"}},
	})

	got := i.combinedTypeResolutionFacts()
	if got.UnitsTotal != 5 || got.ConfirmedEdges != 30 || got.TypeErrors != 5 ||
		got.UnitsDegraded != 1 || got.SkippedFiles != 1 || got.DroppedIntents != 2 {
		t.Fatalf("multi-language counters must sum: %+v", got)
	}
	if len(got.DegradedUnits) != 2 || got.DegradedUnits[0].Dir != "a" || got.DegradedUnits[1].Dir != "z" {
		t.Fatalf("degraded sample must re-merge under (Dir, Name): %+v", got.DegradedUnits)
	}
	rows := i.combinedPackageEvidence()
	if len(rows) != 2 || rows[0].PackageKey != "a" || rows[1].PackageKey != "z" {
		t.Fatalf("evidence must concatenate in sorted-language order (java before kotlin): %+v", rows)
	}
}

// TestTrustFold_ReadFailureBlocksReplace pins the degradation rule at the
// fold: a completed run plus a read-failed sibling must NOT be ready — a
// wholesale replace would delete the failed language's persisted rows.
func TestTrustFold_ReadFailureBlocksReplace(t *testing.T) {
	i := &Ingester{}
	i.recordSemanticRun("go", semanticRun{facts: trust.TypeResolutionFacts{UnitsTotal: 1}})
	i.semanticReadFailure = true
	if i.semanticEvidenceReady() {
		t.Fatal("a read-failed pass must never replace persisted evidence wholesale")
	}
}
