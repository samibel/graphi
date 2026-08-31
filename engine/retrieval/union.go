package retrieval

import (
	"sort"
	"strconv"
)

// row is the internal ranking record. The public Row type is a derived
// view (Result.Rows); row stays unexported to keep the package's public
// surface minimal (AC-1).
type row struct {
	nodeID        string
	documentID    string
	kind          string
	qualifiedName string
	path          string
	span          string // "start-end" or empty when unknown
	lexicalRank   int    // 1-based, 0 when not in lexical candidates
	semanticRank  int    // 1-based, 0 when not in semantic candidates
	lexicalScore  int    // provider's rerank score for this row (e.g. hybridsearch's audit score), 0 when unranked
	semanticScore int    // quantised cosine, 0 when semantic-only candidate or absent
	rrfScore      int    // RRFScale/(RRFk+rank) summed over the sources that contributed; 0 when only one source contributed (lexical-only byte-parity path)
	graphScore    int    // bounded rerank contribution; for the byte-parity lexical-only path this equals LexicalHit.Score
	classScore    int    // classification penalty (negative or zero)
	finalScore    int    // rrfScore + graphScore + classScore
	pathClass     string // "vendor", "generated", "" otherwise (the integer penalty comes from pathClass)
	isDefinition  bool   // "function" / "method" / "type" kind — bonus path (default weight 0)
}

// toRow projects an internal row to the public Row shape (AC-1). The Span
// is engine-owned: "start-end" when both are known, "" otherwise.
func (r row) toRow() Row {
	return Row{
		NodeID:     r.nodeID,
		DocumentID: r.documentID,
		Path:       r.path,
		Span:       r.span,
		Explain: Explain{
			LexicalRank:    r.lexicalRank,
			SemanticRank:   r.semanticRank,
			RRF:            r.rrfScore,
			Graph:          r.graphScore,
			Classification: r.classScore,
			Final:          r.finalScore,
		},
	}
}

// union combines the lexical and semantic candidate lists into one
// deduped row set, preserving lexical rank, semantic rank, and the
// lexical provider's pre-computed score per row (AC-2). Dedupe is on
// node_id; the document_id, when present, is carried on the row. Ties
// break on canonical node_id ascending so two runs over the same
// inputs produce the same row set in the same order — a precondition
// for the byte-stability test (AC-8).
func (e *Engine) union(query string, lex []LexicalHit, sem []SemanticHit) []row {
	byID := map[string]*row{}

	for i, h := range lex {
		r := byID[h.NodeID]
		if r == nil {
			r = &row{nodeID: h.NodeID, kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
			byID[h.NodeID] = r
		}
		if r.lexicalRank == 0 || i+1 < r.lexicalRank {
			r.lexicalRank = i + 1
		}
		// The lexical provider's pre-computed score (the audit score from
		// search_hybrid via HybridSearchBridge) is carried as the row's
		// lexicalScore. The rerank stage treats this as authoritative when
		// set (non-zero) so the no-embedder byte-parity path can mirror
		// search_hybrid's Reason template verbatim.
		if r.lexicalScore == 0 && h.Score != 0 {
			r.lexicalScore = h.Score
		}
	}
	for i, h := range sem {
		r := byID[h.NodeID]
		if r == nil {
			r = &row{nodeID: h.NodeID, documentID: h.DocumentID, kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
			byID[h.NodeID] = r
		}
		if r.documentID == "" {
			r.documentID = h.DocumentID
		}
		if r.semanticRank == 0 || i+1 < r.semanticRank {
			r.semanticRank = i + 1
		}
		if r.semanticScore == 0 {
			r.semanticScore = QuantiseScore(h.CosineScore)
		}
	}
	out := make([]row, 0, len(byID))
	for _, r := range byID {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].lexicalRank != out[j].lexicalRank {
			if out[i].lexicalRank == 0 {
				return false
			}
			if out[j].lexicalRank == 0 {
				return true
			}
			return out[i].lexicalRank < out[j].lexicalRank
		}
		if out[i].semanticRank != out[j].semanticRank {
			if out[i].semanticRank == 0 {
				return false
			}
			if out[j].semanticRank == 0 {
				return true
			}
			return out[i].semanticRank < out[j].semanticRank
		}
		return out[i].nodeID < out[j].nodeID
	})
	return out
}

func spanFromLine(line int) string {
	if line <= 0 {
		return ""
	}
	return formatSpan(line, line)
}

// formatSpan renders a "start-end" span from two 1-based line numbers.
func formatSpan(start, end int) string {
	if start <= 0 || end <= 0 {
		return ""
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}
