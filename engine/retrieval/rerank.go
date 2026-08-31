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
// reusing that score is what gives the AC-7 byte-parity invariant.
// A non-delegating provider leaves lexicalScore at 0; the rerank then
// computes the score from the hybridsearch signals (SegmentExact,
// SegmentPrefix, NameSubstring, PathSegment, FullCoverage) plus the
// bounded degree signal plus the optional definition / classification
// bonuses — with default weights that match the audited hybridsearch
// numbers (SegmentExact=100, SegmentPrefix=40, NameSubstring=15,
// PathSegment=30, FullCoverage=50, DegreePoint=2) so a non-delegating
// pipeline still produces byte-equivalent scores when no extra
// bonuses are tuned.
//
// sort.SliceStable with the explicit tie-break (finalScore desc,
// node_id asc) guarantees a byte-identical Result across two runs
// (AC-8).
func (e *Engine) rerank(ctx context.Context, query string, rows []row) []row {
	tokens := hybridsearch.Tokenize(query)
	for i := range rows {
		if rows[i].lexicalScore != 0 {
			// Delegating path: the score is already final.
			rows[i].graphScore = rows[i].lexicalScore
		} else {
			rows[i].graphScore, rows[i].isDefinition, rows[i].pathClass = e.scoreOne(ctx, tokens, rows[i])
		}
		if rows[i].pathClass != "" {
			rows[i].classScore = classifyPenalty(rows[i].pathClass)
		}
		if rows[i].isDefinition {
			rows[i].graphScore += defaultRerankWeights.DefinitionBonus
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
