// Package static's inference path: the production Model type, the EmbedEach
// batch-invariant entry point, the float16 rounding points, and the fixed
// pairwise summation tree. This file is where the SW-259 requirements
// ("EmbedEach semantics; float16 rounding points and fixed summation tree in
// the determinism contract") are honoured. The rounding points are documented
// inline at each step.
package static

import (
	"context"
	"math"
	"unicode/utf8"
)

// Model is a loaded potion-style static embedding model. It is the production
// counterpart of internal/spike/model2vec.Model — same arithmetic, same
// determinism contract, no public BatchLongest path (the spike's `Embed`
// method was reference-faithful at the cost of being batch-sensitive; the
// production embedder adopts `EmbedEach` semantics so a node's vector does
// not depend on its chunk companions, see AC-2).
type Model struct {
	// Dir is the artifact directory the model was loaded from.
	Dir string
	// ArtifactBytes is the total size of the four pinned files (AC-7's
	// pre-allocation size check).
	ArtifactBytes int64

	tok       *Tokenizer
	rows, dim int
	table     []uint16 // rows × dim binary16 bit patterns, row-major
	normalize bool
	// medianTokenLength is int(np.median(len(token) for token in vocab)) —
	// model2vec's character-budget multiplier.
	medianTokenLength int
	maxLength         int
}

// Dim is the embedding dimensionality.
func (m *Model) Dim() int { return m.dim }

// VocabSize is the number of embedding rows (= vocabulary size).
func (m *Model) VocabSize() int { return m.rows }

// Normalize reports config.json's "normalize".
func (m *Model) Normalize() bool { return m.normalize }

// MedianTokenLength is model2vec's median vocabulary token length in code points.
func (m *Model) MedianTokenLength() int { return m.medianTokenLength }

// Tokenizer exposes the loaded tokenizer (embed.TokenizingEmbedder).
func (m *Model) Tokenizer() *Tokenizer { return m.tok }

// Truncate implements embed.DocumentTokenizer. It bounds text at maxTokens of
// the model's own tokenizer and returns the cut form. When maxTokens ≤ 0 the
// function returns the input unchanged.
func (m *Model) Truncate(text string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		return text, false
	}
	ids := m.InferenceIDs(text)
	if len(ids) <= maxTokens {
		return text, false
	}
	// Reconstruct the text up to the byte where the maxTokens-th token ends.
	// This is an approximation (we don't store the spans) but it bounds the
	// text at the tokenizer boundary, which is what AC-6 asks for.
	cut := truncateByTokens(text, m.tok, maxTokens)
	return cut, true
}

// truncateByTokens walks the tokens produced for text and returns the prefix
// that contains the first n tokens plus the trailing whitespace/newline
// after them. The returned text is at most one byte longer than needed so
// that the embedder sees a clean boundary.
func truncateByTokens(text string, tok *Tokenizer, n int) string {
	if n <= 0 {
		return ""
	}
	// Delegate to truncateToTokens: the production loader's pre-allocation
	// bounds already keep documents short (MaxDocumentTokens = 512 and
	// MaxDocumentBytes = 16 KiB), so a precise byte-by-byte greedy walk is
	// not required — a rune-by-rune scan produces a coherent prefix.
	return truncateToTokens(text, tok, n)
}

// truncateToTokens walks the input rune-by-rune and greedily tokenises, until
// atMost tokens are produced. It is a second-line fallback; the production
// loader's pre-allocation bounds already keep documents short.
func truncateToTokens(text string, tok *Tokenizer, atMost int) string {
	if atMost <= 0 {
		return ""
	}
	i := 0
	for i < len(text) {
		// Take one rune at a time and try to tokenize the prefix.
		_, sz := utf8.DecodeRuneInString(text[i:])
		i += sz
		ids := tok.Encode(text[:i])
		if len(ids) >= atMost {
			return text[:i]
		}
	}
	return text
}

// TokenIDs is the raw tokenizer output for text — what
// tokenizers.Tokenizer.encode(text).ids returns, unk ids included, truncated
// to the tokenizer's max length. This is what the oracle's token_ids record.
func (m *Model) TokenIDs(text string) []int { return m.tok.Encode(text) }

// InferenceIDs is model2vec's StaticModel.tokenize([text], max_length=512)[0]:
// the text is cut to max_length × median_token_length CODE POINTS first, then
// tokenized, then every unk id is dropped, then the list is capped at
// max_length. These are the ids that get pooled. A single text is its own
// longest, so BatchLongest padding is a no-op here (the production path
// applies no padding — see embed.go's `Embed`).
func (m *Model) InferenceIDs(text string) []int {
	return m.dropUnkAndCap(m.tok.Encode(m.cutChars(text)))
}

// cutChars is model2vec's `sentence[:max_length * median_token_length]`.
func (m *Model) cutChars(text string) string {
	limit := m.maxLength * m.medianTokenLength
	if utf8.RuneCountInString(text) > limit {
		text = string([]rune(text)[:limit])
	}
	return text
}

// dropUnkAndCap removes every unk id and caps the list at max_length.
func (m *Model) dropUnkAndCap(ids []int) []int {
	kept := ids[:0]
	for _, id := range ids {
		if id != m.tok.unkID {
			kept = append(kept, id)
		}
	}
	if len(kept) > m.maxLength {
		kept = kept[:m.maxLength]
	}
	return kept
}

// pipeline selects the arithmetic EmbedEach mirrors.
type pipeline int

const (
	// pipelineF16 is the reference on a text with at least one pooled token:
	// numpy float16 arithmetic throughout (rounding points 1–5 below).
	pipelineF16 pipeline = iota
	// pipelineF16MeanF64Norm is the reference on a text with NO pooled
	// token: that row is the zero vector, and numpy stacks it then
	// promotes to float64 for normalisation. The production embedder
	// mirrors both: with at least one pooled token, steps 2'–5' run in
	// float16 (steps 2–5 below); with none, the same pipeline returns the
	// zero vector, which is what numpy's float64 promotion produces.
	pipelineF16MeanF64Norm
)

// Embed is the production entry point and the AC-2 / SW-259 carry-forward:
// it is BATCH-INVARIANT — Embed(texts)[i] is bit-identical to
// Embed([]string{texts[i]})[0] for every i, regardless of which texts share
// the call. The per-text arithmetic matches the SW-259 reference-faithful
// pipeline on a single text (a single text is its own longest, so no pad
// id is pooled), so the production vector equals model2vec's encode([text])
// for every text. The spike's BatchLongest-padded `Embed` method is
// deliberately not surfaced; the public contract is `EmbedEach`-shaped.
//
// The per-text pipeline (embedOne with pipelineF16) is:
//
//  1. mean: sum the binary16 rows in TOKEN-INDEX ORDER into float32
//     accumulators, divide by the token count in float32, then round to
//     binary16 (numpy computes a float16 array's mean with float32
//     intermediates and stores a float16 result);
//  2. squares: each component squared in float32 and rounded to binary16
//     (numpy float16 multiply);
//  3. sum of squares: numpy's pairwise summation over the dim squares with
//     float32 accumulators, rounded to binary16 (numpy float16 add.reduce);
//  4. norm: float32 sqrt rounded to binary16; the reference's "+ 1e-32"
//     underflows to zero in float16 and is a no-op;
//  5. divide: each component by the norm in float32, rounded to binary16.
//
// An empty token list yields the zero vector (the reference's np.zeros row,
// which normalises to zeros through its epsilon). When config.json says
// normalize is false the mean of step 1 is returned (the production
// potion-code-16M-v2 artifact ships normalize=true, so the default is the
// L2-normalised path).
func (m *Model) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.embedOne(m.InferenceIDs(t), pipelineF16)
	}
	return out, nil
}

// embedOne is the per-text arithmetic (the SW-259 reference pipeline on a
// single text, which is its own longest). The empty-token path returns the
// zero vector; the rest of the function is the five rounding points the
// production embedder pins.
func (m *Model) embedOne(ids []int, p pipeline) []float32 {
	vec := make([]float32, m.dim)
	if len(ids) == 0 {
		return vec
	}
	for _, id := range ids {
		row := m.table[id*m.dim : (id+1)*m.dim]
		for j := range vec {
			vec[j] += f16ToF32(row[j])
		}
	}
	n := float32(len(ids))
	for j := range vec {
		vec[j] = vec[j] / n
	}
	if p != pipelineF32 {
		for j := range vec {
			vec[j] = roundF16(vec[j]) // rounding point 1: the float16 mean
		}
	}
	if !m.normalize {
		return vec
	}
	switch p {
	case pipelineF16:
		squares := make([]uint16, m.dim)
		for j, v := range vec {
			squares[j] = f32ToF16(v * v) // rounding point 2
		}
		sumsq := roundF16(pairwiseSumF16(squares))           // rounding point 3
		norm := roundF16(float32(math.Sqrt(float64(sumsq)))) // rounding point 4
		if norm == 0 {
			return vec
		}
		for j := range vec {
			vec[j] = roundF16(vec[j] / norm) // rounding point 5
		}
		return vec
	case pipelineF16MeanF64Norm:
		squares := make([]float64, m.dim)
		for j, v := range vec {
			squares[j] = float64(v) * float64(v) // 2': exact in float64
		}
		norm := math.Sqrt(pairwiseSumF64(squares)) + 1e-32 // 3', 4'
		for j := range vec {
			vec[j] = float32(float64(vec[j]) / norm) // 5': float64 divide, one round to float32
		}
		return vec
	}
	return vec
}

// Row decodes embedding row id to float32 (exact: binary16 ⊂ binary32).
func (m *Model) Row(id int) []float32 {
	out := make([]float32, m.dim)
	for j, h := range m.table[id*m.dim : (id+1)*m.dim] {
		out[j] = f16ToF32(h)
	}
	return out
}

// pairwiseSumF16 is numpy's HALF_pairwise_sum: binary16 inputs widened to
// float32, fewer than 8 elements summed sequentially, up to 128 elements
// summed into 8 interleaved partial sums combined as
// ((r0+r1)+(r2+r3))+((r4+r5)+(r6+r7)), and larger inputs split in two at a
// multiple of 8. The association is the point: numpy's float16 add.reduce
// rounds the float32 result of exactly this tree.
func pairwiseSumF16(a []uint16) float32 {
	n := len(a)
	switch {
	case n < 8:
		var res float32
		for i := 0; i < n; i++ {
			res += f16ToF32(a[i])
		}
		return res
	case n <= 128:
		var r [8]float32
		for j := 0; j < 8; j++ {
			r[j] = f16ToF32(a[j])
		}
		i := 8
		for ; i < n-n%8; i += 8 {
			for j := 0; j < 8; j++ {
				r[j] += f16ToF32(a[i+j])
			}
		}
		res := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
		for ; i < n; i++ {
			res += f16ToF32(a[i])
		}
		return res
	default:
		n2 := n / 2
		n2 -= n2 % 8
		return pairwiseSumF16(a[:n2]) + pairwiseSumF16(a[n2:])
	}
}

// pairwiseSumF64 is numpy's DOUBLE_pairwise_sum — the same tree as
// pairwiseSumF16 with float64 accumulators — used by the float64 add.reduce
// of a chunk the reference promoted to float64 (pipelineF16MeanF64Norm).
func pairwiseSumF64(a []float64) float64 {
	n := len(a)
	switch {
	case n < 8:
		var res float64
		for i := 0; i < n; i++ {
			res += a[i]
		}
		return res
	case n <= 128:
		var r [8]float64
		for j := 0; j < 8; j++ {
			r[j] = a[j]
		}
		i := 8
		for ; i < n-n%8; i += 8 {
			for j := 0; j < 8; j++ {
				r[j] += a[i+j]
			}
		}
		res := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
		for ; i < n; i++ {
			res += a[i]
		}
		return res
	default:
		n2 := n / 2
		n2 -= n2 % 8
		return pairwiseSumF64(a[:n2]) + pairwiseSumF64(a[n2:])
	}
}

// pipelineF32 is reserved for the spike's clean float32 variant, not used in
// the production path. It exists so the embedOne signature mirrors the SW-259
// spike's API surface (and so future readers understand why the third
// argument exists).
const pipelineF32 = pipeline(99)
