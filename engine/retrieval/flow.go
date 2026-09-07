// SW-282 natural-language candidate scoring (engine/retrieval).
//
// readyDispatch's earlier natural-language sub-dispatch reused
// semanticFirstRows: the AC-3 quantised semantic list formed an immutable
// prefix and lexical candidates only backfilled unfilled positions. That
// discipline is right for an identifier query, but it structurally cannot
// recover a candidate whose embedding ranks it outside the prefix window
// even when its literal name overlaps the question, and it cannot recover a
// candidate that never appears in either top-K list at all — for example a
// short unexported callee ("execute") a well-ranked wrapper ("ExecuteC")
// calls directly (docs/eval/retrieval/runs/2026-09-06-recovery-dev/README.md,
// "Remaining structural retrieval failure").
//
// naturalLanguageRows instead scores the COMPLETE lexical+semantic union
// together: every row keeps its semantic/lexical floor score and adds a
// bounded name-term coverage bonus, so a complementary literal name match
// can move a row ahead of an approximate semantic neighbour without
// requiring it to already occupy a top-K slot in either source list. Before
// the caller's Top-K cut, a second bounded, selective graph read
// (calleeCandidates, docs/adr/0003-selective-read-contract.md) admits the
// direct "calls" callees of the highest-scoring rows as first-class
// candidates in the SAME scoring pass — never a whole-graph scan, never
// more than wrapperExpansionWidth seeds or calleeExpansionCap edges per
// seed.
package retrieval

import (
	"context"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
)

const (
	// nameTermWeight is the maximum bonus a row can earn from bounded
	// name-term coverage: 100% query-term coverage of the row's split
	// qualified name. Partial coverage earns a proportional share
	// (nameTermWeight*matches/terms); repeating one term cannot inflate the
	// bonus past this ceiling (SW-263's original "repeated name tokens
	// cannot accumulate an unbounded bonus" invariant, preserved here).
	nameTermWeight     = 3000
	lexicalFloorMargin = 500

	// wrapperExpansionWidth bounds how many of the highest-scoring rows are
	// treated as callee-expansion seeds. This is independent of the
	// caller's Request.Limit — the graph reads stay bounded and selective
	// regardless of how many rows the caller ultimately asked for.
	wrapperExpansionWidth = 12
	// calleeExpansionCap bounds the direct "calls" callees fetched per
	// seed (one bounded OutgoingBounded + one batched NodesByID read).
	calleeExpansionCap = 4
	// calleeBaseDecay discounts an admitted callee's inherited base score
	// below its wrapper's own base score (the wrapper's finalScore minus
	// its own name-term bonus — the wrapper's semantic/lexical evidence
	// alone). The callee then adds its OWN name-term bonus on top, so two
	// callees of the same wrapper are told apart by their own query-term
	// relevance rather than ranking identically. The cap below
	// (wrapper.finalScore - 1) guarantees a callee can still never outrank
	// the very row that admitted it, so a weakly-matching callee cannot use
	// a strong wrapper to leapfrog it (the cb-24 shape: a wrapper calling a
	// same-purpose helper several times). Replacing the wrapper's own
	// name-term bonus with the callee's (rather than keeping both) was
	// measured to matter: keeping the wrapper's own bonus in the floor let
	// low-relevance callees crowd out real grade-3 rows elsewhere in the
	// pool (SW-282 architecture-flow tuning).
	calleeBaseDecay = 0
)

// naturalLanguageRows scores the complete candidate union — lexical,
// semantic, and bounded wrapper->callee expansions of the highest-scoring
// rows — before the caller's Top-K cut. No row's position is pinned ahead
// of scoring; complementary name evidence or a strong caller relationship
// can both change the order.
func (e *engine) naturalLanguageRows(ctx context.Context, query string, lex []lexicalHit, sem []semanticHit) ([]row, error) {
	rows := e.union(query, lex, sem)
	terms := flowTerms(query)
	floor := 0
	if ordered := quantisedOrder(sem); len(ordered) > 0 {
		floor = max(0, quantiseScore(ordered[len(ordered)-1].CosineScore)-lexicalFloorMargin)
	}
	for i := range rows {
		scoreRow(&rows[i], terms, floor)
	}
	var err error
	rows, err = e.expandCallees(ctx, rows, terms, floor)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].finalScore != rows[j].finalScore {
			return rows[i].finalScore > rows[j].finalScore
		}
		return rows[i].nodeID < rows[j].nodeID
	})
	seen := map[string]bool{}
	out := rows[:0]
	for _, r := range rows {
		if !r.ineligible && !seen[r.nodeID] {
			seen[r.nodeID] = true
			out = append(out, r)
		}
	}
	return out, nil
}

// scoreRow applies the shared candidate score: a row ranked by either
// source keeps its semantic score as the base; an unranked (lexical-only)
// row uses the pool floor (the lowest quantised semantic score admitted,
// less a fixed margin) so it can still compete without inheriting an
// unearned semantic score. Every row then adds its own bounded name-term
// coverage bonus on top.
func scoreRow(r *row, terms []string, floor int) {
	base := r.semanticScore
	if r.semanticRank == 0 {
		base = floor
	}
	r.graphScore = nameTermScore(terms, r.qualifiedName)
	r.baseScore = base
	r.finalScore = base + r.graphScore
	r.region = "evidence_ranked"
}

// nameTermScore is the bounded name-term coverage bonus: query terms
// (flowTerms, already tokenized/stopworded/stemmed) matched against the
// candidate's split qualified name, capped at nameTermWeight regardless of
// how many terms repeat.
func nameTermScore(terms []string, qualifiedName string) int {
	if len(terms) == 0 || qualifiedName == "" {
		return 0
	}
	name := flowTerms(strings.Join(hybridsearch.SplitIdentifier(qualifiedName), " "))
	matches := 0
	for _, term := range terms {
		for _, word := range name {
			if term == word {
				matches++
				break
			}
		}
	}
	return nameTermWeight * matches / len(terms)
}

// expandCallees admits the direct "calls" callees of the wrapperExpansionWidth
// highest-scoring rows as first-class candidates, scored through the same
// nameTermScore pass every other row uses. A callee that is a brand-new
// node_id is appended; a callee that already exists in the union (typically
// at a low, purely-semantic rank — the documented cb-20/cb-22 shape, where
// the callee IS a real candidate but ranks far outside the top window) has
// its score RAISED when the wrapper-derived score is higher, rather than
// being left untouched: "already a candidate" and "already scored on its
// wrapper relationship" are different things, and only the latter makes the
// expansion a no-op. This runs strictly before the caller's Top-K cut
// (naturalLanguageRows returns the complete deduped set; Retrieve applies
// Request.Limit afterward), so an admitted or boosted callee competes on
// equal footing for a Top-K slot rather than being adjusted after
// truncation. A nil graph reader (no bounded graph wired, e.g. most unit
// tests) makes this a no-op, matching the existing degree-signal fallback.
func (e *engine) expandCallees(ctx context.Context, rows []row, terms []string, floor int) ([]row, error) {
	if e.graph == nil || len(rows) == 0 {
		return rows, ctx.Err()
	}
	ranked := append([]row(nil), rows...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].finalScore != ranked[j].finalScore {
			return ranked[i].finalScore > ranked[j].finalScore
		}
		return ranked[i].nodeID < ranked[j].nodeID
	})
	width := wrapperExpansionWidth
	if width > len(ranked) {
		width = len(ranked)
	}
	indexByID := make(map[string]int, len(rows))
	for i, r := range rows {
		indexByID[r.nodeID] = i
	}
	var expanded []row
	for _, wrapper := range ranked[:width] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wrapper.ineligible || wrapper.nodeID == "" {
			continue
		}
		callees, err := e.graph.calleeCandidates(ctx, wrapper.nodeID, calleeExpansionCap)
		if err != nil {
			return nil, err
		}
		if len(callees) == 0 {
			continue
		}
		base := wrapper.finalScore - wrapper.graphScore - calleeBaseDecay
		if base < floor {
			base = floor
		}
		for _, c := range callees {
			if c.Path == "" {
				continue
			}
			derivedGraphScore := nameTermScore(terms, c.QualifiedName)
			derivedFinal := base + derivedGraphScore
			// A callee reached through this wrapper can climb close to the
			// wrapper's own rank, but a wrapper is never demoted below a
			// candidate it only surfaced by relation: cap the contribution
			// at one point under the wrapper's own score (e.g. cb-24's
			// enforceFlagGroupsForCompletion calls processFlagForGroupAnnotation
			// three times; the callee must not outrank the very row that
			// admitted it).
			if wrapperCap := wrapper.finalScore - 1; derivedFinal > wrapperCap {
				derivedFinal = wrapperCap
			}
			if i, exists := indexByID[c.NodeID]; exists {
				// i < 0 marks a node_id already admitted earlier in THIS
				// expansion pass (appended to `expanded`, not yet part of
				// `rows`) — a later wrapper reaching the same callee cannot
				// re-score it via a `rows` index; the first admission wins,
				// consistent with the rest of the union's first-occurrence
				// discipline.
				if i >= 0 && derivedFinal > rows[i].finalScore {
					rows[i].finalScore = derivedFinal
					rows[i].graphScore = derivedFinal - rows[i].baseScore
				}
				continue
			}
			indexByID[c.NodeID] = -1 // claimed by this expansion pass; see append below
			r := row{
				nodeID:        c.NodeID,
				kind:          c.Kind,
				qualifiedName: c.QualifiedName,
				path:          c.Path,
				span:          spanFromLine(c.Line),
				region:        "evidence_ranked",
			}
			r.baseScore = base
			r.graphScore = derivedFinal - base
			r.finalScore = derivedFinal
			expanded = append(expanded, r)
		}
	}
	if len(expanded) == 0 {
		return rows, nil
	}
	return append(rows, expanded...), nil
}

func flowTerms(text string) []string {
	words := hybridsearch.TokenizeForRetrieval(text)
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) > 4 && strings.HasSuffix(w, "ing") {
			w = strings.TrimSuffix(w, "ing")
		} else if len(w) > 4 && strings.HasSuffix(w, "ed") {
			w = strings.TrimSuffix(w, "ed")
		} else if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		if !seen[w] {
			out = append(out, w)
			seen[w] = true
		}
	}
	return out
}
