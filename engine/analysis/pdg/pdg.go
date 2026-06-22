package pdg

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// Analyzer is the Program Dependence Graph analyzer. It is exported so the
// parent analysis package can wrap it with a thin adapter for registry
// dispatch, avoiding an import cycle (pdg cannot import analysis).
type Analyzer struct {
	config PDGConfig
}

// New creates a PDG Analyzer with the given configuration.
func New(cfg PDGConfig) *Analyzer {
	return &Analyzer{config: cfg}
}

// Name returns the analyzer dispatch key.
func (a *Analyzer) Name() string { return AnalyzerName }

// Run executes the full PDG analysis: computes data-dependence edges via
// reaching-definitions and control-dependence edges via post-dominance,
// returning the combined PDGResult.
func (a *Analyzer) Run(ctx context.Context, r query.Reader) (PDGResult, error) {
	// Load all nodes and edges from the graph.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return PDGResult{}, fmt.Errorf("pdg: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return PDGResult{}, fmt.Errorf("pdg: load edges: %w", err)
	}

	// Handle empty graph.
	if len(nodes) == 0 {
		return PDGResult{
			DataDepEdges:    []DepEdge{},
			ControlDepEdges: []DepEdge{},
			Nodes:           []PDGNode{},
		}, nil
	}

	// Build edge-kind filter set.
	kindSet := make(map[string]struct{}, len(a.config.EdgeKinds))
	for _, k := range a.config.EdgeKinds {
		kindSet[k] = struct{}{}
	}

	// Collect sorted node IDs and build node-by-ID index.
	nodeByID := make(map[model.NodeId]model.Node, len(nodes))
	nodeIDSet := make(map[model.NodeId]struct{}, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID()] = n
		nodeIDSet[n.ID()] = struct{}{}
	}
	nodeIDs := make([]model.NodeId, 0, len(nodeIDSet))
	for nid := range nodeIDSet {
		nodeIDs = append(nodeIDs, nid)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	// Apply MaxNodes cap.
	if a.config.MaxNodes > 0 && len(nodeIDs) > a.config.MaxNodes {
		nodeIDs = nodeIDs[:a.config.MaxNodes]
		allowed := make(map[model.NodeId]struct{}, len(nodeIDs))
		for _, nid := range nodeIDs {
			allowed[nid] = struct{}{}
		}
		nodeIDSet = allowed
	}

	// Build forward adjacency (from → list of to), filtered by configured
	// edge kinds and restricted to known nodes.
	adj := make(map[model.NodeId][]model.NodeId, len(nodeIDs))
	for _, e := range edges {
		if len(kindSet) > 0 {
			if _, ok := kindSet[e.Kind()]; !ok {
				continue
			}
		}
		if _, ok := nodeIDSet[e.From()]; !ok {
			continue
		}
		if _, ok := nodeIDSet[e.To()]; !ok {
			continue
		}
		adj[e.From()] = append(adj[e.From()], e.To())
	}

	// Sort and deduplicate adjacency lists for deterministic traversal.
	for k := range adj {
		sort.Slice(adj[k], func(i, j int) bool { return adj[k][i] < adj[k][j] })
		adj[k] = dedup(adj[k])
	}

	// Determine entry node: the node with smallest ID that has outgoing edges
	// but no incoming edges. Fallback: smallest node ID.
	incoming := make(map[model.NodeId]struct{})
	for _, succs := range adj {
		for _, s := range succs {
			incoming[s] = struct{}{}
		}
	}
	entryNode := nodeIDs[0]
	for _, nid := range nodeIDs {
		if _, hasIn := incoming[nid]; !hasIn && len(adj[nid]) > 0 {
			entryNode = nid
			break
		}
	}

	// Phase 1: Reaching-definitions → data-dependence edges.
	dataDeps, rdDiags := reachingDefs(ctx, nodeIDs, adj, a.config)

	// Phase 2: Post-dominance → control-dependence edges.
	ctrlDeps := controlDeps(nodeIDs, adj, entryNode)

	// Build PDGNode list (ALL input nodes, not just participating ones).
	pdgNodes := make([]PDGNode, 0, len(nodeIDs))
	for _, nid := range nodeIDs {
		n := nodeByID[nid]
		pdgNodes = append(pdgNodes, PDGNode{
			ID:            n.ID(),
			Kind:          n.Kind(),
			QualifiedName: n.QualifiedName(),
			SourcePath:    n.SourcePath(),
			Line:          n.Line(),
			Column:        n.Column(),
		})
	}

	// Ensure non-nil slices for deterministic JSON.
	if dataDeps == nil {
		dataDeps = []DepEdge{}
	}
	if ctrlDeps == nil {
		ctrlDeps = []DepEdge{}
	}

	return PDGResult{
		DataDepEdges:    dataDeps,
		ControlDepEdges: ctrlDeps,
		Nodes:           pdgNodes,
		Diagnostics:     rdDiags,
	}, nil
}

// dedup removes duplicates from a sorted slice of NodeIds.
func dedup(s []model.NodeId) []model.NodeId {
	if len(s) <= 1 {
		return s
	}
	out := s[:1]
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1] {
			out = append(out, s[i])
		}
	}
	return out
}
