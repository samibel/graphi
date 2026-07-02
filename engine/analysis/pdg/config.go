package pdg

import "github.com/samibel/graphi/core/model"

// AnalyzerName is the dispatch key for the PDG analyzer in the registry.
const AnalyzerName = "pdg"

// Edge kinds emitted by the PDG analyzer. These are the stable, canonical
// relationship vocabulary for program-dependence edges.
const (
	// EdgeKindDataDep is the edge kind for data-dependence edges (def→use).
	EdgeKindDataDep = "data_dep"
	// EdgeKindControlDep is the edge kind for control-dependence edges
	// (predicate→guarded statement).
	EdgeKindControlDep = "control_dep"
)

// PDGConfig holds the configuration for the PDG analyzer.
type PDGConfig struct {
	// EdgeKinds constrains which existing edge kinds are considered as CFG
	// edges for reaching-definitions and post-dominance analysis. Empty means
	// the default set: {"calls", "references", "defines"}.
	EdgeKinds []string `json:"edge_kinds,omitempty"`

	// MaxNodes bounds the number of nodes processed. Zero means unlimited.
	MaxNodes int `json:"max_nodes,omitempty"`

	// MaxWork bounds the total worklist iterations. Zero means unlimited.
	MaxWork int `json:"max_work,omitempty"`

	// MaxDepth bounds the maximum propagation depth. Zero means unlimited.
	MaxDepth int `json:"max_depth,omitempty"`
}

// DefaultConfig returns sensible defaults for intraprocedural PDG analysis.
func DefaultConfig() PDGConfig {
	return PDGConfig{
		EdgeKinds: []string{"calls", "references", "defines"},
		MaxNodes:  10000,
		MaxWork:   200000,
		MaxDepth:  500,
	}
}

// DepEdge is a single dependence edge in the PDG, carrying provenance.
type DepEdge struct {
	From           model.NodeId `json:"from"`
	To             model.NodeId `json:"to"`
	Kind           string       `json:"kind"` // EdgeKindDataDep or EdgeKindControlDep
	DerivationRule string       `json:"derivation_rule"`
}

// PDGNode is a node participating in the PDG, with its original model metadata.
type PDGNode struct {
	ID            model.NodeId `json:"id"`
	Kind          string       `json:"kind"`
	QualifiedName string       `json:"qualified_name"`
	SourcePath    string       `json:"source_path"`
	Line          int          `json:"line"`
	Column        int          `json:"column"`
}

// PDGResult is the complete output of a PDG analysis run.
type PDGResult struct {
	DataDepEdges    []DepEdge `json:"data_dep_edges"`
	ControlDepEdges []DepEdge `json:"control_dep_edges"`
	Nodes           []PDGNode `json:"nodes"`
	Diagnostics     []string  `json:"diagnostics,omitempty"`
}
