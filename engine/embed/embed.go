// Package embed is graphi's OPTIONAL, provider-agnostic text-embedding seam.
//
// Layering: embed is an engine leaf. It consumes only the Go standard library
// (plus core/model for NodeId in the sibling vector store) and MUST NOT import
// surfaces/ or cmd/. It owns the Embedder contract and an open/closed Registry
// whose ZERO VALUE is the graceful-skip state.
//
// Local-first / CGo-free contract (SW-059, resolves OQ6): the DEFAULT build
// registers NOTHING here, so semantic search is OFF by default — `Configured()`
// is false and `Active()` returns (nil, false). No embedder is constructed, no
// network is dialed, and no CGO is pulled into the default binary. A configured
// embedder is strictly opt-in via explicit config (e.g. `GRAPHI_EMBEDDER=...`)
// resolved through Constructor, which returns a graceful-skip (nil, nil) when the
// selector is empty or unknown. Network embedders (Ollama) are loopback-only and
// constructed only on the explicit opt-in path; the CGO ONNX embedder lives
// behind `//go:build embed_onnx` and never enters the default graph.
package embed

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Embedder is the provider-agnostic text-embedding contract. Implementations
// must be deterministic: identical input text yields value-identical vectors
// (SW-059 determinism requirement). Implementations must NOT dial any
// non-loopback host.
type Embedder interface {
	// ID returns a stable, human-readable identifier for the embedder
	// (e.g. "mock", "ollama:nomic-embed-text"). It is recorded in diagnostics.
	ID() string
	// Dim returns the fixed dimensionality of every vector this embedder emits.
	Dim() int
	// Embed maps each input text to a Dim()-length vector. The returned slice has
	// one vector per input, in input order. An empty input slice returns an empty
	// (non-nil) result and no error.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Registry is the open/closed selection point for the embed boundary, mirroring
// core/parse.Registry. It maps a lowercase embedder ID to its Embedder and tracks
// which one is Active.
//
// The ZERO Registry is the graceful-skip state: no embedder is registered, none
// is active, Configured() reports false, and Active() returns (nil, false). The
// default build constructs exactly this — RegisterDefaults registers NOTHING — so
// semantic search is OFF until an embedder is explicitly opted in.
//
// Register and the lookups are safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	byID   map[string]Embedder
	active string // lowercase ID of the active embedder; "" when none
}

// NewRegistry returns an empty, ready-to-use Registry. The zero Registry is also
// valid and equivalent (the lazy map init covers both).
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds e to the registry indexed by its (lowercased) ID and marks it as
// the active embedder. A later Register overrides the active selection, allowing
// an opt-in backend to supersede a prior one. Registering nil, or an embedder
// with an empty ID, is a no-op.
//
// Register never panics and leaves the registry consistent.
func (r *Registry) Register(e Embedder) {
	if e == nil {
		return
	}
	id := strings.ToLower(strings.TrimSpace(e.ID()))
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = make(map[string]Embedder)
	}
	r.byID[id] = e
	r.active = id
}

// Configured reports whether an embedder is active. The zero Registry reports
// false (graceful-skip). This is the single predicate engine/search consults to
// decide between the semantic path and the typed Unavailable response.
func (r *Registry) Configured() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active != "" && r.byID[r.active] != nil
}

// Active returns the active embedder and true, or (nil, false) when none is
// configured (the graceful-skip state). It never constructs or dials anything.
func (r *Registry) Active() (Embedder, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.active == "" {
		return nil, false
	}
	e, ok := r.byID[r.active]
	if !ok || e == nil {
		return nil, false
	}
	return e, true
}

// IDs returns the sorted set of registered embedder IDs. Useful for diagnostics
// and tests; the returned slice is a copy.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}
