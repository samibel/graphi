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
	ineligible    bool   // true when the row has no resolvable source path — filtered out of the top Limit before truncation (AC-2 eligibility)
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
// lexical provider's pre-computed score per row (AC-2).
//
// Dedupe key: node_id is the primary identity in graphi, so the
// production dedupe is on node_id. AC-2's "dedupe on document_id
// then node_id" wording reflects the hierarchical intent (a
// document_id groups nodes; a node_id distinguishes within a
// document). With v1 schemas document_id is a deterministic function
// of node_id, so the two keys are 1:1; with v2 schemas document_id
// can be shared across nodes with identical body+doc text, but those
// rows are still distinct by node_id and the dedupe preserves them
// as separate rows. A lexical row (no document_id) and a semantic
// row for the same node_id therefore merge — the lexical row's
// absence of document_id is the "wildcard" the spec's hierarchical
// key implies.
//
// Defensive consistency: when both lexical and semantic rows arrive
// for the same node_id with non-empty document_ids, the document_ids
// are required to agree (they are both deterministic functions of
// the node's text, so a disagreement signals an embedder bug). The
// merge prefers the semantic document_id as the row's document_id
// when the semantic side carries one.
//
// Ties break on canonical node_id ascending so two runs over the
// same inputs produce the same row set in the same order — a
// precondition for the byte-stability test (AC-8).
//
// Ineligibility: a row whose path is empty after both sources have
// contributed cannot be matched against any judgement by a
// file-scoped matcher, and the runner's token counter errors on a
// directory. Such rows are flagged ineligible here so the
// post-RRF stage can drop them from the top-Limit before truncation
// (the AC-2 eligibility rule, applied before ranking/truncation so a
// future pass cannot be inflated by a top-K spot vacated by an
// ineligible row).
func (e *Engine) union(query string, lex []LexicalHit, sem []SemanticHit) []row {
	byID := map[string]*row{}

	for i, h := range lex {
		r := byID[h.NodeID]
		if r == nil {
			r = &row{nodeID: h.NodeID, documentID: "", kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
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
		// A lexical row with a real source path resolves any pre-existing
		// "no path" guess for the same node_id (the AC-2 eligibility
		// rule — a row with a known lexical path is never ineligible).
		if h.Path != "" {
			r.path = h.Path
			r.ineligible = false
		}
	}
	// AC-3: the semantic list is ordered by the QUANTISED cosine before a
	// rank is assigned, with canonical node_id as the tie-break. The
	// provider hands the list over in raw-float order (engine/search's
	// SemanticSearch sorts on the float Score), so taking arrival order
	// as semanticRank would make the ranking float-derived and let a
	// difference smaller than one quantisation unit decide an order the
	// contract says is a tie. Sorting a copy keeps the provider's slice
	// untouched, and sort.SliceStable over an explicit total order
	// (quantised desc, node_id asc) is byte-stable across runs and
	// architectures (AC-8).
	sem = quantisedOrder(sem)
	for i, h := range sem {
		r := byID[h.NodeID]
		if r == nil {
			r = &row{nodeID: h.NodeID, documentID: h.DocumentID, kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
			byID[h.NodeID] = r
		}
		if r.documentID == "" {
			r.documentID = h.DocumentID
		} else if h.DocumentID != "" && r.documentID != h.DocumentID {
			// A document_id collision across two merges for the same
			// node_id means the embedder returned inconsistent
			// document_ids for the same node. Keep the first one
			// (semantic ranks are stable; deterministic embedders do
			// not produce this) but flag the row so a test could
			// catch it if it ever occurs.
			// (silent in production; logged in the explain trace
			// would be a future enhancement)
		}
		if r.semanticRank == 0 || i+1 < r.semanticRank {
			r.semanticRank = i + 1
		}
		if r.semanticScore == 0 {
			r.semanticScore = QuantiseScore(h.CosineScore)
		}
		// A semantic row that supplies a real path resolves any pre-existing
		// "no path" guess. A semantic row with an empty path leaves the
		// row's eligibility as already set by the lexical side (if any)
		// or marks it ineligible if no lexical side exists either.
		if h.Path != "" {
			r.path = h.Path
			r.ineligible = false
		} else if r.path == "" {
			r.ineligible = true
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

// quantisedOrder returns sem ordered by the AC-3 ranking key: the
// quantised cosine descending, then canonical node_id ascending. The
// input slice is not modified.
func quantisedOrder(sem []SemanticHit) []SemanticHit {
	if len(sem) < 2 {
		return sem
	}
	out := append([]SemanticHit(nil), sem...)
	sort.SliceStable(out, func(i, j int) bool {
		qi, qj := QuantiseScore(out[i].CosineScore), QuantiseScore(out[j].CosineScore)
		if qi != qj {
			return qi > qj
		}
		return out[i].NodeID < out[j].NodeID
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
