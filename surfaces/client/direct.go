package client

import (
	"context"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/query"
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

// Query implements Client.
func (d *Direct) Query(ctx context.Context, op, symbol string, depth int) ([]byte, error) {
	res, err := d.querySvc.Dispatch(ctx, op, model.NodeId(symbol), depth)
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
		Symbol:    model.NodeId(p.Symbol),
		Target:    model.NodeId(p.Target),
		Concept:   p.Concept,
		Direction: analysis.Direction(p.Direction),
		Kinds:     p.Kinds,
		MaxNodes:  p.MaxNodes,
		MaxPaths:  p.MaxPaths,
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
