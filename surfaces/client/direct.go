package client

import (
	"context"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// Direct is an in-process Client backed by query.Service and search.Service, and
// optionally a savings ledger (SW-020) and an analysis service (SW-022).
type Direct struct {
	querySvc    *query.Service
	searchSvc   *search.Service
	ledger      *ledger.Ledger
	analysisSvc *analysis.Service
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
