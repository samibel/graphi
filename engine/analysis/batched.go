package analysis

import (
	"context"
	"strings"

	"github.com/samibel/graphi/engine/query"
)

// DefaultBatchedTokenBudget is the documented aggregate token budget for the
// batched response. The serialized JSON of the aggregated impact+chain+metrics
// result is measured with the EP-003-consistent tokenizer; if it exceeds this
// budget, sections are trimmed in a documented priority order (impact nodes,
// then metrics; the chain is preserved as it is small and high-value) and
// Analysis.Truncated is set. This honors "respects the configured token budget,
// no unbounded payload" using the same token-accounting method the EP-003
// context engine and internal/eval use.
const DefaultBatchedTokenBudget = 4000

// batchedAnalyzer is the EP-004 headline job: a thin orchestrator that composes
// the impact (SW-022), call-chain (SW-023), and metrics (SW-025) analyzers into
// a single one-call response. It holds DIRECT references to the concrete sibling
// analyzers (no dispatch indirection, no registry cycle), aggregates their
// outputs into one Analysis with provenance preserved verbatim, and enforces an
// aggregate token budget. It is registered as "batched" alongside its siblings.
type batchedAnalyzer struct {
	impact    Analyzer
	callChain Analyzer
	metrics   Analyzer
}

func (batchedAnalyzer) Name() string { return "batched" }

// Analyze dispatches the constituent analyzers, aggregates their outputs, and
// enforces the token budget. Each section is independent: an empty section
// (no dependents, no chain, no metrics) is reported empty, never failing the
// whole call. Provenance is preserved verbatim through aggregation.
func (a batchedAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "batched"

	var nodes []ReachedNode
	var paths [][]query.ResultEdge
	var metrics []NodeScore
	outcome := query.OutcomeEmpty

	// Impact (forward dependents/blast-radius).
	if a.impact != nil {
		imp, err := a.impact.Analyze(ctx, r, Params{Symbol: p.Symbol, Direction: Forward, MaxNodes: p.MaxNodes})
		if err != nil {
			return Analysis{}, err
		}
		// A genuinely missing symbol propagates as not-found for the whole call.
		if imp.Outcome == query.OutcomeNotFound {
			return notFound(op, p.Symbol), nil
		}
		nodes = imp.Nodes
	}

	// Call-chain (only when a target is provided).
	if a.callChain != nil && p.Target != "" {
		ch, err := a.callChain.Analyze(ctx, r, Params{Symbol: p.Symbol, Target: p.Target, MaxPaths: p.MaxPaths})
		if err != nil {
			return Analysis{}, err
		}
		paths = ch.Paths
	}

	// Metrics (graph-wide signals).
	if a.metrics != nil {
		mt, err := a.metrics.Analyze(ctx, r, Params{MaxNodes: p.MaxNodes})
		if err != nil {
			return Analysis{}, err
		}
		metrics = mt.Metrics
	}

	if len(nodes) > 0 || len(paths) > 0 || len(metrics) > 0 {
		outcome = query.OutcomeFound
	}

	res := Analysis{
		Analyzer: op,
		Outcome:  outcome,
		Symbol:   p.Symbol,
		Nodes:    nodes,
		Paths:    paths,
		Metrics:  metrics,
	}

	budget := p.MaxTokens
	if budget <= 0 {
		budget = DefaultBatchedTokenBudget
	}
	enforceBudget(&res, budget)
	return res, nil
}

// countTokens is the deterministic offline tokenizer used for SOURCE-TEXT
// budgeting, intentionally the SAME whitespace-split counter as
// engine/context.countTokens and internal/eval.CountTokens. (Unexported in
// engine/context; the 1-line duplication is documented and matches the
// existing intentional duplication between engine/context and internal/eval.)
//
// NOTE: whitespace-split is the right measure for source spans (natural word
// boundaries) but NOT for compact structured JSON, which has no whitespace —
// strings.Fields of a compact JSON payload collapses to ~1. estimateJSONTokens
// below is the structured-payload adaptation used by the batched budget.
func countTokens(text string) int { return len(strings.Fields(text)) }

// estimateJSONTokens estimates the token cost of a compact JSON payload using
// the standard ~4-chars-per-token heuristic. This is the structured-payload
// counterpart to the EP-003 whitespace tokenizer: whitespace-split is correct
// for source spans, but compact JSON has no whitespace, so a byte-derived
// estimate is the honest measure for analysis-result budgeting. (Consistent in
// spirit with common tokenizers' byte fallback and with internal/eval's parity
// harness, which applies the 4-chars/token convention to serialized payloads.)
func estimateJSONTokens(b []byte) int {
	const charsPerToken = 4
	return (len(b) + charsPerToken - 1) / charsPerToken
}

// enforceBudget trims the aggregated analysis until its canonical serialized
// JSON fits within budget tokens (estimated via estimateJSONTokens). Trimming
// priority (cheapest signal loss first, highest-value preserved): drop the
// lowest-ranked impact nodes, then drop the lowest-ranked metrics. The
// call-chain paths are preserved (small, high-value). Sets
// Analysis.Truncated if any trimming occurred. Trimming respects the canonical
// per-section sort already applied by the analyzers/Marshal.
func enforceBudget(a *Analysis, budget int) {
	if budget <= 0 {
		return
	}
	for {
		b, err := Marshal(*a)
		if err != nil {
			return
		}
		if estimateJSONTokens(b) <= budget {
			return
		}
		// Priority 1: drop the last (lowest-ranked) impact node.
		if len(a.Nodes) > 0 {
			a.Nodes = a.Nodes[:len(a.Nodes)-1]
			a.Truncated = true
			continue
		}
		// Priority 2: drop the last (lowest-ranked) metric.
		if len(a.Metrics) > 0 {
			a.Metrics = a.Metrics[:len(a.Metrics)-1]
			a.Truncated = true
			continue
		}
		// Nothing left to trim (chain only, or empty); stop to avoid looping.
		return
	}
}
