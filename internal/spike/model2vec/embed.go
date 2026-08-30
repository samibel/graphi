package model2vec

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"
)

// Artifact file names of a Model2Vec directory layout.
const (
	FileConfig      = "config.json"
	FileTokenizer   = "tokenizer.json"
	FileSafetensors = "model.safetensors"
	FileModules     = "modules.json"

	// tensorName is the embedding table's key in model.safetensors for the
	// Model2Vec layout (the sentence-transformers layout uses another name and
	// a subfolder; not implemented).
	tensorName = "embeddings"

	// DefaultMaxLength is model2vec's StaticModel.encode(max_length=512): the
	// token cap applied after unk removal, and (× median token length) the
	// character cap applied before tokenisation.
	DefaultMaxLength = 512

	// DefaultBatchSize is model2vec's StaticModel.encode(batch_size=1024): the
	// reference tokenises — and therefore pads — texts in consecutive chunks
	// of this many, so a text's padding depends only on its own chunk. (The
	// multiprocessing path above 10,000 texts uses the same chunks and
	// concatenates in order; it changes nothing numerically.)
	DefaultBatchSize = 1024
)

// modelConfig is config.json. Model2Vec reads only "normalize" (default false).
type modelConfig struct {
	Normalize      *bool  `json:"normalize"`
	EmbeddingDtype string `json:"embedding_dtype"`
}

// Model is a loaded potion-style static embedding model.
type Model struct {
	// Dir is the artifact directory the model was loaded from.
	Dir string
	// ArtifactBytes is the total size of the four pinned files.
	ArtifactBytes int64

	tok       *Tokenizer
	rows, dim int
	table     []uint16 // rows × dim binary16 bit patterns, row-major
	normalize bool
	// medianTokenLength is int(np.median(len(token) for token in vocab)) —
	// model2vec's character-budget multiplier.
	medianTokenLength int
	maxLength         int
	batchSize         int
}

// LoadModel reads config.json, tokenizer.json and model.safetensors from dir.
func LoadModel(dir string) (*Model, error) {
	cfgRaw, err := os.ReadFile(filepath.Join(dir, FileConfig))
	if err != nil {
		return nil, err
	}
	var cfg modelConfig
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", filepath.Join(dir, FileConfig), err)
	}
	tok, err := LoadTokenizer(filepath.Join(dir, FileTokenizer))
	if err != nil {
		return nil, err
	}
	rows, dim, table, err := loadF16Matrix(filepath.Join(dir, FileSafetensors), tensorName)
	if err != nil {
		return nil, err
	}
	if rows != tok.VocabSize() {
		return nil, fmt.Errorf("model %s: %d embedding rows for %d vocabulary tokens", dir, rows, tok.VocabSize())
	}
	m := &Model{
		Dir:       dir,
		tok:       tok,
		rows:      rows,
		dim:       dim,
		table:     table,
		maxLength: DefaultMaxLength,
		batchSize: DefaultBatchSize,
	}
	if cfg.Normalize != nil {
		m.normalize = *cfg.Normalize
	}
	lengths := make([]int, rows)
	for id := range lengths {
		lengths[id] = utf8.RuneCountInString(tok.Token(id))
	}
	sort.Ints(lengths)
	if rows%2 == 1 {
		m.medianTokenLength = lengths[rows/2]
	} else {
		// np.median averages the two middle values; int() truncates.
		m.medianTokenLength = int(float64(lengths[rows/2-1]+lengths[rows/2]) / 2)
	}
	for _, name := range []string{FileConfig, FileTokenizer, FileSafetensors, FileModules} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
			m.ArtifactBytes += st.Size()
		}
	}
	return m, nil
}

// Dim is the embedding dimensionality.
func (m *Model) Dim() int { return m.dim }

// VocabSize is the number of embedding rows (= vocabulary size).
func (m *Model) VocabSize() int { return m.rows }

// Normalize reports config.json's "normalize".
func (m *Model) Normalize() bool { return m.normalize }

// MedianTokenLength is model2vec's median vocabulary token length in code points.
func (m *Model) MedianTokenLength() int { return m.medianTokenLength }

// Tokenizer exposes the loaded tokenizer.
func (m *Model) Tokenizer() *Tokenizer { return m.tok }

// Row decodes embedding row id to float32 (exact: binary16 ⊂ binary32).
func (m *Model) Row(id int) []float32 {
	out := make([]float32, m.dim)
	for j, h := range m.table[id*m.dim : (id+1)*m.dim] {
		out[j] = f16ToF32(h)
	}
	return out
}

// TokenIDs is the raw tokenizer output for text — what
// tokenizers.Tokenizer.encode(text).ids returns, unk ids included, truncated
// to the tokenizer's max length. This is what the oracle's token_ids record.
func (m *Model) TokenIDs(text string) []int { return m.tok.Encode(text) }

// InferenceIDs is model2vec's StaticModel.tokenize([text], max_length=512)[0]:
// the text is cut to max_length × median_token_length CODE POINTS first, then
// tokenized, then every unk id is dropped, then the list is capped at
// max_length. These are the ids that get pooled. A single text is its own
// longest, so BatchLongest padding is a no-op here; InferenceIDsBatch is the
// multi-text form.
func (m *Model) InferenceIDs(text string) []int {
	return m.dropUnkAndCap(m.tok.Encode(m.cutChars(text)))
}

// InferenceIDsBatch is model2vec's StaticModel.tokenize(texts, max_length=512)
// over one batch: each text is cut to max_length × median_token_length code
// points, the batch is encoded with Tokenizer.EncodeBatch (truncation, then
// BatchLongest padding with the pad id from tokenizer.json), then every unk
// id is dropped from every list and each list is capped at max_length. Pad
// ids are NOT dropped — the reference removes only the unk id — which is
// why a padded text pools the pad row into its mean.
func (m *Model) InferenceIDsBatch(texts []string) [][]int {
	cut := make([]string, len(texts))
	for i, t := range texts {
		cut[i] = m.cutChars(t)
	}
	ids := m.tok.EncodeBatch(cut)
	for i := range ids {
		ids[i] = m.dropUnkAndCap(ids[i])
	}
	return ids
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

// pipeline selects the arithmetic embedOne mirrors.
type pipeline int

const (
	// pipelineF16 is the reference on a chunk whose texts all have at least
	// one pooled token: numpy float16 arithmetic throughout (rounding points
	// 1–5 below).
	pipelineF16 pipeline = iota
	// pipelineF16MeanF64Norm is the reference on a chunk in which at least one
	// text has NO pooled token: that text's row is np.zeros(dim) — float64 —
	// and np.stack promotes the whole chunk to float64, so every row of the
	// chunk keeps its float16-rounded mean (step 1) but is normalised in
	// float64 (steps 2'–5'), then cast to float32 by the caller of the
	// reference.
	pipelineF16MeanF64Norm
	// pipelineF32 is the "clean" float32 pipeline with no float16 rounding
	// after the table lookup (EmbedFloat32; recorded, not adopted).
	pipelineF32
)

// Embed is REFERENCE-FAITHFUL: it returns what
// model2vec.StaticModel.encode(texts) returns (model2vec 0.9.0 + numpy 2.x on
// a float16 embedding table, cast to float32), to the bit for every text of
// the oracle batch. That includes the reference's batch behaviour:
//
//   - texts are processed in consecutive chunks of DefaultBatchSize (1024);
//   - within a chunk, tokenizer.json's padding (BatchLongest, Right, pad_id
//     read from the file) pads every text to the chunk's longest encoding
//     and the pad ids are pooled — so a text's vector depends on the other
//     texts in its chunk (see PINNED.md and the record §2.1); and
//   - a chunk containing a text with no pooled token is normalised in float64
//     (the reference stacks a float64 np.zeros row for it).
//
// Batch-INVARIANT vectors — the reference's encode([text]) for each text,
// which SW-262 is recommended to adopt — are EmbedEach.
//
// The per-text pipeline (embedOne with pipelineF16) is:
//
//  1. mean: sum the binary16 rows in TOKEN-INDEX ORDER (pad rows last, as
//     right padding places them) into float32 accumulators, divide by the
//     token count in float32, then round to binary16 (numpy computes a
//     float16 array's mean with float32 intermediates and stores a float16
//     result);
//  2. squares: each component squared in float32 and rounded to binary16
//     (numpy float16 multiply);
//  3. sum of squares: numpy's pairwise summation over the 256 squares with
//     float32 accumulators (blocks of 8 partial sums, halves above 128
//     elements), rounded to binary16 (numpy float16 add.reduce);
//  4. norm: float32 sqrt rounded to binary16; the reference's "+ 1e-32"
//     underflows to zero in float16 and is a no-op;
//  5. divide: each component by the norm in float32, rounded to binary16.
//
// With pipelineF16MeanF64Norm, step 1 is unchanged and steps 2–5 become:
// 2'. squares in float64 (exact for binary16 values); 3'. numpy's pairwise
// summation in float64; 4'. float64 sqrt plus 1e-32; 5'. float64 division,
// then a single round to float32 (the oracle's np.float32 cast).
//
// An empty token list yields the zero vector (the reference's np.zeros row,
// which normalises to zeros through its epsilon). When config.json says
// normalize is false the mean of step 1 is returned.
func (m *Model) Embed(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += m.batchSize {
		end := min(start+m.batchSize, len(texts))
		ids := m.InferenceIDsBatch(texts[start:end])
		p := pipelineF16
		for _, l := range ids {
			if len(l) == 0 {
				p = pipelineF16MeanF64Norm
				break
			}
		}
		for i, l := range ids {
			out[start+i] = m.embedOne(l, p)
		}
	}
	return out
}

// EmbedEach is the BATCH-INVARIANT form: EmbedEach(texts)[i] is bit-identical
// to Embed([]string{texts[i]})[0] — the reference's encode([text]) — for
// every i, regardless of which texts share the call. A single text is the
// longest of its own batch, so no pad id is pooled, and it can only be
// normalised in float16 (or be the zero vector). This is the semantics
// recommended for SW-262 (spec determinism decision 4: a node's vector must
// not depend on which other nodes share its embedding chunk); the divergence
// from Embed is exactly the padding effect and is measured by
// TestEmbedEach_DivergenceFromEmbedIsPadding.
func (m *Model) EmbedEach(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.embedOne(m.InferenceIDs(t), pipelineF16)
	}
	return out
}

// EmbedFloat32 is the "clean" pipeline SW-262 would naturally write — the same
// token-index-ordered float32 mean, then a float32 L2 normalisation with NO
// binary16 rounding anywhere, per text (batch-invariant). It is measured
// against the oracle for the record (so the cost of NOT mirroring the
// reference's float16 storage is a number), but it is not what Embed returns.
func (m *Model) EmbedFloat32(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.embedOne(m.InferenceIDs(t), pipelineF32)
	}
	return out
}

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
			squares[j] = f32ToF16(float32(v * v)) // rounding point 2
		}
		sumsq := roundF16(float32(0) + pairwiseSumF16(squares)) // rounding point 3
		norm := roundF16(float32(math.Sqrt(float64(sumsq))))    // rounding point 4
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
	var sumsq float32
	for _, v := range vec {
		sumsq += float32(v * v)
	}
	norm := float32(math.Sqrt(float64(sumsq)))
	if norm == 0 {
		return vec
	}
	for j := range vec {
		vec[j] = vec[j] / norm
	}
	return vec
}

// pairwiseSumF16 is numpy's HALF_pairwise_sum (numpy/_core/src/umath/
// loops_utils.h.src): binary16 inputs widened to float32, fewer than 8
// elements summed sequentially, up to 128 elements summed into 8 interleaved
// partial sums combined as ((r0+r1)+(r2+r3))+((r4+r5)+(r6+r7)), and larger
// inputs split in two at a multiple of 8. The association is the point:
// numpy's float16 add.reduce rounds the float32 result of exactly this tree.
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
