package opcatalog

import (
	"testing"
)

// The embedded shadow document must decode, validate and freeze. Everything
// this asserts is a property of the DATA — the comparison against the live
// legacy builders lives at surface rank
// (surfaces/mcp/opcatalog_parity_test.go), because the catalog sits at engine
// rank and must not import a surface.
func TestShadow_LoadsValidatedAndFrozen(t *testing.T) {
	catalog, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	if !catalog.Frozen() {
		t.Fatal("the shadow catalog is not frozen")
	}
	if catalog.Len() == 0 {
		t.Fatal("the shadow catalog is empty")
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("the frozen shadow catalog does not validate: %v", err)
	}
}

// Shadow() is memoised; two calls must hand back the same immutable catalog
// rather than re-decoding into two divergent ones.
func TestShadow_IsLoadedOnce(t *testing.T) {
	first, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	second, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	if first != second {
		t.Fatal("Shadow() returned two different catalogs")
	}
}

// Canonical iteration order, pinned on the real data rather than on a fixture.
func TestShadow_IterationOrderIsSortedByID(t *testing.T) {
	catalog, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	ids := catalog.IDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("shadow catalog ids are not strictly sorted at %d: %q then %q",
				i, ids[i-1], ids[i])
		}
	}
	specs := catalog.All()
	if len(specs) != len(ids) {
		t.Fatalf("All() returned %d specs but IDs() %d", len(specs), len(ids))
	}
	for i, s := range specs {
		if s.ID != ids[i] {
			t.Fatalf("All()[%d].ID = %q, IDs()[%d] = %q", i, s.ID, i, ids[i])
		}
	}
}

// AC-5's honesty half, read off the shipped data: every entry either cites
// where its ports came from and carries a real port set, or says
// `ports_unaudited` out loud. This test also REPORTS the unaudited entries so a
// reviewer sees the current state of the audit without opening the JSON.
func TestShadow_EveryEntryIsEitherAuditedOrExplicitlyNot(t *testing.T) {
	catalog, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	var unaudited []string
	for _, spec := range catalog.All() {
		if spec.PortsEvidence == "" {
			t.Errorf("%s: no ports evidence", spec.ID)
		}
		if !spec.PortsAudited() {
			unaudited = append(unaudited, spec.ID)
		}
	}
	t.Logf("shadow catalog: %d operations, %d with an unaudited port set: %v",
		catalog.Len(), len(unaudited), unaudited)
}

// The determinism split is a fact worth reporting on every run: it is the
// number AX-04's executor will have to honour when it decides what may be
// cached or replayed.
func TestShadow_DeterminismCensus(t *testing.T) {
	catalog, err := Shadow()
	if err != nil {
		t.Fatalf("Shadow(): %v", err)
	}
	census := map[Determinism]int{}
	for _, spec := range catalog.All() {
		census[spec.Determinism]++
	}
	t.Logf("determinism census: deterministic=%d environment-dependent=%d external=%d unaudited=%d",
		census[DeterminismDeterministic], census[DeterminismEnvironmentDependent],
		census[DeterminismExternal], census[DeterminismUnaudited])
	if census[DeterminismDeterministic] == 0 {
		t.Error("no operation is classified deterministic; the census is almost certainly wrong")
	}
}
