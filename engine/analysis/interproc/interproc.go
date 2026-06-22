package interproc

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// AnalyzerName is the dispatch key for the interprocedural analyzer in the
// analysis registry.
const AnalyzerName = "interproc"

// InterprocResult is the complete output of an interprocedural analysis run.
type InterprocResult struct {
	// Summaries maps procedure ID to its Sharir-Pnueli functional summary.
	Summaries map[string]Summary `json:"summaries"`
	// SCCs is the list of strongly connected components in reverse topological
	// order (callees before callers).
	SCCs []SCC `json:"sccs"`
	// Diagnostics collects cap-hit messages and solver diagnostics.
	Diagnostics []string `json:"diagnostics,omitempty"`
	// CacheStats is a snapshot of the summary cache statistics at completion.
	CacheStats CacheStats `json:"cache_stats"`
}

// Analyzer is the Sharir-Pnueli interprocedural analysis engine. It is exported
// so the parent analysis package can wrap it with a thin adapter for registry
// dispatch, avoiding an import cycle (interproc cannot import analysis).
type Analyzer struct {
	caps              Caps
	wideningThreshold int
	cache             *SummaryCache
}

// New creates an interprocedural Analyzer with the given caps and widening
// threshold. If wideningThreshold <= 0, DefaultWideningThreshold is used.
func New(caps Caps, wideningThreshold int) *Analyzer {
	if wideningThreshold <= 0 {
		wideningThreshold = DefaultWideningThreshold
	}
	return &Analyzer{
		caps:              caps,
		wideningThreshold: wideningThreshold,
		cache:             NewSummaryCache(caps.MaxSummaryEntries),
	}
}

// Name returns the analyzer dispatch key.
func (a *Analyzer) Name() string { return AnalyzerName }

// Run executes the full interprocedural analysis over the graph accessible via
// the read-only Reader. It builds the call graph, detects SCCs, and runs the
// fixpoint solver to compute procedure summaries.
func (a *Analyzer) Run(ctx context.Context, r query.Reader) (InterprocResult, error) {
	// Load all nodes and edges from the graph.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return InterprocResult{}, fmt.Errorf("interproc: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return InterprocResult{}, fmt.Errorf("interproc: load edges: %w", err)
	}

	// If the graph is empty, return an empty result immediately.
	if len(nodes) == 0 {
		return InterprocResult{
			Summaries:  map[string]Summary{},
			SCCs:       nil,
			CacheStats: a.cache.Stats(),
		}, nil
	}

	// Index nodes by ID.
	nodeByID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID()] = n
	}

	// Build the call graph from "calls" edges.
	callGraph := make(CallGraph)
	for _, e := range edges {
		if e.Kind() == "calls" {
			fromID := string(e.From())
			toID := string(e.To())
			callGraph[fromID] = append(callGraph[fromID], toID)
		}
	}

	// Sort call graph adjacency lists for determinism.
	for k := range callGraph {
		sort.Strings(callGraph[k])
	}

	// Collect all procedure IDs: any node that appears in the call graph.
	procSet := make(map[string]bool)
	for caller, callees := range callGraph {
		procSet[caller] = true
		for _, callee := range callees {
			procSet[callee] = true
		}
	}

	// If no call edges found, treat each node as a leaf procedure.
	if len(procSet) == 0 {
		for _, n := range nodes {
			procSet[string(n.ID())] = true
		}
	}

	// Detect SCCs via Tarjan.
	sccs := TarjanSCC(callGraph)

	// Build ProcBody for each procedure.
	procs := make(map[string]ProcBody, len(procSet))
	for procID := range procSet {
		callees := callGraph[procID]
		// Input labels: the procedure's own ID (representing reachability).
		inputLabels := []string{procID}
		sort.Strings(inputLabels)

		localCallees := callees
		transfer := buildTransfer(localCallees)

		procs[procID] = ProcBody{
			ID:          procID,
			Callees:     callees,
			InputLabels: inputLabels,
			Transfer:    transfer,
		}
	}

	// Run fixpoint solver.
	solver := NewFixpointSolver(a.caps, a.wideningThreshold, a.cache)
	solveResult, err := solver.Solve(ctx, procs, sccs)
	if err != nil {
		return InterprocResult{}, fmt.Errorf("interproc: solve: %w", err)
	}

	return InterprocResult{
		Summaries:   solveResult.Summaries,
		SCCs:        solveResult.SCCs,
		Diagnostics: solveResult.Diagnostics,
		CacheStats:  solveResult.CacheStats,
	}, nil
}

// buildTransfer creates a transfer function that propagates input labels
// and composes callee summaries at call sites.
func buildTransfer(callees []string) func([]string, func(string) Summary) []string {
	return func(input []string, calleeSummary func(string) Summary) []string {
		outputSet := make(map[string]bool, len(input))
		for _, l := range input {
			outputSet[l] = true
		}

		// Compose callee summaries: union the output labels of all callees.
		for _, calleeID := range callees {
			s := calleeSummary(calleeID)
			for _, l := range s.OutputLabels {
				outputSet[l] = true
			}
		}

		out := make([]string, 0, len(outputSet))
		for l := range outputSet {
			out = append(out, l)
		}
		sort.Strings(out)
		return out
	}
}
