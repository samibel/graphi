package client

// This file holds the AX-04 legacy adapters (SW-224): one typed argument struct
// per legacy Client method, and the id → adapter table the Executor resolves
// against.
//
// # What an adapter is
//
// An adapter is a pair of directions over ONE existing Client method:
//
//	legacy → executor   Executor.NewRequest(args) — marshal the typed arguments
//	                    and stamp the catalog's contract version onto them.
//	executor → legacy   invoke(ctx, c) — call the Client method with those
//	                    arguments and return its canonical bytes UNCHANGED.
//
// There is no third thing in between. The adapter holds no defaulting, no
// validation, no re-serialization and no result shaping, which is why the two
// paths are byte-identical by construction rather than by reconciliation
// (executor_parity_test.go proves it for every adapted operation).
//
// # Why this set
//
// AX-04 adapts a representative set, not the whole 56-operation catalog:
//
//   - all ELEVEN Stable MCP operations (the ten structural queries plus search,
//     impact and the four agent-context tools), because AC-4 is about the frozen
//     twelve and a "representative set" that omitted them would prove nothing;
//   - Labs operations spanning the other legacy method SHAPES — a text query
//     (compound), pattern queries (search_ast, find_clones), the optional
//     graceful-skip path (search_semantic), the generic analyzer selector
//     (analyze), and two operations whose service is OPTIONAL (savings, memory)
//     so the capability-unavailable sentinels are exercised through the adapter
//     path and not just described.
//
// SW-226 (AX-06) added one more, and for a different reason: dead_code was the
// CANARY — the first operation whose MCP and HTTP dispatch reached this table in
// production (canary.go).
//
// SW-228 (AX-08) added six more for that same reason, and moved nine further
// operations onto the dispatch path. The set that DISPATCHES is
// migratedOperations in canary.go — deliberately a different, smaller list than
// the one below, because being adapted and being dispatched are two different
// claims: an adapter is a proven translation, dispatch is a production decision.
// search, impact, explain_symbol, related_files, change_risk, agent_brief,
// memory, savings, analyze and the ten structural queries are adapted here and
// dispatched nowhere; each has a reason recorded in canary.go.
//
// Operations left unadapted are rejected loudly by Execute. Two are worth naming
// because their omission is a decision, not an oversight:
//
//   - trust_report / graph_health returns four values (bytes, verdict, state,
//     error); the verdict and state exist so the CLI can map exit codes without
//     re-parsing the document. Collapsing them into a []byte would either lose
//     them or re-encode the document — the exact "canonical bytes come from the
//     engine" rule this story is defending. A multi-value result contract is
//     SW-227+ work.
//   - The edit/refactor family (refactor, undo, inline, safe_delete) is a WRITE
//     path. ADR 0013 I3 makes V1 extension capability read-only, and putting the
//     write path behind a generic by-name executor before the read path has a
//     canary is the wrong order.

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/query"
)

// legacyAdapters builds the id → adapter table.
//
// It applies core/registry's collision vocabulary rather than trusting a map
// literal: an operation id is a frozen wire identifier, so a duplicated entry is
// a programming fault (PolicyFirstWins, ErrDuplicate) and never a silent
// override. Nothing here is mutable after construction — the table is built once
// per Executor and never handed out.
func legacyAdapters() (map[string]argumentsFactory, error) {
	table := make(map[string]argumentsFactory)
	add := func(id string, factory argumentsFactory) error {
		_, exists := table[id]
		if err := registry.GuardDuplicate(registry.PolicyFirstWins, executorRegistry, "adapter", id, exists); err != nil {
			return err
		}
		table[id] = factory
		return nil
	}

	// The ten structural query operations share one argument struct and one
	// legacy method: Client.Query(ctx, op, symbol, depth). engine/query.Operations
	// is the list — re-listing them here would be a second dispatch table.
	for _, op := range query.Operations {
		if err := add(op, func(operation string) Arguments {
			return &QueryArgs{Op: operation}
		}); err != nil {
			return nil, err
		}
	}

	for id, factory := range map[string]argumentsFactory{
		"dead_code": func(string) Arguments { return &DeadCodeArgs{} },
		// SW-228 (AX-08): the six simple read-only Labs agent tools whose
		// dispatch this story moves onto the executor. Each is a single
		// Client method taking a small params struct, tier labs, determinism
		// "deterministic", permissions {graph.read} — the same five criteria
		// the canary had to satisfy, now checked mechanically per operation
		// (migrationCriteria in canary.go) rather than argued once in a comment.
		"architecture":            func(string) Arguments { return &ArchitectureArgs{} },
		"architecture_violations": func(string) Arguments { return &ArchitectureViolationsArgs{} },
		"framework_map":           func(string) Arguments { return &FrameworkMapArgs{} },
		"repo_overview":           func(string) Arguments { return &RepoOverviewArgs{} },
		"search_hybrid":           func(string) Arguments { return &SearchHybridArgs{} },
		"test_impact":             func(string) Arguments { return &TestImpactArgs{} },
		"search":                  func(string) Arguments { return &SearchArgs{} },
		"search_semantic":         func(string) Arguments { return &SemanticSearchArgs{} },
		"search_ast":              func(string) Arguments { return &SearchASTArgs{} },
		"find_clones":             func(string) Arguments { return &FindClonesArgs{} },
		"compound":                func(string) Arguments { return &CompoundArgs{} },
		"impact":                  func(string) Arguments { return &ImpactArgs{} },
		"analyze":                 func(string) Arguments { return &AnalyzeArgs{} },
		"agent_brief":             func(string) Arguments { return &BriefArgs{} },
		"explain_symbol":          func(string) Arguments { return &ExplainSymbolArgs{} },
		"related_files":           func(string) Arguments { return &RelatedFilesArgs{} },
		"change_risk":             func(string) Arguments { return &ChangeRiskArgs{} },
		"savings":                 func(string) Arguments { return &SavingsArgs{} },
		"memory":                  func(string) Arguments { return &MemoryArgs{} },
	} {
		if err := add(id, factory); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// sortedAdapterIDs returns the table's ids in canonical order. Map order is
// never allowed to reach an observable surface in graphi, and construction
// diagnostics are observable.
func sortedAdapterIDs(table map[string]argumentsFactory) []string {
	out := make([]string, 0, len(table))
	for id := range table {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// QueryArgs are the arguments of the ten structural query operations.
//
// Op carries the operation id and is json:"-": the id already addresses the
// request (Request.Operation), and encoding it twice would create two facts that
// can disagree. Depth is passed through verbatim — the surface owns its default
// (surfaces/mcp/toolcalls.go defaults it to 1), and a second defaulting site
// here is how two surfaces start returning different bytes for the same call.
type QueryArgs struct {
	Op     string `json:"-"`
	Symbol string `json:"symbol"`
	Depth  int    `json:"depth"`
}

// Operation names the catalog operation.
func (a *QueryArgs) Operation() string { return a.Op }

func (a *QueryArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Query(ctx, a.Op, a.Symbol, a.Depth)
}

// DeadCodeArgs are the arguments of the Labs dead_code operation — the SW-226
// (AX-06) canary, and the first operation whose SURFACE DISPATCH reaches the
// executor rather than only its tests.
//
// MaxItems is passed through verbatim. The engine owns the default
// (deadcode.DefaultMaxItems = 40, applied inside Assemble), and the surfaces
// already pass a plain zero when the caller supplies no cap — MCP's
// derefInt(limit), HTTP's maxItems, the CLI's -max-items flag. Defaulting here
// would be a second defaulting site for a value three surfaces currently agree
// on by not touching it.
type DeadCodeArgs struct {
	MaxItems int `json:"max_items"`
}

// Operation names the catalog operation.
func (a *DeadCodeArgs) Operation() string { return CanaryOperation }

func (a *DeadCodeArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.DeadCode(ctx, DeadCodeParams{MaxItems: a.MaxItems})
}

// ArchitectureArgs are the arguments of the Labs architecture view (SW-228).
//
// Like every adapter here it passes MaxItems through verbatim: the engine owns
// the cap (archintel.Assemble applies its own default), and all three surfaces
// already hand it a plain zero when the caller supplies none.
type ArchitectureArgs struct {
	MaxItems int `json:"max_items"`
}

// Operation names the catalog operation.
func (a *ArchitectureArgs) Operation() string { return "architecture" }

func (a *ArchitectureArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Architecture(ctx, ArchitectureParams{MaxItems: a.MaxItems})
}

// ArchitectureViolationsArgs are the arguments of the Labs
// architecture_violations findings list (SW-228).
type ArchitectureViolationsArgs struct {
	MaxItems int `json:"max_items"`
}

// Operation names the catalog operation.
func (a *ArchitectureViolationsArgs) Operation() string { return "architecture_violations" }

func (a *ArchitectureViolationsArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.ArchitectureViolations(ctx, ArchitectureViolationsParams{MaxItems: a.MaxItems})
}

// FrameworkMapArgs are the arguments of the Labs framework_map view (SW-228).
type FrameworkMapArgs struct {
	MaxItems int `json:"max_items"`
}

// Operation names the catalog operation.
func (a *FrameworkMapArgs) Operation() string { return "framework_map" }

func (a *FrameworkMapArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.FrameworkMap(ctx, FrameworkMapParams{MaxItems: a.MaxItems})
}

// RepoOverviewArgs are the arguments of the Labs repo_overview summary (SW-228).
//
// Communities is the opt-in full-graph Louvain pass. It is carried explicitly
// rather than dropped: it is the one argument of this operation that changes
// what work the engine does, so an adapter that silently defaulted it would
// change the answer AND the cost.
type RepoOverviewArgs struct {
	MaxItems    int  `json:"max_items"`
	Communities bool `json:"communities"`
}

// Operation names the catalog operation.
func (a *RepoOverviewArgs) Operation() string { return "repo_overview" }

func (a *RepoOverviewArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.RepoOverview(ctx, RepoOverviewParams{MaxItems: a.MaxItems, Communities: a.Communities})
}

// SearchHybridArgs are the arguments of the Labs embedding-free hybrid search
// (SW-228). Version is SW-264: 0/1 selects /1 (the shipped default, byte-
// identical to today's output); 2 selects /2 (the retrieval-rendered path).
// The dispatcher's "legacy" branch forces version=1 so the AC-6 dual-run
// comparison is between /1 and /2 bytes; the executor branch honors the
// caller's version.
type SearchHybridArgs struct {
	Query    string `json:"query"`
	MaxItems int    `json:"max_items"`
	Version  int    `json:"version,omitempty"`
}

// Operation names the catalog operation.
func (a *SearchHybridArgs) Operation() string { return "search_hybrid" }

func (a *SearchHybridArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.SearchHybrid(ctx, SearchHybridParams{Query: a.Query, MaxItems: a.MaxItems, Version: a.Version})
}

// TestImpactArgs are the arguments of the Labs test_impact buckets (SW-228).
//
// Diff is carried even though the HTTP surface does not offer it (the GET-only
// surface is target-mode only, the change_risk precedent). The adapter's job is
// to be able to express what the legacy METHOD takes, not what one transport
// chooses to expose; a field the adapter could not carry would make the MCP arm
// unmigratable.
type TestImpactArgs struct {
	Target   string `json:"target"`
	Diff     string `json:"diff"`
	Depth    int    `json:"depth"`
	MaxItems int    `json:"max_items"`
}

// Operation names the catalog operation.
func (a *TestImpactArgs) Operation() string { return "test_impact" }

func (a *TestImpactArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.TestImpact(ctx, TestImpactParams{
		Target:   a.Target,
		Diff:     a.Diff,
		Depth:    a.Depth,
		MaxItems: a.MaxItems,
	})
}

// SearchArgs are the arguments of the Stable lexical search.
type SearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Operation names the catalog operation.
func (a *SearchArgs) Operation() string { return "search" }

func (a *SearchArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Search(ctx, a.Query, a.Limit)
}

// SemanticSearchArgs are the arguments of the OPTIONAL semantic search. Without
// an embedder the legacy method returns the typed graceful-skip response and NO
// error; the adapter passes both through unchanged, so the graceful skip stays
// a graceful skip on this path too.
type SemanticSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Operation names the catalog operation.
func (a *SemanticSearchArgs) Operation() string { return "search_semantic" }

func (a *SemanticSearchArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.SemanticSearch(ctx, a.Query, a.Limit)
}

// SearchASTArgs are the arguments of the structural AST pattern query.
type SearchASTArgs struct {
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
}

// Operation names the catalog operation.
func (a *SearchASTArgs) Operation() string { return "search_ast" }

func (a *SearchASTArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.SearchAST(ctx, a.Pattern, a.Limit)
}

// FindClonesArgs are the arguments of the clone-group detection query.
type FindClonesArgs struct {
	Config string `json:"config"`
}

// Operation names the catalog operation.
func (a *FindClonesArgs) Operation() string { return "find_clones" }

func (a *FindClonesArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.FindClones(ctx, a.Config)
}

// CompoundArgs are the arguments of the compound / Cypher-style graph query.
type CompoundArgs struct {
	Query string `json:"query"`
}

// Operation names the catalog operation.
func (a *CompoundArgs) Operation() string { return "compound" }

func (a *CompoundArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Compound(ctx, a.Query)
}

// ImpactArgs are the arguments of the Stable impact operation.
//
// It invokes through AsStable's analyzer-selector-free port rather than
// Client.Analyze directly. Both reach the same engine call — AsStable.Impact IS
// a call to Analyze with Name pinned to "impact" — but going through the narrow
// port means the adapter for a Stable operation cannot be edited into one that
// dispatches an arbitrary Labs analyzer.
type ImpactArgs struct {
	Symbol    string `json:"symbol"`
	Direction string `json:"direction"`
	MaxNodes  int    `json:"max_nodes"`
}

// Operation names the catalog operation.
func (a *ImpactArgs) Operation() string { return "impact" }

func (a *ImpactArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return AsStable(c).Impact(ctx, ImpactParams{
		Symbol:    a.Symbol,
		Direction: a.Direction,
		MaxNodes:  a.MaxNodes,
	})
}

// AnalyzeArgs are the arguments of the Labs generic analyzer selector. It
// embeds AnalyzeParams so there is one field list, not a copy that can drift
// from the type the legacy method actually takes.
type AnalyzeArgs struct {
	AnalyzeParams
}

// Operation names the catalog operation.
func (a *AnalyzeArgs) Operation() string { return "analyze" }

func (a *AnalyzeArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Analyze(ctx, a.AnalyzeParams)
}

// BriefArgs are the arguments of the Stable agent_brief assembler.
//
// Client.Brief returns TWO byte slices: the canonical Result bytes and a
// Markdown rendering. The Executor transports the CANONICAL bytes; the Markdown
// is a presentation of them, assembled for display by the surface that wants it
// (surfaces/mcp/toolcalls.go concatenates the two into one text payload today,
// and keeps doing so — nothing dispatches through here). Carrying a rendering in
// a canonical-byte channel would make "the executor transports canonical bytes"
// false for exactly one operation.
type BriefArgs struct {
	Topic string `json:"topic"`
}

// Operation names the catalog operation.
func (a *BriefArgs) Operation() string { return "agent_brief" }

func (a *BriefArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	canonical, _, err := AsStable(c).Brief(ctx, a.Topic)
	return canonical, err
}

// ExplainSymbolArgs are the arguments of the Stable explain_symbol tool.
type ExplainSymbolArgs struct {
	Symbol   string `json:"symbol"`
	MaxItems int    `json:"max_items"`
}

// Operation names the catalog operation.
func (a *ExplainSymbolArgs) Operation() string { return "explain_symbol" }

func (a *ExplainSymbolArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return AsStable(c).ExplainSymbol(ctx, a.Symbol, a.MaxItems)
}

// RelatedFilesArgs are the arguments of the Stable related_files tool.
type RelatedFilesArgs struct {
	Target    string `json:"target"`
	Direction string `json:"direction"`
	MaxFiles  int    `json:"max_files"`
}

// Operation names the catalog operation.
func (a *RelatedFilesArgs) Operation() string { return "related_files" }

func (a *RelatedFilesArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return AsStable(c).RelatedFiles(ctx, a.Target, a.Direction, a.MaxFiles)
}

// ChangeRiskArgs are the arguments of the Stable change_risk tool.
type ChangeRiskArgs struct {
	Target   string `json:"target"`
	Diff     string `json:"diff"`
	MaxItems int    `json:"max_items"`
}

// Operation names the catalog operation.
func (a *ChangeRiskArgs) Operation() string { return "change_risk" }

func (a *ChangeRiskArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return AsStable(c).ChangeRisk(ctx, a.Target, a.Diff, a.MaxItems)
}

// SavingsArgs are the (empty) arguments of the Labs savings readout. It is
// adapted precisely because its service is OPTIONAL: without a ledger the
// legacy method returns ErrSavingsUnavailable, and the adapter path must return
// the SAME sentinel so a surface renders the same message it renders today.
type SavingsArgs struct{}

// Operation names the catalog operation.
func (a *SavingsArgs) Operation() string { return "savings" }

func (a *SavingsArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Savings(ctx)
}

// MemoryArgs are the arguments of the Labs memory operation. Like savings it is
// backed by an OPTIONAL service (ErrMemoryUnavailable without one), and it
// carries the SAFE-01 rejected legacy field, so the adapter path also proves a
// rejected argument stays rejected.
type MemoryArgs struct {
	MemoryRequest
}

// Operation names the catalog operation.
func (a *MemoryArgs) Operation() string { return "memory" }

func (a *MemoryArgs) invoke(ctx context.Context, c Client) ([]byte, error) {
	return c.Memory(ctx, a.MemoryRequest)
}
