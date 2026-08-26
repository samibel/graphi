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
	"github.com/samibel/graphi/core/registry"
)

// CollisionPolicy is this registry's DECLARED collision rule (SW-222 / AX-02):
// LAST-WINS, and ORDER-STABLE with it. A later registration for the same
// language supersedes the earlier resolver but keeps its original dispatch
// position, because dispatch order is registration order and the ingest pass
// must stay byte-deterministic.
//
// Like core/parse and unlike engine/analysis, this is a last-wins seam — the
// kind ADR 0013 (threat T5, D5.3) keeps closed to third-party registrants,
// since a registration here also moves the published `typed-confirmed`
// capability level the trust surface derives from Languages().
const CollisionPolicy = registry.PolicyLastWins

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
	// Owns reports whether a node sourced at relPath belongs to THIS
	// resolver's sweep domain — the LANGUAGE half of the (directory,
	// language) stale-confirmed sweep key (ADR 0008 ruling D9).
	//
	// WHY IT EXISTS AT ALL. engine/ingest sweeps a stored confirmed edge as
	// stale when the pass no longer emits it and the edge's FROM-node sits in
	// a unit this pass checked. Keyed on the directory ALONE that question is
	// unanswerable in a directory holding more than one language: the java
	// registrant, told only "this directory checked clean", would delete the
	// kotlin registrant's confirmed edges out of the same directory. The
	// pre-D9 code avoided that by EXEMPTING mixed directories from the sweep
	// entirely, which is worse — an exemption is unobservable (no unit, no
	// counter, no diagnostic) and it kept superseded confirmed edges alive
	// forever. Owns makes the sweep unit (directory, language) instead: a
	// mixed directory becomes two units rather than one exemption, and every
	// unit is swept.
	//
	// CONTRACT. Owns is a pure function of the path, it must be a SUPERSET of
	// Subject (a file this resolver checks is a file whose nodes it owns), and
	// the registrants' Owns sets must be pairwise DISJOINT — two resolvers
	// claiming one file would each sweep the other's edges, which is the
	// defect D9 removes. Both properties are pinned by
	// engine/semantic's TestRegistry_OwnsIsDisjointAndCoversSubject, which is
	// where they CAN be pinned: engine/semantic is the only package that holds
	// every registrant at once (typeresolve importing jvmresolve would be a
	// cycle — see that package's doc comment).
	Owns(relPath string) bool
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

// Owns implements Resolver: every Go source file, test files INCLUDED. It is
// deliberately WIDER than Subject: _test.go is not checked, but a node sourced
// there is still Go's, and no other registrant may claim it.
//
// WHAT THIS NARROWS, STATED RATHER THAN LEFT TO BE DISCOVERED. Before D9 the
// Go sweep was keyed on the directory alone, so it swept confirmed
// calls/references/implements edges out of ANY file in a checked Go directory,
// whatever its extension. Under the shipped default that set is empty: those
// three edge kinds reach the confirmed tier only from a registered semantic
// resolver (the linker never returns TierConfirmed — engine/link/link.go:60 —
// and the only other confirmed-tier producers are `defines` and
// `notebook_cell`, neither of which this sweep touches), and with
// GRAPHI_JVM_TYPERESOLVE unset go/types is the only registrant. The measured
// delta on the shipped default is therefore ZERO, and it is measured, not
// assumed: TestGoResolver_OwnsNarrowingIsUnobservable pins the reasoning's
// load-bearing half, and SW-170 recorded byte-identical conformance snapshots
// and an identical real-repository graph digest across this change.
func (goResolver) Owns(relPath string) bool { return strings.HasSuffix(relPath, ".go") }

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
//
// Lifecycle (SW-222): Register → Freeze → Execute. NewRegistry does NOT freeze:
// it is the BUILDER, and engine/semantic.NewRegistry decides — from the
// GRAPHI_JVM_TYPERESOLVE opt-in — whether more resolvers join. The pass that
// takes ownership (engine/ingest) freezes it.
type Registry struct {
	life   registry.Lifecycle
	order  []string
	byLang map[string]Resolver
}

// NewRegistry returns the registry of shipped semantic resolvers, UNFROZEN. A
// new language is a new Register call here.
func NewRegistry() *Registry {
	r := &Registry{byLang: map[string]Resolver{}}
	r.Register(goResolver{})
	return r
}

// Policy reports this registry's declared collision policy (CollisionPolicy).
func (r *Registry) Policy() registry.Policy { return CollisionPolicy }

// Freeze marks composition complete: a later Register returns a
// registry.ErrFrozen-typed error. Idempotent and one-way.
func (r *Registry) Freeze() { r.life.Freeze() }

// Frozen reports whether Freeze has been called.
func (r *Registry) Frozen() bool { return r.life.Frozen() }

// Register adds a resolver under its language. Later registrations override an
// earlier one for the same language (CollisionPolicy is last-wins), keeping the
// original registration position so dispatch order stays deterministic. After
// Freeze it mutates nothing and returns a registry.ErrFrozen-typed error.
func (r *Registry) Register(res Resolver) error {
	lang := res.Language()
	if err := r.life.CheckMutable("typeresolve", "Register", lang); err != nil {
		return err
	}
	_, dup := r.byLang[lang]
	if err := registry.GuardDuplicate(CollisionPolicy, "typeresolve", "resolver", lang, dup); err != nil {
		return err
	}
	if !dup {
		r.order = append(r.order, lang)
	}
	r.byLang[lang] = res
	return nil
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
