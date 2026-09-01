package static_test

// SW-267 reviewer fix Critical 1: the HONESTY test.
//
// This test FAILS without the fix that exposes an uncapped encode
// path. The previous shape called Tokenizer.Encode, which truncates
// to maxLength internally; therefore `len(EncodeRaw(text))` was never
// greater than MaxAdmissionTokens for the pinned tokenizer, and the
// Admit overflow check was structurally unreachable.
//
// The test feeds the real `writePreamble` fixture (12,447 bytes,
// well over the 512-token admission limit) through Admit and
// asserts:
//   - Truncated == true (the adapter cut the body)
//   - Bound == "tokens" (the token bound closed the gap)
//   - AdmissionTokenCount > MaxAdmissionTokens (the HONEST pre-cap
//     count is reported, not the cap value)
//   - The admitted text length is bounded below MaxCapsuleBytes
//   - The admitted text contains the function signature (the
//     header survives the cut)
//
// Without the fix, Admit returns Bound="none" and Truncated=false
// because Tokenizer.Encode already silently capped at 512 and the
// overflow check never fires. With the fix, Admit returns
// Bound="tokens" and Truncated=true.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
)

// TestStatic_AdmitReportsOverflowOnWritePreamble is the FAIL-WITHOUT-
// THE-FIX test for Critical 1. It loads the pinned artifact (if
// available) and feeds `writePreamble` through Admit. Without the
// uncapped-encode fix, this test fails: Admit returns Bound="none"
// because the silent cap eats the overflow before the check can fire.
func TestStatic_AdmitReportsOverflowOnWritePreamble(t *testing.T) {
	ctors := embed.DefaultConstructors()
	make := ctors["static"]
	if make == nil {
		t.Fatal("the `static` scheme is not registered")
	}
	emb, err := make(pinnedModel + "@" + pinnedRevision)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	m, ok := emb.(interface {
		Admit(ctx context.Context, text string) (embed.Admitted, error)
	})
	if !ok {
		t.Fatal("embedder does not implement Admission (AC-2 requires it)")
	}

	// Load the writePreamble fixture.
	src, err := os.ReadFile("../testdata/cobra/writePreamble.go")
	if err != nil {
		t.Fatalf("read writePreamble fixture: %v", err)
	}
	if len(src) < 12000 {
		t.Fatalf("writePreamble fixture = %d bytes, want >= 12000", len(src))
	}
	// Build the document using the production admission path. The
	// embedder's Admit is the HONEST pre-cap count (reviewer fix
	// Critical 1); without the fix, the count would be ≤ 512 and
	// the assertions below would fail.
	n, _ := model.NewNode("function", "cobra.writePreamble", "writePreamble.go", 1, 1)
	s := parse.SourceSpan{StartByte: 0, EndByte: len(src), StartLine: 1, EndLine: 367, Method: parse.SpanMethodAST}
	var admitter embed.Admission
	if a, ok := emb.(embed.Admission); ok {
		admitter = a
	}
	d, err := embed.BuildDocument(n, s, embed.Source{Language: "go", Bytes: src, Admitter: admitter})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}

	// Critical 1 assertions — each one fails without the fix:
	if !d.Truncated {
		t.Errorf("writePreamble Truncated = false; want true. The pinned tokenizer caps at 512 tokens internally; without an uncapped encode path the overflow check is unreachable and Truncated stays false.")
	}
	if d.Bound != "tokens" {
		t.Errorf("writePreamble Bound = %q, want %q. The adapter's admit must report the token bound that closed the gap.", d.Bound, embed.BoundTokens)
	}
	// AdmissionTokenCount must be the HONEST pre-cap count (reviewer fix).
	// Without the fix, the count is the post-cap number (≤ 512) so this
	// assertion fails. With the fix, the count is > 512 for writePreamble.
	if d.AdmissionLimit != 512 {
		t.Errorf("writePreamble AdmissionLimit = %d, want 512 (the production MaxAdmissionTokens)", d.AdmissionLimit)
	}
	if d.AdmissionTokenCount <= d.AdmissionLimit {
		t.Errorf("writePreamble AdmissionTokenCount = %d, want > %d (HONEST pre-cap count; the body has more than 512 tokens)", d.AdmissionTokenCount, d.AdmissionLimit)
	}
	// The admitted Text must be bounded by the resource cap.
	if len(d.Text) > embed.MaxCapsuleBytes {
		t.Errorf("writePreamble Text = %d bytes, want <= %d", len(d.Text), embed.MaxCapsuleBytes)
	}
	// The signature must survive the cut.
	if !strings.Contains(d.Text, "func writePreamble(") {
		t.Errorf("writePreamble Text does not contain the signature; AC-1 says the signature survives the bound")
	}

	// Direct Admit call asserts the HONEST pre-cap count.
	admitted, err := m.Admit(context.Background(), string(src))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if admitted.Bound != embed.BoundTokens {
		t.Errorf("Admit Bound = %q, want %q", admitted.Bound, embed.BoundTokens)
	}
	if admitted.TokenCount <= 512 {
		t.Errorf("Admit TokenCount = %d, want > 512 (HONEST pre-cap count)", admitted.TokenCount)
	}
	_ = admitted
}
