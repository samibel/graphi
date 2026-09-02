// SW-263 semantic-first dispatchers (owner decision 2026-09-01).
//
// The shipped ModeAuto takes the reviewer's replacement AC-2 path:
// semantic candidates S (AC-3 quantised-ordered, unique by canonical
// node_id) form the result prefix, lexical candidates L (the delegated
// hybrid_v1 candidates) backfill unfilled positions. AC-7's byte
// parity with search_hybrid is preserved on the lexical-only fallback
// and on every non-ready state. The exact-PATH override (owner-decided
// 2026-09-01) restores lexical dominance for path queries — the path
// rule is the part of the original AC-6 override the evidence
// supported keeping; the IDENTIFIER rule stays lifted.
//
// Symmetric RRF fusion (ModeFusionNoGraph, ModeFusionGraph) is
// evaluator-only — it implements the pre-redirection SW-263 pipeline
// and is reachable only through the explicit mode pins the AC-9 eval
// harness and the cmd/differential diagnostic select.
package retrieval

import (
	"context"
	"path"
	"sort"
	"strings"
)

// lexicalOnlyRows projects the delegated lexical candidate list into
// the row shape the AC-7 byte-parity contract pins: LexicalRank = i+1,
// SemanticRank = 0, RRF = 0, Graph = lexicalHit.Score (the
// search_hybrid audit score the delegating bridge carries),
// Classification = 0, Final = Score. Region = "lexical_only".
//
// This is the same row projection the AC-7 byte parity with
// search_hybrid's audit output relies on: every row's audit score
// travels through unchanged and the rendered bytes match the
// SW-257 golden verbatim.
func (e *engine) lexicalOnlyRows(lexHits []lexicalHit) []row {
	out := make([]row, len(lexHits))
	for i, h := range lexHits {
		out[i] = row{
			nodeID:        h.NodeID,
			kind:          h.Kind,
			qualifiedName: h.QualifiedName,
			path:          h.Path,
			span:          spanFromLine(h.Line),
			lexicalRank:   i + 1,
			lexicalScore:  h.Score,
			graphScore:    h.Score,
			finalScore:    h.Score,
			region:        regionLexicalOnly,
		}
		if h.Path == "" {
			out[i].ineligible = true
		}
	}
	return out
}

// lexicalPathOverrideRows is the row projection the AC-6 path-override
// sub-dispatch (readyDispatch) emits when isExactPath(query) fires. It
// has the same scoring fields lexicalOnlyRows produces
// (LexicalRank = i+1, SemanticRank = 0, RRF = 0, Graph = Score,
// Final = Score) so the bytes the production-composed retrieval
// module emits on an exact-path query are still byte-identical to
// what search_hybrid (the only production LexicalProvider) emits; the
// only difference is the region tag, which the AC-11 audit carries
// verbatim so a reader of the bytes can distinguish a path-override
// result from the AC-7 non-ready fallback (both produce L unchanged).
//
// Strategy stays "semantic_first" because the dispatch is still
// semantic-first — the path override is a documented sub-case of the
// semantic-first strategy, not a separate strategy. The Region tag is
// where AC-11 carries the per-row provenance.
func (e *engine) lexicalPathOverrideRows(lexHits []lexicalHit) []row {
	out := make([]row, len(lexHits))
	for i, h := range lexHits {
		out[i] = row{
			nodeID:        h.NodeID,
			kind:          h.Kind,
			qualifiedName: h.QualifiedName,
			path:          h.Path,
			span:          spanFromLine(h.Line),
			lexicalRank:   i + 1,
			lexicalScore:  h.Score,
			graphScore:    h.Score,
			finalScore:    h.Score,
			region:        regionLexicalPathOverride,
		}
		if h.Path == "" {
			out[i].ineligible = true
		}
	}
	return out
}

// semanticFirstRows implements the reviewer's replacement AC-2 / AC-5:
//
//   - The eligible semantic candidates S, unique by canonical node_id,
//     form the prefix in exactly their AC-3 quantised order.
//   - If fewer than Limit rows are emitted, scan L (delegated hybrid_v1)
//     in order and append rows whose canonical node_id has not been
//     emitted and whose normalized path has count < MaxPerFile, with
//     the count seeded from the prefix so a saturated prefix path
//     admits no backfill.
//   - An overlapping lexical candidate stamps the prefix row's
//     LexicalRank provenance without changing its semantic identity or
//     position.
//   - A `ready` semantic generation with zero eligible hits stays
//     `ready` and produces lexical backfill; it is not reclassified
//     as a degradation.
//   - The semantic prefix is never reordered, removed or demoted; the
//     cap is a backfill-admission threshold, not a global result cap.
//
// The returned rows are already bounded by Limit. Ineligible rows (no
// resolvable source path) are skipped before dedupe and cap accounting.
func (e *engine) semanticFirstRows(lexHits []lexicalHit, semHits []semanticHit, limit int) []row {
	if limit <= 0 {
		limit = limitDefault
	}
	type rankedLexical struct {
		hit  lexicalHit
		rank int
	}
	// Keep the first lexical occurrence for both provenance and metadata.
	lexByNodeID := make(map[string]rankedLexical, len(lexHits))
	for i, h := range lexHits {
		if _, exists := lexByNodeID[h.NodeID]; !exists {
			lexByNodeID[h.NodeID] = rankedLexical{hit: h, rank: i + 1}
		}
	}

	// Enforce AC-3 here even when a test provider or future provider does not
	// already return quantised order. The production index currently does,
	// but shipped ordering must not depend on that implementation detail.
	semHits = quantisedOrder(semHits)

	// Prefix: the first min(Limit, len(S)) eligible rows of S, unique by
	// canonical node_id. DocumentID remains provenance for the first eligible
	// semantic row and never participates in cross-channel identity.
	seen := make(map[string]struct{}, len(semHits))
	prefix := make([]row, 0, min(limit, len(semHits)))
	for _, sh := range semHits {
		if len(prefix) == limit {
			break
		}
		if _, ok := seen[sh.NodeID]; ok {
			continue
		}
		r := row{
			nodeID:        sh.NodeID,
			documentID:    sh.DocumentID,
			kind:          sh.Kind,
			qualifiedName: sh.QualifiedName,
			path:          sh.Path,
			span:          spanFromLine(sh.Line),
			semanticRank:  len(prefix) + 1, // 1-based over the deduped prefix
			semanticScore: quantiseScore(sh.CosineScore),
			region:        regionSemanticPrefix,
		}
		// Stamp LexicalRank provenance from the delegated lexical list
		// when one exists. The prefix row keeps its semantic identity
		// (DocumentID, SemanticRank) and position; only LexicalRank
		// and (when missing on the semantic side) the kind/path come
		// from the lexical side.
		if ranked, ok := lexByNodeID[sh.NodeID]; ok {
			lh := ranked.hit
			r.lexicalRank = ranked.rank
			if r.kind == "" {
				r.kind = lh.Kind
			}
			if r.qualifiedName == "" {
				r.qualifiedName = lh.QualifiedName
			}
			if r.path == "" {
				r.path = lh.Path
				r.span = spanFromLine(lh.Line)
			}
			r.lexicalScore = lh.Score
		}
		if normalizedResultPath(r.path) == "" {
			// Eligibility precedes dedupe: a later semantic record for the
			// same node may carry resolvable provenance and must remain
			// eligible to become the retained row.
			continue
		}
		seen[sh.NodeID] = struct{}{}
		prefix = append(prefix, r)
	}

	// Backfill: scan L in order. A row's node_id must be unseen AND its
	// normalized path must be under MaxPerFile (with the count seeded
	// from the prefix). The cap is a backfill-admission threshold; the
	// prefix itself is untouched.
	pathCount := make(map[string]int, len(prefix))
	for _, r := range prefix {
		pathCount[normalizedResultPath(r.path)]++
	}
	out := append([]row(nil), prefix...)
	for i, lh := range lexHits {
		if len(out) == limit {
			break
		}
		if _, ok := seen[lh.NodeID]; ok {
			continue // already in the prefix; provenance already stamped
		}
		r := row{
			nodeID:        lh.NodeID,
			kind:          lh.Kind,
			qualifiedName: lh.QualifiedName,
			path:          lh.Path,
			span:          spanFromLine(lh.Line),
			lexicalRank:   i + 1,
			lexicalScore:  lh.Score,
			region:        regionLexicalBackfill,
		}
		pathKey := normalizedResultPath(r.path)
		if pathKey == "" {
			continue
		}
		if pathCount[pathKey] >= maxPerFile {
			continue // saturated prefix path admits no backfill
		}
		seen[lh.NodeID] = struct{}{}
		pathCount[pathKey]++
		out = append(out, r)
	}
	return out
}

// normalizedResultPath is the canonical key for AC-5 admission counts. It
// accepts the repository-relative forms providers may hand us while ensuring
// syntactic aliases such as "./pkg/x.go", "pkg/./x.go", and Windows-style
// separators share one counter.
func normalizedResultPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		return ""
	}
	clean := path.Clean(value)
	if clean == "." {
		return ""
	}
	return strings.TrimPrefix(clean, "./")
}

// fusionRows is the EVALUATOR-ONLY symmetric RRF pipeline. It is
// unchanged from the SW-263 pre-redirection behaviour: union + RRF +
// (optionally) rerank + diversify. The shipped semantic-first mode
// never calls it; production surfaces are forbidden from selecting
// ModeFusionNoGraph or ModeFusionGraph.
//
// AC-6 LIFTED for the shipped semantic-first path (owner decision
// 2026-09-01); the classifier stays live for the evaluator-only
// fusion ablations so a future story can reproduce the
// pre-redirection behaviour when debugging or comparing; no
// production surface can reach this branch.
func (e *engine) fusionRows(ctx context.Context, req Request, lexHits []lexicalHit, semHits []semanticHit, withGraph bool) []row {
	rows := e.union(req.Query, lexHits, semHits)
	rows = e.rrf(rows, true /* semanticActive */, isExactQuery(req.Query))
	if withGraph {
		rows = e.rerank(ctx, req.Query, rows, true /* semanticActive */)
	} else {
		for i := range rows {
			rows[i].graphScore = 0
			rows[i].classScore = 0
			rows[i].finalScore = rows[i].rrfScore
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].finalScore != rows[j].finalScore {
				return rows[i].finalScore > rows[j].finalScore
			}
			return rows[i].nodeID < rows[j].nodeID
		})
	}
	rows = e.diversify(rows, req.Limit)
	for i := range rows {
		rows[i].region = regionFused
	}
	return rows
}
