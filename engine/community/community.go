// Package community provides deterministic graph community detection for the
// wiki generator (SW-041). A community is the structural package of a symbol —
// the prefix of its qualified name before the final "." (e.g. "pkg.sub.Foo" →
// "pkg.sub"). This is a deterministic, stable, structure-based partition that
// is the natural "community" unit for a code graph: one wiki page per package,
// with cross-links derived from real inter-package edges.
//
// Why package-based over weakly-connected components: WCC treats every edge as
// a link, so any two connected nodes collapse into one component — leaving no
// inter-community edges and therefore no neighbor cross-links (which AC-3
// requires). Package-based partitioning preserves inter-package edges, making
// neighbor cross-links derivable from graph facts.
//
// Determinism: communities are sorted by their package key; members within a
// community are sorted by NodeId. There is no random seed and no wall-clock
// dependence, so the same graph always yields the same partition.
//
// Layering: engine. Depends only on core/model + core/graphstore (read-only).
// Richer clustering (Louvain/modularity) is a documented fast-follow.
package community

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// Community is a detected community: a stable ID (1-based, sorted by package
// key), the package key, and its member node ids (sorted by NodeId).
type Community struct {
	ID      int            // 1-based, stable for a given graph (sorted by Key)
	Key     string         // package key (qualified-name prefix)
	Members []model.NodeId // sorted by NodeId asc
}

// Detect computes the package-based community partition of the graph reachable
// from reader. It is deterministic and read-only. An empty graph yields no
// communities. A symbol whose qualified name has no "." is its own package.
func Detect(ctx context.Context, reader graphstore.Graphstore) ([]Community, error) {
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	// group members by package key
	groups := make(map[string][]model.NodeId)
	for _, n := range nodes {
		k := packageKey(n.QualifiedName())
		groups[k] = append(groups[k], n.ID())
	}

	// stable ordering: communities by package key asc; members by NodeId asc
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Community, 0, len(keys))
	for i, k := range keys {
		members := groups[k]
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		out = append(out, Community{ID: i + 1, Key: k, Members: members})
	}
	return out, nil
}

// packageKey extracts the community key from a qualified name: the prefix before
// the final ".". A name with no "." is its own key. It is the structural
// package, used purely as a deterministic grouping key.
func packageKey(qn string) string {
	if i := lastDot(qn); i >= 0 {
		return qn[:i]
	}
	return qn
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
