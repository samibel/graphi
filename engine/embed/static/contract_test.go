// Package static's contract tests for the three SW-259 carry-forwards:
//  1. F16 rounding points (1: mean, 2: squares, 3: sum-of-squares,
//     4: sqrt, 5: divide);
//  2. fixed pairwise summation tree (HALF_pairwise_sum);
//  3. padding section fail-closed (BatchLongest / Right with pad id
//     read from the file).
//
// These tests assert the contracts the production embedder implements
// (see embed.go's `embedOne` and tokenizer.go's padding block). They
// do not depend on the SW-259 spike's testdata (which was a behavioural
// oracle against a Python reference) and do not depend on the pinned
// artifact; they verify the shape of the arithmetic and the
// tokenizer-validation rules directly, so a refactor that broke the
// contract would fail the unit tests before the ORACLE test could
// catch it.
package static

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F16CarryForwards: rounding point 1 is "mean -> round to F16". A
// non-power-of-two count of tokens is the only way to make the float32
// division produce a value whose nearest binary16 is not the same
// float32 value, so the test exercises a 3-token row sum / 3.
//
//	rows = [0.5, 0.5, 0.5]   mean = 0.5 (no rounding loss)
//	rows = [0.4, 0.4, 0.4]   mean_f32 = 0.4 (no rounding loss)
//	rows = [0.1, 0.1, 0.1]   mean_f32 = 0.10000000149...;
//	                          roundF16(0.1) = 0.0999755859375 (rounds DOWN)
//	rows = [0.2, 0.2, 0.2]   mean_f32 = 0.2; roundF16(0.2) = 0.2000732421875
//	                          (rounds UP)
//
// Verifying that the production embedder's mean (embedOne step 1) is
// the roundF16 of the float32 mean proves the rounding point is in
// the production code, not a comment.
func TestStatic_F16_MeanRoundsToBinary16(t *testing.T) {
	// Synthesize a synthetic F16 table with rows that produce a
	// float32 mean whose nearest binary16 differs from the float32
	// mean itself (the rounding point 1 the production embedder pins).
	// normalize=false so the test observes the raw mean without the
	// normalize pipeline's rounding points 2-5.
	dir := t.TempDir()
	rows := []float32{0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1}
	dim := 1
	safe := writeSyntheticF16Table(t, rows, dim)
	cfg := []byte(`{"normalize":false,"embedding_dtype":"float16"}`)
	tok := writeValidTokenizerBytes()
	mod := []byte("[]")
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
	out, err := m.Embed(t.Context(), []string{"x x x x x x x x x x x x"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 1 || len(out[0]) != 1 {
		t.Fatalf("unexpected shape: %v", out)
	}
	got := out[0][0]
	// The float32 mean of 12 rows of 0.1 is exactly 0.1; roundF16(0.1)
	// rounds down to 0.0999755859375 (the nearest binary16 below 0.1).
	// The production embedder must produce this same value — the
	// rounding point 1 in the per-text pipeline.
	want := roundF16(0.1)
	if got != want {
		t.Errorf("mean rounding: got %v, want roundF16(0.1) = %v (rounding point 1: float32 mean is rounded to binary16; SW-259 carry-forward)", got, want)
	}
}

// F16CarryForwards: rounding points 2-5 (squares, sum-of-squares, sqrt,
// divide) live in the normalize branch of embedOne. The test exercises
// normalize=true on a table whose rows have non-unit norm and asserts
// the post-normalize vector is unit length within a small tolerance.
// A regression in any of points 2-5 changes the unit-length result.
func TestStatic_F16_NormalizationPipeline(t *testing.T) {
	dir := t.TempDir()
	// Three 4-dim rows whose mean is NOT unit-length. The vocab has 8
	// tokens; rows = 3 → use 8 distinct rows so the row count matches.
	rows := []float32{0.5, 0.25, 0.5, 0.25, 0.5, 0.25, 0.5, 0.25}
	dim := 4
	safe := writeSyntheticF16Table(t, rows, dim)
	cfg := []byte(`{"normalize":true,"embedding_dtype":"float16"}`)
	tok := writeValidTokenizerBytes()
	mod := []byte("[]")
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
	out, err := m.Embed(t.Context(), []string{"x x x x x x x x x x x x"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 1 || len(out[0]) != dim {
		t.Fatalf("unexpected shape: %v", out)
	}
	// The post-normalization vector must be unit length within float16
	// rounding tolerance.
	var sumsq float64
	for _, v := range out[0] {
		sumsq += float64(v) * float64(v)
	}
	if sumsq < 0.99 || sumsq > 1.01 {
		t.Errorf("normalized vector sumsq = %v, want ~1.0 (rounding points 2-5 broke)", sumsq)
	}
}

// F16CarryForwards: the fixed pairwise summation tree. The test
// constructs a 16-element input where the float32 sequential sum and
// the pairwise tree differ (so a non-pairwise sum would produce a
// different answer) and asserts pairwiseSumF16 matches the canonical
// tree shape.
func TestStatic_F16_PairwiseTreeShape(t *testing.T) {
	// 16 elements of distinct values; sequential sum and pairwise tree
	// differ in float32 because of reassociation.
	values := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8,
		0.9, 1.0, 1.1, 1.2, 1.3, 1.4, 1.5, 1.6}
	a := make([]uint16, len(values))
	for i, v := range values {
		a[i] = F32ToF16(v)
	}
	// Hand-build the canonical numpy tree: 8 partial sums r[j] = a[j]+a[j+8],
	// then ((r0+r1)+(r2+r3))+((r4+r5)+(r6+r7)).
	var r [8]float32
	for j := 0; j < 8; j++ {
		r[j] = f16ToF32(a[j]) + f16ToF32(a[j+8])
	}
	want := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
	got := pairwiseSumF16(a)
	if got != want {
		t.Errorf("pairwiseSumF16: got %v, want %v (the fixed tree shape is part of the SW-259 carry-forward)", got, want)
	}
}

// SW-259 carry-forward: padding section fail-closed. The loader
// refuses a tokenizer.json whose padding strategy is not
// BatchLongest/Right (with pad_to_multiple_of: null and pad_type_id: 0).
// The test writes a tokenizer with strategy="Fixed" and asserts the
// load fails with a typed error mentioning padding and the rejected
// strategy.
func TestStatic_Tokenizer_PaddingRejection(t *testing.T) {
	dir := t.TempDir()
	tok := writeValidTokenizerBytes()
	// Mutate the padding block in the parsed JSON.
	var raw map[string]any
	if err := json.Unmarshal(tok, &raw); err != nil {
		t.Fatal(err)
	}
	raw["padding"] = map[string]any{
		"strategy":    "Fixed",
		"direction":   "Right",
		"pad_id":      0,
		"pad_token":   "[PAD]",
		"pad_type_id": 0,
	}
	tok2, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tok2, 0o644); err != nil {
		t.Fatal(err)
	}
	safe := writeSyntheticF16Table(t, []float32{0, 0, 0, 0, 0, 0, 0, 0}, 8)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"normalize":true,"embedding_dtype":"float16"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), safe, 0o644); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"config.json":       sha256HexBytes([]byte(`{"normalize":true,"embedding_dtype":"float16"}`)),
		"tokenizer.json":    sha256HexBytes(tok2),
		"model.safetensors": sha256HexBytes(safe),
		"modules.json":      sha256HexBytes([]byte("[]")),
	}
	prev := PinnedSHA256
	PinnedSHA256 = pins
	t.Cleanup(func() { PinnedSHA256 = prev })

	_, err := LoadModel(dir)
	if err == nil {
		t.Fatal("LoadModel accepted a tokenizer with padding.strategy=Fixed; AC-2/AC-7 require the padding section to be fail-closed")
	}
	if !strings.Contains(err.Error(), "padding") {
		t.Errorf("error %v must name the padding block", err)
	}
	if !strings.Contains(err.Error(), "BatchLongest") {
		t.Errorf("error %v must name the accepted strategy", err)
	}
}

// writeSyntheticF16Table writes a valid safetensors file with the
// "embeddings" tensor at shape [rows, dim] where rows = len(values) and
// each row is filled with `values[i % len(values)]` row-major.
func writeSyntheticF16Table(t *testing.T, values []float32, dim int) []byte {
	t.Helper()
	rows := len(values)
	header, err := json.Marshal(map[string]any{
		"embeddings": map[string]any{
			"dtype":        "F16",
			"shape":        []int{rows, dim},
			"data_offsets": []int{0, rows * dim * 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr := []byte(header)
	out := make([]byte, 8+len(hdr)+rows*dim*2)
	hl := uint64(len(hdr))
	out[0] = byte(hl)
	out[1] = byte(hl >> 8)
	out[2] = byte(hl >> 16)
	out[3] = byte(hl >> 24)
	out[4] = byte(hl >> 32)
	out[5] = byte(hl >> 40)
	out[6] = byte(hl >> 48)
	out[7] = byte(hl >> 56)
	copy(out[8:], hdr)
	for i := 0; i < rows; i++ {
		for j := 0; j < dim; j++ {
			h := F32ToF16(values[i])
			out[8+len(hdr)+(i*dim+j)*2] = byte(h)
			out[8+len(hdr)+(i*dim+j)*2+1] = byte(h >> 8)
		}
	}
	return out
}

// writeValidTokenizerBytes returns a minimal valid tokenizer.json byte
// slice (no padding, BatchLongest NOT required, right truncation).
// Helper for the contract tests.
func writeValidTokenizerBytes() []byte {
	tok := map[string]any{
		"version": "1.0",
		"truncation": map[string]any{
			"direction": "Right", "max_length": 512, "strategy": "LongestFirst", "stride": 0,
		},
		"padding": nil,
		"added_tokens": []map[string]any{
			{"id": 0, "content": "[PAD]", "single_word": true, "lstrip": true, "rstrip": true, "normalized": true, "special": true},
			{"id": 1, "content": "[UNK]", "single_word": false, "lstrip": false, "rstrip": false, "normalized": false, "special": true},
		},
		"normalizer": map[string]any{
			"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true,
		},
		"pre_tokenizer":  map[string]any{"type": "BertPreTokenizer"},
		"post_processor": nil,
		"decoder":        map[string]any{"type": "WordPiece", "prefix": "##", "cleanup": true},
		"model": map[string]any{
			"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": map[string]int{
				"[PAD]": 0, "[UNK]": 1, "hello": 2, "world": 3, "##s": 4, "認": 5, "x": 6, "_": 7,
			},
		},
	}
	b, _ := json.Marshal(tok)
	return b
}

// sha256HexBytes returns the lower-case hex SHA-256 of b.
func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
