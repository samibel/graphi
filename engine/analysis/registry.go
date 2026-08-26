package analysis

import (
	"fmt"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/registry"
)

// CollisionPolicy is this registry's DECLARED collision rule (SW-222 / AX-02):
// FIRST-WINS, with one narrow sanctioned override.
//
//   - Register refuses a duplicate name with a registry.ErrDuplicate-typed
//     error; the first registration keeps the slot. A duplicate analyzer name
//     is a programming fault, not an extension point.
//   - Replace overrides an ALREADY-REGISTERED name — the git-provider seam — and
//     refuses an unknown one (registry.ErrMissingDependency) so it cannot become
//     a second registration path.
//
// This is the deliberate OPPOSITE of core/parse.CollisionPolicy (last-wins).
// ADR 0013 threat T5 records the divergence and keeps it: both rules are correct
// for their own package, and AX-02's job was to make each one declared rather
// than to pick a winner.
const CollisionPolicy = registry.PolicyFirstWinsWithReplace

// Registry is the concurrency-safe mapping from analyzer name to Analyzer. It
// mirrors the parser registry pattern in core/parse/registry.go so analyzers
// are discoverable and selectable uniformly. A registered analyzer is immutable
// for the lifetime of the registry apart from the Replace seam below:
// duplicate registration of a name is rejected with an error rather than
// silently overwriting.
//
// Lifecycle (SW-222): Register → Replace → Freeze → Execute. NewRegistry returns
// an UNFROZEN registry, because composition here is not finished when the
// registry is built — Service.WithGitProvider re-arms analyzers afterwards. The
// composition root calls Service.Freeze when it is done.
type Registry struct {
	life      registry.Lifecycle
	mu        sync.RWMutex
	analyzers map[string]Analyzer
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{analyzers: map[string]Analyzer{}}
}

// Policy reports this registry's declared collision policy (CollisionPolicy).
func (r *Registry) Policy() registry.Policy { return CollisionPolicy }

// Freeze marks composition complete: from here on Register and Replace mutate
// nothing and return a registry.ErrFrozen-typed error. Idempotent and one-way.
func (r *Registry) Freeze() { r.life.Freeze() }

// Frozen reports whether Freeze has been called.
func (r *Registry) Frozen() bool { return r.life.Frozen() }

// Register adds a to the registry under a.Name(). It returns an error if a is
// nil, has an empty name, a name is already registered (registry.ErrDuplicate),
// or the registry is frozen (registry.ErrFrozen). Register never panics and
// leaves the registry consistent.
func (r *Registry) Register(a Analyzer) error {
	if a == nil {
		return fmt.Errorf("analysis: register nil analyzer")
	}
	name := a.Name()
	if name == "" {
		return fmt.Errorf("analysis: register analyzer with empty name")
	}
	if err := r.life.CheckMutable("analysis", "Register", name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.analyzers[name]
	if err := registry.GuardDuplicate(CollisionPolicy, "analysis", "analyzer", name, exists); err != nil {
		return err
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

// Replace swaps the analyzer registered under a.Name() for a. Unlike Register
// it REQUIRES the name to already exist: it exists solely to re-arm a
// registered analyzer with an injected dependency (the git-provider seam), and
// refusing unknown names keeps it from becoming a second registration path.
// An unknown name is a registry.ErrMissingDependency-typed error; a frozen
// registry is a registry.ErrFrozen-typed error.
func (r *Registry) Replace(a Analyzer) error {
	if a == nil {
		return fmt.Errorf("analysis: replace nil analyzer")
	}
	name := a.Name()
	if name == "" {
		return fmt.Errorf("analysis: replace analyzer with empty name")
	}
	if err := r.life.CheckMutable("analysis", "Replace", name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.analyzers[name]
	if err := registry.GuardReplace(CollisionPolicy, "analysis", "analyzer", name, exists); err != nil {
		return err
	}
	r.analyzers[name] = a
	return nil
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
