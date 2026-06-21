package analysis

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is the concurrency-safe mapping from analyzer name to Analyzer. It
// mirrors the parser registry pattern in core/parse/registry.go so analyzers
// are discoverable and selectable uniformly. A registered analyzer is immutable
// for the lifetime of the registry (the same plug-in contract the parse registry
// enforces): duplicate registration of a name is rejected with an error rather
// than silently overwriting.
type Registry struct {
	mu        sync.RWMutex
	analyzers map[string]Analyzer
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{analyzers: map[string]Analyzer{}}
}

// Register adds a to the registry under a.Name(). It returns an error if a is
// nil, has an empty name, or a name is already registered. Register never
// panics and leaves the registry consistent.
func (r *Registry) Register(a Analyzer) error {
	if a == nil {
		return fmt.Errorf("analysis: register nil analyzer")
	}
	name := a.Name()
	if name == "" {
		return fmt.Errorf("analysis: register analyzer with empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.analyzers[name]; exists {
		return fmt.Errorf("analysis: analyzer %q already registered", name)
	}
	r.analyzers[name] = a
	return nil
}

// Get returns the analyzer registered under name and whether one exists.
func (r *Registry) Get(name string) (Analyzer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.analyzers[name]
	return a, ok
}

// Names returns the sorted, deduplicated list of registered analyzer names.
// Sorting makes the list deterministic across runs (surfaces advertise tools
// from this list, so stable ordering keeps MCP tool listings byte-stable).
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.analyzers))
	for name := range r.analyzers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
