package contracts

import (
	"context"

	"github.com/samibel/graphi/engine/query"
)

// AnalyzerName is the dispatch key for the contracts analyzer in the registry.
const AnalyzerName = "contracts"

// ContractResult is the complete output of a contract drift analysis run.
type ContractResult struct {
	// Contracts is the set of detected producer and consumer contracts.
	Contracts []Contract `json:"contracts"`
	// Links is the set of matched producer↔consumer pairs.
	Links []ContractLink `json:"links,omitempty"`
	// Drifts is the set of structural drifts detected between linked pairs.
	Drifts []Drift `json:"drifts,omitempty"`
	// Diagnostics carries human-readable operational notes (e.g. cap limits,
	// pattern matching notes).
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Analyzer is the contract drift detection analyzer. It is exported so the
// parent analysis package can wrap it with a thin adapter for registry
// dispatch, avoiding an import cycle (contracts cannot import analysis).
type Analyzer struct {
	registry *PatternRegistry
}

// New creates a contracts Analyzer with the given pattern registry. Pass nil
// to use the DefaultPatternRegistry.
func New(registry *PatternRegistry) *Analyzer {
	if registry == nil {
		registry = DefaultPatternRegistry()
	}
	return &Analyzer{registry: registry}
}

// Name returns the analyzer dispatch key.
func (a *Analyzer) Name() string { return AnalyzerName }

// Run executes the full contract drift analysis over the read-only graph:
//  1. Detect contracts (producers and consumers) via the pattern registry.
//  2. Match producer↔consumer pairs by protocol and service key.
//  3. Compare linked surfaces and detect structural drift.
//
// The result is fully deterministic: sorted contracts, links, and drifts.
func (a *Analyzer) Run(ctx context.Context, r query.Reader) (ContractResult, error) {
	// Step 1: detect all contracts.
	detectedContracts, err := detectContracts(ctx, r, a.registry)
	if err != nil {
		return ContractResult{}, err
	}

	if len(detectedContracts) == 0 {
		return ContractResult{
			Contracts: []Contract{},
		}, nil
	}

	// Step 2: match producer↔consumer pairs.
	links := matchProducerConsumer(detectedContracts)

	// Step 3: detect drifts on linked pairs.
	drifts := detectDrifts(links)

	var diagnostics []string
	if len(links) == 0 && len(detectedContracts) > 0 {
		diagnostics = append(diagnostics, "contracts detected but no producer/consumer matches found")
	}

	return ContractResult{
		Contracts:   detectedContracts,
		Links:       links,
		Drifts:      drifts,
		Diagnostics: diagnostics,
	}, nil
}

// Registry returns the pattern registry for external inspection or testing.
func (a *Analyzer) Registry() *PatternRegistry { return a.registry }
