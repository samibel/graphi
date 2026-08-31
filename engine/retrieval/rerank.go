package retrieval

import (
	"context"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/classify"
)

// rerank applies the integer-signal rerank to each row. The lexical
// provider's pre-computed score (carried on row.lexicalScore) is
// authoritative when non-zero: the production HybridSearchBridge
// already scored the row with the audited hybridsearch signals, and
// reusing that score is what gives the AC-7 byte-parity invariant on
// the lexical-only path.
//
// A non-delegating provider leaves lexicalScore at 0; the rerank then
// computes the score from the hybridsearch signals (SegmentExact,
// SegmentPrefix, NameSubstring, PathSegment, FullCoverage) plus the
// bounded degree signal — with default weights that match the audited
// hybridsearch numbers (SegmentExact=100, SegmentPrefix=40,
// NameSubstring=15, PathSegment=30, FullCoverage=50, DegreePoint=2) so a
// non-delegating pipeline still produces byte-equivalent scores.
//
// AC-4 (SW-263 review / item 3): on the fused path (semanticActive=true)
// every row receives the definition bonus AND the vendor/generated
// classification penalty uniformly, regardless of which scoring path
// produced the base. AC-4 vs AC-7 resolution (item 5): on the
// lexical-only path (semanticActive=false) those rerank signals are
// NOT applied — the AC-7 byte-parity contract is that the
// lexical-only row set's final scores match search_hybrid's audit
// scores byte-for-byte, and search_hybrid does not apply the bonus or
// the penalty. The signal set is conditional on the semantic path
// being active — the documented resolution the SW-263 review requires.
//
// sort.SliceStable with the explicit tie-break (finalScore desc,
// node_id asc) guarantees a byte-identical Result across two runs
// (AC-8).
func (e *Engine) rerank(ctx context.Context, query string, rows []row, semanticActive bool) []row {
	tokens := hybridsearch.Tokenize(query)
	for i := range rows {
		if rows[i].lexicalScore != 0 {
			// Delegating path: search_hybrid's audit score is the base
			// for the audited integer signals.
			rows[i].graphScore = rows[i].lexicalScore
		} else {
			rows[i].graphScore, _, _ = e.scoreOne(ctx, tokens, rows[i])
		}
		// AC-4 vs AC-7 resolution (SW-263 review / item 5): the rerank's
		// own signals (definition bonus, vendor/generated classification
		// penalty) apply on the fused path only. On the lexical-only
		// path the score carries through unchanged so the row bytes
		// match search_hybrid's audit score verbatim — the AC-7
		// byte-parity contract is preserved.
		if semanticActive {
			if rows[i].lexicalScore != 0 {
				// Delegating: derive the verdict from row fields.
				rows[i].isDefinition = isDefinitionKind(rows[i].kind)
				rows[i].pathClass = classifyPathClass(rows[i].path)
			} else {
				// Non-delegating: re-score to recover the verdicts
				// scoreOne returned (we discarded them above to keep
				// the multi-return binding simple). The score is
				// identical, so no ranking shift.
				_, rows[i].isDefinition, rows[i].pathClass = e.scoreOne(ctx, tokens, rows[i])
			}
			if rows[i].pathClass != "" {
				rows[i].classScore = classifyPenalty(rows[i].pathClass)
			}
			if rows[i].isDefinition {
				rows[i].graphScore += defaultRerankWeights.DefinitionBonus
			}
		}
		rows[i].finalScore = rows[i].rrfScore + rows[i].graphScore + rows[i].classScore
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].finalScore != rows[j].finalScore {
			return rows[i].finalScore > rows[j].finalScore
		}
		return rows[i].nodeID < rows[j].nodeID
	})
	return rows
}

// scoreOne computes the per-row rerank contribution from the
// hybridsearch signals + degree signal. It is the non-delegating
// fallback; the production delegating HybridSearchBridge produces
// the same numbers and bypasses this function.
func (e *Engine) scoreOne(ctx context.Context, tokens []string, r row) (int, bool, string) {
	hWeights := hybridsearch.DefaultWeights()
	segments := hybridsearch.SplitIdentifier(r.qualifiedName)
	pathSegs := hybridsearch.SplitPath(r.path)
	lowerName := strings.ToLower(r.qualifiedName)
	var nameScore int
	var matched bool
	for _, tok := range tokens {
		var hit bool
		if containsString(segments, tok) {
			nameScore += hWeights.SegmentExact
			hit = true
		} else if len(tok) >= 3 && hasPrefixAny(segments, tok) {
			nameScore += hWeights.SegmentPrefix
			hit = true
		} else if strings.Contains(lowerName, tok) {
			nameScore += hWeights.NameSubstring
			hit = true
		}
		if containsString(pathSegs, tok) {
			nameScore += hWeights.PathSegment
			hit = true
		}
		if hit {
			matched = true
		}
	}
	if len(tokens) > 0 && matched {
		fullCoverage := true
		// Match all tokens: full coverage bonus applies.
		for _, tok := range tokens {
			hit := false
			if containsString(segments, tok) || strings.Contains(lowerName, tok) {
				hit = true
			}
			if len(tok) >= 3 && hasPrefixAny(segments, tok) {
				hit = true
			}
			if containsString(pathSegs, tok) {
				hit = true
			}
			if !hit {
				fullCoverage = false
				break
			}
		}
		if fullCoverage {
			nameScore += hWeights.FullCoverage
		}
	}
	isDefinition := r.kind == "function" || r.kind == "method" || r.kind == "type"
	var pathClass string
	if classify.IsGeneratedPath(r.path) {
		pathClass = "generated"
	}
	var degScore int
	if e.graph != nil {
		if d, err := e.graph.InboundDegree(ctx, r.nodeID, 32); err == nil {
			if d < 0 {
				d = 0
			}
			degScore = d * hWeights.DegreePoint
		}
	}
	return nameScore + degScore, isDefinition, pathClass
}

// classifyPenalty returns the integer penalty for a path class string.
// Empty pathClass yields 0 (no penalty).
func classifyPenalty(class string) int {
	switch class {
	case "generated":
		return defaultRerankWeights.GeneratedPenalty
	case "vendor":
		return defaultRerankWeights.VendorPenalty
	}
	return 0
}

// isDefinitionKind is the boolean test the rerank applies to decide
// whether to add the definition bonus. It is the same predicate
// scoreOne uses internally so a delegating row (whose base score
// already includes search_hybrid's audited signals) and a non-delegating
// row get the same bonus-or-no-bonus verdict.
func isDefinitionKind(kind string) bool {
	return kind == "function" || kind == "method" || kind == "type"
}

// classifyPathClass returns the integer class name the rerank uses for
// the vendor/generated classification penalty. Empty string means no
// penalty applies. The single source of truth is engine/classify so
// a path the runtime classifies as generated is the same path the
// rerank penalises.
func classifyPathClass(path string) string {
	if classify.IsGeneratedPath(path) {
		return "generated"
	}
	return ""
}

// containsString and hasPrefixAny are the local copies the rerank uses
// so the import surface stays internal.
func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

func hasPrefixAny(set []string, prefix string) bool {
	for _, v := range set {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}
