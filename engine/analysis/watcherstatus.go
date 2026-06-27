package analysis

// SW-104 (EP-017 capstone): surface the SW-101 filesystem-watcher health as the
// canonical `watcher-status` operation behind the ONE dispatch table. Unlike the
// graph analyzers, watcher status is daemon/watch runtime state, not a graph
// analysis — so it rides the SAME (*Direct).Analyze -> Dispatch -> Marshal path
// backed by a read-only WatchStatusProvider injected at service construction,
// rather than a bespoke status endpoint. This keeps its envelope encoded by the
// one shared encoder like every other operation.
//
// Honesty obligation (SW-101 follow-up, surfaced not masked): the provider must
// report watcher health/errors HONESTLY — including the SW-101 Reconcile error on
// repos with non-code files — rather than hide a failing watcher behind a green
// status. The SW-101 Reconcile bug itself is out of scope here; this operation
// only reports it faithfully and deterministically.

import (
	"context"

	"github.com/samibel/graphi/engine/query"
)

// WatcherStatusAnalyzerName is the canonical dispatch key for the watcher-status
// operation.
const WatcherStatusAnalyzerName = "watcher-status"

// WatcherStatusReport is the canonical, byte-stable envelope payload for the
// `watcher-status` operation. It carries NO wall-clock / timestamp field so the
// serialized bytes are deterministic; per-root health is reported honestly,
// including any last watcher/reconcile error. Roots are ordered by path at the
// encoder.
type WatcherStatusReport struct {
	Active bool              `json:"active"`
	Roots  []WatchRootStatus `json:"roots"`
}

// WatchRootStatus is the honest health of one watched root: whether it is being
// watched, whether it is healthy (no recorded error), and the verbatim last error
// (empty when healthy). The last error is reported, never masked.
type WatchRootStatus struct {
	Root      string `json:"root"`
	Watching  bool   `json:"watching"`
	Healthy   bool   `json:"healthy"`
	LastError string `json:"last_error,omitempty"`
}

// WatchStatusProvider is the read-only seam the watcher-status operation reads
// from. It is injected at service construction (NewDefaultServiceWithWatch) by the
// cmd layer over the engine/watch Manager; surfaces never construct it. A nil
// provider yields an honest "not active" report (there genuinely is no watcher in
// that context — e.g. a one-shot CLI/MCP/HTTP invocation).
type WatchStatusProvider interface {
	// WatchStatus returns the current, deterministic watcher health snapshot. It
	// must not include any wall-clock/timestamp field in the report.
	WatchStatus(ctx context.Context) WatcherStatusReport
}

// watchStatusAnalyzer routes the `watcher-status` operation to the injected
// provider. It ignores the graph Reader (status is not a graph analysis).
type watchStatusAnalyzer struct {
	provider WatchStatusProvider
}

// Name implements Analyzer.
func (watchStatusAnalyzer) Name() string { return WatcherStatusAnalyzerName }

// Analyze returns the watcher status envelope. With no provider wired it reports
// an honest inactive status (Active=false, empty roots) — not an error and not a
// masked-green status.
func (a watchStatusAnalyzer) Analyze(ctx context.Context, _ query.Reader, p Params) (Analysis, error) {
	var report WatcherStatusReport
	if a.provider != nil {
		report = a.provider.WatchStatus(ctx)
	}
	if report.Roots == nil {
		report.Roots = []WatchRootStatus{}
	}
	rep := report
	return Analysis{
		Analyzer:      WatcherStatusAnalyzerName,
		Outcome:       query.OutcomeFound,
		Symbol:        p.Symbol,
		WatcherStatus: &rep,
	}, nil
}
