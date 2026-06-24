package embed

import (
	"context"
	"math"

	"github.com/cespare/xxhash/v2"
)

// MockEmbedder is a DETERMINISTIC, dependency-free embedder used by tests across
// packages (engine/embed, engine/search, surfaces parity) to exercise the
// configured semantic path without any network, CGO, or model files. It is
// hash-seeded: identical input text always yields value-identical vectors
// (SW-059 determinism requirement), and it holds no global or mutable state.
//
// It is exported (not _test.go) ONLY so sibling test packages can construct it;
// it is never registered by RegisterDefaults and never reaches the default build
// path. It is pure Go with no heavy deps, so it is a no-op for the CGo-free and
// zero-egress gates.
type MockEmbedder struct {
	dim int
}

// NewMockEmbedder returns a deterministic mock embedder of the given dimension.
// A non-positive dim defaults to 8.
func NewMockEmbedder(dim int) *MockEmbedder {
	if dim <= 0 {
		dim = 8
	}
	return &MockEmbedder{dim: dim}
}

// ID implements Embedder.
func (m *MockEmbedder) ID() string { return "mock" }

// Dim implements Embedder.
func (m *MockEmbedder) Dim() int { return m.dim }

// Embed implements Embedder. Each text is mapped to a fixed-dim, L2-normalized
// vector derived purely from a per-component xxhash64 of (component-index, text),
// so the mapping is total, deterministic, and stable across runs/processes.
func (m *MockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.vector(t)
	}
	return out, nil
}

// vector derives the deterministic unit vector for a single text.
func (m *MockEmbedder) vector(text string) []float32 {
	v := make([]float32, m.dim)
	var sumSq float64
	for c := 0; c < m.dim; c++ {
		d := xxhash.New()
		// Domain-separate each component by its index so components differ.
		var idx [4]byte
		idx[0] = byte(c)
		idx[1] = byte(c >> 8)
		idx[2] = byte(c >> 16)
		idx[3] = byte(c >> 24)
		_, _ = d.Write(idx[:])
		_, _ = d.Write([]byte(text))
		// Map the 64-bit digest into [-1, 1] deterministically.
		h := d.Sum64()
		f := float64(h)/float64(math.MaxUint64)*2 - 1
		v[c] = float32(f)
		sumSq += f * f
	}
	// L2-normalize so cosine similarity is well-conditioned; a zero vector
	// (vanishingly unlikely) is left as-is.
	norm := math.Sqrt(sumSq)
	if norm > 0 {
		for c := range v {
			v[c] = float32(float64(v[c]) / norm)
		}
	}
	return v
}
