//go:build ignore

// Frozen benchmark fixture (SW-010). Deterministic input for the budget-gated
// benchmark suite. DO NOT edit without bumping bench-budget.yml fixture_digest.
package fixture

// Alpha is the root of the frozen call graph.
func Alpha() int { return beta() + gamma() }

func beta() int  { return delta() }
func gamma() int { return delta()*2 + epsilon() }
