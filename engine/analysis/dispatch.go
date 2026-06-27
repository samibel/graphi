package analysis

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/analysis/contracts"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/analysis/interproc"
	"github.com/samibel/graphi/engine/analysis/interproctaint"
	"github.com/samibel/graphi/engine/analysis/pdg"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/community"
	"github.com/samibel/graphi/engine/query"
)

// Service is graphi's single shared analysis dispatch service. It holds a
// read-only graph Reader and a Registry of analyzers, and exposes one Dispatch
// entry point — the ONLY place a named analyzer is routed — so surfaces hold no
// analysis logic of their own and can never diverge (parity by construction,
// mirroring query.Service.Dispatch). It is safe for concurrent use.
type Service struct {
	reader query.Reader
	reg    *Registry
}

// NewService constructs a Service over the given read-only Reader and Registry.
func NewService(reader query.Reader, reg *Registry) *Service {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Service{reader: reader, reg: reg}
}

// NewDefaultService constructs a Service pre-registered with the built-in
// analyzers (impact in SW-022; sibling analyzers add themselves in their own
// stories). It is the convenience constructor surfaces use. The watcher-status
// operation is registered with no provider, so it reports an honest "not active"
// status (a one-shot CLI/MCP/HTTP invocation has no running watcher); the daemon
// wires a real provider via NewDefaultServiceWithWatch.
func NewDefaultService(reader query.Reader) *Service {
	return newDefaultService(reader, nil)
}

// NewDefaultServiceWithWatch is NewDefaultService plus a read-only watcher-status
// provider (SW-104). The daemon, which owns the engine/watch Manager, injects a
// provider here so `watcher-status` reports live, honest per-root health over the
// SAME single dispatch path and shared encoder as every other operation.
func NewDefaultServiceWithWatch(reader query.Reader, watchProvider WatchStatusProvider) *Service {
	return newDefaultService(reader, watchProvider)
}

func newDefaultService(reader query.Reader, watchProvider WatchStatusProvider) *Service {
	reg := NewRegistry()
	// The built-in analyzer registration is best-effort at construction; a
	// duplicate-name error here would indicate a programming fault, so it panics
	// to surface the bug immediately rather than silently dropping an analyzer.
	mustRegister(reg, impactAnalyzer{}, callchainAnalyzer{}, metricsAnalyzer{})
	if s, ok := reader.(Searcher); ok {
		mustRegister(reg, conceptAnalyzer{searcher: s})
	}
	mustRegister(reg, batchedAnalyzer{
		impact:    impactAnalyzer{},
		callChain: callchainAnalyzer{},
		metrics:   metricsAnalyzer{},
	})
	// SW-028: register flow-sensitive taint analyzer with default Go config.
	// SW-102: the adapter now solves the interprocedural taint fixpoint and
	// injects the real solved-summary provider (replacing NoOpSummaryProvider) so
	// cross-procedure source→sink flows resolve from the solved relation. It holds
	// the config + caps and constructs the inner analyzer per call (stateless).
	mustRegister(reg, taintAdapter{cfg: taint.DefaultConfig(), caps: taint.DefaultCaps()})
	// SW-029: register PDG (Program Dependence Graph) analyzer with default
	// config. Like taint, the pdg sub-package cannot import analysis (cycle),
	// so we wrap it with a thin adapter that satisfies analysis.Analyzer.
	mustRegister(reg, pdgAdapter{inner: pdg.New(pdg.DefaultConfig())})
	// SW-030: register interprocedural (Sharir-Pnueli) analyzer with default
	// caps. Like taint, the interproc sub-package cannot import analysis
	// (cycle), so we wrap it with a thin adapter.
	mustRegister(reg, interprocAdapter{inner: interproc.New(interproc.DefaultCaps(), 3)})
	// SW-031: register contract drift detection analyzer with default patterns.
	// Like taint, the contracts sub-package cannot import analysis (cycle), so
	// we wrap it with a thin adapter.
	mustRegister(reg, contractsAdapter{inner: contracts.New(nil)})
	// SW-032: register git-history signal analyzer with a nil provider. The
	// production provider is injected by the caller (CLI/daemon) after
	// constructing the service; the nil provider makes Run return empty results
	// gracefully. The githistory sub-package cannot import analysis (cycle),
	// so we wrap it with a thin adapter that satisfies analysis.Analyzer.
	mustRegister(reg, gitHistoryAdapter{inner: githistory.New(nil, githistory.Config{})})
	// SW-039 (EP-007 1/5): register the pr-risk scorer. It is a composite,
	// read-only Analyzer that consumes EP-004 impact/metrics and EP-005 taint
	// RESULTS through an injectable signalProvider seam (never recomputing them)
	// and emits a versioned per-region RiskReport. Additive: a single
	// registration line plus one MCP descriptor entry.
	mustRegister(reg, newPriskAnalyzer())
	// SW-040 (EP-007 2/5): register the pr-signals detector. It is a composite,
	// read-only Analyzer that consumes EP-004 metrics (hub/bridge), EP-005 PDG
	// (cross-module coupling), and git-history churn RESULTS through an injectable
	// signalSource seam (never recomputing them) and emits a versioned per-region
	// hub/bridge/surprise SignalReport. Additive: a single registration line plus
	// one MCP descriptor entry.
	mustRegister(reg, newPrSignalsAnalyzer())
	// SW-041 (EP-007 3/5): register the pr-questions generator. It is a composite,
	// read-only, DETERMINISTIC Analyzer that consumes the SW-039 RiskReport and the
	// SW-040 SignalReport RESULTS through an injectable questionSource seam (never
	// recomputing scoring or signal detection, no LLM, no network) and emits a
	// versioned reviewer-question set. Additive: a single registration line plus
	// one MCP descriptor entry.
	mustRegister(reg, newPrQuestionsAnalyzer())
	// SW-104 (EP-017 capstone): register the four canonical EP-017 operations
	// EXACTLY ONCE behind this single dispatch table — no parallel per-op path, no
	// per-surface handler. Each routes to its real engine entry point and serializes
	// through the one shared analysis.Marshal encoder.
	//   - taint-query  -> the existing taint/interproc adapter (interproctaint.Solve)
	//   - communities  -> engine/community.DefaultDetector()/Detector.Detect
	//   - notebook-ingest -> committed SW-100 notebook_cell provenance edges
	//   - watcher-status  -> the injected read-only WatchStatusProvider (SW-101)
	mustRegister(reg, taintQueryAdapter{inner: taintAdapter{cfg: taint.DefaultConfig(), caps: taint.DefaultCaps()}})
	mustRegister(reg, communitiesAnalyzer{detector: community.DefaultDetector()})
	mustRegister(reg, notebookAnalyzer{})
	mustRegister(reg, watchStatusAnalyzer{provider: watchProvider})
	// SW-105 (EP-018 1/4): register the triage-prs ranker. It is a composite,
	// read-only, DETERMINISTIC batch driver that consumes an already-enumerated
	// open-PR set (handed in via Params.PRs by the surface-boundary forge client —
	// the engine never touches the network) and reuses the EP-007 pr-risk kernel
	// (scoreRegion) plus the graph primitives (metrics/impact/churn) in a SINGLE
	// pass to emit a byte-stable, totally-ordered ranked TriageReport.
	mustRegister(reg, newTriageAnalyzer())
	// SW-106 (EP-018 2/4): register the conflicts-prs detector. It is a composite,
	// read-only, DETERMINISTIC batch driver that consumes an already-enumerated
	// open-PR set (handed in via Params.ConflictPRs by the surface-boundary forge
	// client — the engine never touches the network), reuses the EP-007 change-set
	// resolution (parseDiff/resolveRef) + the graph primitives (metrics centrality,
	// the impact.go reverse-dependency adjacency) and reports the conflicting PR
	// pairs (textual / graph-semantic / asymmetric contract-dependency) as a
	// byte-stable, totally-ordered ConflictReport over an entity→PRs inverted index.
	mustRegister(reg, newConflictsAnalyzer())
	return NewService(reader, reg)
}

func mustRegister(r *Registry, as ...Analyzer) {
	for _, a := range as {
		if err := r.Register(a); err != nil {
			panic(fmt.Sprintf("analysis: default registration: %v", err))
		}
	}
}

// Dispatch routes a named analyzer to its Analyze method with the given params.
// It is the single entry point both the CLI and MCP surfaces call. An unknown
// analyzer name is a caller error (returned as an error), distinct from an
// unresolved symbol (a typed not-found Analysis, never an error).
func (s *Service) Dispatch(ctx context.Context, name string, p Params) (Analysis, error) {
	a, ok := s.reg.Get(name)
	if !ok {
		return Analysis{}, fmt.Errorf("analysis: unknown analyzer %q (want one of %v)", name, s.reg.Names())
	}
	return a.Analyze(ctx, s.reader, p)
}

// Names returns the sorted list of registered analyzer names (delegates to the
// Registry so surfaces can advertise the available analyzers).
func (s *Service) Names() []string { return s.reg.Names() }

// Reader returns the read-only graph reader the service dispatches against.
// Exposed so callers (and tests) can build additional read-only services over
// the same store.
func (s *Service) Reader() query.Reader { return s.reader }

// taintAdapter wraps taint.Analyzer to satisfy the analysis.Analyzer interface
// without introducing an import cycle (the taint sub-package imports query but
// not analysis). The adapter delegates Name() and maps Run() into the standard
// Analysis envelope.
type taintAdapter struct {
	cfg  taint.Config
	caps taint.Caps
}

func (a taintAdapter) Name() string { return taint.AnalyzerName }

func (a taintAdapter) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	// SW-102: solve the interprocedural taint fixpoint over the same graph, then
	// run the intraprocedural taint analyzer with the solved-summary provider
	// (replacing NoOpSummaryProvider). The single Service.Dispatch path makes the
	// envelope (including the verdict and cross-procedure flows) byte-identical
	// across CLI/MCP/HTTP/daemon.
	sol, err := interproctaint.Solve(ctx, r, a.cfg, interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		return Analysis{}, err
	}
	provider := interproctaint.NewSolvedProvider(sol)

	inner := taint.New(a.cfg, a.caps, provider)
	result, err := inner.Run(ctx, r)
	if err != nil {
		return Analysis{}, err
	}

	report := buildInterprocTaintReport(sol)

	outcome := query.OutcomeFound
	if len(result.Findings) == 0 && len(report.Flows) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer:       taint.AnalyzerName,
		Outcome:        outcome,
		Symbol:         p.Symbol,
		Truncated:      result.Truncated,
		InterprocTaint: report,
	}, nil
}

// TaintQueryAnalyzerName is the SW-104 canonical dispatch key for the
// interprocedural taint-query operation.
const TaintQueryAnalyzerName = "taint-query"

// taintQueryAdapter exposes the EXISTING taint/interproc adapter under the
// canonical `taint-query` operation key (SW-104). It adds no analysis logic: it
// delegates to taintAdapter (interproctaint.Solve + the solved-summary provider)
// and only relabels the envelope's analyzer field to the canonical operation key,
// so the verdict + cross-procedure flows are produced by exactly the same code
// path. Byte-parity across surfaces and across the Solved/Loaded verdict paths
// therefore holds by construction (the flows are canonically sorted by the
// solver; the Loaded/Solved flags are the intended no-recompute observability
// signal).
type taintQueryAdapter struct {
	inner taintAdapter
}

func (taintQueryAdapter) Name() string { return TaintQueryAnalyzerName }

func (a taintQueryAdapter) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	res, err := a.inner.Analyze(ctx, r, p)
	if err != nil {
		return Analysis{}, err
	}
	res.Analyzer = TaintQueryAnalyzerName
	return res, nil
}

// buildInterprocTaintReport maps the engine-layer Solution into the surface
// envelope payload. The flows are already canonically sorted by the solver.
func buildInterprocTaintReport(sol interproctaint.Solution) *InterprocTaintReport {
	flows := make([]InterprocTaintFlow, 0, len(sol.Flows))
	for _, f := range sol.Flows {
		labels := f.Labels
		if labels == nil {
			labels = []string{}
		}
		path := f.CallPath
		if path == nil {
			path = []string{}
		}
		flows = append(flows, InterprocTaintFlow{
			SourceName:   f.SourceName,
			SinkName:     f.SinkName,
			SinkCategory: f.SinkCategory,
			Labels:       labels,
			CallPath:     path,
		})
	}
	return &InterprocTaintReport{
		Verdict: string(sol.Verdict),
		CapKind: sol.CapKind,
		Loaded:  sol.Loaded,
		Solved:  sol.Solved,
		Flows:   flows,
	}
}

// pdgAdapter wraps pdg.Analyzer to satisfy the analysis.Analyzer interface
// without introducing an import cycle (the pdg sub-package imports query but
// not analysis). The adapter delegates Name() and maps Run() into the standard
// Analysis envelope.
type pdgAdapter struct {
	inner *pdg.Analyzer
}

func (a pdgAdapter) Name() string { return a.inner.Name() }

func (a pdgAdapter) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	result, err := a.inner.Run(ctx, r)
	if err != nil {
		return Analysis{}, err
	}
	outcome := query.OutcomeFound
	if len(result.DataDepEdges) == 0 && len(result.ControlDepEdges) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: pdg.AnalyzerName,
		Outcome:  outcome,
		Symbol:   p.Symbol,
	}, nil
}

// interprocAdapter wraps interproc.Analyzer to satisfy the analysis.Analyzer
// interface without introducing an import cycle (the interproc sub-package
// imports query but not analysis). The adapter delegates Name() and maps Run()
// into the standard Analysis envelope.
type interprocAdapter struct {
	inner *interproc.Analyzer
}

func (a interprocAdapter) Name() string { return a.inner.Name() }

func (a interprocAdapter) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	result, err := a.inner.Run(ctx, r)
	if err != nil {
		return Analysis{}, err
	}
	outcome := query.OutcomeFound
	if len(result.Summaries) == 0 && len(result.SCCs) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: interproc.AnalyzerName,
		Outcome:  outcome,
		Symbol:   p.Symbol,
	}, nil
}

// contractsAdapter wraps contracts.Analyzer to satisfy the analysis.Analyzer
// interface without introducing an import cycle (the contracts sub-package
// must not import analysis). The adapter delegates Name() and maps Run() into
// the standard Analysis envelope.
type contractsAdapter struct {
	inner *contracts.Analyzer
}

func (a contractsAdapter) Name() string { return a.inner.Name() }

func (a contractsAdapter) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	result, err := a.inner.Run(ctx, r)
	if err != nil {
		return Analysis{}, err
	}
	outcome := query.OutcomeFound
	if len(result.Contracts) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: contracts.AnalyzerName,
		Outcome:  outcome,
		Symbol:   p.Symbol,
	}, nil
}

// gitHistoryAdapter wraps githistory.Analyzer to satisfy the analysis.Analyzer
// interface without introducing an import cycle (the githistory sub-package
// must not import analysis). The adapter delegates Name() and maps Run() into
// the standard Analysis envelope.
type gitHistoryAdapter struct {
	inner *githistory.Analyzer
}

func (a gitHistoryAdapter) Name() string { return a.inner.Name() }

func (a gitHistoryAdapter) Analyze(ctx context.Context, _ query.Reader, p Params) (Analysis, error) {
	result, err := a.inner.Run(ctx)
	if err != nil {
		return Analysis{}, err
	}
	outcome := query.OutcomeFound
	if len(result.ChurnScores) == 0 && len(result.BusFactors) == 0 && len(result.CoChangeGroups) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: githistory.AnalyzerName,
		Outcome:  outcome,
		Symbol:   p.Symbol,
	}, nil
}
