package query

import "sort"

// tierRank assigns a stable, documented total order to the closed ConfidenceTier
// enum so the comparator can sort by confidence first. Higher confidence sorts
// FIRST (confirmed before derived before heuristic) so the most trustworthy
// edges lead the result; unknown tiers sort last but deterministically.
//
//	confirmed (0) < derived (1) < heuristic (2) < unknown (3)
func tierRank(t string) int {
	switch t {
	case "confirmed":
		return 0
	case "derived":
		return 1
	case "heuristic":
		return 2
	default:
		return 3
	}
}

// sortNodes applies the single canonical node comparator: by node ID ascending.
// Node IDs are content-addressed and unique, so ID alone is a total order. This
// is the ONLY place node ordering is decided, shared by every operation.
// SortNodes is the exported alias of the canonical node comparator, so sibling
// engine packages (e.g. engine/query/compound) reuse the SINGLE ordering and
// cannot drift. Behavior is identical to sortNodes.
func SortNodes(nodes []ResultNode) { sortNodes(nodes) }

func sortNodes(nodes []ResultNode) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
}

// sortEdges applies the single canonical edge comparator (deterministic
// composite tie-break), shared by every operation: confidence tier (most
// confident first), then a fully stable lexicographic cascade over from, to,
// kind, and finally the content-addressed edge ID, which is a unique total-order
// backstop. This guarantees byte-stable ordering regardless of insertion or
// map-iteration order.
// SortEdges is the exported alias of the canonical edge comparator, so sibling
// engine packages reuse the SINGLE deterministic ordering and cannot drift.
// Behavior is identical to sortEdges.
func SortEdges(edges []ResultEdge) { sortEdges(edges) }

func sortEdges(edges []ResultEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if ra, rb := tierRank(string(a.Tier)), tierRank(string(b.Tier)); ra != rb {
			return ra < rb
		}
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
}
