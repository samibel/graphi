package context

import (
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// FromSearchMatches converts an EP-001 search.Response into context candidates.
// Each search.Match is a point location (a single line/column), so it maps to a
// candidate whose match span is exactly that line (StartLine == EndLine). The
// upstream FTS5 rank is carried through; better matches rank first.
//
// Matches with no source path are dropped (a citation requires a path).
func FromSearchMatches(resp search.Response) []Candidate {
	out := make([]Candidate, 0, len(resp.Matches))
	for _, m := range resp.Matches {
		if m.SourcePath == "" || m.Line < 1 {
			continue
		}
		out = append(out, Candidate{
			Path:      m.SourcePath,
			StartLine: m.Line,
			EndLine:   m.Line,
			Rank:      m.Rank,
			Symbol:    m.QualifiedName,
			Kind:      m.Kind,
		})
	}
	return out
}

// FromQueryResult converts an EP-001 query.Result into context candidates. Each
// result node is a point location (definition/reference line), mapping to a
// candidate whose match span is exactly that line. Query results carry no
// numeric rank, so Rank is left at 0 (neutral); callers that want finer control
// build Candidates directly.
//
// Nodes with no source path are dropped (a citation requires a path).
func FromQueryResult(res query.Result) []Candidate {
	out := make([]Candidate, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		if n.SourcePath == "" || n.Line < 1 {
			continue
		}
		out = append(out, Candidate{
			Path:      n.SourcePath,
			StartLine: n.Line,
			EndLine:   n.Line,
			Rank:      0,
			Symbol:    n.QualifiedName,
			Kind:      n.Kind,
		})
	}
	return out
}
