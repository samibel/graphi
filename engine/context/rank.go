package context

import (
	"sort"
)

// rankCandidates returns a copy of candidates sorted by the deterministic total
// order: Rank ascending (better/upstream-rank-first for search, where smaller is
// better), then Path ascending, then StartLine ascending, then EndLine ascending.
// This stable total order is what makes assembly a pure function of its inputs —
// identical candidate sets always produce identical snippet ordering, regardless
// of input slice order or any map iteration.
//
// Note on Rank direction: EP-001 search ranks are lower-is-better (FTS5), so
// ascending Rank puts the best matches first, matching how the search service
// itself orders results. Query-derived candidates use Rank 0 and sort among
// themselves by Path/StartLine.
func rankCandidates(candidates []Candidate) []Candidate {
	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	sort.SliceStable(out, func(i, j int) bool {
		return candidateLess(out[i], out[j])
	})
	return out
}

// candidateLess is the deterministic comparator. It is exported indirectly via
// rankCandidates and unit-tested directly to pin the total order.
func candidateLess(a, b Candidate) bool {
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	return a.EndLine < b.EndLine
}

// winnow expands a candidate's match span by bounded context padding (clamped to
// file bounds via the reader), reads exactly that span, and returns the snippet.
// The snippet's Citation reflects the ACTUAL expanded+clamped span (got), so it
// always round-trips to the bytes carried in Text.
func winnow(reader engineFor, c Candidate, contextLines int) (Snippet, error) {
	want := Span{
		Start: c.StartLine - contextLines,
		End:   c.EndLine + contextLines,
	}
	text, got, err := reader.ReadSpan(c.Path, want)
	if err != nil {
		return Snippet{}, err
	}
	return Snippet{
		Citation: Citation{Path: c.Path, StartLine: got.Start, EndLine: got.End},
		Text:     text,
		Rank:     c.Rank,
		Tokens:   countTokens(text),
	}, nil
}
