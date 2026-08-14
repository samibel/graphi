package typeresolve

// The semantic-resolver registry (ADR 0007, language-GA program WP-J0): the
// open/closed seam behind which per-language semantic resolvers plug into the
// third ingest phase. engine/ingest dispatches the pass over this registry
// instead of hard-coding Go, and the trust surface derives `typed-confirmed`
// from the registry union. A new language is a new Register call in
// NewRegistry — never an edit to an existing resolver (the engine/link.New /
// core/parse defaults.go registry shape).
//
// Every registrant inherits this package's hard constraints (doc.go): pure Go,
// stdlib preference, no network, no external toolchain, CGo-free,
// deterministic — and the tier-honesty contract (check.go): partial
// information may only DROP edges (skip+count), never mint false ones.

import (
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// Resolver is one language's semantic (confirmed-tier) resolution pass. The
// three path predicates carve up the roles a repository path can play for the
// pass, because they are genuinely three different sets (Go: go.mod is Input
// and Triggers but never Subject; _test.go is Input only):
type Resolver interface {
	// Language is the canonical parser language id (core/parse vocabulary)
	// whose relationships this resolver can raise to the confirmed tier.
	Language() string
	// Subject reports whether relPath is a subject file: the ingest pass runs
	// this resolver only when the walked snapshot contains at least one
	// subject. A configuration file alone is never a subject — a repo with a
	// go.mod and no Go sources has nothing to check.
	Subject(relPath string) bool
	// Input reports whether relPath belongs in the resolver's input file map —
	// a superset of Subject plus whatever steers resolution without being
	// checked itself.
	Input(relPath string) bool
	// Triggers reports whether a CHANGE to relPath can change the resolution
	// result — the incremental site's gate for re-running the whole-repo pass.
	Triggers(relPath string) bool
	// Resolve turns the snapshot in files into confirmed-tier edges whose
	// endpoints exist in committed. It must be pure and deterministic:
	// identical inputs yield byte-identical results (the property the
	// full-vs-incremental parity design leans on).
	Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (Result, error)
}

// goResolver adapts this package's go/types pass to the Resolver seam — the
// first registrant (WP-J0). Its predicates are the exact path gates the ingest
// pass applied inline before the seam existed; they are pinned by
// TestGoResolver_PathPredicates so the extraction cannot drift.
type goResolver struct{}

// Language implements Resolver.
func (goResolver) Language() string { return "go" }

// Subject implements Resolver: a non-test Go source file. Test files are
// deliberately not subjects — the typeresolve grouping skips them in v1.
func (goResolver) Subject(relPath string) bool {
	return strings.HasSuffix(relPath, ".go") && !strings.HasSuffix(relPath, "_test.go")
}

// Input implements Resolver: every Go source INCLUDING _test.go — their PATHS
// steer GroupPackages' skip bookkeeping — plus go.mod, whose module path
// steers intra-repo import resolution.
func (goResolver) Input(relPath string) bool {
	return relPath == "go.mod" || strings.HasSuffix(relPath, ".go")
}

// Triggers implements Resolver: a non-test Go source or go.mod. Test files
// cannot change the result (the grouping skips them in v1) — but a rename
// between _test and non-test arrives as the non-test path anyway, so plain
// suffix matching stays correct and cheap.
func (goResolver) Triggers(relPath string) bool {
	return relPath == "go.mod" || (strings.HasSuffix(relPath, ".go") && !strings.HasSuffix(relPath, "_test.go"))
}

// Resolve implements Resolver via this package's go/types pass.
func (goResolver) Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (Result, error) {
	return Resolve(files, committed)
}

// Registry is the ordered, open/closed set of semantic resolvers. Dispatch
// order is registration order (deterministic); Languages() is the sorted union
// the trust surface consumes.
type Registry struct {
	order  []string
	byLang map[string]Resolver
}

// NewRegistry returns the registry of shipped semantic resolvers. A new
// language is a new Register call here.
func NewRegistry() *Registry {
	r := &Registry{byLang: map[string]Resolver{}}
	r.Register(goResolver{})
	return r
}

// Register adds a resolver under its language. Later registrations override an
// earlier one for the same language (open/closed extension point), keeping the
// original registration position so dispatch order stays deterministic.
func (r *Registry) Register(res Resolver) {
	lang := res.Language()
	if _, dup := r.byLang[lang]; !dup {
		r.order = append(r.order, lang)
	}
	r.byLang[lang] = res
}

// Resolvers returns the registered resolvers in registration order — the
// dispatch order of the ingest pass.
func (r *Registry) Resolvers() []Resolver {
	out := make([]Resolver, 0, len(r.order))
	for _, lang := range r.order {
		out = append(out, r.byLang[lang])
	}
	return out
}

// Languages returns the registered language ids as a fresh, sorted copy.
func (r *Registry) Languages() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}
