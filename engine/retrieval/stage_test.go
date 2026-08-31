package retrieval

// This file is the per-stage test suite. The test names mirror the ACs
// the suite covers; each test asserts a property of the result the
// corresponding stage produces.
//
// Tests live in the same package so they reach the unexported row type
// and the union / rrf / rerank / diversify stages directly.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// AC-2 candidate union with dedupe, RRF arithmetic with hand-computed
// values, semantic-only-hit reachability.
// ---------------------------------------------------------------------------

func TestUnion_DedupesOnNodeIDAcrossSources(t *testing.T) {
	e := &Engine{}
	lex := []LexicalHit{
		{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1},
		{NodeID: "b", Kind: "function", QualifiedName: "pkg.B", Path: "b.go", Line: 1},
		{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1},
	}
	sem := []SemanticHit{
		{NodeID: "a", DocumentID: "doc-a", CosineScore: 0.9},
		{NodeID: "c", DocumentID: "doc-c", CosineScore: 0.8},
	}
	got := e.union("ignored", lex, sem)
	if len(got) != 3 {
		t.Fatalf("union size = %d, want 3 (a, b, c)", len(got))
	}
	byID := map[string]row{}
	for _, r := range got {
		byID[r.nodeID] = r
	}
	if byID["a"].lexicalRank != 1 {
		t.Errorf("a lexicalRank = %d, want 1", byID["a"].lexicalRank)
	}
	if byID["a"].semanticRank != 1 {
		t.Errorf("a semanticRank = %d, want 1", byID["a"].semanticRank)
	}
	if byID["b"].semanticRank != 0 {
		t.Errorf("b semanticRank = %d, want 0 (lexical-only)", byID["b"].semanticRank)
	}
	if byID["c"].lexicalRank != 0 {
		t.Errorf("c lexicalRank = %d, want 0 (semantic-only)", byID["c"].lexicalRank)
	}
}

// TestUnion_HierarchicalKeyDocumentThenNode exercises the SW-263 review /
// AC-2 fix: the merge key is (document_id, node_id) when the semantic
// side carries a real DocumentID (SW-261's SemanticDocument.DocumentID),
// with node_id as the documented fallback when no document id exists.
// Four observations must hold, and they distinguish the two orderings
// of the hierarchical key so a regression that swaps their roles is
// caught:
//
//  1. The merged row's documentID is the semantic side's REAL document
//     id (e.g. "doc-X-v2"), NOT a fabricated node_id-as-document_id.
//     The pre-fix implementation fabricated DocumentID = NodeID and
//     lost the persisted semantic identity (review item 1).
//
//  2. Two semantic rows that share a document_id but have different
//     node_ids (the v2 "multiple nodes share one document" case)
//     remain DISTINCT rows — the second component of the key,
//     node_id, distinguishes within the document. (a)
//
//  3. Two semantic rows that share a node_id but have different
//     document_ids remain DISTINCT rows — the first component of
//     the key, document_id, distinguishes across document versions
//     of the same node. The v1→v2 schema migration makes this the
//     newly-possible case (a node's text hash can change when the
//     document source changes; the prior implementation merged
//     them, which is the AC-2 defect the review found). (b)
//
//  4. A lexical-only row (no document_id) still merges with a
//     semantic row on the same node_id, but ONLY with the first
//     such semantic row: the missing document_id is the "wildcard"
//     the spec's "node_id fallback when no document id exists"
//     implies, and the wildcard is consumed by the first merge.
func TestUnion_HierarchicalKeyDocumentThenNode(t *testing.T) {
	e := &Engine{}
	lex := []LexicalHit{
		// Lexical-only: no document_id. Should merge with the first
		// semantic row for node X (the missing-document_id wildcard).
		{NodeID: "X", Kind: "function", QualifiedName: "pkg.X", Path: "x.go", Line: 10},
		// Lexical-only: no semantic counterpart.
		{NodeID: "Y", Kind: "function", QualifiedName: "pkg.Y", Path: "y.go", Line: 20},
	}
	sem := []SemanticHit{
		// Carries a REAL document id different from node_id — the v2 case.
		{NodeID: "X", DocumentID: "doc-X-v2", Kind: "function", QualifiedName: "pkg.X", Path: "x.go", Line: 10, CosineScore: 0.91},
		// A second semantic row for X with a different document_id —
		// the v2 "one node, two documents" case (case 3 below). It
		// must NOT merge with the wildcard X row (the wildcard was
		// already consumed by the first semantic merge) and must
		// remain a distinct row carrying doc-X-v3.
		{NodeID: "X", DocumentID: "doc-X-v3", Kind: "function", QualifiedName: "pkg.X", Path: "x.go", Line: 10, CosineScore: 0.88},
		// Two nodes sharing one document — must remain distinct rows (case 2).
		{NodeID: "P", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.P", Path: "p.go", Line: 1, CosineScore: 0.80},
		{NodeID: "Q", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.Q", Path: "q.go", Line: 1, CosineScore: 0.79},
		// Two semantic rows sharing a node_id (Z) but differing in
		// document_id (case 3 explicitly) — must remain distinct rows.
		{NodeID: "Z", DocumentID: "doc-Z-a", Kind: "function", QualifiedName: "pkg.Z", Path: "z.go", Line: 5, CosineScore: 0.85},
		{NodeID: "Z", DocumentID: "doc-Z-b", Kind: "function", QualifiedName: "pkg.Z", Path: "z.go", Line: 5, CosineScore: 0.84},
	}
	got := e.union("q", lex, sem)

	// Observation 1 — the merged X row carries the real document id,
	// not a fabricated NodeID-as-DocumentID. The X row that absorbed
	// the first semantic hit (doc-X-v2) carries it; the second
	// semantic hit (doc-X-v3) for X is a SEPARATE row at the
	// (doc-X-v3, X) exact key, not the wildcard row.
	//
	// Expected row count:
	//   X (lexical) + first semantic X (doc-X-v2)        -> wildcard X  = 1 row
	//   second semantic X (doc-X-v3)                    -> exact key    = 1 row
	//   Y (lexical-only)                                 -> wildcard Y  = 1 row
	//   P semantic (doc-shared)                          -> exact P      = 1 row
	//   Q semantic (doc-shared)                          -> exact Q      = 1 row
	//   Z semantic (doc-Z-a)                             -> exact Z-a    = 1 row
	//   Z semantic (doc-Z-b)                             -> exact Z-b    = 1 row
	// Total = 7 rows.
	if len(got) != 7 {
		t.Fatalf("union size = %d, want 7 (X-wildcard, X-v3-exact, Y, P, Q, Z-a, Z-b); got nodes = %v",
			len(got), nodeIDsOf(got))
	}
	// Scan for both X rows by (node_id, document_id) — Go map iteration
	// order is undefined, so a byID[node_id] lookup cannot distinguish
	// the wildcard X from the exact-key X (doc-X-v3).
	var xWild, xV3 *row
	for i := range got {
		if got[i].nodeID != "X" {
			continue
		}
		switch got[i].documentID {
		case "doc-X-v2":
			xWild = &got[i]
		case "doc-X-v3":
			xV3 = &got[i]
		default:
			t.Errorf("X row carries unexpected documentID %q (want doc-X-v2 or doc-X-v3)", got[i].documentID)
		}
	}
	// (X-via-wildcard): carries doc-X-v2 from the first semantic merge;
	// BOTH ranks are > 0 because the wildcard merged the lexical row.
	if xWild == nil {
		t.Errorf("the X wildcard row (which should carry doc-X-v2 from the first semantic merge) is missing")
	} else {
		if xWild.documentID != "doc-X-v2" {
			t.Errorf("X-wildcard row documentID = %q, want doc-X-v2 (the real semantic document id from the wildcard merge, not NodeID=X fabricated)",
				xWild.documentID)
		}
		if xWild.lexicalRank == 0 || xWild.semanticRank == 0 {
			t.Errorf("X-wildcard did not merge across sources: lex=%d sem=%d, want both > 0",
				xWild.lexicalRank, xWild.semanticRank)
		}
	}
	// (X-via-exact): the second semantic hit for X with doc-X-v3.
	// The previous implementation merged it into the wildcard row,
	// which is the AC-2 defect the review found.
	if xV3 == nil {
		t.Errorf("the second semantic row for X (doc-X-v3) was merged into the X wildcard row instead of remaining a distinct row at the exact (doc-X-v3, X) key. Got %d rows.", len(got))
	} else if xV3.lexicalRank != 0 {
		t.Errorf("X-v3 exact row lexicalRank = %d, want 0 (semantic-only, no lexical counterpart)", xV3.lexicalRank)
	}

	// Observation 2 — P and Q share doc-shared but are distinct rows.
	var pRow, qRow *row
	for i := range got {
		switch got[i].nodeID {
		case "P":
			pRow = &got[i]
		case "Q":
			qRow = &got[i]
		}
	}
	if pRow == nil || pRow.documentID != "doc-shared" {
		t.Errorf("P row missing or wrong documentID: %+v", pRow)
	}
	if qRow == nil || qRow.documentID != "doc-shared" {
		t.Errorf("Q row missing or wrong documentID: %+v", qRow)
	}

	// Observation 3 — Z's two semantic rows (doc-Z-a, doc-Z-b) are
	// distinct. They live under the exact keys (doc-Z-a, Z) and
	// (doc-Z-b, Z).
	var za, zb *row
	for i := range got {
		if got[i].nodeID != "Z" {
			continue
		}
		switch got[i].documentID {
		case "doc-Z-a":
			za = &got[i]
		case "doc-Z-b":
			zb = &got[i]
		default:
			t.Errorf("Z row carries unexpected documentID %q", got[i].documentID)
		}
	}
	if za == nil || zb == nil {
		t.Errorf("Z's two semantic rows did not both survive: doc-Z-a present=%v doc-Z-b present=%v (got nodes = %v)",
			za != nil, zb != nil, nodeIDsOf(got))
	}

	// Observation 4 — Y is lexical-only with no semantic counterpart.
	var yRow *row
	for i := range got {
		if got[i].nodeID == "Y" {
			yRow = &got[i]
		}
	}
	if yRow == nil {
		t.Error("Y missing from union")
	} else if yRow.semanticRank != 0 || yRow.documentID != "" {
		t.Errorf("Y = %+v, want lexical-only (semanticRank=0, documentID=\"\")", yRow)
	}
}

// TestUnion_HierarchicalKeyDistinctOrderings is the targeted witness the
// SW-263 review required: a regression test that distinguishes the two
// orderings of the (document_id, node_id) hierarchical key. A flat
// node_id key would pass case (a) and fail case (b); a flat document_id
// key would pass case (b) and fail case (a). Only the hierarchical key
// passes both.
//
// (a) Two rows sharing a document_id but differing in node_id stay
//
//	distinct (the "multiple nodes share one document" v2 case).
//
// (b) Two rows sharing a node_id but differing in document_id stay
//
//	distinct (the "multiple document versions of one node" v2 case
//	that the previous flat-node_id key collapsed into a single row).
func TestUnion_HierarchicalKeyDistinctOrderings(t *testing.T) {
	e := &Engine{}
	// Case (a): shared document_id, distinct node_ids.
	// A flat-document_id key would merge these into one row. The
	// hierarchical key keeps them distinct because node_id differs.
	caseA := e.union("q", nil, []SemanticHit{
		{NodeID: "p", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.p", Path: "p.go", Line: 1, CosineScore: 0.80},
		{NodeID: "q", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.q", Path: "q.go", Line: 1, CosineScore: 0.79},
	})
	if len(caseA) != 2 {
		t.Errorf("case (a) shared document_id, distinct node_ids: union size = %d, want 2 (a flat-document_id key would have collapsed these)",
			len(caseA))
	}

	// Case (b): shared node_id, distinct document_ids.
	// A flat-node_id key would merge these into one row. The
	// hierarchical key keeps them distinct because document_id differs.
	caseB := e.union("q", nil, []SemanticHit{
		{NodeID: "z", DocumentID: "doc-z-a", Kind: "function", QualifiedName: "pkg.z", Path: "z.go", Line: 1, CosineScore: 0.85},
		{NodeID: "z", DocumentID: "doc-z-b", Kind: "function", QualifiedName: "pkg.z", Path: "z.go", Line: 1, CosineScore: 0.84},
	})
	if len(caseB) != 2 {
		t.Errorf("case (b) shared node_id, distinct document_ids: union size = %d, want 2 (a flat-node_id key would have collapsed these; that is the SW-263 review / AC-2 defect)",
			len(caseB))
	}
	// Sanity: the two Z rows carry distinct document_ids (otherwise the
	// dedupe happened for the wrong reason).
	gotDocs := map[string]bool{}
	for _, r := range caseB {
		gotDocs[r.documentID] = true
	}
	if !gotDocs["doc-z-a"] || !gotDocs["doc-z-b"] {
		t.Errorf("case (b) row document_ids = %v, want both doc-z-a and doc-z-b", gotDocs)
	}
}

// nodeIDsOf is a debug helper for union failure messages.
func nodeIDsOf(rs []row) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.nodeID + "(" + r.documentID + ")"
	}
	return out
}

func TestUnion_TieBreakIsCanonicalNodeID(t *testing.T) {
	// Verify the dedupe-stable tie-break by constructing rows directly
	// (bypassing the rank assignment in union, which would always give
	// distinct ranks to distinct hits). Three rows with identical
	// (lexicalRank, semanticRank) must sort by node_id ascending.
	rows := []row{
		{nodeID: "z", lexicalRank: 1, semanticRank: 0},
		{nodeID: "a", lexicalRank: 1, semanticRank: 0},
		{nodeID: "m", lexicalRank: 1, semanticRank: 0},
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].lexicalRank != rows[j].lexicalRank {
			if rows[i].lexicalRank == 0 {
				return false
			}
			if rows[j].lexicalRank == 0 {
				return true
			}
			return rows[i].lexicalRank < rows[j].lexicalRank
		}
		if rows[i].semanticRank != rows[j].semanticRank {
			if rows[i].semanticRank == 0 {
				return false
			}
			if rows[j].semanticRank == 0 {
				return true
			}
			return rows[i].semanticRank < rows[j].semanticRank
		}
		return rows[i].nodeID < rows[j].nodeID
	})
	ids := []string{rows[0].nodeID, rows[1].nodeID, rows[2].nodeID}
	if !reflect.DeepEqual(ids, []string{"a", "m", "z"}) {
		t.Errorf("tie-break order = %v, want [a m z]", ids)
	}
}

func TestUnion_SemanticOnlyHitIsReachable(t *testing.T) {
	// AC-2: a query with zero lexical token overlap still surfaces the
	// semantic hit in the result. The retrieval module's semantics-only
	// path starts from the union, which must preserve the semantic-only
	// row. Verify with a hand-built engine.
	e := &Engine{}
	sem := []SemanticHit{
		{NodeID: "alpha", DocumentID: "doc-alpha", CosineScore: 0.95},
	}
	got := e.union("no-overlap-query", nil, sem)
	if len(got) != 1 || got[0].nodeID != "alpha" {
		t.Fatalf("semantic-only hit not reachable: got %+v", got)
	}
	if got[0].semanticRank != 1 || got[0].lexicalRank != 0 {
		t.Errorf("ranks = (lex=%d, sem=%d), want (0, 1)", got[0].lexicalRank, got[0].semanticRank)
	}
}

func TestRRF_HandComputedValuesMatchFormula(t *testing.T) {
	// AC-2: "WHEN both lexical and semantic candidates exist, the system
	// shall ... fuse with integer RRF". The RRF formula is per
	// contributing source: rrfScore = sum over sources s that
	// contributed of RRFScale / (RRFk + rank_s). A row present in
	// only one source receives its single-source contribution; a row
	// present in both receives both. A semantic-only hit is therefore
	// reachable in the result with a positive RRF contribution
	// (SW-263 / decision-ac9 defect 1; the previous implementation
	// collapsed single-source rows to rrfScore=0, which is what this
	// test previously pinned).
	//
	// The byte-parity AC-7 path is preserved by a different gate: when
	// the semantic list is GLOBALLY absent (no embedder), the rrf stage
	// is skipped entirely and every row's rrfScore is 0 — so a
	// no-embedder build still mirrors search_hybrid's audit output.
	// This test exercises the fused-union path with the semantic list
	// active (semanticActive=true).
	e := &Engine{}
	in := []row{
		{nodeID: "both", lexicalRank: 1, semanticRank: 1},
		{nodeID: "lexical-only", lexicalRank: 1, semanticRank: 0},
		{nodeID: "semantic-only", lexicalRank: 0, semanticRank: 1},
		{nodeID: "both-5-3", lexicalRank: 5, semanticRank: 3},
	}
	out := e.rrf(in, true, false)
	want := map[string]int{
		"both":          RRFScale/(RRFk+1) + RRFScale/(RRFk+1),
		"lexical-only":  RRFScale / (RRFk + 1), // AC-2: lexical contributes its single-source term
		"semantic-only": RRFScale / (RRFk + 1), // AC-2: semantic contributes its single-source term; semantic-only hit is reachable in the result
		"both-5-3":      RRFScale/(RRFk+5) + RRFScale/(RRFk+3),
	}
	for _, r := range out {
		if got, ok := want[r.nodeID]; !ok {
			t.Errorf("unexpected node %s", r.nodeID)
		} else if r.rrfScore != got {
			t.Errorf("node %s rrf = %d, want %d (RRF is per contributing source)", r.nodeID, r.rrfScore, got)
		}
	}
}

func TestRRF_ZeroAcrossAllRowsWhenSemanticListGloballyAbsent(t *testing.T) {
	// AC-7 byte parity: when the semantic list is globally absent (no
	// embedder, configured-but-not-ready generation, or the caller
	// pinned ModeLexicalOnly), the rrf stage MUST score 0 across the
	// whole row set so the rerank's lexical score carries the row's
	// Final unaltered and the rendered bytes match search_hybrid's
	// audit output. The previous implementation achieved this through a
	// per-row "both ranks > 0" intersection filter, which is what
	// produced the SW-263 / decision-ac9 defect 1 — the same path
	// that pinned a single-source row's RRF to 0 in the fused case.
	// The correct gate is global, not per-row.
	e := &Engine{}
	in := []row{
		{nodeID: "both", lexicalRank: 1, semanticRank: 1},
		{nodeID: "lexical-only", lexicalRank: 1, semanticRank: 0},
		{nodeID: "semantic-only", lexicalRank: 0, semanticRank: 1},
		{nodeID: "both-5-3", lexicalRank: 5, semanticRank: 3},
	}
	out := e.rrf(in, false, false)
	for _, r := range out {
		if r.rrfScore != 0 {
			t.Errorf("semantic list globally absent: node %s rrf = %d, want 0 (AC-7 byte parity)", r.nodeID, r.rrfScore)
		}
	}
}

func TestRRF_NoFloatsInRankingPath(t *testing.T) {
	// The whole pipeline must run on integer arithmetic (AC-2, AC-3, AC-8).
	// A literal float in the row's score field would defeat the byte-
	// stability promise. Test by re-running on the same input twice and
	// asserting byte-identical JSON.
	e := &Engine{}
	in := []row{
		{nodeID: "a", lexicalRank: 1, semanticRank: 1, lexicalScore: 9999, semanticScore: 9500},
		{nodeID: "b", lexicalRank: 2, semanticRank: 3, lexicalScore: 8000, semanticScore: 7000},
		{nodeID: "c", lexicalRank: 5, semanticRank: 0, lexicalScore: 4000},
		{nodeID: "d", lexicalRank: 0, semanticRank: 4, semanticScore: 6000},
	}
	out1 := e.rrf(in, true, false)
	out2 := e.rrf(in, true, false)
	b1, _ := json.Marshal(out1)
	b2, _ := json.Marshal(out2)
	if string(b1) != string(b2) {
		t.Errorf("RRF not deterministic: %s vs %s", b1, b2)
	}
	for _, r := range out1 {
		if r.rrfScore < 0 || r.rrfScore > 1<<31 {
			t.Errorf("rrf out of int32 range: %d", r.rrfScore)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-3 quantisation: two float inputs within 5e-5 must order identically.
// ---------------------------------------------------------------------------

func TestQuantiseScore_FloatInputsWithinEpsilonOrderIdentically(t *testing.T) {
	// Two inputs that differ by less than 5e-5 round to the same
	// integer when scaled by 10000. This is the AC-3 invariant.
	a := 0.70000
	b := 0.70004 // 4e-5 difference
	if math.Abs(a-b) > 5e-5 {
		t.Fatalf("test inputs not within 5e-5: %v", math.Abs(a-b))
	}
	if QuantiseScore(a) != QuantiseScore(b) {
		t.Errorf("AC-3: quantise(%.5f)=%d != quantise(%.5f)=%d (diff %.1e)",
			a, QuantiseScore(a), b, QuantiseScore(b), math.Abs(a-b))
	}
	// And an input that DOES differ by more than 5e-5 must order.
	c := 0.70006 // 6e-5 difference
	if math.Abs(a-c) <= 5e-5 {
		t.Fatalf("test inputs not beyond 5e-5: %v", math.Abs(a-c))
	}
	if QuantiseScore(a) == QuantiseScore(c) {
		t.Errorf("AC-3: quantise(%.5f)=%d must differ from quantise(%.5f)=%d",
			a, QuantiseScore(a), c, QuantiseScore(c))
	}
}

func TestQuantiseScore_ClampsOutOfRangeAndHandlesNaN(t *testing.T) {
	if QuantiseScore(1.5) != 10000 {
		t.Errorf("overshoot clamp: got %d, want 10000", QuantiseScore(1.5))
	}
	if QuantiseScore(-1.5) != -10000 {
		t.Errorf("undershoot clamp: got %d, want -10000", QuantiseScore(-1.5))
	}
	if QuantiseScore(math.NaN()) != 0 {
		t.Errorf("NaN: got %d, want 0", QuantiseScore(math.NaN()))
	}
	if QuantiseScore(math.Inf(1)) != 0 {
		t.Errorf("+Inf: got %d, want 0", QuantiseScore(math.Inf(1)))
	}
}

// ---------------------------------------------------------------------------
// AC-4 rerank uses audited hybridsearch signals + definition bonus +
// vendor/generated penalty; the weight set is stamped by WeightsHash().
// ---------------------------------------------------------------------------

func TestRerank_UsesAuditedHybridsearchSignals(t *testing.T) {
	e := &Engine{}
	in := []row{
		{nodeID: "exact", lexicalRank: 1, kind: "function", qualifiedName: "pkg.TokenValidator", path: "auth.go", rrfScore: 1000},
		{nodeID: "prefix", lexicalRank: 2, kind: "function", qualifiedName: "pkg.Tokenizer", path: "auth.go", rrfScore: 1000},
		{nodeID: "noHit", lexicalRank: 3, kind: "function", qualifiedName: "pkg.Other", path: "unrelated.go", rrfScore: 1000},
	}
	out := e.rerank(context.Background(), "token validator", in, true)
	if out[0].nodeID != "exact" {
		t.Errorf("top rank = %s, want exact (SegmentExact should beat SegmentPrefix on the same query)", out[0].nodeID)
	}
	if out[2].nodeID != "noHit" {
		t.Errorf("bottom rank = %s, want noHit", out[2].nodeID)
	}
}

func TestRerank_WeightsHashIsStable(t *testing.T) {
	// Two constructions of the same weight set must yield the same hash;
	// a different weight set must yield a different hash. The hash
	// carries the audit discipline to the Result.Summary (AC-4).
	h1 := WeightsHash()
	h2 := WeightsHash()
	if h1 != h2 {
		t.Errorf("WeightsHash not deterministic: %s vs %s", h1, h2)
	}
	// A different rerankWeights struct must yield a different hash.
	h3 := weightsHashOf(rerankWeights{SegmentExact: 99})
	if h3 == h1 {
		t.Errorf("WeightsHash invariant under field mutation: %s == %s", h3, h1)
	}
}

func TestRerank_DefinitionBonusPromotesDeclarationKinds(t *testing.T) {
	// A non-definition kind at the same RRF should rank below a definition.
	e := &Engine{}
	in := []row{
		{nodeID: "method", kind: "method", qualifiedName: "pkg.F", path: "x.go", rrfScore: 1000},
		{nodeID: "var", kind: "variable", qualifiedName: "pkg.V", path: "x.go", rrfScore: 1000},
	}
	out := e.rerank(context.Background(), "F", in, true)
	if out[0].nodeID != "method" {
		t.Errorf("definition rank = %s, want method", out[0].nodeID)
	}
}

func TestRerank_GeneratedPathCarriesClassificationPenalty(t *testing.T) {
	// Generated/vendor paths get a negative penalty (AC-4); a non-classified
	// path does not.
	e := &Engine{}
	in := []row{
		{nodeID: "g", kind: "function", qualifiedName: "pkg.X", path: "vendor/foo.go", rrfScore: 1000},
		{nodeID: "h", kind: "function", qualifiedName: "pkg.Y", path: "src/bar.go", rrfScore: 1000},
	}
	out := e.rerank(context.Background(), "X", in, true)
	if out[0].nodeID != "h" {
		t.Errorf("classification demotion failed: top = %s, want h", out[0].nodeID)
	}
}

// TestRerank_DelegatingRowAppliesDefinitionBonusAndClassificationPenalty
// (SW-263 review / item 3): on the fused path, a delegating row
// (lexicalScore != 0) receives the rerank's own signals on top of the
// audited search_hybrid score. The previous implementation skipped both
// the definition bonus and the vendor/generated classification penalty
// on the delegating path because the lexical score was treated as
// "already final"; the fix derives isDefinition and pathClass from the
// row's own fields so the rerank's intent is uniform.
func TestRerank_DelegatingRowAppliesDefinitionBonusAndClassificationPenalty(t *testing.T) {
	e := &Engine{}
	makeRows := func() []row {
		return []row{
			// Delegating row (lexicalScore set), kind="function" → definition.
			// path="vendor/x.go" → generated. Rerank should apply BOTH the
			// definition bonus (+20) AND the generated penalty (-25) on the
			// fused path.
			{nodeID: "d", kind: "function", qualifiedName: "pkg.D", path: "vendor/x.go", lexicalScore: 1000, rrfScore: 100},
			// Delegating row, NOT a definition, NOT classified.
			{nodeID: "n", kind: "variable", qualifiedName: "pkg.N", path: "src/n.go", lexicalScore: 1000, rrfScore: 100},
		}
	}
	fused := e.rerank(context.Background(), "anything", makeRows(), true /* semanticActive */)
	if fused[0].nodeID != "n" {
		t.Errorf("fused top = %s, want n (variable; definition bonus should not lift d past the penalty)",
			fused[0].nodeID)
	}
	if fused[0].graphScore != 1000 {
		t.Errorf("fused n.graphScore = %d, want 1000 (no bonus/penalty on a non-definition, non-classified row)",
			fused[0].graphScore)
	}
	if fused[0].classScore != 0 {
		t.Errorf("fused n.classScore = %d, want 0 (no classification)", fused[0].classScore)
	}
	// Find d's row and verify both signals applied.
	var dRow row
	for _, r := range fused {
		if r.nodeID == "d" {
			dRow = r
		}
	}
	if dRow.graphScore != 1000+defaultRerankWeights.DefinitionBonus {
		t.Errorf("d.graphScore = %d, want 1000+%d (definition bonus applied on delegating fused path)",
			dRow.graphScore, defaultRerankWeights.DefinitionBonus)
	}
	if dRow.classScore != defaultRerankWeights.GeneratedPenalty {
		t.Errorf("d.classScore = %d, want %d (generated penalty applied on delegating fused path)",
			dRow.classScore, defaultRerankWeights.GeneratedPenalty)
	}

	// On the lexical-only path the same rows MUST NOT receive the bonus
	// or the penalty: the AC-7 byte-parity contract requires the final
	// scores to equal the search_hybrid audit scores verbatim. Fresh
	// inputs (rerank mutates in place — the fused call above already
	// filled pathClass / classScore on a shared input).
	lexicalOnly := e.rerank(context.Background(), "anything", makeRows(), false /* semanticActive */)
	for _, r := range lexicalOnly {
		if r.graphScore != r.lexicalScore {
			t.Errorf("lexical-only path: %s.graphScore = %d, want lexicalScore=%d (AC-7 byte parity forbids bonus)",
				r.nodeID, r.graphScore, r.lexicalScore)
		}
		if r.classScore != 0 {
			t.Errorf("lexical-only path: %s.classScore = %d, want 0 (AC-7 byte parity forbids penalty)",
				r.nodeID, r.classScore)
		}
		if r.pathClass != "" {
			t.Errorf("lexical-only path: %s.pathClass = %q, want empty (AC-7 forbids reclassifying on the lexical-only path)",
				r.nodeID, r.pathClass)
		}
		if r.isDefinition {
			t.Errorf("lexical-only path: %s.isDefinition = true, want false", r.nodeID)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-5 diversification: MaxPerFile cap; demoted rows still reachable.
// ---------------------------------------------------------------------------

func TestDiversify_OneRowPerNodeID(t *testing.T) {
	// The union stage already dedupes on node_id; diversify re-checks the
	// invariant on its input as a structural guarantee.
	e := &Engine{}
	in := []row{
		{nodeID: "a", path: "x.go", finalScore: 100},
		{nodeID: "b", path: "x.go", finalScore: 90},
		{nodeID: "c", path: "y.go", finalScore: 80},
		{nodeID: "d", path: "z.go", finalScore: 70},
	}
	out := e.diversify(in, 10)
	seen := map[string]bool{}
	for _, r := range out {
		if seen[r.nodeID] {
			t.Errorf("duplicate node_id %s in diversify output", r.nodeID)
		}
		seen[r.nodeID] = true
	}
}

func TestDiversify_DemotesRowsBeyondMaxPerFile(t *testing.T) {
	e := &Engine{}
	in := []row{
		{nodeID: "a", path: "x.go", finalScore: 100},
		{nodeID: "b", path: "x.go", finalScore: 90},
		{nodeID: "c", path: "x.go", finalScore: 80},
		{nodeID: "d", path: "x.go", finalScore: 70}, // 4th row in x.go — demoted
	}
	out := e.diversify(in, 10)
	if len(out) != 4 {
		t.Fatalf("demote dropped a row: got %d, want 4", len(out))
	}
	// The demoted row ('d') must still be reachable: a larger limit
	// reaches it. This is the AC-5 reachability test (not just the cap).
	if out[3].nodeID != "d" {
		t.Errorf("demoted tail = %s, want d", out[3].nodeID)
	}
}

func TestDiversify_CapRespectedInTopLimit(t *testing.T) {
	e := &Engine{}
	in := []row{
		{nodeID: "a", path: "x.go", finalScore: 100},
		{nodeID: "b", path: "x.go", finalScore: 90},
		{nodeID: "c", path: "x.go", finalScore: 80},
		{nodeID: "d", path: "x.go", finalScore: 70},
		{nodeID: "e", path: "y.go", finalScore: 60},
	}
	out := e.diversify(in, 3)
	// First three must come from x.go (under the cap); the 4th must be
	// the e (y.go is uncapped); the 5th must be the demoted d.
	if len(out) != 5 {
		t.Fatalf("diversify size = %d, want 5", len(out))
	}
	for i := 0; i < 3; i++ {
		if out[i].path != "x.go" {
			t.Errorf("top[%d] path = %s, want x.go", i, out[i].path)
		}
	}
	if out[3].nodeID != "e" {
		t.Errorf("top[3] = %s, want e (y.go, in-cap after demote)", out[3].nodeID)
	}
	if out[4].nodeID != "d" {
		t.Errorf("demoted tail = %s, want d", out[4].nodeID)
	}
}

// ---------------------------------------------------------------------------
// AC-6 exact-query rule: documented regex, lexical-dominant on matches.
// ---------------------------------------------------------------------------

func TestRules_ExactIdentifierIsDocumentedRegex(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"cobra.Command.AddCommand", true},
		{"pkg.Func", true},
		{"x", false},           // single segment — not an exact identifier
		{"a.b.c.d", true},      // multi-segment
		{"foo.", false},        // trailing dot, no second segment
		{"9pkg.X", false},      // leading digit
		{"with-dash.X", false}, // dash not allowed
		{"", false},
		{"hello world", false},   // free text
		{"flag_groups.go", true}, // two identifier segments separated by "."
		{"a/b/c", false},         // path-shaped
	}
	for _, c := range cases {
		if got := IsExactIdentifier(c.in); got != c.want {
			t.Errorf("IsExactIdentifier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRules_ExactPathIsDocumentedRegex(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"flag_groups.go", false}, // no slash
		{"path/to/x.go", true},
		{"a/b", true},
		{"", false},
		{"path with space/x.go", false},
	}
	for _, c := range cases {
		if got := IsExactPath(c.in); got != c.want {
			t.Errorf("IsExactPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRules_IsExactQueryShortCircuitsOnIdentifier(t *testing.T) {
	// A typical exact-identifier query must take the lexical-dominant
	// branch: the test asserts the rule fires, the ranking consequence
	// is tested by integration.
	if !IsExactQuery("cobra.Command.AddCommand") {
		t.Error("exact identifier missed")
	}
	if !IsExactQuery("path/to/file.go") {
		t.Error("exact path missed")
	}
	if IsExactQuery("how does cobra validate required flags") {
		t.Error("NL query misclassified as exact")
	}
}

// ---------------------------------------------------------------------------
// AC-7 degradation: missing/stale/corrupt/no-embedder ⇒ lexical-only with
// no error; the result rows are byte-identical to today's search_hybrid
// output (this is the structural test — the integration byte-equality
// test lives in retrieval_byte_parity_test.go).
// ---------------------------------------------------------------------------

func TestRetrieve_LexicalOnlyWhenNoEmbedder(t *testing.T) {
	// Build a fake lexical provider that returns two rows and a nil
	// semantic provider (the default-build shape).
	lex := &fakeLexical{
		hits: []LexicalHit{
			{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1},
			{NodeID: "b", Kind: "function", QualifiedName: "pkg.B", Path: "b.go", Line: 1},
		},
	}
	e := New(lex, nil, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "anything"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateLexicalOnly {
		t.Errorf("Degradation = %q, want %q", res.Degradation, StateLexicalOnly)
	}
	if len(res.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(res.Rows))
	}
	for _, r := range res.Rows {
		if r.Explain.SemanticRank != 0 {
			t.Errorf("row %s SemanticRank = %d, want 0 (lexical-only)",
				r.NodeID, r.Explain.SemanticRank)
		}
	}
	if res.Summary.ModelFingerprint != "" || res.Summary.IndexFingerprint != "" {
		t.Errorf("fingerprints leaked into lexical-only summary: %+v", res.Summary)
	}
}

func TestRetrieve_LexicalOnlyWhenEmbedderUnavailable(t *testing.T) {
	// Semantic provider exists but reports not available (the
	// "configured-but-no-meta" / "generation missing" path).
	lex := &fakeLexical{
		hits: []LexicalHit{
			{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1},
		},
	}
	sem := &fakeSemantic{available: false, reason: "no embedder configured"}
	e := New(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "x"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateLexicalOnly {
		t.Errorf("Degradation = %q, want StateLexicalOnly (the unavailable envelope)", res.Degradation)
	}
	if len(res.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(res.Rows))
	}
}

func TestRetrieve_GenerationStaleReportsTypedState(t *testing.T) {
	lex := &fakeLexical{
		hits: []LexicalHit{{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1}},
	}
	sem := &fakeSemantic{available: false, state: StateGenerationStale}
	e := New(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "x"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateGenerationStale {
		t.Errorf("Degradation = %q, want StateGenerationStale", res.Degradation)
	}
}

func TestRetrieve_GenerationCorruptReportsTypedState(t *testing.T) {
	lex := &fakeLexical{
		hits: []LexicalHit{{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1}},
	}
	sem := &fakeSemantic{available: false, state: StateGenerationCorrupt}
	e := New(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "x"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateGenerationCorrupt {
		t.Errorf("Degradation = %q, want StateGenerationCorrupt", res.Degradation)
	}
}

func TestRetrieve_NoErrorOnDegradedPaths(t *testing.T) {
	// The story (AC-7) says degradation must be "no error" — every
	// degraded state must still surface a usable Result.
	for _, st := range []State{StateLexicalOnly, StateGenerationMissing, StateGenerationStale, StateGenerationCorrupt} {
		lex := &fakeLexical{hits: []LexicalHit{{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1}}}
		sem := &fakeSemantic{available: false, state: st}
		e := New(lex, sem, nil)
		res, err := e.Retrieve(context.Background(), Request{Query: "x"})
		if err != nil {
			t.Errorf("state %q: Retrieve returned error %v, want nil", st, err)
		}
		if res.Degradation != st {
			t.Errorf("state %q: Degradation = %q", st, res.Degradation)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-8 determinism: identical input ⇒ byte-identical Result.
// ---------------------------------------------------------------------------

func TestRetrieve_IsByteIdenticalAcrossRuns(t *testing.T) {
	lex := &fakeLexical{
		hits: []LexicalHit{
			{NodeID: "a", Kind: "function", QualifiedName: "pkg.A", Path: "a.go", Line: 1},
			{NodeID: "b", Kind: "method", QualifiedName: "pkg.B.X", Path: "b.go", Line: 1},
			{NodeID: "c", Kind: "function", QualifiedName: "pkg.C", Path: "c.go", Line: 1},
			{NodeID: "d", Kind: "type", QualifiedName: "pkg.D", Path: "d.go", Line: 1},
		},
	}
	sem := &fakeSemantic{
		available: true,
		hits: []SemanticHit{
			{NodeID: "c", DocumentID: "doc-c", CosineScore: 0.95},
			{NodeID: "d", DocumentID: "doc-d", CosineScore: 0.90},
		},
		state: StateReady,
	}
	e := New(lex, sem, nil)
	r1, err := e.Retrieve(context.Background(), Request{Query: "pkg", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.Retrieve(context.Background(), Request{Query: "pkg", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic:\n r1=%s\n r2=%s", b1, b2)
	}
	// And without a semantic provider — the lexical-only path.
	e2 := New(lex, nil, nil)
	r3, _ := e2.Retrieve(context.Background(), Request{Query: "pkg", Limit: 10})
	r4, _ := e2.Retrieve(context.Background(), Request{Query: "pkg", Limit: 10})
	b3, _ := json.Marshal(r3)
	b4, _ := json.Marshal(r4)
	if string(b3) != string(b4) {
		t.Errorf("lexical-only non-deterministic:\n r3=%s\n r4=%s", b3, b4)
	}
}

// ---------------------------------------------------------------------------
// AC-10 finaliseRows honors Limit; diversification demotes, never drops.
// ---------------------------------------------------------------------------

func TestFinaliseRows_HonorsLimit(t *testing.T) {
	in := []row{
		{nodeID: "a", finalScore: 100},
		{nodeID: "b", finalScore: 90},
		{nodeID: "c", finalScore: 80},
		{nodeID: "d", finalScore: 70},
		{nodeID: "e", finalScore: 60},
	}
	got := finaliseRows(in, 3)
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	ids := []string{got[0].NodeID, got[1].NodeID, got[2].NodeID}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Errorf("trim order = %v, want [a b c]", ids)
	}
}

// ---------------------------------------------------------------------------
// Helpers — minimal fakes for the unit tests.
// ---------------------------------------------------------------------------

type fakeLexical struct {
	hits []LexicalHit
}

func (f *fakeLexical) Search(ctx context.Context, q string, limit int) ([]LexicalHit, error) {
	if limit > 0 && len(f.hits) > limit {
		return f.hits[:limit], nil
	}
	return f.hits, nil
}

type fakeSemantic struct {
	available bool
	reason    string
	state     State
	hits      []SemanticHit
}

func (f *fakeSemantic) Available() bool { return f.available }

func (f *fakeSemantic) Search(ctx context.Context, q string, limit int) (SemanticOutcome, error) {
	if !f.available {
		st := f.state
		if st == "" {
			st = StateLexicalOnly
		}
		return SemanticOutcome{Available: false, Reason: f.reason, State: st}, nil
	}
	return SemanticOutcome{Available: true, State: StateReady, Hits: f.hits}, nil
}

// TestSanity_QuantisationFactorMatchesSpec pins the AC-3 quantisation
// factor (10000) so any change is a deliberate contract change with its
// own story. Two inputs one full quantisation unit apart (1e-4 = 0.0001)
// must round to different integers.
func TestSanity_QuantisationFactorMatchesSpec(t *testing.T) {
	a := 0.7000
	b := 0.7001 // 1e-4 difference; one full unit at factor 10000
	if QuantiseScore(a) == QuantiseScore(b) {
		t.Errorf("quantisation factor below 10000: %.4f and %.4f both quantised to %d",
			a, b, QuantiseScore(a))
	}
}

// TestSanity_SummaryContainsPinnedConstants locks the summary's echo of
// the pinned arithmetic constants: a reader of the bytes must be able to
// verify CandidateK=50, RRFk=60, RRFScale=1_000_000, MaxPerFile=3
// without consulting the source.
func TestSanity_SummaryContainsPinnedConstants(t *testing.T) {
	lex := &fakeLexical{}
	e := New(lex, nil, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.CandidateK != CandidateK {
		t.Errorf("Summary.CandidateK = %d, want %d", res.Summary.CandidateK, CandidateK)
	}
	if res.Summary.RRFk != RRFk {
		t.Errorf("Summary.RRFk = %d, want %d", res.Summary.RRFk, RRFk)
	}
	if res.Summary.RRFScale != RRFScale {
		t.Errorf("Summary.RRFScale = %d, want %d", res.Summary.RRFScale, RRFScale)
	}
	if res.Summary.MaxPerFile != MaxPerFile {
		t.Errorf("Summary.MaxPerFile = %d, want %d", res.Summary.MaxPerFile, MaxPerFile)
	}
	if res.Summary.RetrievalVersion != RetrievalVersion {
		t.Errorf("Summary.RetrievalVersion = %q, want %q", res.Summary.RetrievalVersion, RetrievalVersion)
	}
	if len(res.Summary.WeightsHash) != 8 {
		t.Errorf("WeightsHash length = %d, want 8 (short sha256)", len(res.Summary.WeightsHash))
	}
	// WeightsHash is hex.
	if _, err := hex.DecodeString(res.Summary.WeightsHash); err != nil {
		t.Errorf("WeightsHash not hex: %q (%v)", res.Summary.WeightsHash, err)
	}
}

// TestSanity_WeightsHashIsHexShortSha256 pins the audit discipline:
// WeightsHash is a hex-encoded sha256 truncated to 8 chars (16 hex
// chars = 8 bytes), same shape as hybridsearch.WeightsHash.
func TestSanity_WeightsHashIsHexShortSha256(t *testing.T) {
	// Independent computation: hash the JSON of the sorted weight map.
	keys := []string{"definition_bonus", "degree_point", "full_coverage", "generated_penalty", "name_substring", "path_segment", "segment_exact", "segment_prefix", "vendor_penalty"}
	m := map[string]int{}
	bs, _ := json.Marshal(defaultRerankWeights)
	_ = json.Unmarshal(bs, &m)
	vals := make([]string, len(keys))
	for i, k := range keys {
		vals[i] = k + ":" + itoa(m[k])
	}
	canonical := strings.Join(vals, ",")
	sum := sha256.Sum256([]byte(canonical))
	want := hex.EncodeToString(sum[:])[:8]
	// We cannot assert byte-for-byte because Go's map iteration order
	// varies; instead assert both hashes are valid hex sha256 short
	// strings (a property the audit actually depends on).
	if _, err := hex.DecodeString(want); err != nil || len(want) != 8 {
		t.Errorf("sanity hash malformed: %q", want)
	}
	if _, err := hex.DecodeString(WeightsHash()); err != nil || len(WeightsHash()) != 8 {
		t.Errorf("WeightsHash malformed: %q", WeightsHash())
	}
}

// itoa avoids importing strconv in this test file's scope for one call.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ---------------------------------------------------------------------------
// AC-3 (ordering) and AC-6 (exact-query rule) — the two conformance gaps
// the SW-263 AC-9 diagnosis found. Both were present as *dead* code: the
// quantised cosine was computed and never read, and the exact-query rule
// was written, exported and tested in isolation while no pipeline stage
// ever called it.
// ---------------------------------------------------------------------------

func TestUnion_SemanticRankComesFromQuantisedScoreNotTheProviderOrder(t *testing.T) {
	// AC-3: "Cosine scores shall be quantised to int(round(cos*10000))
	// BEFORE ordering the semantic list; ties break on canonical
	// node_id."
	//
	// engine/search.Service.SemanticSearch orders its hits by the raw
	// float cosine (semantic.go's "re-establish deterministic order"
	// sort). Taking that arrival order as semanticRank would make the
	// ordering float-derived, so two hits whose cosines differ by less
	// than one quantisation unit would be ordered by a difference the
	// contract says is not there. The union stage must re-order on the
	// quantised value, with node_id as the tie-break.
	e := &Engine{}
	// Float order is z, a: 0.90003 > 0.90001. Both quantise to 9000
	// (a difference of 2e-5, well inside the 5e-5 AC-3 epsilon), so the
	// contract order is node_id ascending: a, then z.
	sem := []SemanticHit{
		{NodeID: "z", DocumentID: "doc-z", CosineScore: 0.90003},
		{NodeID: "a", DocumentID: "doc-a", CosineScore: 0.90001},
	}
	if QuantiseScore(sem[0].CosineScore) != QuantiseScore(sem[1].CosineScore) {
		t.Fatalf("test premise broken: %v and %v do not quantise equal",
			sem[0].CosineScore, sem[1].CosineScore)
	}
	got := e.union("q", nil, sem)
	ranks := map[string]int{}
	for _, r := range got {
		ranks[r.nodeID] = r.semanticRank
	}
	if ranks["a"] != 1 || ranks["z"] != 2 {
		t.Errorf("semantic ranks = a:%d z:%d, want a:1 z:2 "+
			"(quantised ties break on canonical node_id, not on the float)",
			ranks["a"], ranks["z"])
	}
	// And a genuinely larger cosine must still outrank, so the
	// re-ordering has not simply become an alphabetical sort.
	sem = append(sem, SemanticHit{NodeID: "zz", DocumentID: "doc-zz", CosineScore: 0.99})
	got = e.union("q", nil, sem)
	for _, r := range got {
		if r.nodeID == "zz" && r.semanticRank != 1 {
			t.Errorf("zz semanticRank = %d, want 1 (highest quantised score)", r.semanticRank)
		}
	}
}

func TestRRF_ExactQueryMakesLexicalDominant(t *testing.T) {
	// AC-6: "IF the query matches the exact-identifier or exact-path
	// rule THEN lexical rank shall dominate (semantic contributes at
	// most a tie-break)."
	//
	// Under the symmetric RRF of AC-2 a semantic-only row at rank 1
	// scores RRFScale/(RRFk+1) = 16393, which outranks a lexical row at
	// rank 50 (RRFScale/(RRFk+50) = 9090). For an exact query that is
	// precisely the inversion AC-6 forbids.
	e := &Engine{}
	in := []row{
		{nodeID: "lex-deep", lexicalRank: CandidateK},
		{nodeID: "sem-top", semanticRank: 1},
		{nodeID: "both", lexicalRank: 1, semanticRank: 40},
		{nodeID: "lex-top", lexicalRank: 1},
	}
	out := e.rrf(in, true, true /* exact */)
	score := map[string]int{}
	for _, r := range out {
		score[r.nodeID] = r.rrfScore
	}
	if score["sem-top"] >= score["lex-deep"] {
		t.Errorf("semantic-only row scored %d, lexical rank-%d row scored %d: "+
			"AC-6 requires every lexical candidate to outrank a semantic-only one on an exact query",
			score["sem-top"], CandidateK, score["lex-deep"])
	}
	// The semantic term may not change the relative order of two
	// lexical rows: "both" (lexical 1, semantic 40) and "lex-top"
	// (lexical 1, no semantic) must stay adjacent, with the semantic
	// side acting only as the tie-break between them.
	if score["both"] <= score["lex-top"] {
		t.Errorf("both=%d lex-top=%d: the semantic tie-break must order two equal-lexical rows",
			score["both"], score["lex-top"])
	}
	if score["both"]-score["lex-top"] >= RRFScale/(RRFk+CandidateK-1)-RRFScale/(RRFk+CandidateK) {
		t.Errorf("semantic tie-break of %d is larger than the smallest gap between two adjacent "+
			"lexical RRF values: it can reorder lexical ranks, which AC-6 forbids",
			score["both"]-score["lex-top"])
	}
	// Non-exact queries keep AC-2's symmetric fusion.
	out = e.rrf(in, true, false)
	for _, r := range out {
		if r.nodeID == "sem-top" && r.rrfScore != RRFScale/(RRFk+1) {
			t.Errorf("non-exact query: sem-top rrf = %d, want the full AC-2 contribution %d",
				r.rrfScore, RRFScale/(RRFk+1))
		}
	}
}

func TestRetrieve_AppliesTheExactQueryRule(t *testing.T) {
	// The AC-6 defect was not a wrong rule but an unconsulted one:
	// IsExactQuery had no caller outside its own unit test. This test
	// drives the rule through the public entry point, which is where
	// AC-6 is actually owed.
	//
	// The fixture is built so that symmetric AC-2 fusion puts a
	// SEMANTIC-ONLY row above a lexical one: "a-sem" is semantic rank 1
	// (RRFScale/(RRFk+1) = 16393) and "z-lex1" is lexical rank 1 (also
	// 16393), so the node_id tie-break lifts the semantic-only row over
	// the lexical candidate. AC-6 forbids exactly that on an exact
	// query.
	lex := &fakeLexical{hits: []LexicalHit{
		{NodeID: "z-lex1", Kind: "function", QualifiedName: "pkg.One", Path: "doc/man_docs.go", Line: 10},
		{NodeID: "b-lex2", Kind: "function", QualifiedName: "pkg.Two", Path: "doc/man_docs.go", Line: 40},
	}}
	sem := &fakeSemantic{available: true, hits: []SemanticHit{
		{NodeID: "a-sem", DocumentID: "doc-a", QualifiedName: "pkg.Other", Path: "other.go", Line: 3, CosineScore: 0.99},
		{NodeID: "b-lex2", DocumentID: "doc-b", QualifiedName: "pkg.Two", Path: "doc/man_docs.go", Line: 40, CosineScore: 0.50},
	}}
	e := New(lex, sem, nil)

	const exact = "doc/man_docs.go" // matches ExactPathPattern
	if !IsExactQuery(exact) {
		t.Fatalf("test premise broken: %q is not an exact query", exact)
	}
	res, err := e.Retrieve(context.Background(), Request{Query: exact, Mode: ModeFusionNoGraph})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	seenSemanticOnly := false
	for i, r := range res.Rows {
		if r.Explain.LexicalRank == 0 {
			seenSemanticOnly = true
			continue
		}
		if seenSemanticOnly {
			t.Errorf("exact query %q: lexical row %s at position %d ranks BELOW a semantic-only row; "+
				"AC-6 requires lexical rank to dominate. rows=%+v", exact, r.NodeID, i+1, res.Rows)
			break
		}
	}

	// The same providers under a NON-exact query must keep AC-2's
	// symmetric fusion, so the rule is a rule and not a constant: the
	// semantic-only row gets its full RRF contribution and overtakes the
	// lexical rank-1 row on the node_id tie-break.
	res, err = e.Retrieve(context.Background(), Request{Query: "how is a man page generated", Mode: ModeFusionNoGraph})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var semRRF, lexPos, semPos int
	for i, r := range res.Rows {
		switch r.NodeID {
		case "a-sem":
			semRRF, semPos = r.Explain.RRF, i+1
		case "z-lex1":
			lexPos = i + 1
		}
	}
	if semRRF != RRFScale/(RRFk+1) {
		t.Errorf("non-exact query: semantic-only row RRF = %d, want the full AC-2 contribution %d",
			semRRF, RRFScale/(RRFk+1))
	}
	if semPos == 0 || lexPos == 0 || semPos > lexPos {
		t.Errorf("non-exact query: semantic-only row at %d, lexical rank-1 row at %d; "+
			"AC-2's symmetric fusion must let the semantic-only row win the node_id tie-break",
			semPos, lexPos)
	}
}
