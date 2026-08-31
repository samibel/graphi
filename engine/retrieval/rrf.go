package retrieval

// rrf computes the integer Reciprocal Rank Fusion contribution for one
// row, paying out per contributing source (AC-2):
//
//	rrfScore = sum over sources s that contributed of RRFScale / (RRFk + rank_s)
//
// Where:
//   - lexical contribution fires when lexicalRank > 0;
//   - semantic contribution fires when semanticRank > 0.
//
// Rows in only the lexical list score only the lexical contribution;
// rows in only the semantic list score only the semantic contribution;
// rows in both lists score both contributions. A semantic-only hit is
// therefore reachable in the result (AC-2's "a semantic-only hit shall
// be reachable in the result" requirement) and the fusion reflects
// each source's rank independently, not as an intersection filter.
//
// The whole stage is skipped (all rows score 0) when semanticActive is
// false, which is the case when the semantic list is GLOBALLY absent —
// no embedder configured, no configured-but-not-ready generation, or
// the caller pinned ModeLexicalOnly. The skip is a stage gate, not a
// per-row filter: in particular, lexical rows in a fused union still
// pay their lexical contribution. This is what preserves the AC-7
// byte-parity invariant — in the no-embedder build, rrfScore is
// exactly 0 across the row set, the rerank's lexical score carries
// the row's Final unaltered, and the rendered bytes match
// search_hybrid's audit output verbatim.
//
// The exact flag carries AC-6's verdict for the query (IsExactQuery).
// On an exact identifier / path query the semantic side is demoted from
// a co-equal RRF source to a pure tie-break: the lexical term is paid in
// full and the semantic term becomes exactSemanticTieBreak, which is by
// construction smaller than the gap between any two adjacent lexical RRF
// values. Every lexical candidate therefore outranks every semantic-only
// one, and the semantic rank can only order rows that are already tied
// on lexical rank — which is exactly "lexical rank shall dominate;
// semantic contributes at most a tie-break".
//
// RRFScale=1_000_000 and RRFk=60 are the pinned constants (AC-2).
func (e *Engine) rrf(rows []row, semanticActive, exact bool) []row {
	if !semanticActive {
		for i := range rows {
			rows[i].rrfScore = 0
		}
		return rows
	}
	for i := range rows {
		var rrf int
		if rows[i].lexicalRank > 0 {
			rrf += RRFScale / (RRFk + rows[i].lexicalRank)
		}
		if rows[i].semanticRank > 0 {
			if exact {
				rrf += exactSemanticTieBreak(rows[i].semanticRank)
			} else {
				rrf += RRFScale / (RRFk + rows[i].semanticRank)
			}
		}
		rows[i].rrfScore = rrf
	}
	return rows
}

// exactSemanticTieBreak is the semantic term on an AC-6 exact query: a
// strictly positive value in [1, CandidateK] that decreases with the
// semantic rank.
//
// The bound is what makes it a tie-break rather than a signal. The
// smallest gap between two adjacent lexical RRF values over the pinned
// candidate depth is
//
//	RRFScale/(RRFk+CandidateK-1) - RRFScale/(RRFk+CandidateK)
//
// which is 1_000_000/109 - 1_000_000/110 = 84 at the pinned constants.
// Every value this function returns is at most CandidateK = 50 < 84, so
// adding it can never lift a row past a row with a better lexical rank.
// It can only order rows whose lexical terms are equal — in practice the
// semantic-only tail, every member of which scores below the deepest
// lexical candidate (CandidateK < RRFScale/(RRFk+CandidateK) = 9090).
func exactSemanticTieBreak(semanticRank int) int {
	if semanticRank <= 0 || semanticRank > CandidateK {
		return 0
	}
	return CandidateK + 1 - semanticRank
}
