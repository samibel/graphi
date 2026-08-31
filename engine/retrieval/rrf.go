package retrieval

// rrf computes the integer Reciprocal Rank Fusion contribution for one
// row. When BOTH lexical and semantic candidates exist (AC-2), the row's
// rrfScore is the sum of contributions from each contributing source:
// rrfScore = RRFScale / (RRFk + rank). RRFScale=1_000_000 and RRFk=60
// are the pinned constants.
//
// When only one source contributed (the no-embedder byte-parity path),
// rrfScore is left at 0: RRF over a single list is the identity
// transformation, so the Final equals the rerank score (which equals
// search_hybrid's audit score for that path) and the byte-parity
// invariant (AC-7) holds.
func (e *Engine) rrf(rows []row) []row {
	for i := range rows {
		var rrf int
		if rows[i].lexicalRank > 0 && rows[i].semanticRank > 0 {
			if rows[i].lexicalRank > 0 {
				rrf += RRFScale / (RRFk + rows[i].lexicalRank)
			}
			if rows[i].semanticRank > 0 {
				rrf += RRFScale / (RRFk + rows[i].semanticRank)
			}
		}
		rows[i].rrfScore = rrf
	}
	return rows
}
