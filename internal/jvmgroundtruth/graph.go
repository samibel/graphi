package jvmgroundtruth

import (
	"strings"

	"github.com/samibel/graphi/core/model"
)

// ConfirmedCalls projects a built graph's CONFIRMED `calls` edges into the
// Call fact space, the graphi side of the differential check. Each edge's
// endpoints carry the facts the comparison keys on: the source path and the
// last QN segment (the JVM QN is `<dir>.<name>`, so the last segment is the
// method — or, for a constructor-call edge whose target is the TYPE node, the
// type's simple name, which the parser normalizes `<init>` to on the bytecode
// side so the two agree).
//
// It imports only core/model (a leaf), never graphstore — the caller reads the
// nodes and edges once and hands them in, keeping this package free of any
// store dependency.
func ConfirmedCalls(nodes []model.Node, edges []model.Edge) []Call {
	byID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID()] = n
	}
	seen := map[Call]struct{}{}
	var out []Call
	for _, e := range edges {
		if e.Kind() != "calls" || e.Tier() != model.TierConfirmed {
			continue
		}
		from, ok := byID[e.From()]
		if !ok {
			continue
		}
		to, ok := byID[e.To()]
		if !ok {
			continue
		}
		c := Call{
			CallerFile:   from.SourcePath(),
			CallerMethod: lastSegment(from.QualifiedName()),
			CalleeFile:   to.SourcePath(),
			Callee:       lastSegment(to.QualifiedName()),
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sortCalls(out)
	return out
}

// lastSegment returns the substring after the final '.', or the whole string.
func lastSegment(qn string) string {
	if i := strings.LastIndexByte(qn, '.'); i >= 0 {
		return qn[i+1:]
	}
	return qn
}
