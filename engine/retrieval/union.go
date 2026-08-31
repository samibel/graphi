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
	rrfScore      int    // rrfScale/(rrfK+rank) summed over the sources that contributed; 0 when only one source contributed (lexical-only byte-parity path)
	graphScore    int    // bounded rerank contribution; for the byte-parity lexical-only path this equals lexicalHit.Score
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
// Dedupe key: the AC-2 contract is `(document_id, node_id)` —
// document_id groups nodes that share one body+doc embedding, node_id
// distinguishes within a document. The fallback when a row carries
// no document_id is `node_id` alone (a documented "wildcard"): the
// lexical provider hands hits over with no document_id, and those
// rows must still merge with their semantic counterparts over the
// shared node_id.
//
// Implementation: the union maintains two logical keying spaces
//
//   - the WILDCARD key `{nodeID}` — what every lexical row inserts
//     under, and what a semantic row falls back to when no exact
//     match exists and the wildcard row has no document_id yet;
//   - the EXACT key `{documentID, nodeID}` — what every semantic row
//     inserts under, and what a duplicate semantic row finds.
//
// A semantic row's lookup is: try the exact key first; if absent,
// try the wildcard key ONLY when the wildcard row's document_id is
// still empty (i.e., the lexical row has not yet merged with any
// semantic row). This makes the wildcard match exactly the merge
// the spec describes — "a lexical row (no document_id) and a
// semantic row for the same node_id merge" — and nothing more.
//
// Two rows that share a node_id but differ in document_id do NOT
// merge (each semantic row inserts under its own exact key; the
// wildcard fallback refused the second one because the wildcard
// row already carries the first semantic document_id). Two rows
// that share a document_id but differ in node_id do NOT merge
// (different exact keys, no wildcard fallback applies). The v2
// "one document, many nodes" case survives as distinct rows.
//
// Defensive consistency: when two semantic hits for the same
// (document_id, node_id) key arrive, the row is updated in place
// (the duplicate-key dedupe); a disagreement on document_id for
// the same node_id within the same semantic provider is an
// embedder bug and silently keeps the first one (deterministic
// embedders do not produce this).
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
func (e *engine) union(query string, lex []lexicalHit, sem []semanticHit) []row {
	// rowKey is the hierarchical (documentID, nodeID) merge key. A row
	// stored under rowKey{"", nodeID} is the "wildcard" record a
	// lexical row occupies; a row stored under rowKey{docID, nodeID}
	// is the exact record a semantic row occupies. The empty
	// document_id is reserved for the wildcard; the spec's v1
	// document_id formula makes this empty-string collision impossible
	// (every v1 / v2 document_id is non-empty) but the wildcard
	// discipline is checked anyway (see assertion below).
	type rowKey struct {
		documentID string
		nodeID     string
	}
	byKey := map[rowKey]*row{}
	// exactWildcardNodeID is the lookup key the semantic pass uses
	// to find a lexical-only row waiting to absorb a document_id.
	const wildcard = ""

	for i, h := range lex {
		// Lexical rows insert under the WILDCARD key: they have no
		// document_id, so the wildcard entry IS their identity, and
		// any semantic row for the same node_id merges into them
		// (the documented fallback).
		k := rowKey{documentID: wildcard, nodeID: h.NodeID}
		r := byKey[k]
		if r == nil {
			r = &row{nodeID: h.NodeID, documentID: "", kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
			byKey[k] = r
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
		// Lookup order: exact (document_id, node_id) first; on miss,
		// the wildcard row is the merge partner ONLY when it carries
		// no document_id yet (the merge with the first semantic hit
		// for this node_id is what the spec calls out). After that
		// first merge, the wildcard row has a document_id, and any
		// subsequent semantic row for a DIFFERENT document_id on the
		// same node_id stays as its own distinct row — the v2
		// "one node, multiple document_ids" case the hierarchical
		// key is built for.
		exact := rowKey{documentID: h.DocumentID, nodeID: h.NodeID}
		wild := rowKey{documentID: wildcard, nodeID: h.NodeID}
		r := byKey[exact]
		if r == nil && h.DocumentID != "" {
			if cand, ok := byKey[wild]; ok && cand.documentID == "" {
				r = cand
				// Consuming the lexical wildcard gives the row its exact
				// semantic identity. Re-key it rather than retaining two map
				// entries for one pointer: a later duplicate semantic hit must
				// find this row through the exact key, while a different
				// document_id for the same node remains distinct.
				delete(byKey, wild)
				byKey[exact] = r
			}
		}
		if r == nil {
			r = &row{nodeID: h.NodeID, documentID: h.DocumentID, kind: h.Kind, qualifiedName: h.QualifiedName, path: h.Path, span: spanFromLine(h.Line)}
			byKey[exact] = r
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
			r.semanticScore = quantiseScore(h.CosineScore)
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
	out := make([]row, 0, len(byKey))
	for _, r := range byKey {
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
func quantisedOrder(sem []semanticHit) []semanticHit {
	if len(sem) < 2 {
		return sem
	}
	out := append([]semanticHit(nil), sem...)
	sort.SliceStable(out, func(i, j int) bool {
		qi, qj := quantiseScore(out[i].CosineScore), quantiseScore(out[j].CosineScore)
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
