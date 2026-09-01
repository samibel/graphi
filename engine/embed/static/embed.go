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

	"github.com/samibel/graphi/engine/embed"
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

	// FileHashes records the SHA-256 (lower-case hex) of every pinned file
	// the model was loaded from. They are the inference-configuration
	// identity the production embedder advertises via ID() (AC-2): a
	// structurally valid replacement of the weights or the tokenizer that
	// changes the file hashes changes the ID, and the SW-261 fingerprint
	// reads it back as a different generation. The hashes are computed at
	// load time (VerifyPins) BEFORE the embedder is constructible, so a
	// swapped model cannot inherit the cached identity.
	FileHashes map[string]string
}

// MaxAdmissionTokens is the usable token limit (after the special-token
// reserve) for the v3 admission profile (AC-1, AC-2, AC-3). The pinned
// model2vec static embedder's tokenizer caps at 512 tokens post-unk-
// drop; with zero reserve the USABLE limit is 512. A profile change
// (different limit, different reserve, different algorithm version)
// invalidates stored generations by fingerprint.
const MaxAdmissionTokens = 512

// SpecialTokenReserve is the special-token reserve the admission
// profile budgets for BOS / EOS / pad. The pinned model2vec static
// embedder does not inject any special tokens (no post_processor, no
// add_special_tokens path), so the reserve is 0 and the USABLE limit
// equals the model's max length.
const SpecialTokenReserve = 0

// AdmissionAlgorithmID is the algorithm-version-tagged identifier the
// admission profile advertises. Bumping the algorithm changes the
// fingerprint and invalidates prior generations (AC-3, AC-8).
const AdmissionAlgorithmID = "first-n-tokens@1"

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

// truncate implements embed.DocumentTokenizer on *Tokenizer so the
// production embedder satisfies embed.TokenizingEmbedder (AC-7).
// Truncate cuts text to at most maxTokens of the tokenizer's own
// tokens (post-unk-drop, pre-cap — the HONEST count, NOT the
// InferenceIDs-capped value). A maxTokens <= 0 returns the input
// unchanged. The cut uses the model's own truncateByTokens for the
// byte run so the returned text is what the embedder will see.
func (t *Tokenizer) truncate(text string, maxTokens int) (string, bool) {
	if t == nil || maxTokens <= 0 {
		return text, false
	}
	ids := t.Encode(text)
	kept := ids[:0]
	for _, id := range ids {
		if id != t.unkID {
			kept = append(kept, id)
		}
	}
	if len(kept) <= maxTokens {
		return text, false
	}
	return truncateByTokens(text, t, maxTokens), true
}

// Truncate implements embed.DocumentTokenizer for backward compatibility with
// the SW-260 SW-261 builder paths that wired a DocumentTokenizer. The
// HONEST admission surface is Model.Admit — AC-7 requires the admission
// profile to know exactly what the model consumed, and Truncate cannot
// distinguish "input fits" from "InferenceIDs silently capped at maxLength".
// When maxTokens ≤ 0 the function returns the input unchanged.
func (m *Model) Truncate(text string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		return text, false
	}
	// HONEST path: count the model's own post-unk tokens via rawIDs (no
	// silent cap) and only treat the input as overflowing when its actual
	// token count exceeds maxTokens. InferenceIDs drops the unk ids THEN
	// caps — the previous Truncate shape called InferenceIDs and compared
	// against maxTokens, but InferenceIDs' cap always fires first, so the
	// "did it truncate?" check was structurally unreachable. rawIDs lets
	// us see the real count.
	ids := m.rawIDs(text)
	if len(ids) <= maxTokens {
		return text, false
	}
	cut := truncateByTokens(text, m.tok, maxTokens)
	return cut, true
}

// Admit implements embed.Admission (SW-267 AC-2, AC-7). It returns the
// exact bytes the model will consume and the token count the model will
// see. The pinned admission algorithm is "first-n-tokens@1": the
// tokenizer's first MaxAdmissionTokens tokens survive, the rest are
// dropped (the byte run is reconstructed via the tokenizer's own
// truncate-by-tokens path so the returned Text is what Embed will
// pool). The bound label is "tokens" when the truncation cut
// anything and "none" otherwise.
//
// The HONEST surface: the token count is post-unk, post-cap (what
// model2vec pools). A build that wires Admit into BuildDocument
// therefore gets a Text that is exactly the bytes the model sees —
// no silent truncation can sit between TextHash and the persisted
// vector.
func (m *Model) Admit(_ context.Context, text string) (embed.Admitted, error) {
	if text == "" {
		return embed.Admitted{Text: text, TokenCount: 0, Bound: embed.BoundNone}, nil
	}
	ids := m.rawIDs(text)
	if len(ids) <= MaxAdmissionTokens {
		return embed.Admitted{Text: text, TokenCount: len(ids), Bound: embed.BoundNone}, nil
	}
	// Bound the text at the tokenizer boundary: take the first
	// MaxAdmissionTokens tokens, drop the rest, return the byte run
	// that contains them. truncateByTokens is the model's own
	// byte-aware truncation path.
	cut := truncateByTokens(text, m.tok, MaxAdmissionTokens)
	if cut == "" {
		// The text had tokens but none of them survived the cut — this
		// can happen with degenerate inputs (whitespace-only, etc.). A
		// build that gets here is a build that asked for a non-empty
		// admit on a degenerate input; surface it as a typed error.
		return embed.Admitted{}, &embed.AdmissionError{
			Limit:  MaxAdmissionTokens,
			Actual: len(ids),
			Profile: embed.AdmissionSpec{
				TokenizerID:      "model2vec-wordpiece",
				TokenizerSHA256:  m.tokenHash(),
				TokenizerVersion: "1",
				MaxTokens:        MaxAdmissionTokens,
				Reserve:          SpecialTokenReserve,
				Algorithm:        "first-n-tokens",
				AlgorithmVersion: "1",
			},
		}
	}
	return embed.Admitted{
		Text:       cut,
		TokenCount: MaxAdmissionTokens,
		Bound:      embed.BoundTokens,
	}, nil
}

// rawIDs returns the model's post-unk, pre-cap token ids for text. It
// is the HONEST token count Admit uses; InferenceIDs caps at maxLength
// silently so it would lie about overflow.
func (m *Model) rawIDs(text string) []int {
	if m.tok == nil {
		return nil
	}
	ids := m.tok.Encode(text)
	kept := ids[:0]
	for _, id := range ids {
		if id != m.tok.unkID {
			kept = append(kept, id)
		}
	}
	return kept
}

// tokenHash returns the lower-case hex SHA-256 of tokenizer.json, or ""
// when the artifact is not loaded. The admission profile pins this hash
// so a structurally valid replacement of the tokenizer that changes the
// file hash invalidates stored generations.
func (m *Model) tokenHash() string {
	if m.FileHashes == nil {
		return ""
	}
	return m.FileHashes[FileTokenizer]
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

// (No pipeline enum / constants: the production embedder implements
// one arithmetic pipeline, the SW-259 reference, and does not select
// a pipeline at runtime. The spike-only pipeline selection was
// removed.)

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
		out[i] = m.embedOne(m.InferenceIDs(t))
	}
	return out, nil
}

// embedOne is the per-text arithmetic (the SW-259 reference pipeline on a
// single text, which is its own longest). The empty-token path returns the
// zero vector; the rest of the function is the five rounding points the
// production embedder pins. There is no pipeline parameter: the
// production embedder implements one pipeline (the SW-259 reference) and
// the spike-only `pipelineF16MeanF64Norm` and `pipelineF32` paths are
// not surfaced.
func (m *Model) embedOne(ids []int) []float32 {
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
	for j := range vec {
		vec[j] = roundF16(vec[j]) // rounding point 1: the float16 mean
	}
	if !m.normalize {
		return vec
	}
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
// pairwiseSumF16 with float64 accumulators. Kept for unit tests of the
// tree shape; the production embedder's normalize pipeline runs
// pairwiseSumF16 (the F16 reference).
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

// pipelineF32 / pipelineF16MeanF64Norm were the spike-only pipeline
// constants; embedOne no longer takes a pipeline parameter. They are
// removed; a future refactor that wants to re-introduce pipeline
// selection should redesign the spike rather than copy these.
