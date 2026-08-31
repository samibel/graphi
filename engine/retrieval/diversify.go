package retrieval

// diversify applies the AC-5 cap and the AC-2 eligibility rule:
//
//   - AC-5: no single node_id contributes more than one row in the top
//     Limit (the dedupe in union already enforces this — but we re-check
//     the invariant here so the contract is local and testable) and no
//     single path contributes more than MaxPerFile rows. Rows beyond
//     the cap are DEMOTED, not dropped: a larger Limit still reaches
//     them. The demotion is implemented by partitioning rows into
//     "in-cap" and "demoted" groups (both deterministically ordered)
//     and returning them concatenated so the caller can trim to the
//     requested Limit without losing the demoted rows.
//
//   - AC-2 eligibility (applied BEFORE truncation, per the
//     SW-263/decision-ac9 reviewer judgement): rows with no resolvable
//     source path are dropped from the result entirely, so the top
//     Limit never contains a slot that an eligible row could have
//     backfilled. The downstream matcher cannot credit an
//     unidentified-path row against any judgement, and the runner's
//     token counter errors on a directory; surfacing them only to drop
//     them shifts surviving hits upward without backfill, which a
//     future pass would inflate. Filtering here, before finaliseRows,
//     makes the eligibility rule a property of the retrieval module's
//     output rather than of a downstream projection step.
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
		// AC-2 eligibility: a row with no source path cannot be matched
		// against a file-scoped judgement, so it cannot earn relevance.
		// Drop it BEFORE ranking/truncation so a backfillable eligible
		// row from beyond the cap can take its slot — see the comment
		// above for why this is the property we want on the retrieval
		// module's output, not on a downstream projection.
		if r.ineligible {
			continue
		}
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
