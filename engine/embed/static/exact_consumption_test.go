package static

// SW-267 reviewer fix Critical 1: admission must consume exactly
// the same bytes as inference. InferenceIDs applies cutChars (a
// char prefix to maxLength × medianTokenLength runes) BEFORE
// tokenization. The previous Admit only checked post-unk token
// count and returned the full input text. An input whose first N
// chars all tokenize to UNK and whose later chars are recognized
// words was admitted unchanged while inference silently dropped the
// tail.
//
// The test below feeds an UNK-heavy input (a long run of "?" —
// discarded by the synthetic tokenizer as unk — followed by
// recognized tokens "hello world") through Admit and asserts:
//   - Admit's returned Text is SHORTER than the input (char cut fired).
//   - Admit's returned Text equals the cutChars-prefix that
//     InferenceIDs applies (same bytes consumed).
//   - TokenCount is the HONEST count of the cut text.
//
// Without the fix, Admit returns the full input text. TextHash then
// describes bytes the model never sees — the exact contract the
// story exists to close. The regression test was confirmed RED on
// the previous Admit shape and GREEN on the fix.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatic_AdmitAppliesCharCutToMatchInference is the regression
// test for Critical 1. It builds a synthetic Model (the same shape
// contract_test.go uses for F16 / round-trip tests) so the test
// exercises the production Admit path without depending on the
// pinned artifact.
func TestStatic_AdmitAppliesCharCutToMatchInference(t *testing.T) {
	m := buildSyntheticModelForAdmit(t)

	// Build an input whose first (maxLength × medianTokenLength)
	// chars are "?" (UNK under the synthetic tokenizer) and whose
	// later chars are recognized "hello world hello world ...".
	// Without the fix, Admit only counts post-unk tokens — which
	// drops the "?" prefix and finds "hello world hello world" to
	// fit comfortably under 512 — and returns the FULL input. With
	// the fix, Admit applies the same char cut InferenceIDs does
	// and returns the cut prefix.
	const maxLength = 512
	medianTokenLength := m.medianTokenLength
	if medianTokenLength < 1 {
		medianTokenLength = 1
	}
	limitRunes := maxLength * medianTokenLength
	unkPrefix := strings.Repeat("?", limitRunes)
	recognizedTail := strings.Repeat("hello world ", 200)
	input := unkPrefix + recognizedTail

	// Sanity: the input must have chars beyond what the cut would
	// keep, so a passing test is meaningful.
	if len(input) <= limitRunes*2 {
		t.Fatalf("test setup: input too short to exercise the bypass (%d bytes)", len(input))
	}

	admitted, err := m.Admit(context.Background(), input)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}

	// Reviewer fix Critical 1 assertions:
	if len(admitted.Text) >= len(input) {
		t.Errorf("Admit returned %d bytes (>= input %d). Without the fix Admit returns the full input and TextHash describes bytes inference never saw.", len(admitted.Text), len(input))
	}
	if n := len([]rune(admitted.Text)); n > limitRunes {
		t.Errorf("Admit Text has %d runes, want <= %d (the inference cut boundary maxLength × medianTokenLength).", n, limitRunes)
	}
	if admitted.Bound != "tokens" {
		t.Errorf("Admit Bound = %q, want %q (char cut fired)", admitted.Bound, "tokens")
	}
	// Every char Admit returned must be a "?" — the recognized
	// tail must NOT appear in the admitted text.
	if strings.Contains(admitted.Text, "hello") {
		t.Errorf("Admit returned text containing recognized chars; the recognized tail was not cut by admission")
	}
	// The admitted text must match what cutChars would produce.
	if expected := m.cutChars(input); admitted.Text != expected {
		t.Errorf("Admit Text differs from cutChars output. Admission and inference must consume exactly the same bytes.\n  Admit: %d bytes\n  cutChars: %d bytes", len(admitted.Text), len(expected))
	}
}

// buildSyntheticModelForAdmit builds a Model with a known
// maxLength / medianTokenLength using the contract_test.go
// fixtures. The synthetic tokenizer's vocab is
//
//	[PAD] [UNK] hello world ##s 認 x _
//
// so "?" tokenizes to [UNK]; "hello world" tokenizes to [hello,
// world].
func buildSyntheticModelForAdmit(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg := []byte(`{"normalize":false,"embedding_dtype":"float16"}`)
	tok := writeValidTokenizerBytes()
	mod := []byte("[]")
	// 8 vocab rows × 1 dim — trivially loadable, enough to exercise
	// the InferenceIDs path.
	safe := writeSyntheticF16Table(t, []float32{0, 0, 0, 0, 0, 0, 0, 0}, 1)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tok, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), mod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safe, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       sha256HexBytes(cfg),
		"tokenizer.json":    sha256HexBytes(tok),
		"model.safetensors": sha256HexBytes(safe),
		"modules.json":      sha256HexBytes(mod),
	}
	prev := PinnedSHA256
	PinnedSHA256 = pins
	t.Cleanup(func() { PinnedSHA256 = prev })
	m, err := LoadModel(dir)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	return m
}
