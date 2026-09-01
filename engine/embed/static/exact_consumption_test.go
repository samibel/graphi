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
// The test below feeds an UNK-heavy input (a long run of "?" — discarded by
// the synthetic tokenizer as unk — followed by recognized tokens "hello
// world") through Admit and asserts that the returned Text fits both the
// character and raw-token boundaries and yields exactly the ids inference
// would obtain from the original input.
//
// The character-cut-only implementation returns 2,048 question marks, but
// their raw UNK stream is itself capped at 512 before UNK removal. Admission
// must therefore cut the bytes again at the raw-token boundary.

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
	// The admitted prefix must stop at the earlier effective boundary:
	// the tokenizer emits one raw UNK per question mark and caps that
	// stream before Model2Vec drops the UNKs.
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
	if got := len(m.tok.EncodeRaw(admitted.Text)); got > m.maxLength {
		t.Errorf("admitted UNK prefix emits %d raw tokens, want <= %d", got, m.maxLength)
	}
	expected := truncateByTokens(m.cutChars(input), m.tok, m.maxLength)
	if admitted.Text != expected {
		t.Errorf("Admit Text differs from the combined character/token prefix.\n  Admit: %d bytes\n  expected: %d bytes", len(admitted.Text), len(expected))
	}
	if got, want := admitted.TokenCount, len(m.InferenceIDs(admitted.Text)); got != want {
		t.Errorf("Admit TokenCount = %d, inference consumes %d ids", got, want)
	}
	if got, want := m.InferenceIDs(admitted.Text), m.InferenceIDs(input); !equalIntSlices(got, want) {
		t.Errorf("admitted and original inference ids differ:\n admitted=%v\n original=%v", got, want)
	}
}

// TestStatic_AdmitTruncatesDenseRecognizedTokens covers the token-bound half
// of the exact-consumption contract. The input is deliberately shorter than
// cutChars' character budget but emits 600 recognized tokens. Tokenizer.Encode
// caps at 512, so an overflow check built on Encode can never observe this
// case and incorrectly returns the full input with BoundNone.
func TestStatic_AdmitTruncatesDenseRecognizedTokens(t *testing.T) {
	m := buildSyntheticModelForAdmit(t)
	input := strings.Repeat("x ", 600)
	charLimit := m.maxLength * m.medianTokenLength
	if got := len([]rune(input)); got >= charLimit {
		t.Fatalf("test setup: input has %d runes, want below character limit %d", got, charLimit)
	}
	if got := len(m.tok.EncodeRaw(input)); got != 600 {
		t.Fatalf("test setup: EncodeRaw emits %d tokens, want 600", got)
	}

	admitted, err := m.Admit(context.Background(), input)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if admitted.Text == input {
		t.Fatal("Admit returned the full 600-token input; inference silently caps it at 512")
	}
	if admitted.Bound != "tokens" {
		t.Errorf("Admit Bound = %q, want tokens", admitted.Bound)
	}
	if got := len(m.tok.EncodeRaw(admitted.Text)); got > m.maxLength {
		t.Errorf("admitted text emits %d raw tokens, want <= %d", got, m.maxLength)
	}
	if got, want := admitted.TokenCount, len(m.InferenceIDs(admitted.Text)); got != want {
		t.Errorf("Admit TokenCount = %d, inference consumes %d ids", got, want)
	}
	if got, want := m.InferenceIDs(admitted.Text), m.InferenceIDs(input); !equalIntSlices(got, want) {
		t.Errorf("admitted and original inference ids differ:\n admitted=%v\n original=%v", got, want)
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
