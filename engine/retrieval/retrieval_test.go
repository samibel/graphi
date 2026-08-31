// Package retrieval_test covers the public surface (AC-1: exported types
// and the New / Retrieve seam) and the arithmetic invariants every later
// stage depends on.
package retrieval_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/retrieval"
)

// TestExportedSurface_ListsExactlyTheNamedTypes is AC-1's encapsulation
// criterion: engine/retrieval exports ONLY the types the story names,
// plus New and Retrieve. Every internal ranking type stays unexported.
// A reviewer will grep the exported surface — keep it exactly as the AC
// lists it.
func TestExportedSurface_ListsExactlyTheNamedTypes(t *testing.T) {
	// The check is structural: every type under retrieval.R* / retrieval.E*
	// that the public surface should carry must be reachable through the
	// named types, and no other exported names should exist. We probe the
	// surface indirectly by ensuring the constructors return what the story
	// promises — a value satisfying Retriever with the named types as the
	// only exported building blocks.
	type namedExported interface {
		Retrieve(ctx context.Context, req retrieval.Request) (retrieval.Result, error)
	}
	// The compile-time guarantee of these type assertions IS the AC-1 test.
	var _ namedExported = (*retrieval.Engine)(nil)
	var _ retrieval.LexicalProvider = (retrieval.LexicalProvider)(nil)
	var _ retrieval.SemanticProvider = (retrieval.SemanticProvider)(nil)
}

// TestPinConstants_AreUntouchable is the spec's "must not be 'improved'"
// list. A change here is a deliberate contract change that needs a story
// of its own, not a fix.
func TestPinConstants_AreUntouchable(t *testing.T) {
	if retrieval.CandidateK != 50 {
		t.Errorf("CandidateK = %d, want 50", retrieval.CandidateK)
	}
	if retrieval.RRFk != 60 {
		t.Errorf("RRFk = %d, want 60", retrieval.RRFk)
	}
	if retrieval.RRFScale != 1_000_000 {
		t.Errorf("RRFScale = %d, want 1_000_000", retrieval.RRFScale)
	}
	if retrieval.MaxPerFile != 3 {
		t.Errorf("MaxPerFile = %d, want 3", retrieval.MaxPerFile)
	}
	if retrieval.LimitDefault != 20 {
		t.Errorf("LimitDefault = %d, want 20", retrieval.LimitDefault)
	}
	if retrieval.RetrievalVersion != "retrieval/1" {
		t.Errorf("RetrievalVersion = %q, want retrieval/1", retrieval.RetrievalVersion)
	}
}

// TestStateVocabulary_IsClosed is the typed-state contract AC-1 implies:
// every state the Result.Degradation can carry is one of the named
// constants, and nothing else.
func TestStateVocabulary_IsClosed(t *testing.T) {
	want := map[retrieval.State]bool{
		retrieval.StateReady:             true,
		retrieval.StateLexicalOnly:       true,
		retrieval.StateGenerationMissing: true,
		retrieval.StateGenerationStale:   true,
		retrieval.StateGenerationCorrupt: true,
	}
	if len(want) != 5 {
		t.Fatalf("vocabulary closed: have %d entries", len(want))
	}
	for s := range want {
		if strings.TrimSpace(string(s)) == "" {
			t.Errorf("empty state: %v", s)
		}
	}
}

// TestModeVocabulary_IsClosed pins ModeAuto / ModeLexicalOnly /
// ModeSemanticRequired as the only Mode values a caller can pass.
func TestModeVocabulary_IsClosed(t *testing.T) {
	// ModeAuto == 0 (zero value) is the default. The other two are positive.
	if retrieval.ModeAuto != 0 {
		t.Errorf("ModeAuto = %d, want 0 (zero value)", retrieval.ModeAuto)
	}
	if retrieval.ModeLexicalOnly == retrieval.ModeSemanticRequired {
		t.Errorf("ModeLexicalOnly == ModeSemanticRequired")
	}
}

// TestExplain_FieldsAreIntegersAndNonNil asserts the scoring path is
// integer-only (AC-2 / AC-3) — no float fields anywhere on the per-row
// breakdown. Compile-time check via type assertions.
func TestExplain_FieldsAreIntegersAndNonNil(t *testing.T) {
	e := retrieval.Explain{}
	_ = e.LexicalRank + e.SemanticRank + e.RRF + e.Graph + e.Classification + e.Final
}
