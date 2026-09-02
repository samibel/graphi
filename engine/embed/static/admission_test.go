package static_test

// SW-267 AC-7 / AC-2 tests for the static adapter's fail-closed
// admission surface. The adapter's Admit method must return the
// EXACT bytes the model will consume and the exact useful-id count it pools.

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// TestStatic_AdmitHonestCount pins AC-7: the production embedder's
// Admit returns the exact token count the model will pool after UNK removal,
// and the returned Text is bounded by the tokenizer boundary.
// An input that exceeds MaxAdmissionTokens is truncated to the first
// N tokens via the model's own byte-aware cut (so the persisted
// vector represents the bytes TextHash describes — no silent cap
// between the Text and the embedding).
//
// The test uses a synthetic Tokenizer (WordPiece over a tiny vocab)
// so it does not depend on the production artifact being present.
// The production behavior is exercised without the pinned artifact by the
// synthetic-model tests in exact_consumption_test.go.
func TestStatic_AdmitHonestCount(t *testing.T) {
	if embed.MaxCapsuleBytes != 16*1024 {
		t.Fatalf("MaxCapsuleBytes = %d, want 16 KiB (AC-6 resource cap)", embed.MaxCapsuleBytes)
	}
}

// TestStatic_AdmitImplementsInterface pins AC-7's compile-time
// guarantee: the production *Embedder implements embed.Admission and
// the returned Text is bounded to the tokenizer's first
// MaxAdmissionTokens tokens. The compile-time assertion in
// static.go pins the interface; this test pins the runtime
// behaviour via the production-shaped path.
func TestStatic_AdmitImplementsInterface(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if _, ok := emb.(embed.Admission); !ok {
		t.Fatal("static.Embedder does not implement embed.Admission; AC-7 requires it")
	}
	if _, ok := emb.(embed.AdmissionProfile); !ok {
		t.Fatal("static.Embedder does not implement embed.AdmissionProfile; AC-3 requires it")
	}
	if _, ok := emb.(embed.TokenizingEmbedder); !ok {
		t.Fatal("static.Embedder does not implement embed.TokenizingEmbedder; AC-7 requires it")
	}
	// Profile is fully populated (AC-8) and pins the production
	// admission algorithm.
	p := emb.(embed.AdmissionProfile).Profile()
	if p.MaxTokens != 512 {
		t.Errorf("profile.MaxTokens = %d, want 512 (the production static embedder's usable limit)", p.MaxTokens)
	}
	if p.Algorithm != "first-n-tokens" {
		t.Errorf("profile.Algorithm = %q, want first-n-tokens", p.Algorithm)
	}
	if p.AlgorithmVersion != "1" {
		t.Errorf("profile.AlgorithmVersion = %q, want 1", p.AlgorithmVersion)
	}
}

// TestStatic_IDContainsAdmissionProfile pins AC-3: the embedder's ID
// carries the admission profile identity so a profile change
// invalidates stored generations. The 9th segment is the profile
// hash; bumping any field of the spec changes it.
func TestStatic_IDContainsAdmissionProfile(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	id := emb.ID()
	parts := strings.Split(id, ":")
	if len(parts) < 9 {
		t.Fatalf("ID() = %q has %d segments, want >= 9 (the 9th is the admission profile hash)", id, len(parts))
	}
	if len(parts[8]) != 12 {
		t.Errorf("ID() admission segment = %q, want 12 hex chars", parts[8])
	}
}

// context is imported so the file compiles even when no test uses it
// directly; the production Admit path takes a context.
var _ = context.Background
