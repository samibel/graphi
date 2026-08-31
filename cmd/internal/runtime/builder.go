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
	"context"
	"fmt"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
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
		searchSvc := NewSearchService(c.store, c.metaDir)
		d := client.NewDirect(c.graphQuery, searchSvc).
			WithAnalysis(asvc).
			WithReview(review.NewService(asvc)).
			WithRetrieval(c.composeRetrieval(searchSvc)).
			WithOperationHandlers(c.operationHandlers())
		if c.repoRoot != "" {
			d = d.WithRepoRoot(c.repoRoot).WithGitProvider(c.gitProvider)
		}
		c.client = d
	})
	return c.client
}

// composeRetrieval builds the SW-263 deep retrieval instance exactly once
// at the post-ingest seam (AC-10): after the search service has reloaded
// the durable semantic generation, but before the client is returned. The
// returned resolve.Retriever is exposed on the surface client via
// WithRetrieval, which the agent-tool dependency assembly (agentDeps)
// carries into every resolve.Deps. No global, no locator — a single
// composition per Composition, exactly as the spec requires.
//
// The bridge from engine/retrieval's result shape to resolve.Retriever lives
// here, in the composition root: the retrieval module owns its own typed
// Request/Result pair (the AC-1 export surface), the resolve package
// owns its parallel narrow interface (so the agent-tools layer does
// not import engine/retrieval), and the only place that needs both
// types in one place is the single composition site.
//
// SW-263 review / item 4: New receives the search service itself and derives
// model/index fingerprints from its typed semantic state inside the deep
// module. The composition root can no longer forget that wiring.
func (c *Composition) composeRetrieval(searchSvc *search.Service) resolve.Retriever {
	if c.store == nil {
		// No store: no retrieval. The composition root never builds a
		// retriever without a graph to read; withDeps callers get the
		// "no retrieval" graceful state.
		return nil
	}
	lexical := resolve.Deps{Query: c.graphQuery, Search: searchSvc}
	// Wire a non-nil GraphReader over the store so semantic-only rows in
	// ModeAuto receive the bounded degree boost lexical-only rows
	// already get through the delegating HybridSearchBridge (SW-263 /
	// decision-ac9 defect 3: passing nil here would silently zero the
	// degree contribution on every semantic-only candidate the fused
	// ablations surface). The lexical-only byte-parity path
	// (AC-7) is unaffected: the delegating bridge carries its own
	// degree signal on the lexicalScore and the rerank stage adopts
	// that score unaltered without consulting this reader.
	var graphReader graphstore.BoundedGraphLookup
	if c.store != nil {
		if bg, ok := c.store.(graphstore.BoundedGraphLookup); ok {
			graphReader = bg
		}
	}
	eng := retrieval.New(lexical, searchSvc, graphReader)
	return retrievalAdapter{eng: eng}
}

// retrievalAdapter is the one-place bridge from retrieval's private engine to
// resolve.Retriever. It converts the local Request/Result pair the
// retrieval module owns (AC-1's exported surface) into the parallel
// narrow types the resolve package declares. Field translation is
// mechanical: Mode maps 0:1, the rest copy verbatim, the typed
// degradation state carries over as a string. SW-264 is the consumer
// this adapter is built for.
type retrievalAdapter struct {
	eng interface {
		Retrieve(context.Context, retrieval.Request) (retrieval.Result, error)
	}
}

func (a retrievalAdapter) Retrieve(ctx context.Context, req resolve.RetrieverRequest) (resolve.RetrieverResult, error) {
	rreq := retrieval.Request{
		Query:      req.Query,
		Limit:      req.Limit,
		BudgetHint: req.Budget,
		Mode:       retrieval.Mode(req.Mode),
	}
	res, err := a.eng.Retrieve(ctx, rreq)
	if err != nil {
		return resolve.RetrieverResult{}, err
	}
	out := resolve.RetrieverResult{
		Degradation: string(res.Degradation),
		Rows:        make([]resolve.RetrieverRow, len(res.Rows)),
		Summary: resolve.RetrieverSummary{
			RetrievalVersion: res.Summary.RetrievalVersion,
			WeightsHash:      res.Summary.WeightsHash,
			ModelFingerprint: res.Summary.ModelFingerprint,
			IndexFingerprint: res.Summary.IndexFingerprint,
			Query:            res.Summary.Query,
			Limit:            res.Summary.Limit,
			CandidateK:       res.Summary.CandidateK,
			RRFk:             res.Summary.RRFk,
			RRFScale:         res.Summary.RRFScale,
			MaxPerFile:       res.Summary.MaxPerFile,
		},
	}
	for i, row := range res.Rows {
		out.Rows[i] = resolve.RetrieverRow{
			NodeID:     row.NodeID,
			DocumentID: row.DocumentID,
			Path:       row.Path,
			Line:       lineFromSpan(row.Span),
			Span:       row.Span,
			Explain: resolve.RetrieverExplain{
				LexicalRank:    row.Explain.LexicalRank,
				SemanticRank:   row.Explain.SemanticRank,
				RRF:            row.Explain.RRF,
				Graph:          row.Explain.Graph,
				Classification: row.Explain.Classification,
				Final:          row.Explain.Final,
			},
			Final: row.Explain.Final,
		}
	}
	return out, nil
}

// lineFromSpan parses the engine-owned "start-end" span string back
// into a 1-based start line. It is the inverse of retrieval.spanFromLine
// and is local to the composition root so the engine module does not
// have to expose a span parser.
func lineFromSpan(s string) int {
	if s == "" {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				c := s[j]
				if c < '0' || c > '9' {
					return 0
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
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
