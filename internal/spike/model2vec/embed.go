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
// max_length. These are the ids that get pooled.
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

// Embed returns one vector per text, in input order, reproducing the Python
// reference (model2vec 0.9.0 + numpy 2.x on a float16 embedding table) to the
// bit. Each text is independent of the others in the batch. The per-text
// pipeline (embedOne with mirrorF16 = true) is:
//
//  1. mean: sum the binary16 rows in TOKEN-INDEX ORDER into float32
//     accumulators, divide by the token count in float32, then round to
//     binary16 (numpy computes a float16 array's mean with float32
//     intermediates and stores a float16 result);
//  2. squares: each component squared in float32 and rounded to binary16
//     (numpy float16 multiply);
//  3. sum of squares: numpy's pairwise summation over the 256 squares with
//     float32 accumulators (blocks of 8 partial sums, halves above 128
//     elements), rounded to binary16 (numpy float16 add.reduce);
//  4. norm: float32 sqrt rounded to binary16; the reference's "+ 1e-32"
//     underflows to zero in float16 and is a no-op;
//  5. divide: each component by the norm in float32, rounded to binary16.
//
// An empty token list yields the zero vector (the reference's np.zeros row,
// which normalises to zeros through its epsilon). When config.json says
// normalize is false the mean of step 1 is returned.
func (m *Model) Embed(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.embedOne(m.InferenceIDs(t), true)
	}
	return out
}

// EmbedFloat32 is the "clean" pipeline SW-262 would naturally write — the same
// token-index-ordered float32 mean, then a float32 L2 normalisation with NO
// binary16 rounding anywhere. It is measured against the oracle for the record
// (so the cost of NOT mirroring the reference's float16 storage is a number),
// but it is not what Embed returns.
func (m *Model) EmbedFloat32(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.embedOne(m.InferenceIDs(t), false)
	}
	return out
}

func (m *Model) embedOne(ids []int, mirrorF16 bool) []float32 {
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
	if mirrorF16 {
		for j := range vec {
			vec[j] = roundF16(vec[j])
		}
	}
	if !m.normalize {
		return vec
	}
	if mirrorF16 {
		squares := make([]uint16, m.dim)
		for j, v := range vec {
			squares[j] = f32ToF16(float32(v * v))
		}
		sumsq := roundF16(float32(0) + pairwiseSumF16(squares))
		norm := roundF16(float32(math.Sqrt(float64(sumsq))))
		if norm == 0 {
			return vec
		}
		for j := range vec {
			vec[j] = roundF16(vec[j] / norm)
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
