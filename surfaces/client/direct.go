package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/archintel"
	"github.com/samibel/graphi/engine/agenttools/brief"
	"github.com/samibel/graphi/engine/agenttools/changeimpact"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/agenttools/explain"
	"github.com/samibel/graphi/engine/agenttools/frameworkmap"
	"github.com/samibel/graphi/engine/agenttools/hotspots"
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/agenttools/overview"
	"github.com/samibel/graphi/engine/agenttools/related"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/agenttools/symbolcontext"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/agenttools/testimpact"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/analysis/githistory"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/diagnostic"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/surfaces/forge"
)

// Direct is an in-process Client backed by query.Service and search.Service, and
// optionally a savings ledger (SW-020), an analysis service (SW-022), an
// edit/refactor applier + change recorder (SW-038), and memory/distill/skillgen
// services (EP-012).
type Direct struct {
	querySvc      *query.Service
	searchSvc     *search.Service
	ledger        *ledger.Ledger
	analysisSvc   *analysis.Service
	applier       *edit.Applier
	recorder      *edit.ChangeRecorder
	reviewSvc     *review.Service
	memoryStore   *memory.Store
	distiller     *distill.Distiller
	skillGen      *skillgen.Generator
	forge         forge.Enumerator
	branchState   BranchStateMaterializer
	reviewFetcher forge.ReviewFetcher
	repoRoot      string
	gitProvider   githistory.GitProvider
	// handlers is the module-handler table the composition root installs
	// (SW-255 / AX-15): the engine-side handlers the module set contributed,
	// keyed by operation id. Reachable only through OperationHandler and
	// HandledOperations — never as a map — and copied on install.
	handlers map[string]OperationHandler
}

// NewDirect constructs an in-process client.
func NewDirect(q *query.Service, s *search.Service) *Direct {
	return &Direct{querySvc: q, searchSvc: s}
}

// Compile-time proof that Direct can carry the module set's handlers to the
// executor (SW-255 / AX-15) through the composition root's existing
// Composition.Client() wiring rather than through a global.
var _ OperationHandlerProvider = (*Direct)(nil)

// WithOperationHandlers installs the module-handler table the composition
// root built from engine/module's Composition. The map is COPIED: the caller
// keeps no path into the client's table, so the runtime it hands to surfaces
// stays immutable in this respect too.
//
// It is a post-open mutator like every other With* here, and it inherits
// their fate (backlog: retiring the Direct mutators is AX-16b). Nothing else
// in this package reads the table; NewExecutorWithCatalog discovers it through
// the OperationHandlerProvider interface.
func (d *Direct) WithOperationHandlers(handlers map[string]OperationHandler) *Direct {
	table := make(map[string]OperationHandler, len(handlers))
	for id, h := range handlers {
		if h != nil {
			table[id] = h
		}
	}
	d.handlers = table
	return d
}

// OperationHandler implements OperationHandlerProvider.
func (d *Direct) OperationHandler(id string) (OperationHandler, bool) {
	h, ok := d.handlers[id]
	return h, ok
}

// HandledOperations implements OperationHandlerProvider.
func (d *Direct) HandledOperations() []string {
	out := make([]string, 0, len(d.handlers))
	for id := range d.handlers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Compile-time proof that Direct exposes its optional wiring to capability-
// negotiating catalogs instead of relying on error-producing probe calls.
var _ CapabilityReporter = (*Direct)(nil)

// SupportsCapability reports whether this Direct binding can execute a public
// operation with its current wiring. It is deliberately side-effect free and
// fail-closed for unknown names: adding a new catalog entry cannot advertise it
// until Direct explicitly maps the service it requires.
func (d *Direct) SupportsCapability(name string) bool {
	for _, operation := range query.Operations {
		if name == operation {
			return d.querySvc != nil
		}
	}
	switch name {
	case "search", "search_semantic":
		return d.searchSvc != nil
	case "compound", "search_ast", "find_clones", "diagnose":
		return d.querySvc != nil
	case "savings":
		return d.ledger != nil
	case "impact", "analyze",
		"analyze_taint", "analyze_pdg", "analyze_interproc",
		"analyze_contracts", "analyze_githistory", "analyze_pr_risk",
		"analyze_pr_signals", "analyze_pr_questions", "suggest_reviewers",
		"critique_review":
		return d.analysisSvc != nil
	case "refactor_preview", "inline", "safe_delete":
		return d.applier != nil
	case "refactor", "undo":
		return d.applier != nil && d.recorder != nil
	case "pr_comment":
		return d.reviewSvc != nil
	case "memory":
		return d.memoryStore != nil
	case "distill":
		return d.distiller != nil
	case "skillgen":
		return d.skillGen != nil
	case "list_prs":
		return d.forge != nil
	case "triage_prs", "conflicts_prs":
		return d.forge != nil && d.analysisSvc != nil
	case "compare_branches":
		return d.branchState != nil && d.analysisSvc != nil
	case "agent_brief", "explain_symbol", "related_files", "change_risk":
		// These tools intentionally return a typed unavailable/partial result when
		// graph dependencies are absent; the operation itself is fully executable.
		return true
	case "symbol_context", "task_context", "repo_overview", "test_impact", "change_impact", "hotspots", "search_hybrid", "architecture", "architecture_violations", "dead_code", "framework_map":
		// Labs agent intelligence: same typed-unavailable degradation contract.
		return true
	case "trust_report", "graph_health":
		// The trust-report composition is self-contained ("trust_report" is the
		// operation, "graph_health" its P1 Labs MCP tool name): it opens the
		// auto-managed store read-only itself and fails closed to the valid
		// UNAVAILABLE document when no graph exists, so the operation is
		// always executable in-process.
		return true
	case "strict_query":
		// The P1 strict-query wrapper runs a structural query underneath and
		// then filters its result, so it is executable exactly when the query
		// service is. Its optional trust preflight is self-contained like
		// trust_report and adds no requirement of its own.
		return d.querySvc != nil
	default:
		return false
	}
}

// WithLedger attaches a savings ledger so the Savings readout is available. It
// returns the receiver for chaining. Without a ledger, Savings returns
// ErrSavingsUnavailable (query/search are unaffected).
func (d *Direct) WithLedger(l *ledger.Ledger) *Direct {
	d.ledger = l
	return d
}

// WithAnalysis attaches an analysis service so the Analyze surface is available
// (SW-022). It returns the receiver for chaining. Without a service, Analyze
// returns ErrAnalysisUnavailable (query/search/savings are unaffected).
func (d *Direct) WithAnalysis(svc *analysis.Service) *Direct {
	d.analysisSvc = svc
	return d
}

// WithEditor attaches the shared edit/refactor applier + change recorder so the
// RefactorPreview/Refactor/Undo command surface is available (SW-038). It returns
// the receiver for chaining. This is the SINGLE place the engine edit machinery
// is wired into the surface layer; MCP and CLI both reach it through this one
// implementation (parity by construction). Without it, those methods return
// ErrEditUnavailable (query/search/savings/analysis are unaffected).
func (d *Direct) WithEditor(applier *edit.Applier, recorder *edit.ChangeRecorder) *Direct {
	d.applier = applier
	d.recorder = recorder
	return d
}

// WithReview attaches the SW-042 PR-comment publisher so the PrComment surface
// is available. It returns the receiver for chaining. This is the SINGLE place
// the engine/review pipeline is wired into the surface layer; MCP and CLI both
// reach it through this one implementation (parity by construction). Without it,
// PrComment returns ErrReviewUnavailable (query/search/savings/analysis/edit are
// unaffected).
func (d *Direct) WithReview(svc *review.Service) *Direct {
	d.reviewSvc = svc
	return d
}

// WithMemory attaches a memory store so the Memory surface is available (EP-012).
func (d *Direct) WithMemory(store *memory.Store) *Direct {
	d.memoryStore = store
	return d
}

// WithDistill attaches a distiller so the Distill surface is available (EP-012).
func (d *Direct) WithDistill(dist *distill.Distiller) *Direct {
	d.distiller = dist
	return d
}

// WithSkillGen attaches a skill generator so the SkillGen surface is available (EP-012).
func (d *Direct) WithSkillGen(gen *skillgen.Generator) *Direct {
	d.skillGen = gen
	return d
}

// WithForge attaches a read-only forge PR-enumeration client so the ListPRs /
// TriagePRs PR-triage surface is available (SW-105). It returns the receiver for
// chaining. This is the SINGLE place the forge enumeration boundary is wired into
// the surface layer; every surface reaches it through this one implementation
// (parity by construction). The enumeration is the suite's ONLY outbound path; the
// engine triage analyzer it feeds is zero-egress. Without it, ListPRs/TriagePRs
// return ErrForgeUnavailable (everything else is unaffected).
func (d *Direct) WithForge(e forge.Enumerator) *Direct {
	d.forge = e
	return d
}

// WithBranchStates attaches a branch-state materializer so the CompareBranches
// surface is available (SW-107). It returns the receiver for chaining. This is the
// SINGLE place the branch-ref → graph-state materialization boundary is wired into
// the surface layer; every surface reaches it through this one implementation
// (parity by construction). Materialization (indexer/snapshot reuse) stays ABOVE
// the surface boundary; the engine compare-branches analyzer it feeds receives the
// two already-built states as Params and is zero-egress. Without it, CompareBranches
// returns ErrCompareUnavailable (everything else is unaffected).
func (d *Direct) WithBranchStates(m BranchStateMaterializer) *Direct {
	d.branchState = m
	return d
}

// WithReviewFetcher attaches the NET-NEW surface-boundary existing-review fetch
// seam so the CritiqueReview surface can fetch a prior review from the forge when no
// inline review is supplied (SW-108). It returns the receiver for chaining. The
// fetch (GitHub pulls/{n}/reviews + comments) is the ONLY egress in the critique
// path and stays STRICTLY at this surface boundary; the engine critique-review
// analyzer it feeds receives the structured ReviewInput as Params and is zero-egress.
// Without it (and without an inline review) CritiqueReview returns
// ErrReviewFetchUnavailable (everything else is unaffected).
func (d *Direct) WithReviewFetcher(f forge.ReviewFetcher) *Direct {
	d.reviewFetcher = f
	return d
}

// WithRepoRoot records the repository root so the labs agent-intelligence
// tools can read source snippets from repo-relative graph paths regardless of
// the process working directory. Empty keeps cwd-relative reads (today's CLI
// behavior when run from the repo root). It returns the receiver for chaining.
func (d *Direct) WithRepoRoot(root string) *Direct {
	d.repoRoot = root
	return d
}

// WithGitProvider attaches the surface-boundary git-history provider so the
// git-intelligence agent tools (hotspots, change_impact's co-change section)
// can consume bounded local history. Nil keeps the graceful no-history
// degradation. It returns the receiver for chaining.
func (d *Direct) WithGitProvider(p githistory.GitProvider) *Direct {
	d.gitProvider = p
	return d
}

// archRules loads the architecture rules contributed by the repository's
// enabled declarative rule packs (SW-229).
//
// It returns nil, nil when no repository root is bound or no pack is installed,
// which is every binding that existed before this story — so the pack-free path
// is unchanged, down to not opening a lockfile that is not there.
//
// A pack that fails to load is an ERROR, not a skip. Degrading to "run without
// the packs" would answer an architecture question under rules the caller
// believes are in force and that silently were not, which is the shape of
// false-green this tree fails closed against everywhere else.
func (d *Direct) archRules() ([]extpack.ArchRule, error) {
	if d.repoRoot == "" {
		return nil, nil
	}
	set, err := extpack.Load(d.repoRoot)
	if err != nil {
		return nil, err
	}
	return set.ArchRules(), nil
}

// snippetReader is the source reader handed to the labs agent-intelligence
// tools: disk-backed, repo-root-resolved, remote-rejecting.
func (d *Direct) snippetReader() enginecontext.Reader {
	return enginecontext.NewRootedReader(d.repoRoot)
}

// Query implements Client.
func (d *Direct) Query(ctx context.Context, op, symbol string, depth int) ([]byte, error) {
	res, err := d.querySvc.Dispatch(ctx, op, model.NodeId(symbol), depth)
	if err != nil {
		return nil, err
	}
	return query.Marshal(res)
}

// Compound runs a compound / Cypher-style graph query (EP-011 G1). It parses the
// text form, executes over the SAME read-only Reader the fixed queries use, and
// returns the canonical query.Result bytes — byte-identical in shape to Query.
func (d *Direct) Compound(ctx context.Context, queryText string) ([]byte, error) {
	q, err := compound.Parse(queryText)
	if err != nil {
		return nil, err
	}
	res, err := compound.Execute(ctx, d.querySvc.Reader(), q)
	if err != nil {
		return nil, err
	}
	return query.Marshal(res)
}

// Search implements Client.
func (d *Direct) Search(ctx context.Context, q string, limit int) ([]byte, error) {
	if d.searchSvc == nil {
		return nil, ErrSearchUnavailable
	}
	res, err := d.searchSvc.Search(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	return search.Marshal(res)
}

// SemanticSearch implements Client. It dispatches the OPTIONAL semantic search
// through the single search.Service and returns the canonical serialized
// SemanticResponse. When no search service is wired, or no embedder is
// configured, it returns the typed Unavailable response (graceful skip) — NOT
// ErrSearchUnavailable — so the unconfigured bytes are byte-identical across
// every surface (SW-059 parity).
func (d *Direct) SemanticSearch(ctx context.Context, q string, limit int) ([]byte, error) {
	if d.searchSvc == nil {
		return search.MarshalSemantic(search.SemanticResponse{
			Query:     q,
			Available: false,
			Reason:    search.UnavailableReason,
			Hits:      []search.SemanticHit{},
		})
	}
	res, err := d.searchSvc.SemanticSearch(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	return search.MarshalSemantic(res)
}

// SearchAST implements Client. It parses the JSON AstPattern, runs the structural
// query through the single query.Service, and returns the canonical query.Result
// bytes (query.Marshal) — the SAME serializer the symbol queries use, so the bytes
// are byte-identical across surfaces (SW-085 parity). A malformed pattern surfaces
// the engine's typed *query.InvalidPattern error unchanged.
func (d *Direct) SearchAST(ctx context.Context, patternJSON string, limit int) ([]byte, error) {
	pattern, err := query.ParseAstPattern([]byte(patternJSON))
	if err != nil {
		return nil, err
	}
	res, err := d.querySvc.SearchAst(ctx, pattern, limit)
	if err != nil {
		return nil, err
	}
	return query.Marshal(res)
}

// FindClones implements Client. An empty configJSON uses the engine defaults
// (query.DefaultCloneConfig); otherwise the JSON is decoded onto a copy of the
// defaults so partial configs keep sane values. It returns the canonical
// query.CloneResult bytes (query.MarshalCloneResult) for byte-identical parity.
func (d *Direct) FindClones(ctx context.Context, configJSON string) ([]byte, error) {
	cfg := query.DefaultCloneConfig()
	if s := strings.TrimSpace(configJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &cfg); err != nil {
			return nil, err
		}
	}
	res, err := d.querySvc.FindClones(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return query.MarshalCloneResult(res)
}

// Savings implements Client. It returns the canonical savings-ledger readout
// (per-call/session/cumulative USD + cap flags). Without a ledger it returns
// ErrSavingsUnavailable.
func (d *Direct) Savings(ctx context.Context) ([]byte, error) {
	_ = ctx
	if d.ledger == nil {
		return nil, ErrSavingsUnavailable
	}
	return ledger.MarshalReadout(d.ledger.Readout())
}

// Analyze implements Client. It dispatches a named analyzer through the single
// analysis.Service and returns the canonical serialized result. Without an
// analysis service it returns ErrAnalysisUnavailable.
func (d *Direct) Analyze(ctx context.Context, p AnalyzeParams) ([]byte, error) {
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	res, err := d.analysisSvc.Dispatch(ctx, p.Name, analysis.Params{
		Symbol:     model.NodeId(p.Symbol),
		Target:     model.NodeId(p.Target),
		Concept:    p.Concept,
		Direction:  analysis.Direction(p.Direction),
		Kinds:      p.Kinds,
		MaxNodes:   p.MaxNodes,
		MaxPaths:   p.MaxPaths,
		Diff:       p.Diff,
		Provenance: p.Provenance,
	})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// ListPRs implements Client. It enumerates open PRs through the read-only forge
// boundary (the suite's ONLY outbound path) and returns the canonical serialized
// forge.PRList — forge-sourced metadata ONLY. It performs NO graph scoring and
// NO engine traversal: it never touches the analysis service. Without a forge
// client wired it returns ErrForgeUnavailable.
func (d *Direct) ListPRs(ctx context.Context) ([]byte, error) {
	if d.forge == nil {
		return nil, ErrForgeUnavailable
	}
	prs, err := d.forge.ListOpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	return forge.MarshalPRList(prs)
}

// TriagePRs implements Client. It enumerates open PRs through the read-only forge
// boundary (the only egress), maps the forge metadata onto the engine triage
// input, and dispatches the zero-egress `triage-prs` analyzer through the SINGLE
// shared analysis.Service + encoder — so the ranked TriageReport is byte-identical
// across every surface. The forge call is the only outbound activity; the ranking
// itself is a pure in-memory pass over the local graph. Without a forge client it
// returns ErrForgeUnavailable; without an analysis service, ErrAnalysisUnavailable.
func (d *Direct) TriagePRs(ctx context.Context) ([]byte, error) {
	if d.forge == nil {
		return nil, ErrForgeUnavailable
	}
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	prs, err := d.forge.ListOpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]analysis.TriagePRInput, 0, len(prs))
	for _, p := range prs {
		files := make([]string, len(p.ChangedFiles))
		copy(files, p.ChangedFiles)
		inputs = append(inputs, analysis.TriagePRInput{
			Number:       p.Number,
			Title:        p.Title,
			Author:       p.Author,
			BaseRef:      p.BaseRef,
			HeadRef:      p.HeadRef,
			HeadSHA:      p.HeadSHA,
			ChangedFiles: files,
			Additions:    p.Additions,
			Deletions:    p.Deletions,
			Mergeable:    p.Mergeable,
		})
	}
	res, err := d.analysisSvc.Dispatch(ctx, analysis.TriageAnalyzerName, analysis.Params{PRs: inputs})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// ConflictsPRs implements Client. It enumerates open PRs through the read-only
// forge boundary (the only egress), maps the forge metadata onto the engine
// conflicts input, and dispatches the zero-egress `conflicts-prs` analyzer through
// the SINGLE shared analysis.Service + encoder — so the pairwise ConflictReport is
// byte-identical across every surface. The forge call is the only outbound
// activity; the conflict detection itself is a pure in-memory pass over the local
// graph. Without a forge client it returns ErrForgeUnavailable; without an analysis
// service, ErrAnalysisUnavailable.
func (d *Direct) ConflictsPRs(ctx context.Context) ([]byte, error) {
	if d.forge == nil {
		return nil, ErrForgeUnavailable
	}
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	prs, err := d.forge.ListOpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]analysis.ConflictPRInput, 0, len(prs))
	for _, p := range prs {
		files := make([]string, len(p.ChangedFiles))
		copy(files, p.ChangedFiles)
		inputs = append(inputs, analysis.ConflictPRInput{
			Number:       p.Number,
			ChangedFiles: files,
		})
	}
	res, err := d.analysisSvc.Dispatch(ctx, analysis.ConflictsAnalyzerName, analysis.Params{ConflictPRs: inputs})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// SuggestReviewers implements Client. It hands the local-first diff/ref string to
// the zero-egress engine `suggest-reviewers` analyzer through the SINGLE shared
// analysis.Service + encoder — so the ranked ReviewerReport is byte-identical
// across every surface. The diff is untrusted, bounded, path-sanitized input
// resolved through the reused EP-007 kernel; NO outbound activity happens. Without
// an analysis service it returns ErrAnalysisUnavailable.
func (d *Direct) SuggestReviewers(ctx context.Context, diff string) ([]byte, error) {
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	res, err := d.analysisSvc.Dispatch(ctx, analysis.SuggestReviewersAnalyzerName, analysis.Params{Diff: diff})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// CompareBranches implements Client. It materializes the base and head read-only
// graph states from the two branch refs through the injected BranchStateMaterializer
// (indexer/snapshot reuse, ABOVE the surface boundary), then dispatches the
// zero-egress engine `compare-branches` analyzer with the two states as Params —
// the engine never resolves a ref or egresses. The serialized BranchDiffReport is
// byte-identical across every surface. Without a materializer it returns
// ErrCompareUnavailable; without an analysis service, ErrAnalysisUnavailable.
func (d *Direct) CompareBranches(ctx context.Context, baseRef, headRef string) ([]byte, error) {
	if d.branchState == nil {
		return nil, ErrCompareUnavailable
	}
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	base, err := d.branchState.StateForRef(ctx, baseRef)
	if err != nil {
		return nil, err
	}
	head, err := d.branchState.StateForRef(ctx, headRef)
	if err != nil {
		return nil, err
	}
	res, err := d.analysisSvc.Dispatch(ctx, analysis.CompareBranchesAnalyzerName, analysis.Params{
		CompareBase: base,
		CompareHead: head,
	})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// CritiqueReview implements Client (SW-108, the EP-018 capstone). The EXISTING
// review is obtained at the SURFACE boundary: an inline reviewJSON (decoded into the
// structured analysis.ReviewInput here, NEVER inside the engine) takes precedence;
// otherwise it is fetched from the forge for prNumber via the net-new ReviewFetcher
// egress. The structured review + the touched set (diff, reused EP-007
// parseDiff/resolveRef) are handed to the zero-egress engine `critique-review`
// analyzer through the SINGLE shared analysis.Service + encoder, so the
// CritiqueReport is byte-identical across every surface. The engine never resolves a
// remote ref or opens a socket. Without an analysis service it returns
// ErrAnalysisUnavailable; with neither an inline review nor a wired fetcher it
// returns ErrReviewFetchUnavailable.
func (d *Direct) CritiqueReview(ctx context.Context, prNumber int, diff, reviewJSON string) ([]byte, error) {
	if d.analysisSvc == nil {
		return nil, ErrAnalysisUnavailable
	}
	var review analysis.ReviewInput
	switch {
	case strings.TrimSpace(reviewJSON) != "":
		if err := json.Unmarshal([]byte(reviewJSON), &review); err != nil {
			return nil, fmt.Errorf("client: decode inline review: %w", err)
		}
	case d.reviewFetcher != nil:
		ri, err := d.reviewFetcher.FetchReview(ctx, prNumber)
		if err != nil {
			return nil, err
		}
		review = ri
	default:
		return nil, ErrReviewFetchUnavailable
	}
	res, err := d.analysisSvc.Dispatch(ctx, analysis.CritiqueReviewAnalyzerName, analysis.Params{
		Diff:   diff,
		Review: &review,
	})
	if err != nil {
		return nil, err
	}
	return analysis.Marshal(res)
}

// toRefactorOp maps the transport-agnostic request 1:1 onto engine/edit.RefactorOp.
// Keeping the mapping trivial and shared eliminates input-decoding divergence
// between the MCP and CLI surfaces (the only realistic parity risk).
func toRefactorOp(req RefactorRequest, dryRun bool) edit.RefactorOp {
	return edit.RefactorOp{
		Kind:            edit.RefactorKind(req.Kind),
		TargetSymbol:    req.TargetSymbol,
		OldName:         req.OldName,
		NewName:         req.NewName,
		DestinationFile: req.DestinationFile,
		DryRun:          dryRun,
	}
}

// RefactorPreview implements Client. It calls ApplyRefactor with DryRun=true so
// the EP-004 impact set + planned ops are computed and returned WITHOUT any
// mutation (AC-1: impact set BEFORE mutation), then serializes the RefactorResult
// canonically.
func (d *Direct) RefactorPreview(ctx context.Context, req RefactorRequest) ([]byte, error) {
	if d.applier == nil {
		return nil, ErrEditUnavailable
	}
	res, err := d.applier.ApplyRefactor(ctx, toRefactorOp(req, true))
	if err != nil {
		return nil, err
	}
	return edit.MarshalRefactorResult(res)
}

// Refactor implements Client. It commits the refactor through the shared applier
// and the SW-035/036 saga + SW-037 provenance path (NOT re-implemented here),
// persists the auditable change record with the threaded actor, and returns the
// canonical serialized ChangeRecord.
func (d *Direct) Refactor(ctx context.Context, req RefactorRequest, actor string) ([]byte, error) {
	if d.applier == nil || d.recorder == nil {
		return nil, ErrEditUnavailable
	}
	rec, _, err := d.applier.ApplyRefactorRecorded(ctx, toRefactorOp(req, false), actor, d.recorder)
	if err != nil {
		return nil, err
	}
	return edit.MarshalChangeRecord(rec)
}

// Diagnose implements Client (SW-091/SW-094). It runs the graph-derived
// diagnostics over the SAME read-only Reader the queries use and serializes the
// one canonical result through diagnostic.Marshal — the single byte-source every
// surface consumes.
func (d *Direct) Diagnose(ctx context.Context, kinds []string, opts DiagnoseOptions) ([]byte, error) {
	if d.querySvc == nil {
		return nil, ErrDiagnosticUnavailable
	}
	engineOpts := diagnostic.DiagnoseOptions{
		All:                 opts.All,
		ConfidenceThreshold: opts.ConfidenceThreshold,
		SeverityThreshold:   opts.SeverityThreshold,
		JSON:                opts.JSON,
		ExplainSuppressed:   opts.ExplainSuppressed,
	}
	if opts.Root != "" {
		engineOpts.SuppressionConfig.GeneratedMarkerDetector = GeneratedMarkerDetector(opts.Root)
	}
	res, err := diagnostic.DiagnoseWithOptions(ctx, d.querySvc.Reader(), kinds, engineOpts)
	if err != nil {
		return nil, err
	}
	return diagnostic.Marshal(res)
}

// generatedMarkerWindow bounds how much of a file the marker detector reads.
const generatedMarkerWindow = 4096

// GeneratedMarkerDetector returns a detector for in-content generated-code
// markers ("Code generated ... DO NOT EDIT", "@generated") in the head of
// files under root. It is the surface-side I/O companion to the I/O-free
// engine suppression config: paths are repo-relative as recorded in the graph.
// Unreadable files report false (never an error).
func GeneratedMarkerDetector(root string) func(file string) bool {
	return func(file string) bool {
		if file == "" {
			return false
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return false
		}
		defer f.Close()
		buf := make([]byte, generatedMarkerWindow)
		n, _ := io.ReadFull(f, buf)
		head := string(buf[:n])
		return (strings.Contains(head, "Code generated") && strings.Contains(head, "DO NOT EDIT")) ||
			strings.Contains(head, "@generated")
	}
}

// Inline implements Client (SW-092/SW-094). A blocked/unavailable outcome is a
// typed result (not an error) and is serialized like any applied result, so every
// surface sees the same typed marker. Only a genuine apply fault returns an error.
func (d *Direct) Inline(ctx context.Context, req InlineRequest) ([]byte, error) {
	if d.applier == nil {
		return nil, ErrEditUnavailable
	}
	res, err := d.applier.ApplyInline(ctx, edit.InlineOp{TargetSymbol: req.TargetSymbol, DryRun: req.DryRun})
	if err != nil {
		return nil, err
	}
	return edit.MarshalInlineResult(res)
}

// SafeDelete implements Client (SW-093/SW-094). As with Inline, a blocked report
// is a typed result, not an error.
func (d *Direct) SafeDelete(ctx context.Context, req SafeDeleteRequest) ([]byte, error) {
	if d.applier == nil {
		return nil, ErrEditUnavailable
	}
	res, err := d.applier.ApplySafeDelete(ctx, edit.SafeDeleteOp{TargetSymbol: req.TargetSymbol, DryRun: req.DryRun})
	if err != nil {
		return nil, err
	}
	return edit.MarshalSafeDeleteResult(res)
}

// Undo implements Client. It wraps the engine/edit Undo compensating saga
// (restore source + graph snapshot + re-index + consistency check + reversal
// record) and returns the canonical serialized reversal ChangeRecord.
func (d *Direct) Undo(ctx context.Context, undoToken, actor string) ([]byte, error) {
	if d.applier == nil || d.recorder == nil {
		return nil, ErrEditUnavailable
	}
	rec, err := d.applier.Undo(ctx, undoToken, actor, d.recorder)
	if err != nil {
		return nil, err
	}
	return edit.MarshalChangeRecord(rec)
}

// Memory implements Client. It runs memory store/recall/forget/list/export operations and
// returns the canonical serialized MemoryResponse.
func (d *Direct) Memory(ctx context.Context, req MemoryRequest) ([]byte, error) {
	if d.memoryStore == nil {
		return nil, ErrMemoryUnavailable
	}
	switch req.Op {
	case "store":
		id, err := d.memoryStore.StoreMemoryWithProvenance(ctx, memory.ProvenanceInput{
			Scope:       req.Scope,
			Notebook:    req.Notebook,
			Tags:        req.Tags,
			Payload:     req.Payload,
			Kind:        req.Kind,
			Source:      req.Source,
			Confidence:  req.Confidence,
			Evidence:    req.Evidence,
			OverwriteID: memory.ID(req.ID),
		})
		if err != nil {
			return nil, err
		}
		return marshalJSON(MemoryResponse{ID: string(id), Count: 1})
	case "recall":
		entries, err := d.memoryStore.RecallMemory(ctx, memory.Query{
			Scope:      req.Scope,
			Notebook:   req.Notebook,
			TagPrefix:  "",
			CreatedMin: 0,
			CreatedMax: 0,
		})
		if err != nil {
			return nil, err
		}
		return marshalJSON(MemoryResponse{
			Entries: toMemoryEntries(entries),
			Count:   len(entries),
		})
	case "forget":
		if err := d.memoryStore.ForgetMemory(ctx, memory.ID(req.ID)); err != nil {
			return nil, err
		}
		return marshalJSON(MemoryResponse{ID: req.ID, Count: 0})
	case "list":
		entries, err := d.memoryStore.ListMemory(ctx, memory.Query{
			Scope:      req.Scope,
			Notebook:   req.Notebook,
			TagPrefix:  "",
			CreatedMin: 0,
			CreatedMax: 0,
		}, req.Limit)
		if err != nil {
			return nil, err
		}
		return marshalJSON(MemoryResponse{
			Entries: toMemoryEntries(entries),
			Count:   len(entries),
		})
	case "export":
		// SW-112 / SAFE-01: the transport never writes server-side files. A
		// request naming a destination path is rejected with a typed error;
		// the export payload is returned as bytes for the caller to handle
		// locally (the CLI writes them to a file as its own operator action).
		if req.ExportToPath != "" {
			return nil, ErrExportPathRejected
		}
		var buf bytes.Buffer
		if err := d.memoryStore.ExportMemory(ctx, memory.Query{
			Scope:      req.Scope,
			Notebook:   req.Notebook,
			TagPrefix:  "",
			CreatedMin: 0,
			CreatedMax: 0,
		}, &buf); err != nil {
			return nil, err
		}
		return marshalJSON(MemoryResponse{Count: 0, Export: buf.String()})
	default:
		return nil, fmt.Errorf("client: unsupported memory op %q", req.Op)
	}
}

func toMemoryEntries(entries []memory.Entry) []MemoryEntry {
	out := make([]MemoryEntry, len(entries))
	for i, e := range entries {
		out[i] = MemoryEntry{
			ID:            string(e.ID),
			Scope:         e.Scope,
			Notebook:      e.Notebook,
			Tags:          e.Tags,
			Payload:       e.Payload,
			Kind:          e.Kind,
			Source:        e.Source,
			Confidence:    e.Confidence,
			Evidence:      e.Evidence,
			SecretSuspect: e.SecretSuspect,
			CreatedAt:     e.CreatedAt,
			UpdatedAt:     e.UpdatedAt,
		}
	}
	return out
}

// Distill implements Client. It runs session distillation and returns the
// canonical serialized DistillResponse.
func (d *Direct) Distill(ctx context.Context, req DistillRequest) ([]byte, error) {
	if d.distiller == nil {
		return nil, ErrDistillUnavailable
	}
	turns := make([]distill.Turn, len(req.Turns))
	for i, t := range req.Turns {
		turns[i] = distill.Turn{
			ID:       t.ID,
			Prompt:   t.Prompt,
			FilesIn:  t.FilesIn,
			FilesOut: t.FilesOut,
		}
	}
	art, err := d.distiller.Distill(ctx, distill.SessionTrace{
		SessionID:      req.SessionID,
		Turns:          turns,
		Decisions:      req.Decisions,
		Risks:          req.Risks,
		OpenQuestions:  req.OpenQuestions,
		FileReferences: req.FileReferences,
	})
	if err != nil {
		return nil, err
	}
	return marshalJSON(DistillResponse{
		Version:        art.Version,
		SessionID:      art.SessionID,
		Summary:        art.Summary,
		Decisions:      art.Decisions,
		Risks:          art.Risks,
		OpenQuestions:  art.OpenQuestions,
		FileReferences: art.FileReferences,
		TouchedFiles:   art.TouchedFiles,
	})
}

// SkillGen implements Client. It runs deterministic skill generation and returns
// the canonical serialized SkillGenResponse.
func (d *Direct) SkillGen(ctx context.Context, req SkillGenRequest) ([]byte, error) {
	if d.skillGen == nil {
		return nil, ErrSkillGenUnavailable
	}
	steps := make([]skillgen.Step, len(req.Steps))
	for i, s := range req.Steps {
		steps[i] = skillgen.Step{
			Name:        s.Name,
			Action:      s.Action,
			Inputs:      s.Inputs,
			Outputs:     s.Outputs,
			Guard:       s.Guard,
			Description: s.Description,
		}
	}
	skill, md, err := d.skillGen.Generate(ctx, skillgen.Procedure{
		Name:        req.Name,
		Trigger:     req.Trigger,
		Description: req.Description,
		Inputs:      req.Inputs,
		Outputs:     req.Outputs,
		Steps:       steps,
	})
	if err != nil {
		return nil, err
	}
	return marshalJSON(SkillGenResponse{
		Name:        skill.Name,
		Trigger:     skill.Trigger,
		Description: skill.Description,
		Inputs:      skill.Inputs,
		Outputs:     skill.Outputs,
		Steps:       req.Steps,
		Markdown:    string(md),
	})
}

// Brief implements Client. It assembles the agent_brief context packet from
// the wired graph services and memory store (each optional; the brief states
// what is unavailable) and returns the canonical JSON bytes plus a Markdown
// rendering.
func (d *Direct) Brief(ctx context.Context, topic string) ([]byte, []byte, error) {
	res, err := brief.Assemble(ctx, brief.Params{
		Topic:  topic,
		Deps:   d.agentDeps(),
		Memory: d.memoryStore,
	})
	if err != nil {
		return nil, nil, err
	}
	jsonBytes, err := contract.Serialize(res)
	if err != nil {
		return nil, nil, err
	}
	md, err := brief.Markdown(res)
	if err != nil {
		return nil, nil, err
	}
	return jsonBytes, []byte(md), nil
}

// agentDeps assembles the shared EP-020 agent-tool dependency set from the
// wired services. Missing services degrade to the contract's "unavailable"
// outcome inside the tools rather than erroring here.
func (d *Direct) agentDeps() resolve.Deps {
	return resolve.Deps{Query: d.querySvc, Search: d.searchSvc}
}

// ExplainSymbol implements Client via the shared engine/agenttools/explain
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) ExplainSymbol(ctx context.Context, symbol string, maxItems int) ([]byte, error) {
	res, err := explain.Explain(ctx, d.agentDeps(), symbol, maxItems)
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// RelatedFiles implements Client via the shared engine/agenttools/related package.
func (d *Direct) RelatedFiles(ctx context.Context, target, direction string, maxFiles int) ([]byte, error) {
	res, err := related.Files(ctx, d.agentDeps(), target, direction, maxFiles)
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// ChangeRisk implements Client via the shared engine/agenttools/risk package.
func (d *Direct) ChangeRisk(ctx context.Context, target, diff string, maxItems int) ([]byte, error) {
	res, err := risk.Assess(ctx, d.agentDeps(), target, diff, maxItems)
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// SymbolContext implements Client via the shared engine/agenttools/symbolcontext
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) SymbolContext(ctx context.Context, p SymbolContextParams) ([]byte, error) {
	res, err := symbolcontext.Context(ctx, symbolcontext.Params{
		Ref:         p.Symbol,
		Depth:       p.Depth,
		MaxItems:    p.MaxItems,
		TokenBudget: p.TokenBudget,
		Deps:        d.agentDeps(),
		Reader:      d.snippetReader(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// TaskContext implements Client via the shared engine/agenttools/taskctx
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) TaskContext(ctx context.Context, p TaskContextParams) ([]byte, error) {
	res, err := taskctx.Assemble(ctx, taskctx.Params{
		Task:        p.Task,
		TokenBudget: p.TokenBudget,
		MaxItems:    p.MaxItems,
		Deps:        d.agentDeps(),
		Reader:      d.snippetReader(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// RepoOverview implements Client via the shared engine/agenttools/overview
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) RepoOverview(ctx context.Context, p RepoOverviewParams) ([]byte, error) {
	res, err := overview.Assemble(ctx, overview.Params{
		Deps:        d.agentDeps(),
		MaxItems:    p.MaxItems,
		Communities: p.Communities,
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// SearchHybrid implements Client via the shared engine/agenttools/hybridsearch
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) SearchHybrid(ctx context.Context, p SearchHybridParams) ([]byte, error) {
	res, err := hybridsearch.Search(ctx, hybridsearch.Params{
		Query:    p.Query,
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// Architecture implements Client via the shared engine/agenttools/archintel
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) Architecture(ctx context.Context, p ArchitectureParams) ([]byte, error) {
	res, err := archintel.Assemble(ctx, archintel.Params{
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// ArchitectureViolations implements Client via the shared
// engine/agenttools/archintel package, so CLI, MCP, and HTTP emit the same
// canonical bytes.
func (d *Direct) ArchitectureViolations(ctx context.Context, p ArchitectureViolationsParams) ([]byte, error) {
	rules, err := d.archRules()
	if err != nil {
		return nil, err
	}
	res, err := archintel.Violations(ctx, archintel.ViolationsParams{
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
		Rules:    rules,
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// DeadCode implements Client via the shared engine/agenttools/deadcode
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) DeadCode(ctx context.Context, p DeadCodeParams) ([]byte, error) {
	res, err := deadcode.Assemble(ctx, deadcode.Params{
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// FrameworkMap implements Client via the shared engine/agenttools/frameworkmap
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) FrameworkMap(ctx context.Context, p FrameworkMapParams) ([]byte, error) {
	res, err := frameworkmap.Assemble(ctx, frameworkmap.Params{
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// Hotspots implements Client via the shared engine/agenttools/hotspots
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) Hotspots(ctx context.Context, p HotspotsParams) ([]byte, error) {
	res, err := hotspots.Assemble(ctx, hotspots.Params{
		Provider:   d.gitProvider,
		MaxCommits: p.MaxCommits,
		MaxItems:   p.MaxItems,
		Deps:       d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// TestImpact implements Client via the shared engine/agenttools/testimpact
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) TestImpact(ctx context.Context, p TestImpactParams) ([]byte, error) {
	res, err := testimpact.Assemble(ctx, testimpact.Params{
		Target:   p.Target,
		Diff:     p.Diff,
		Depth:    p.Depth,
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// ChangeImpact implements Client via the shared engine/agenttools/changeimpact
// package, so CLI, MCP, and HTTP emit the same canonical bytes.
func (d *Direct) ChangeImpact(ctx context.Context, p ChangeImpactParams) ([]byte, error) {
	res, err := changeimpact.Assemble(ctx, changeimpact.Params{
		Target:   p.Target,
		Diff:     p.Diff,
		Depth:    p.Depth,
		MaxItems: p.MaxItems,
		Deps:     d.agentDeps(),
		Provider: d.gitProvider,
	})
	if err != nil {
		return nil, err
	}
	return contract.Serialize(res)
}

// TrustReport implements Client via the shared trust-report composition
// (trust_report.go) — the SINGLE place the contract §2 document is assembled
// and canonically encoded, so CLI and MCP bytes are identical by construction
// (the explain_symbol template). It is deliberately independent of the wired
// query service: the trust surface observes the repository's durable
// auto-managed store read-only from opts (ADR 0006 observer discipline —
// nothing is created, nothing is repaired), degrading to the fail-closed
// UNAVAILABLE document when no store exists.
func (d *Direct) TrustReport(ctx context.Context, opts TrustReportOptions) ([]byte, trust.Verdict, trust.State, error) {
	return composeTrustReport(ctx, opts)
}

func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// PrComment implements Client. It runs the SW-042 publisher pipeline through the
// single review.Service: consume the three sibling reports once (via the
// service's findings seam over the shared analysis.Service), render the
// deterministic sticky body, evaluate the optional merge gate, and — when
// req.Publish is true — upsert through the mockable host boundary. The default
// (req.Publish=false) is an offline dry-run; the host is never contacted.
//
// SW-043 wires the REAL PR host: on a publish request, the host is resolved from
// the GitHub Actions environment (review.HostFromEnv reads GITHUB_TOKEN from env —
// never argv). When a token is present the upsert goes through the real GitHub
// REST API (the single outbound boundary); when it is absent (local dry-run / no
// CI token) the offline in-memory MockHost keeps the publish path deterministic
// and zero-egress. Without a review service it returns ErrReviewUnavailable.
func (d *Direct) PrComment(ctx context.Context, req PrCommentRequest) ([]byte, error) {
	if d.reviewSvc == nil {
		return nil, ErrReviewUnavailable
	}
	var host review.CommentHost
	if req.Publish {
		gh, err := review.HostFromEnv(prIssueNumber(req.PR))
		if err != nil {
			return nil, err
		}
		if gh != nil {
			host = gh // real GitHub host: the single permitted egress
		} else {
			// No token in the environment: keep the publish path offline and
			// deterministic (local dry-run / tests).
			host = review.NewMockHost()
		}
	}
	res, err := d.reviewSvc.Publish(ctx, host, review.PublishOptions{
		PR:         req.PR,
		Diff:       req.Diff,
		Provenance: req.Provenance,
		Gate:       review.GateConfig{Enabled: req.GateEnabled, BlockThreshold: req.GateThreshold},
		Publish:    req.Publish,
	})
	if err != nil {
		return nil, err
	}
	return review.Marshal(res)
}

// prIssueNumber extracts the PR/issue number from the PR reference rendered in the
// comment header (e.g. "owner/repo#42" or a bare "42"). It returns 0 when no
// number can be parsed, in which case review.HostFromEnv falls back to the
// GITHUB_PR_NUMBER env var (set by the Action entrypoint from the event payload).
func prIssueNumber(pr string) int {
	if i := strings.LastIndexByte(pr, '#'); i >= 0 {
		pr = pr[i+1:]
	}
	pr = strings.TrimSpace(pr)
	n, err := strconv.Atoi(pr)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
