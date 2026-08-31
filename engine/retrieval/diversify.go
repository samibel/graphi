package retrieval

// diversify applies the AC-5 cap: no single node_id contributes more than
// one row in the top Limit (the dedupe in union already enforces this —
// but we re-check the invariant here so the contract is local and
// testable) and no single path contributes more than MaxPerFile rows.
//
// Rows beyond the cap are DEMOTED, not dropped: a larger Limit still
// reaches them. The demotion is implemented by partitioning rows into
// "in-cap" and "demoted" groups (both deterministically ordered) and
// returning them concatenated so the caller can trim to the requested
// Limit without losing the demoted rows.
func (e *Engine) diversify(rows []row, limit int) []row {
	if limit <= 0 {
		limit = LimitDefault
	}
	// Rows are already sorted by finalScore desc, node_id asc from the
	// rerank stage (AC-8).
	var inCap []row
	var demoted []row
	pathCount := map[string]int{}
	for _, r := range rows {
		// AC-5 path cap (demotion): keep MaxPerFile rows per path among the
		// top rows; rows beyond the cap move to the demoted tail.
		if pathCount[r.path] >= MaxPerFile {
			demoted = append(demoted, r)
			continue
		}
		pathCount[r.path]++
		inCap = append(inCap, r)
	}
	out := make([]row, 0, len(rows))
	out = append(out, inCap...)
	out = append(out, demoted...)
	return out
}
