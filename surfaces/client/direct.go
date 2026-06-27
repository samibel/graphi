package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/diagnostic"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/surfaces/forge"
)

// Direct is an in-process Client backed by query.Service and search.Service, and
// optionally a savings ledger (SW-020), an analysis service (SW-022), an
// edit/refactor applier + change recorder (SW-038), and memory/distill/skillgen
// services (EP-012).
type Direct struct {
	querySvc    *query.Service
	searchSvc   *search.Service
	ledger      *ledger.Ledger
	analysisSvc *analysis.Service
	applier     *edit.Applier
	recorder    *edit.ChangeRecorder
	reviewSvc   *review.Service
	memoryStore *memory.Store
	distiller   *distill.Distiller
	skillGen    *skillgen.Generator
	forge       forge.Enumerator
}

// NewDirect constructs an in-process client.
func NewDirect(q *query.Service, s *search.Service) *Direct {
	return &Direct{querySvc: q, searchSvc: s}
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
func (d *Direct) Diagnose(ctx context.Context, kinds []string) ([]byte, error) {
	if d.querySvc == nil {
		return nil, ErrDiagnosticUnavailable
	}
	res, err := diagnostic.Diagnose(ctx, d.querySvc.Reader(), kinds)
	if err != nil {
		return nil, err
	}
	return diagnostic.Marshal(res)
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

// Memory implements Client. It runs memory store/recall/forget operations and
// returns the canonical serialized MemoryResponse.
func (d *Direct) Memory(ctx context.Context, req MemoryRequest) ([]byte, error) {
	if d.memoryStore == nil {
		return nil, ErrMemoryUnavailable
	}
	switch req.Op {
	case "store":
		id, err := d.memoryStore.StoreMemory(ctx, req.Scope, req.Notebook, req.Tags, req.Payload)
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
	default:
		return nil, fmt.Errorf("client: unsupported memory op %q", req.Op)
	}
}

func toMemoryEntries(entries []memory.Entry) []MemoryEntry {
	out := make([]MemoryEntry, len(entries))
	for i, e := range entries {
		out[i] = MemoryEntry{
			ID:        string(e.ID),
			Scope:     e.Scope,
			Notebook:  e.Notebook,
			Tags:      e.Tags,
			Payload:   e.Payload,
			CreatedAt: e.CreatedAt,
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
