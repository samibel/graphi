package taint

// Caps defines the resource bounds for taint propagation. When any cap is
// exceeded, the analyzer emits a diagnostic, marks affected findings as
// "incomplete", and records the cap-hit in provenance. No silent truncation.
type Caps struct {
	// MaxNodes bounds the number of distinct nodes visited during propagation.
	// Zero means unlimited.
	MaxNodes int `json:"max_nodes,omitempty"`
	// MaxWork bounds the total number of worklist iterations (node-visit * label
	// combinations). Zero means unlimited.
	MaxWork int `json:"max_work,omitempty"`
	// MaxDepth bounds the maximum propagation depth from any source. Zero means
	// unlimited.
	MaxDepth int `json:"max_depth,omitempty"`
}

// DefaultCaps returns sensible defaults for intraprocedural analysis.
func DefaultCaps() Caps {
	return Caps{
		MaxNodes: 10000,
		MaxWork:  100000,
		MaxDepth: 200,
	}
}

// capHit tracks which cap was exceeded and the value at which it triggered.
type capHit struct {
	Cap   string `json:"cap"`
	Value int    `json:"value"`
	Limit int    `json:"limit"`
}

// exceeded reports whether any cap was hit, returning the first violation.
func (c Caps) exceeded(nodes, work, depth int) (capHit, bool) {
	if c.MaxNodes > 0 && nodes > c.MaxNodes {
		return capHit{Cap: "max_nodes", Value: nodes, Limit: c.MaxNodes}, true
	}
	if c.MaxWork > 0 && work > c.MaxWork {
		return capHit{Cap: "max_work", Value: work, Limit: c.MaxWork}, true
	}
	if c.MaxDepth > 0 && depth > c.MaxDepth {
		return capHit{Cap: "max_depth", Value: depth, Limit: c.MaxDepth}, true
	}
	return capHit{}, false
}
