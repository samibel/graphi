package client

import (
	"context"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
)

// Direct is an in-process Client backed by query.Service and search.Service, and
// optionally a savings ledger (SW-020), an analysis service (SW-022), and an
// edit/refactor applier + change recorder (SW-038).
type Direct struct {
	querySvc    *query.Service
	searchSvc   *search.Service
	ledger      *ledger.Ledger
	analysisSvc *analysis.Service
	applier     *edit.Applier
	recorder    *edit.ChangeRecorder
	reviewSvc   *review.Service
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
