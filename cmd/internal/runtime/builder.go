// SW-227 (AX-07): the RuntimeBuilder.
//
// Before this file, "how a graphi runtime is wired" was a sequence of
// constructor calls repeated — with small, silent differences — at every start
// path: an analyzer service here, a search service there, a parser registry
// built inline for the ingester, a git provider injected in one path and not the
// other. Nothing named the set of capabilities a process actually has, and every
// registry was still mutable after the surfaces had it.
//
// The builder replaces that with one shape:
//
//	NewBuilder(store) → WithMetaDir/WithRepoRoot/WithWatchProvider → Build()
//	→ Composition (immutable, every registry frozen)
//
// Build runs the deterministic built-in module set (engine/module), which
// contributes parsers, analyzers and operation specs through typed Add* methods
// into the SW-222 lifecycle registries, validates the module dependency DAG,
// then freezes. The Composition that comes back has no setters: the only things
// it hands out are frozen registries, a copy of the module inventory, and the
// memoized surface client.
//
// The builder is NOT a service locator. It exists only during startup, nothing
// stores one, and the only package permitted to import engine/module is this
// one — a boundary the AX-07 test in engine/module enforces in both directions.
package runtime

import (
	"fmt"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/gitlog"
)

// Builder composes one runtime's capability wiring exactly once.
//
// Its With* methods are the ONLY mutation surface, they are usable only before
// Build, and Build consumes the builder: a second Build returns an error rather
// than quietly composing a second set of registries over the same store.
type Builder struct {
	store         graphstore.Graphstore
	metaDir       string
	repoRoot      string
	gitProvider   githistory.GitProvider
	watchProvider analysis.WatchStatusProvider
	built         bool
}

// NewBuilder starts a composition over store. A nil store is legal — it is the
// daemon/attach-without-a-database shape — and produces a composition whose
// client reports the corresponding capabilities as unavailable, exactly as the
// pre-AX-07 wiring did.
func NewBuilder(store graphstore.Graphstore) *Builder {
	return &Builder{store: store}
}

// WithMetaDir names the evidence sidecar the search service reloads durable
// vectors from. Empty means "no sidecar", which is the in-memory/attach shape.
func (b *Builder) WithMetaDir(dir string) *Builder {
	b.metaDir = dir
	return b
}

// WithRepoRoot pins the repository this runtime serves and installs the
// surface-boundary git-history provider for it (bounded local `git log`). The
// engine stays exec-free; this is the one place the seam is handed in.
func (b *Builder) WithRepoRoot(root string) *Builder {
	b.repoRoot = root
	if root != "" {
		b.gitProvider = gitlog.New(root)
	}
	return b
}

// WithWatchProvider injects the daemon's read-only watcher-status provider.
// Everywhere else it stays nil, which is the honest "not active" status a
// one-shot CLI/MCP/HTTP invocation should report.
func (b *Builder) WithWatchProvider(p analysis.WatchStatusProvider) *Builder {
	b.watchProvider = p
	return b
}

// Build runs the built-in module set and returns the immutable Composition.
//
// Everything the modules contribute is frozen before this returns: the operation
// catalog, the parser registry, the analyzer registry (through the analysis
// service) and the resolver registry. A Register call against any of them
// afterwards returns a registry.ErrFrozen-typed error rather than mutating.
func (b *Builder) Build() (*Composition, error) {
	if b.built {
		return nil, fmt.Errorf("runtime: Build called twice on one builder")
	}
	// SW-255 (AX-15): the typed ports a handler-bearing module receives. The
	// graph.query port is the SAME query service the surface client is composed
	// over (see Client), so the module handler and the legacy method read one
	// service, not two equal ones. Both constructors take the store whatever it
	// is, including nil, exactly as the pre-AX-15 client wiring did. The
	// graph.search port is the lexical read service over the store; the
	// client's search service is composed later, in Client, because its
	// optional semantic layer reloads the vector sidecar and that reload must
	// stay where it is — after the session ingest.
	graphQuery := query.New(b.store)
	graphSearch := search.New(b.store)
	contributions, err := module.BuildBuiltins(module.Inputs{
		Reader:        b.reader(),
		GitProvider:   b.gitProvider,
		WatchProvider: b.watchProvider,
		GraphQuery:    graphQuery,
		GraphSearch:   graphSearch,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: compose modules: %w", err)
	}
	b.built = true
	return &Composition{
		contributions: contributions,
		store:         b.store,
		metaDir:       b.metaDir,
		repoRoot:      b.repoRoot,
		gitProvider:   b.gitProvider,
		graphQuery:    graphQuery,
	}, nil
}

// reader returns the read-only graph the analyzers dispatch against. A nil
// graphstore.Graphstore must be passed on as a nil query.Reader rather than as a
// non-nil interface holding a nil pointer.
func (b *Builder) reader() query.Reader {
	if b.store == nil {
		return nil
	}
	return b.store
}

// Composition is what a finished Builder hands out: the frozen module
// contributions plus the surface client composed over them.
//
// It has no exported fields and no setters. Every accessor returns either a
// copy, an already-frozen registry, or the memoized client — so a surface
// holding a Composition cannot re-arm the runtime it was given.
type Composition struct {
	contributions *module.Composition
	store         graphstore.Graphstore
	metaDir       string
	repoRoot      string
	gitProvider   githistory.GitProvider
	// graphQuery is the graph.query port handed to the module set, reused as
	// the surface client's query service so the two are one value.
	graphQuery *query.Service

	clientOnce sync.Once
	client     *client.Direct
}

// Contributions returns the frozen module contributions (operation catalog,
// parser registry, analysis service, resolver registry) and the module
// inventory that produced them.
func (c *Composition) Contributions() *module.Composition { return c.contributions }

// Modules returns the composed module manifests, in composition order.
func (c *Composition) Modules() []module.Manifest { return c.contributions.Modules() }

// Parsers returns the frozen parser registry the ingester parses through.
func (c *Composition) Parsers() *parse.Registry { return c.contributions.Parsers() }

// Analysis returns the frozen analysis service.
func (c *Composition) Analysis() *analysis.Service { return c.contributions.Analysis() }

// Frozen reports whether every registry in this composition is frozen. It is the
// machine-checkable form of "the runtime state handed to surfaces is immutable".
func (c *Composition) Frozen() bool { return c.contributions.Frozen() }

// Client returns the surface client for this composition, composed on first call
// and memoized thereafter.
//
// It is deliberately NOT composed inside Build. The search service reloads
// durable vectors from the meta sidecar, and OpenSession composes its ingester
// (and runs the session ingest) between resolving the state directory and
// handing out a client. Composing the client eagerly would move that reload
// across the ingest — an ordering change in the semantic-search path that no
// acceptance criterion asked for. Client() therefore keeps the composition step
// exactly where the pre-AX-07 code had it, while everything with a registry in
// it is already frozen by then.
func (c *Composition) Client() *client.Direct {
	c.clientOnce.Do(func() {
		asvc := c.contributions.Analysis()
		d := client.NewDirect(c.graphQuery, NewSearchService(c.store, c.metaDir)).
			WithAnalysis(asvc).
			WithReview(review.NewService(asvc)).
			WithOperationHandlers(c.operationHandlers())
		if c.repoRoot != "" {
			d = d.WithRepoRoot(c.repoRoot).WithGitProvider(c.gitProvider)
		}
		c.client = d
	})
	return c.client
}

// operationHandlers converts the module set's frozen handler table into the
// surface client's view of it (SW-255 / AX-15). The two types are the same
// function shape; the conversion exists because surfaces/client may not import
// engine/module, and this file is the one place that holds both. The handlers
// reach the executor through the client the surfaces already receive — no
// global, no second composition.
func (c *Composition) operationHandlers() map[string]client.OperationHandler {
	handled := c.contributions.Handled()
	out := make(map[string]client.OperationHandler, len(handled))
	for _, id := range handled {
		h, ok := c.contributions.Handler(id)
		if !ok {
			continue // Handled and Handler read one frozen table; unreachable
		}
		out[id] = client.OperationHandler(h)
	}
	return out
}
