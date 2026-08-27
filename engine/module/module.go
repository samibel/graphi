// Package module is graphi's built-in module set and the builder the runtime
// composes it through (SW-227 / AX-07).
//
// # What a module is
//
// A Module is a Manifest (id, version, the module ids it requires) plus one
// Register function that contributes capabilities through typed Add* methods.
// It is ADR 0013 tier B and nothing else: statically compiled first-party Go,
// inside the graphi binary, with no runtime loading, no ABI and no discovery.
// The type exists so that "which capabilities does this build have, and where
// did each of them come from" is a value the composition root can read, instead
// of being the sum of a dozen constructor side effects scattered across cmd/.
//
// # Lifecycle
//
// The vocabulary is SW-222's, reused rather than re-invented:
//
//	Set.Add → Set.Validate → Set.Build (order → register → freeze)
//
// Set.Add rejects a malformed or duplicate manifest. Set.Validate runs the
// CROSS-registrant obligations, which is what makes a Validate step worth having
// here at all: SW-222 deliberately shipped none because no registry had an
// obligation spanning its registrants, and SW-223 added one to the operation
// catalog because ids must be unique across the whole set. The module set is the
// first registrant set whose members declare dependencies ON EACH OTHER, so it
// has three obligations no single Add can see — every required id must exist,
// nothing may require itself, and the graph must be acyclic. Set.Build then
// orders, registers and freezes.
//
// Failures are core/registry's typed errors: registry.ErrDuplicate for a
// repeated module id or a contribution two modules both claim,
// registry.ErrMissingDependency for a Requires naming nothing, and
// registry.ErrCycle (added by this story to the shared vocabulary rather than
// beside it) for a cycle. Every one names its offenders in the message and
// carries them in the registry.Error fields.
//
// # Determinism
//
// Registration order is a total order and is pinned by test. Modules are ordered
// by a topological sort over Requires with lexicographic tie-breaking, so the
// order is a function of the manifests alone: it does not depend on the order
// modules were added, on map iteration, or on anything about the process. That
// matters because several of the underlying registries resolve collisions by
// order (core/parse is last-wins), and a composition whose order varies is a
// composition whose behaviour varies.
//
// # This is not a service locator
//
// A Builder exists only during startup and is not reachable afterwards: Build
// consumes it and returns a Composition whose registries are frozen and whose
// accessors are read-only. Nothing may hold a Builder past composition, and
// nothing outside cmd/internal/runtime may import this package at all — the
// AX-07 boundary test in this package enforces both directions.
//
// # Contribution kinds
//
// Four kinds are typed today — operations, parsers, analyzers, resolvers — and
// each delegates to the SW-222 lifecycle registry that owns it. Only three have
// a built-in contributor:
//
//   - operations → engine/opcatalog. The engine.operations module contributes
//     the shadow catalog's specs.
//   - parsers    → core/parse. The core.parse module contributes
//     parse.DefaultParsers(), one at a time.
//   - analyzers  → engine/analysis. The engine.analysis module contributes
//     analysis.DefaultAnalyzers(), one at a time.
//   - resolvers  → engine/typeresolve. NO built-in module contributes one, and
//     that is deliberate: engine/ingest constructs the semantic-resolver
//     registry itself (engine/ingest/ingester.go, engine/ingest/readonly.go), so
//     moving that composition here would be a change to ingest — which AC-5 of
//     this story excludes. The kind is implemented and tested so the seam is
//     ready when the ingest constructor is opened, and so a later story
//     contributes a resolver without reshaping the builder.
//     Composition.Resolvers therefore holds exactly what
//     typeresolve.NewRegistry pre-arms (the built-in go/types resolver),
//     frozen, in every shipped build.
//
// # Collision policy
//
// The BUILDER's own policy is registry.PolicyFirstWins for every kind: two
// modules claiming one contribution key is a composition fault, never an
// override. This is deliberately STRICTER than some of the registries it feeds —
// core/parse is last-wins so an opt-in CGO grammar may supersede a stdlib
// default (core/parse/registry.go), and that seam stays exactly as it is for
// direct registrants. What the builder adds is that reaching a last-wins seam
// THROUGH THE MODULE SET cannot silently shadow a built-in, which is ADR 0013
// threat T5 stated as code rather than as a comment.
package module

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/typeresolve"
)

// registryName is the short name the typed lifecycle errors carry.
const registryName = "module"

// CollisionPolicy is the module builder's DECLARED collision rule: FIRST-WINS.
// See the package doc for why it is stricter than core/parse's.
const CollisionPolicy = registry.PolicyFirstWins

// Manifest is a module's identity and its declared dependencies.
type Manifest struct {
	// ID is the module's stable identifier, e.g. "engine.analysis". It is the
	// key the dependency graph is expressed in and the tie-break for ordering.
	ID string
	// Version is the module's contract version. It is required — an unversioned
	// module cannot be depended on with any meaning — and is carried into the
	// composition inventory.
	Version string
	// Requires lists the module ids that must be registered BEFORE this one.
	// Order within the slice is irrelevant; the builder sorts it.
	Requires []string
}

// Module is one built-in capability provider: a manifest plus the single
// function that contributes its capabilities.
type Module struct {
	Manifest Manifest
	// Register contributes this module's capabilities through the builder's
	// typed Add* methods. It runs exactly once, during Set.Build, in the
	// deterministic order Set.Order computes.
	Register func(*Builder) error
}

// Inputs are the per-session values the built-in modules compose over. They are
// resolved by the composition root (which owns environment reading and the
// surface-boundary providers) and handed in, so no module reaches for ambient
// state of its own.
type Inputs struct {
	// Reader is the read-only graph the analyzers dispatch against.
	Reader query.Reader
	// GitProvider is the surface-boundary bounded `git log` provider. Nil on the
	// attach path, where no repository root was resolved; the git-consuming
	// analyzers then keep their graceful empty results.
	GitProvider githistory.GitProvider
	// WatchProvider is the daemon's read-only watcher-status provider. Nil
	// everywhere else, which is the honest "not active" state.
	WatchProvider analysis.WatchStatusProvider
}

// Set is the module set: an unordered collection of manifests that Build turns
// into one ordered, validated, frozen Composition.
type Set struct {
	modules map[string]Module
	frozen  bool
}

// NewSet returns an empty set.
func NewSet() *Set { return &Set{modules: map[string]Module{}} }

// Policy reports the set's declared collision rule (CollisionPolicy).
func (s *Set) Policy() registry.Policy { return CollisionPolicy }

// Len reports how many modules the set holds.
func (s *Set) Len() int { return len(s.modules) }

// Add registers one module. It rejects a malformed manifest outright and a
// duplicate id with a registry.ErrDuplicate-typed error.
func (s *Set) Add(m Module) error {
	if s.frozen {
		return registry.Errorf(registry.ErrFrozen, registryName, "Add", m.Manifest.ID,
			"%s: Add %q after build: the module set is frozen", registryName, m.Manifest.ID)
	}
	if m.Manifest.ID == "" {
		return fmt.Errorf("%s: module with an empty id", registryName)
	}
	if m.Manifest.Version == "" {
		return fmt.Errorf("%s: module %q declares no version", registryName, m.Manifest.ID)
	}
	if m.Register == nil {
		return fmt.Errorf("%s: module %q has no Register function", registryName, m.Manifest.ID)
	}
	_, exists := s.modules[m.Manifest.ID]
	if err := registry.GuardDuplicate(s.Policy(), registryName, "module", m.Manifest.ID, exists); err != nil {
		return err
	}
	s.modules[m.Manifest.ID] = m
	return nil
}

// Validate runs the cross-registrant obligations: every declared dependency
// must resolve, nothing may require itself, and the graph must be acyclic.
func (s *Set) Validate() error {
	for _, id := range s.ids() {
		m := s.modules[id]
		for _, need := range m.Manifest.Requires {
			if need == id {
				return registry.Errorf(registry.ErrCycle, registryName, "Validate", id,
					"%s: module %q requires itself", registryName, id)
			}
			if _, ok := s.modules[need]; !ok {
				return registry.Errorf(registry.ErrMissingDependency, registryName, "Validate", need,
					"%s: module %q requires %q, which is not registered", registryName, id, need)
			}
		}
	}
	_, err := s.Order()
	return err
}

// ids returns every registered module id in lexicographic order.
func (s *Set) ids() []string {
	out := make([]string, 0, len(s.modules))
	for id := range s.modules {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Order returns the modules in the deterministic composition order: a
// topological sort over Requires, with the lexicographically smallest ready
// module chosen at every step.
//
// The tie-break is what makes the order TOTAL. A plain topological sort is only
// a partial order, so two independent modules could legitimately come out either
// way and the composition would differ between runs of the same binary — which,
// for registries that resolve collisions by order, is a behaviour difference.
//
// A cycle is reported with the offending module ids sorted, so the message is
// the same on every run and can be asserted.
func (s *Set) Order() ([]Module, error) {
	ids := s.ids()
	indegree := make(map[string]int, len(ids))
	dependents := make(map[string][]string, len(ids))
	for _, id := range ids {
		if _, ok := indegree[id]; !ok {
			indegree[id] = 0
		}
		seen := map[string]bool{}
		for _, need := range s.modules[id].Manifest.Requires {
			if _, ok := s.modules[need]; !ok {
				return nil, registry.Errorf(registry.ErrMissingDependency, registryName, "Order", need,
					"%s: module %q requires %q, which is not registered", registryName, id, need)
			}
			if seen[need] {
				continue // a repeated Requires entry is one edge, not two
			}
			seen[need] = true
			indegree[id]++
			dependents[need] = append(dependents[need], id)
		}
	}

	var ready []string
	for _, id := range ids {
		if indegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	out := make([]Module, 0, len(ids))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, s.modules[id])
		next := append([]string(nil), dependents[id]...)
		sort.Strings(next)
		for _, dep := range next {
			indegree[dep]--
			if indegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		sort.Strings(ready)
	}

	if len(out) != len(ids) {
		var stuck []string
		for _, id := range ids {
			if indegree[id] > 0 {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, registry.Errorf(registry.ErrCycle, registryName, "Order", strings.Join(stuck, ","),
			"%s: module dependency cycle among %s", registryName, strings.Join(stuck, ", "))
	}
	return out, nil
}

// Manifests returns every manifest in composition order.
func (s *Set) Manifests() ([]Manifest, error) {
	ordered, err := s.Order()
	if err != nil {
		return nil, err
	}
	out := make([]Manifest, 0, len(ordered))
	for _, m := range ordered {
		manifest := m.Manifest
		manifest.Requires = append([]string(nil), m.Manifest.Requires...)
		sort.Strings(manifest.Requires)
		out = append(out, manifest)
	}
	return out, nil
}

// Build validates the set, runs every module's Register in composition order,
// freezes every registry and returns the immutable Composition.
//
// After Build the set is frozen: a further Add is refused. The Builder is
// consumed and never escapes, so there is no post-build mutation path — the
// property AC-3 of this story asks for.
func (s *Set) Build(in Inputs) (*Composition, error) {
	if s.frozen {
		return nil, registry.Errorf(registry.ErrFrozen, registryName, "Build", "",
			"%s: Build called twice: the module set is frozen", registryName)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ordered, err := s.Order()
	if err != nil {
		return nil, err
	}
	b := newBuilder(in)
	for _, m := range ordered {
		b.current = m.Manifest.ID
		if rerr := m.Register(b); rerr != nil {
			return nil, fmt.Errorf("%s: module %q: %w", registryName, m.Manifest.ID, rerr)
		}
	}
	b.current = ""
	manifests, merr := s.Manifests()
	if merr != nil {
		return nil, merr
	}
	comp, cerr := b.finish(manifests)
	if cerr != nil {
		return nil, cerr
	}
	s.frozen = true
	return comp, nil
}

// Builder is the ModuleBuilder handed to each module's Register function. It
// exists only for the duration of Set.Build.
type Builder struct {
	inputs  Inputs
	current string
	done    bool

	catalog   *opcatalog.Catalog
	parsers   *parse.Registry
	analyzers *analysis.Registry
	resolvers *typeresolve.Registry

	// owners maps "<kind>:<key>" to the module that contributed it, so a
	// collision names BOTH offenders rather than only the second one.
	owners map[string]string
}

// builtInResolverOwner is the pseudo-module recorded as the owner of the
// resolvers typeresolve.NewRegistry pre-arms. That constructor is not empty — it
// registers the built-in go/types resolver itself (engine/typeresolve/registry.go)
// — and typeresolve is last-wins, so without recording those keys a module
// contributing a "go" resolver would silently shadow the built-in instead of
// being refused. Seeding the owner map is what makes the builder's first-wins
// policy true rather than merely declared.
const builtInResolverOwner = "engine/typeresolve (built-in)"

func newBuilder(in Inputs) *Builder {
	resolvers := typeresolve.NewRegistry()
	b := &Builder{
		inputs:    in,
		catalog:   opcatalog.New(),
		parsers:   parse.NewRegistry(),
		analyzers: analysis.NewRegistry(),
		resolvers: resolvers,
		owners:    map[string]string{},
	}
	for _, r := range resolvers.Resolvers() {
		b.owners["Resolver:"+r.Language()] = builtInResolverOwner
	}
	return b
}

// Inputs returns the per-session values this composition was given.
func (b *Builder) Inputs() Inputs { return b.inputs }

// Policy reports the builder's declared collision rule (CollisionPolicy).
func (b *Builder) Policy() registry.Policy { return CollisionPolicy }

// claim applies the builder's own first-wins policy to one contribution key and
// records the owner. It runs BEFORE the underlying registry is touched, so a
// last-wins registry never sees the shadowing registration at all.
func (b *Builder) claim(kind, key string) error {
	if b.done {
		return registry.Errorf(registry.ErrFrozen, registryName, "Add"+kind, key,
			"%s: Add%s %q after build: the composition is frozen", registryName, kind, key)
	}
	if key == "" {
		return fmt.Errorf("%s: module %q contributed a %s with an empty key", registryName, b.current, strings.ToLower(kind))
	}
	slot := kind + ":" + key
	if owner, exists := b.owners[slot]; exists {
		return registry.Errorf(registry.ErrDuplicate, registryName, "Add"+kind, key,
			"%s: module %q contributes %s %q, already contributed by module %q",
			registryName, b.current, strings.ToLower(kind), key, owner)
	}
	b.owners[slot] = b.current
	return nil
}

// AddOperation contributes one operation spec to the runtime catalog.
func (b *Builder) AddOperation(spec opcatalog.OperationSpec) error {
	if err := b.claim("Operation", spec.ID); err != nil {
		return err
	}
	return b.catalog.Add(spec)
}

// AddParser contributes one parser to the runtime parser registry. The parser's
// language is the contribution key: two modules shipping a "go" parser is a
// composition fault even though core/parse itself would take the later one.
func (b *Builder) AddParser(p parse.Parser) error {
	if p == nil {
		return fmt.Errorf("%s: module %q contributed a nil parser", registryName, b.current)
	}
	if err := b.claim("Parser", p.Language()); err != nil {
		return err
	}
	return b.parsers.Register(p)
}

// AddAnalyzer contributes one analyzer to the runtime analyzer registry.
func (b *Builder) AddAnalyzer(a analysis.Analyzer) error {
	if a == nil {
		return fmt.Errorf("%s: module %q contributed a nil analyzer", registryName, b.current)
	}
	if err := b.claim("Analyzer", a.Name()); err != nil {
		return err
	}
	return b.analyzers.Register(a)
}

// AddResolver contributes one semantic resolver to the runtime resolver
// registry.
//
// No built-in module calls this today — engine/ingest still constructs its own
// semantic registry, and moving that is an ingest change this story excludes.
// See the package doc: the kind is here, typed and tested, so the seam is ready
// rather than improvised when the ingest constructor is opened.
func (b *Builder) AddResolver(r typeresolve.Resolver) error {
	if r == nil {
		return fmt.Errorf("%s: module %q contributed a nil resolver", registryName, b.current)
	}
	if err := b.claim("Resolver", r.Language()); err != nil {
		return err
	}
	return b.resolvers.Register(r)
}

// finish validates and freezes every registry and hands back the immutable
// composition. It is the ONLY caller of the freeze methods in this package.
func (b *Builder) finish(manifests []Manifest) (*Composition, error) {
	if b.done {
		return nil, registry.Errorf(registry.ErrFrozen, registryName, "Build", "",
			"%s: Build called twice", registryName)
	}
	catalog, err := b.catalog.Build()
	if err != nil {
		return nil, err
	}
	b.parsers.Freeze()
	b.resolvers.Freeze()

	// The analyzer service is composed in the SAME order the pre-AX-07
	// composition root used — register, then re-arm the git-consuming analyzers,
	// then freeze — because that order is what makes the resulting registry
	// byte-identical to analysis.NewDefaultService(reader).WithGitProvider(gp).
	svc := analysis.NewService(b.inputs.Reader, b.analyzers)
	if b.inputs.GitProvider != nil {
		svc = svc.WithGitProvider(b.inputs.GitProvider)
	}
	svc = svc.Freeze()

	b.done = true
	return &Composition{
		manifests: manifests,
		catalog:   catalog,
		parsers:   b.parsers,
		analysis:  svc,
		resolvers: b.resolvers,
	}, nil
}

// Composition is the immutable result of building a module set. It has no
// exported fields and no mutators: everything it hands out is either a copy or
// an already-frozen registry, so a surface holding one cannot re-arm the runtime.
type Composition struct {
	manifests []Manifest
	catalog   *opcatalog.Catalog
	parsers   *parse.Registry
	analysis  *analysis.Service
	resolvers *typeresolve.Registry
}

// Modules returns the composed manifests in composition order.
func (c *Composition) Modules() []Manifest {
	out := make([]Manifest, 0, len(c.manifests))
	for _, m := range c.manifests {
		manifest := m
		manifest.Requires = append([]string(nil), m.Requires...)
		out = append(out, manifest)
	}
	return out
}

// ModuleIDs returns the composed module ids in composition order.
func (c *Composition) ModuleIDs() []string {
	out := make([]string, 0, len(c.manifests))
	for _, m := range c.manifests {
		out = append(out, m.ID)
	}
	return out
}

// Operations returns the frozen operation catalog.
func (c *Composition) Operations() *opcatalog.Catalog { return c.catalog }

// Parsers returns the frozen parser registry.
func (c *Composition) Parsers() *parse.Registry { return c.parsers }

// Analysis returns the frozen analysis service.
func (c *Composition) Analysis() *analysis.Service { return c.analysis }

// Resolvers returns the frozen semantic-resolver registry — see AddResolver.
func (c *Composition) Resolvers() *typeresolve.Registry { return c.resolvers }

// Frozen reports whether every registry in this composition is frozen. It is the
// machine-checkable form of "the runtime state handed to surfaces is immutable".
func (c *Composition) Frozen() bool {
	return c.catalog.Frozen() && c.parsers.Frozen() && c.analysis.Frozen() && c.resolvers.Frozen()
}
