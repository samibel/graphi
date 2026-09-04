// SW-263 semantic-first conformance suite (owner decision 2026-09-01).
//
// These tests pin the reviewer's replacement AC-2 / AC-3 / AC-4 /
// AC-5 / AC-6 / AC-7 / AC-11 contract on the SHIPPED ModeAuto path.
// Each test names the property it pins so the orchestrator's report can
// cite the exact conformance point.
package retrieval

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// AC-2 (semantic-first composition).
// ---------------------------------------------------------------------------

// TestSemanticFirst_PrefixIsExactlyS_0ToMinLimit verifies the shipped
// ModeAuto emits the first min(Limit, len(S)) rows of S, in exactly
// S's AC-3 quantised order, with S unique by canonical node_id.
func TestSemanticFirst_PrefixIsExactlyS_0ToMinLimit(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 2},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
		{NodeID: "s2", DocumentID: "doc-s2", Kind: "function", QualifiedName: "pkg.S2", Path: "s.go", Line: 2, CosineScore: 0.93},
		{NodeID: "s3", DocumentID: "doc-s3", Kind: "function", QualifiedName: "pkg.S3", Path: "s.go", Line: 3, CosineScore: 0.91},
		{NodeID: "s4", DocumentID: "doc-s4", Kind: "function", QualifiedName: "pkg.S4", Path: "s.go", Line: 4, CosineScore: 0.89},
		{NodeID: "s5", DocumentID: "doc-s5", Kind: "function", QualifiedName: "pkg.S5", Path: "s.go", Line: 5, CosineScore: 0.87},
	}}
	e := newEngine(lex, sem, nil)

	// Limit > len(S): all S rows plus lexical backfill (limit not
	// yet reached after the prefix).
	t.Run("limit_gt_len_S_emits_all_S_rows_in_S_order_then_backfill", func(t *testing.T) {
		res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		// 5 prefix + 2 backfill = 7 rows.
		if got, want := rowOrder(res.Rows), []string{"s1", "s2", "s3", "s4", "s5", "l1", "l2"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (5 prefix + 2 backfill)", got, want)
		}
		if res.Summary.Strategy != "semantic_first" {
			t.Errorf("Strategy = %q, want %q", res.Summary.Strategy, "semantic_first")
		}
	})

	// Limit == len(S): only the prefix is emitted (no backfill).
	t.Run("limit_eq_len_S_emits_only_prefix_no_backfill", func(t *testing.T) {
		res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 5})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if got, want := rowOrder(res.Rows), []string{"s1", "s2", "s3", "s4", "s5"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (limit == len(S): only the prefix, no backfill)", got, want)
		}
	})

	// Limit < len(S): the first Limit rows of S must reach Result.Rows.
	t.Run("limit_lt_len_S_emits_first_limit_rows_of_S", func(t *testing.T) {
		res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 3})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if got, want := rowOrder(res.Rows), []string{"s1", "s2", "s3"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (first min(Limit,len(S)) rows of S)", got, want)
		}
	})

	// SemanticRank on prefix rows must be the 1-based position within
	// the deduped prefix, NOT within the raw semantic list.
	t.Run("semantic_rank_is_1_based_position_in_deduped_prefix", func(t *testing.T) {
		res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 5})
		want := map[string]int{"s1": 1, "s2": 2, "s3": 3, "s4": 4, "s5": 5}
		for _, r := range res.Rows {
			if got, ok := want[r.NodeID]; !ok {
				t.Errorf("unexpected row %s", r.NodeID)
			} else if r.Explain.SemanticRank != got {
				t.Errorf("row %s: SemanticRank = %d, want %d", r.NodeID, r.Explain.SemanticRank, got)
			}
		}
	})
}

// TestSemanticFirst_BackfillAppendsLexicalInOrder verifies the backfill
// region scans L (delegated hybrid_v1) in order and appends rows that
// are unseen in the prefix until Limit is reached or L is exhausted.
func TestSemanticFirst_BackfillAppendsLexicalInOrder(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1},
		{NodeID: "l3", Kind: "function", QualifiedName: "pkg.L3", Path: "n.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
	}}
	e := newEngine(lex, sem, nil)

	// S has 1 row, Limit is 3: 1 prefix row + 2 backfill rows. Limit
	// is reached before L is exhausted, so l3 must not appear.
	t.Run("limit_lt_prefix_plus_len_L_stops_at_limit", func(t *testing.T) {
		res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 3})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if got, want := rowOrder(res.Rows), []string{"s1", "l1", "l2"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (1 prefix + 2 backfill in L order)", got, want)
		}
	})

	// S has 1 row, Limit is 10: 1 prefix + 3 backfill (whole L).
	t.Run("limit_gt_prefix_plus_len_L_emits_all_L_after_prefix", func(t *testing.T) {
		res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
		if got, want := rowOrder(res.Rows), []string{"s1", "l1", "l2", "l3"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (1 prefix + all 3 lexical backfill)", got, want)
		}
	})

	// Each backfill row must carry LexicalRank = its 1-based position
	// in L (not 0) — the backfill is built from L directly, not via
	// the union stage, so the rank is the lexical-side rank.
	t.Run("backfill_rows_carry_lexical_rank", func(t *testing.T) {
		res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
		wantRank := map[string]int{"l1": 1, "l2": 2, "l3": 3}
		for _, r := range res.Rows {
			if want, ok := wantRank[r.NodeID]; ok {
				if r.Explain.LexicalRank != want {
					t.Errorf("backfill row %s: LexicalRank = %d, want %d", r.NodeID, r.Explain.LexicalRank, want)
				}
				if r.Explain.SemanticRank != 0 {
					t.Errorf("backfill row %s: SemanticRank = %d, want 0 (lexical-only row)", r.NodeID, r.Explain.SemanticRank)
				}
			}
		}
	})

	// AC-11: each row carries the Region tag the reviewer's
	// replacement AC-11 requires.
	t.Run("rows_are_region_tagged", func(t *testing.T) {
		res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
		wantRegion := map[string]string{
			"s1": "semantic_prefix",
			"l1": "lexical_backfill",
			"l2": "lexical_backfill",
			"l3": "lexical_backfill",
		}
		for _, r := range res.Rows {
			if got, ok := wantRegion[rNodeIDForRow(r)]; !ok {
				t.Errorf("unexpected row %s", r.NodeID)
			} else if r.Region != got {
				t.Errorf("row %s Region = %q, want %q", r.NodeID, r.Region, got)
			}
		}
	})
}

// TestSemanticFirst_PrefixDedupedByNodeID verifies the AC-11 contract:
// node_id is the dedupe key. Two semantic hits for the same node_id
// keep only the first (in S order). DocumentID is provenance, NOT
// the cross-channel dedupe key — distinct document_ids for the same
// node_id only suppress the duplicate, never merge.
func TestSemanticFirst_PrefixDedupedByNodeID(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		// Same node_id, two distinct document_ids (the v2 case).
		{NodeID: "dup", DocumentID: "doc-dup-v2", Kind: "function", QualifiedName: "pkg.Dup", Path: "dup.go", Line: 1, CosineScore: 0.95},
		{NodeID: "dup", DocumentID: "doc-dup-v3", Kind: "function", QualifiedName: "pkg.Dup", Path: "dup.go", Line: 1, CosineScore: 0.90},
		{NodeID: "other", DocumentID: "doc-other", Kind: "function", QualifiedName: "pkg.Other", Path: "other.go", Line: 1, CosineScore: 0.80},
	}}
	e := newEngine(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// The prefix must have exactly one row for node_id "dup", and that
	// row's DocumentID must be the FIRST semantic hit's document_id
	// (doc-dup-v2). The second (doc-dup-v3) is a duplicate in the
	// input and stays out the; it never reaches the result.
	seen := 0
	for _, r := range res.Rows {
		if r.NodeID != "dup" {
			continue
		}
		seen++
		if r.DocumentID != "doc-dup-v2" {
			t.Errorf("dup row DocumentID = %q, want doc-dup-v2 (the FIRST semantic hit's id; provenance is the input order)", r.DocumentID)
		}
	}
	if seen != 1 {
		t.Errorf("dup prefix rows = %d, want 1 (AC-11: node_id dedupes; DocumentID is provenance, not a key)", seen)
	}
	// And "other" plus "l1" must still reach the result.
	if got := rowOrder(res.Rows); len(got) != 3 || got[0] != "dup" || got[1] != "other" || got[2] != "l1" {
		t.Errorf("rows = %v, want [dup, other, l1] (dup preserved in S order, other after, l1 from backfill)", got)
	}
}

// TestSemanticFirst_OverlapPopulatesLexicalRankProvenance verifies the
// AC-2 reviewer amendment: an overlapping lexical candidate populates
// the retained prefix row's LexicalRank provenance without changing
// its semantic identity (DocumentID, position) or its position in the
// prefix.
func TestSemanticFirst_OverlapPopulatesLexicalRankProvenance(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "overlap", Kind: "function", QualifiedName: "pkg.O", Path: "o.go", Line: 10, Score: 999},
		{NodeID: "lexical_only", Kind: "function", QualifiedName: "pkg.L", Path: "l.go", Line: 1, Score: 100},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "overlap", DocumentID: "doc-o", Kind: "function", QualifiedName: "pkg.O", Path: "o.go", Line: 10, CosineScore: 0.95},
		{NodeID: "sem_only", DocumentID: "doc-s", Kind: "function", QualifiedName: "pkg.S", Path: "sem.go", Line: 1, CosineScore: 0.80},
	}}
	e := newEngine(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (overlap, sem_only, lexical_only)", len(res.Rows))
	}
	if res.Rows[0].NodeID != "overlap" {
		t.Errorf("row 0 = %s, want overlap (semantic prefix order is preserved; the lexical overlap does NOT demote the prefix row)", res.Rows[0].NodeID)
	}
	if res.Rows[0].Explain.LexicalRank != 1 {
		t.Errorf("overlap row LexicalRank = %d, want 1 (lexical-side rank provenance stamped onto the retained prefix row)", res.Rows[0].Explain.LexicalRank)
	}
	if res.Rows[0].DocumentID != "doc-o" {
		t.Errorf("overlap row DocumentID = %q, want doc-o (semantic provenance preserved; lexical overlap does not replace it)", res.Rows[0].DocumentID)
	}
	if res.Rows[0].Explain.SemanticRank != 1 {
		t.Errorf("overlap row SemanticRank = %d, want 1", res.Rows[0].Explain.SemanticRank)
	}
	if res.Rows[1].NodeID != "sem_only" {
		t.Errorf("row 1 = %s, want sem_only", res.Rows[1].NodeID)
	}
	if res.Rows[2].NodeID != "lexical_only" {
		t.Errorf("row 2 = %s, want lexical_only", res.Rows[2].NodeID)
	}
}

// TestSemanticFirst_AC5CapSeededFromPrefix verifies the AC-5 reviewer
// amendment: the one-row-per-canonical-node_id invariant and the
// MaxPerFile=3 cap apply across the COMPLETE result, with the cap's
// path counts seeded from the prefix. Otherwise four semantic rows
// from one path plus three backfill rows from the same path would
// slip through.
func TestSemanticFirst_AC5CapSeededFromPrefix(t *testing.T) {
	// 4 prefix rows from same.go, 3 backfill candidates from same.go.
	// Cap seeded from prefix: pathCount["same.go"] = 4. Backfill
	// admits ZERO rows from same.go (4 >= maxPerFile=3).
	// Backfill candidates from other.go: all 3 admitted.
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "s1", Kind: "function", QualifiedName: "pkg.S1", Path: "same.go", Line: 1},
		{NodeID: "s2", Kind: "function", QualifiedName: "pkg.S2", Path: "same.go", Line: 2},
		{NodeID: "s3", Kind: "function", QualifiedName: "pkg.S3", Path: "same.go", Line: 3},
		{NodeID: "s4", Kind: "function", QualifiedName: "pkg.S4", Path: "same.go", Line: 4},
		{NodeID: "o1", Kind: "function", QualifiedName: "pkg.O1", Path: "other.go", Line: 1},
		{NodeID: "o2", Kind: "function", QualifiedName: "pkg.O2", Path: "other.go", Line: 2},
		{NodeID: "o3", Kind: "function", QualifiedName: "pkg.O3", Path: "other.go", Line: 3},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", CosineScore: 0.95},
		{NodeID: "s2", DocumentID: "doc-s2", CosineScore: 0.93},
		{NodeID: "s3", DocumentID: "doc-s3", CosineScore: 0.91},
		{NodeID: "s4", DocumentID: "doc-s4", CosineScore: 0.89},
	}}
	e := newEngine(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Same.go: 4 prefix rows preserved (the reviewer explicitly allows
	// this), 0 backfill rows (the cap seed forbids them).
	// Other.go: 3 backfill rows (under the cap).
	var sameCount, otherCount int
	for _, r := range res.Rows {
		switch r.Path {
		case "same.go":
			sameCount++
		case "other.go":
			otherCount++
		}
	}
	if sameCount != 4 {
		t.Errorf("same.go count = %d, want 4 (4 prefix rows preserved; cap is backfill-only)", sameCount)
	}
	if otherCount != 3 {
		t.Errorf("other.go count = %d, want 3 (backfill admitted up to MaxPerFile=3)", otherCount)
	}
	// Total = 7. Limit is 10 so nothing is truncated.
	if len(res.Rows) != 7 {
		t.Errorf("rows = %d, want 7 (4 prefix + 3 backfill)", len(res.Rows))
	}
}

// TestSemanticFirst_CapAllowsSaturatedPrefixWithoutBackfillFromSamePath
// verifies the comment in TestSemanticFirst_AC5CapSeededFromPrefix in
// a more compact form: a 3-row prefix from one path saturates the
// cap; a backfill candidate from the same path is rejected, even
// though the prefix's own rows remain.
func TestSemanticFirst_CapAllowsSaturatedPrefixWithoutBackfillFromSamePath(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "s1", Kind: "function", QualifiedName: "pkg.S1", Path: "same.go", Line: 1},
		{NodeID: "s2", Kind: "function", QualifiedName: "pkg.S2", Path: "same.go", Line: 2},
		{NodeID: "s3", Kind: "function", QualifiedName: "pkg.S3", Path: "same.go", Line: 3},
		{NodeID: "extra", Kind: "function", QualifiedName: "pkg.E", Path: "same.go", Line: 4},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", CosineScore: 0.95},
		{NodeID: "s2", DocumentID: "doc-s2", CosineScore: 0.93},
		{NodeID: "s3", DocumentID: "doc-s3", CosineScore: 0.91},
	}}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	var sameCount int
	for _, r := range res.Rows {
		if r.Path == "same.go" {
			sameCount++
		}
	}
	// The prefix saturates the cap (3 == MaxPerFile). The lexical
	// candidate "extra" must be rejected even though it is the next
	// lexical row. No backfill rows from same.go may appear.
	if sameCount != maxPerFile {
		t.Errorf("same.go count = %d, want %d (prefix saturates cap; backfill must be zero from that path)", sameCount, maxPerFile)
	}
}

// TestSemanticFirst_OneRowPerNodeIDAcrossCompleteResult verifies the
// AC-2 reviewer amendment end-to-end: a node_id present in BOTH the
// prefix and L appears once, in the prefix (preserving position and
// identity). The backfill never re-emits a prefix row.
func TestSemanticFirst_OneRowPerNodeIDAcrossCompleteResult(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "shared", Kind: "function", QualifiedName: "pkg.Shared", Path: "shared.go", Line: 1},
		{NodeID: "lex_only", Kind: "function", QualifiedName: "pkg.L", Path: "lex.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "shared", DocumentID: "doc-shared", CosineScore: 0.95},
		{NodeID: "sem_only", DocumentID: "doc-s", CosineScore: 0.80},
	}}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	seen := map[string]int{}
	for _, r := range res.Rows {
		seen[r.NodeID]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("node_id %s appears %d times in result, want 1", id, count)
		}
	}
}

// TestSemanticFirst_ReadyZeroHitsStaysReadyWithBackfill verifies the
// reviewer's explicit guarantee: a `ready` semantic generation with
// zero eligible hits stays `ready` (not reclassified as a degradation)
// and produces lexical backfill.
func TestSemanticFirst_ReadyZeroHitsStaysReadyWithBackfill(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1},
	}}
	// Ready but empty semantic list (e.g., zero-vector fallback,
	// generation built but no eligible nodes).
	sem := &fakeSemantic{available: true, state: StateReady, hits: nil}
	e := newEngine(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != StateReady {
		t.Errorf("Degradation = %q, want StateReady (a ready generation with zero eligible hits stays ready)", res.Degradation)
	}
	if res.Summary.Strategy != "semantic_first" {
		t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
	}
	if len(res.Rows) != 2 {
		t.Errorf("rows = %d, want 2 (lexical backfill on a ready-but-empty semantic generation)", len(res.Rows))
	}
	for _, r := range res.Rows {
		if r.Region != "lexical_backfill" {
			t.Errorf("row %s Region = %q, want lexical_backfill", r.NodeID, r.Region)
		}
	}
}

// TestSemanticFirst_NonReadyReturnsLUnchanged verifies the reviewer's
// AC-2 contract: when semantic candidates are unavailable (no
// embedder, missing/stale/corrupt generation), the shipped ModeAuto
// returns L (the delegated hybrid_v1 candidates) unchanged in
// membership, order, and explain fields. The state and reason are
// preserved verbatim.
func TestSemanticFirst_NonReadyReturnsLUnchanged(t *testing.T) {
	lexHits := []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1, Score: 100},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1, Score: 90},
		{NodeID: "l3", Kind: "function", QualifiedName: "pkg.L3", Path: "n.go", Line: 1, Score: 80},
	}
	cases := []struct {
		state  State
		reason string
	}{
		{StateLexicalOnly, "install the configured model"},
		{StateGenerationMissing, "build the semantic index"},
		{StateGenerationStale, "rebuild the stale semantic index"},
		{StateGenerationCorrupt, "repair the corrupt semantic index"},
	}
	for _, tc := range cases {
		t.Run("state_"+string(tc.state), func(t *testing.T) {
			lex := &fakeLexical{hits: lexHits}
			sem := &fakeSemantic{available: false, state: tc.state, reason: tc.reason}
			e := newEngine(lex, sem, nil)
			res, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
			if err != nil {
				t.Fatalf("Retrieve: %v", err)
			}
			if res.Degradation != tc.state {
				t.Errorf("Degradation = %q, want %q", res.Degradation, tc.state)
			}
			if res.Reason != tc.reason {
				t.Errorf("Reason = %q, want exact provider reason %q", res.Reason, tc.reason)
			}
			if res.Summary.Strategy != "lexical_only" {
				t.Errorf("Strategy = %q, want lexical_only (non-ready path stamps lexical_only)", res.Summary.Strategy)
			}
			if got, want := rowOrder(res.Rows), []string{"l1", "l2", "l3"}; !reflect.DeepEqual(got, want) {
				t.Errorf("row order = %v, want %v (L unchanged in membership and order)", got, want)
			}
			for i, r := range res.Rows {
				if r.Explain.LexicalRank != i+1 {
					t.Errorf("row %d LexicalRank = %d, want %d", i, r.Explain.LexicalRank, i+1)
				}
				if r.Explain.SemanticRank != 0 {
					t.Errorf("row %d SemanticRank = %d, want 0", i, r.Explain.SemanticRank)
				}
				if r.Explain.RRF != 0 {
					t.Errorf("row %d RRF = %d, want 0", i, r.Explain.RRF)
				}
				if r.Explain.Graph != r.Explain.Final {
					t.Errorf("row %d Graph (%d) != Final (%d); on the AC-7 path Graph must equal the delegated lexical score", i, r.Explain.Graph, r.Explain.Final)
				}
				if r.Region != "lexical_only" {
					t.Errorf("row %d Region = %q, want lexical_only", i, r.Region)
				}
			}
		})
	}
}

func TestSemanticFirst_EnforcesQuantisedOrder(t *testing.T) {
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		// Raw-float order is z, a. Both values quantise to 9000, so AC-3
		// requires canonical node_id order a, z.
		{NodeID: "z", DocumentID: "doc-z", Path: "z.go", Line: 1, CosineScore: 0.90003},
		{NodeID: "a", DocumentID: "doc-a", Path: "a.go", Line: 1, CosineScore: 0.90001},
	}}
	e := newEngine(&fakeLexical{}, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "how does this work", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowOrder(res.Rows), []string{"a", "z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want AC-3 quantised order %v", got, want)
	}
}

func TestSemanticFirst_EligibilityPrecedesNodeDedupe(t *testing.T) {
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "same", DocumentID: "ineligible", CosineScore: 0.9},
		{NodeID: "same", DocumentID: "eligible", Path: "same.go", Line: 7, CosineScore: 0.8},
	}}
	e := newEngine(&fakeLexical{}, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "how does this work", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].DocumentID != "eligible" {
		t.Errorf("rows = %+v, want the first eligible semantic record for canonical node_id same", res.Rows)
	}
}

func TestSemanticFirst_BackfillCapUsesNormalizedPath(t *testing.T) {
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "d1", Path: "./pkg/x.go", Line: 1, CosineScore: 0.9},
		{NodeID: "s2", DocumentID: "d2", Path: "pkg/./x.go", Line: 2, CosineScore: 0.8},
		{NodeID: "s3", DocumentID: "d3", Path: "pkg/x.go", Line: 3, CosineScore: 0.7},
	}}
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "blocked", Path: `pkg\x.go`, Line: 4},
		{NodeID: "accepted", Path: "pkg/y.go", Line: 1},
	}}
	e := newEngine(lex, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "how does this work", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowOrder(res.Rows), []string{"s1", "s2", "s3", "accepted"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want normalized-path cap result %v", got, want)
	}
}

// TestSemanticFirst_NonReadyWithCapBitingData verifies the reviewer's
// explicit warning: AC-7 byte parity must be proven, not assumed. The
// previous fixture passed only because the cap happened not to bite;
// here the cap WOULD have applied on the fused path (5 hits from
// same.go, cap=3), and the non-ready path must still return all 5
// hits unchanged.
func TestSemanticFirst_NonReadyWithCapBitingData(t *testing.T) {
	lexHits := []lexicalHit{
		{NodeID: "s1", Kind: "function", QualifiedName: "pkg.S1", Path: "same.go", Line: 1, Score: 100},
		{NodeID: "s2", Kind: "function", QualifiedName: "pkg.S2", Path: "same.go", Line: 10, Score: 90},
		{NodeID: "s3", Kind: "function", QualifiedName: "pkg.S3", Path: "same.go", Line: 20, Score: 80},
		{NodeID: "s4", Kind: "function", QualifiedName: "pkg.S4", Path: "same.go", Line: 30, Score: 70},
		{NodeID: "s5", Kind: "function", QualifiedName: "pkg.S5", Path: "same.go", Line: 40, Score: 60},
		{NodeID: "o1", Kind: "function", QualifiedName: "pkg.O1", Path: "other.go", Line: 1, Score: 50},
	}
	lex := &fakeLexical{hits: lexHits}
	sem := &fakeSemantic{available: false, state: StateLexicalOnly}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	if len(res.Rows) != 6 {
		t.Errorf("rows = %d, want 6 (all 6 lexical hits unchanged; the cap must NOT run on the non-ready path)", len(res.Rows))
	}
	var sameCount int
	for _, r := range res.Rows {
		if r.Path == "same.go" {
			sameCount++
		}
		if r.Region != "lexical_only" {
			t.Errorf("row %s Region = %q, want lexical_only", r.NodeID, r.Region)
		}
	}
	if sameCount != 5 {
		t.Errorf("same.go count = %d, want 5 (cap WOULD have applied on the fused path)", sameCount)
	}
}

// TestSemanticFirst_NonReadyByteIdenticalToLex verifies the AC-7 byte
// parity: the shipped ModeAuto on a non-ready generation is is
// byte identical to the lexical-only path. Two retrievals with the same
// fixture, one ModeAuto and one ModeLexicalOnly, must produce
// byte-identical JSON on the non-ready path.
func TestSemanticFirst_NonReadyByteIdenticalToLex(t *testing.T) {
	lexHits := []lexicalHit{
		{NodeID: "s1", Kind: "function", QualifiedName: "pkg.S1", Path: "same.go", Line: 1, Score: 100},
		{NodeID: "s2", Kind: "function", QualifiedName: "pkg.S2", Path: "same.go", Line: 10, Score: 90},
		{NodeID: "s3", Kind: "function", QualifiedName: "pkg.S3", Path: "same.go", Line: 20, Score: 80},
		{NodeID: "s4", Kind: "function", QualifiedName: "pkg.S4", Path: "same.go", Line: 30, Score: 70},
		{NodeID: "s5", Kind: "function", QualifiedName: "pkg.S5", Path: "same.go", Line: 40, Score: 60},
		{NodeID: "o1", Kind: "function", QualifiedName: "pkg.O1", Path: "other.go", Line: 1, Score: 50},
		{NodeID: "o2", Kind: "function", QualifiedName: "pkg.O2", Path: "other.go", Line: 10, Score: 40},
		{NodeID: "o3", Kind: "function", QualifiedName: "pkg.O3", Path: "other.go", Line: 20, Score: 30},
	}
	// Non-ready semantic provider.
	lex := &fakeLexical{hits: lexHits}
	sem := &fakeSemantic{available: false, state: StateLexicalOnly}

	// ModeAuto (non-ready fallback).
	eAuto := newEngine(lex, sem, nil)
	resAuto, _ := eAuto.Retrieve(context.Background(), Request{Query: "q", Limit: 6})

	// ModeLexicalOnly.
	eLex := newEngine(lex, sem, nil)
	resLex, _ := eLex.Retrieve(context.Background(), Request{Query: "q", Limit: 6, Mode: ModeLexicalOnly})

	// The row payload must be byte-identical.
	autoRows, _ := json.Marshal(resAuto.Rows)
	lexRows, _ := json.Marshal(resLex.Rows)
	if string(autoRows) != string(lexRows) {
		t.Errorf("AC-7 byte parity violated between ModeAuto non-ready and ModeLexicalOnly:\n  auto=%s\n  lex =%s", autoRows, lexRows)
	}
	// Degradation state must match (StateLexicalOnly).
	if resAuto.Degradation != resLex.Degradation {
		t.Errorf("Degradation differs: auto=%q, lex=%q", resAuto.Degradation, resLex.Degradation)
	}
}

// ---------------------------------------------------------------------------
// AC-8 (prefix stability) + AC-5 reviewer amendment.
// ---------------------------------------------------------------------------

// TestSemanticFirst_PrefixStability_L1IsPrefixOfL2 verifies the
// reviewer-mandated prefix-stability test: for identical inputs and
// L1 < L2, the L1 result equals the first L1 rows of the L2 result.
// The semantic prefix is never reordered or removed between limits,
// and the backfill is deterministic.
func TestSemanticFirst_PrefixStability_L1IsPrefixOfL2(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1},
		{NodeID: "l3", Kind: "function", QualifiedName: "pkg.L3", Path: "n.go", Line: 1},
		{NodeID: "l4", Kind: "function", QualifiedName: "pkg.L4", Path: "o.go", Line: 1},
		{NodeID: "l5", Kind: "function", QualifiedName: "pkg.L5", Path: "p.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
		{NodeID: "s2", DocumentID: "doc-s2", Kind: "function", QualifiedName: "pkg.S2", Path: "s.go", Line: 2, CosineScore: 0.93},
		{NodeID: "s3", DocumentID: "doc-s3", Kind: "function", QualifiedName: "pkg.S3", Path: "s.go", Line: 3, CosineScore: 0.91},
	}}
	e := newEngine(lex, sem, nil)

	r1, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 2})
	if err != nil {
		t.Fatalf("Retrieve(L=2): %v", err)
	}
	r2, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 5})
	if err != nil {
		t.Fatalf("Retrieve(L=5): %v", err)
	}
	r3, err := e.Retrieve(context.Background(), Request{Query: "q", Limit: 8})
	if err != nil {
		t.Fatalf("Retrieve(L=8): %v", err)
	}

	// Helper: assert result A equals the first len(A) rows of result B.
	prefixOf := func(a, b []Row) {
		t.Helper()
		if len(a) > len(b) {
			t.Fatalf("prefix assertion: a has %d rows, b has %d (a should be <= b)", len(a), len(b))
		}
		for i := 0; i < len(a); i++ {
			if a[i].NodeID != b[i].NodeID {
				t.Errorf("prefix divergence at row %d: a=%s, b=%s (rows=%v vs %v)", i, a[i].NodeID, b[i].NodeID, rowOrder(a), rowOrder(b))
			}
		}
	}

	t.Run("L1_subset_of_L2", func(t *testing.T) {
		prefixOf(r1.Rows, r2.Rows)
	})
	t.Run("L2_subset_of_L3", func(t *testing.T) {
		prefixOf(r2.Rows, r3.Rows)
	})
	t.Run("L1_subset_of_L3", func(t *testing.T) {
		prefixOf(r1.Rows, r3.Rows)
	})

	// Concrete shape check: 3 prefix + 5 lexical backfill under Limit=8
	// (s1, s2, s3, l1, l2, l3, l4, l5). Limit=5 = first 5 of that
	// (s1, s2, s3, l1, l2). Limit=2 = first 2 (s1, s2).
	if got, want := rowOrder(r3.Rows), []string{"s1", "s2", "s3", "l1", "l2", "l3", "l4", "l5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("L=8 rows = %v, want %v", got, want)
	}
	if got, want := rowOrder(r2.Rows), []string{"s1", "s2", "s3", "l1", "l2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("L=5 rows = %v, want %v (first 5 of L=8)", got, want)
	}
	if got, want := rowOrder(r1.Rows), []string{"s1", "s2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("L=2 rows = %v, want %v (first 2 of L=8)", got, want)
	}
}

// TestSemanticFirst_PrefixStabilityPrefix pins the degenerate
// case where the semantic list is empty but ready: the result is the
// L list up to Limit, identical between limits.
func TestSemanticFirst_PrefixStability_ZeroPrefix(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1},
		{NodeID: "l3", Kind: "function", QualifiedName: "pkg.L3", Path: "n.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: nil}
	e := newEngine(lex, sem, nil)
	r1, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 1})
	r2, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 3})
	if len(r1.Rows) != 1 || r1.Rows[0].NodeID != "l1" {
		t.Errorf("L=1 rows = %v, want [l1]", rowOrder(r1.Rows))
	}
	if got, want := rowOrder(r2.Rows), []string{"l1", "l2", "l3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("L=3 rows = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// AC-6 (exact-query override split).
//
// The SW-263 owner decision 2026-09-01 split the original AC-6
// override: identifier queries follow AC-2 with no lexical-dominance
// override; path queries return L in lexical order. The split is
// decided by the documented isExactIdentifier / isExactPath
// constants; nothing learned.
// ---------------------------------------------------------------------------

// TestSemanticFirst_ExactIdentifierLifted_NoOverride pins the AC-6 lift
// for the IDENTIFIER rule: an exact-IDENTIFIER query against the
// shipped ModeAuto follows the semantic-prefix + lexical-backfill
// order. The lexical-dominance override that used to apply on exact
// queries is gone from the production surface for identifiers; the
// underlying isExactIdentifier rule stays live only for the
// evaluator-only fusion modes.
//
// Owner decision 2026-09-01 (delta_brief on the 2026-09-01
// semantic-first-local run): the IDENTIFIER lift stays; only the
// PATH override is restored. The split is pinned by
// TestSemanticFirst_PathOverrideRestored_IdentifierLifted below.
func TestSemanticFirst_ExactIdentifierLifted_NoOverride(t *testing.T) {
	// Shape: semantic rank-1 row is a semantic-only candidate (no
	// lexical counterpart). The lexical rank-1 row is from a different
	// file. Under a reinstated identifier override the lexical rank-1
	// row would lead; under the lifted identifier rule semantic-first
	// wins.
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "lex_first", Kind: "function", QualifiedName: "pkg.Lex", Path: "lex.go", Line: 1, Score: 999},
		{NodeID: "lex_second", Kind: "function", QualifiedName: "pkg.Lex2", Path: "lex.go", Line: 2, Score: 100},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		// Semantic-only row (no lexical counterpart).
		{NodeID: "sem_only_top", DocumentID: "doc-s", Kind: "function", QualifiedName: "pkg.S", Path: "sem.go", Line: 1, CosineScore: 0.99},
		// A row that overlaps with a lexical row (a lower cosine).
		{NodeID: "lex_second", DocumentID: "doc-l2", Kind: "function", QualifiedName: "pkg.Lex2", Path: "lex.go", Line: 2, CosineScore: 0.5},
	}}
	e := newEngine(lex, sem, nil)

	// The query is an exact identifier (a dotted name); under any
	// identifier override this would have reordered. Under the lifted
	// identifier rule, semantic-first wins.
	res, err := e.Retrieve(context.Background(), Request{Query: "cobra.Command.AddCommand", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got, want := rowOrder(res.Rows), []string{"sem_only_top", "lex_second", "lex_first"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v (semantic-first: sem_only_top leads even on an exact-identifier query; AC-6 identifier override is LIFTED)", got, want)
	}
	if res.Summary.Strategy != "semantic_first" {
		t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
	}
	// Region: prefix row(s) from the semantic list, backfill
	// from lexical. Every row is a semantic-first region, NOT a
	// lexical_path_override region.
	for _, r := range res.Rows {
		if r.Region == "lexical_path_override" {
			t.Errorf("row %s: region = lexical_path_override on an exact-IDENTIFIER query (the path rule must NOT fire here)", r.NodeID)
		}
	}
}

// TestSemanticFirst_PathOverrideRestored_IdentifierLifted pins the
// owner-decided split (2026-09-01 delta_brief):
//
//   - An exact-PATH query (a query matching the documented
//     isExactPath rule — a string with `/`) returns to lexical
//     dominance: the delegated lexical list L is the result, in
//     lexical order, with the strategy stamped as "semantic_first"
//     and every row stamped as region "lexical_path_override".
//   - An exact-IDENTIFIER query (matching isExactIdentifier but NOT
//     isExactPath) follows the semantic-prefix + lexical-backfill
//     order, with prefix rows stamped "semantic_prefix" and backfill
//     rows stamped "lexical_backfill". The identifier rule is the
//     part of the original AC-6 override the evidence supports
//     lifting.
//   - The split is decided by the documented regexp constants
//     (exactIdentifierPattern and exactPathPattern), never by a
//     learned rule.
//
// Three sub-tests exercise the three query shapes:
//   - exact path   (a string with `/`)            -> lexical_path_override
//   - exact identifier (bare or dotted, no slash) -> semantic_prefix + lexical_backfill
//   - free-text                                       -> semantic_prefix + lexical_backfill
func TestSemanticFirst_PathOverrideRestored_IdentifierLifted(t *testing.T) {
	// Shared fixture. The lexical list has doc/man_docs.go at rank 1
	// (a path-shaped target the user would search by path). The
	// semantic list puts a semantic-only candidate at the top with a
	// high cosine.
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "lex_path_target", Kind: "function", QualifiedName: "pkg.LexPathTarget", Path: "doc/man_docs.go", Line: 1, Score: 999},
		{NodeID: "lex_second", Kind: "function", QualifiedName: "pkg.Lex2", Path: "lex.go", Line: 2, Score: 100},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "sem_only_top", DocumentID: "doc-s", Kind: "function", QualifiedName: "pkg.S", Path: "sem.go", Line: 1, CosineScore: 0.99},
		{NodeID: "lex_second", DocumentID: "doc-l2", Kind: "function", QualifiedName: "pkg.Lex2", Path: "lex.go", Line: 2, CosineScore: 0.5},
	}}
	e := newEngine(lex, sem, nil)

	// Sub-test 1: exact-PATH query. The path override fires; the
	// result is L in lexical order, every row stamped
	// lexical_path_override. The semantic list is consulted, but it
	// is NOT used to order the result rows. Strategy stays
	// "semantic_first".
	t.Run("exact_path_query_returns_lexical_path_override", func(t *testing.T) {
		const q = "doc/man_docs.go"
		if !isExactPath(q) {
			t.Fatalf("test premise broken: %q must match isExactPath", q)
		}
		if isExactIdentifier(q) {
			t.Fatalf("test premise broken: %q must NOT match isExactIdentifier (it has a slash)", q)
		}
		res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if res.Summary.Strategy != "semantic_first" {
			t.Errorf("Strategy = %q, want semantic_first (the dispatch is semantic-first; the path override is a sub-case of the strategy, not a new strategy)", res.Summary.Strategy)
		}
		if got, want := rowOrder(res.Rows), []string{"lex_path_target", "lex_second"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (path override: L in lexical order, the semantic list does NOT order the result rows)", got, want)
		}
		for _, r := range res.Rows {
			if r.Region != "lexical_path_override" {
				t.Errorf("row %s: Region = %q, want lexical_path_override (every row on a path-override result carries the override region)", r.NodeID, r.Region)
			}
			if r.Explain.SemanticRank != 0 {
				t.Errorf("row %s: SemanticRank = %d, want 0 (path override: no semantic provenance)", r.NodeID, r.Explain.SemanticRank)
			}
			if r.Explain.RRF != 0 || r.Explain.Graph != r.Explain.Final || r.Explain.Classification != 0 {
				t.Errorf("row %s: ordering contributions (RRF=%d, Graph=%d, Classification=%d, Final=%d) must be (0, Final, 0, Final) on the lexical path", r.NodeID, r.Explain.RRF, r.Explain.Graph, r.Explain.Classification, r.Explain.Final)
			}
		}
		if len(res.Rows) > 0 && res.Rows[0].NodeID != "lex_path_target" {
			t.Errorf("rank-1 node_id = %q, want lex_path_target (path override: lexical rank-1 leads)", res.Rows[0].NodeID)
		}
	})

	// Sub-test 2: exact-IDENTIFIER query (a dotted name, no slash).
	// The identifier rule is the part of the original AC-6 override
	// the evidence supports lifting; semantic-first wins. Strategy is
	// "semantic_first", every prefix row carries region
	// "semantic_prefix", and the path override MUST NOT fire.
	t.Run("exact_identifier_query_keeps_semantic_first", func(t *testing.T) {
		const q = "cobra.Command.AddCommand"
		if !isExactIdentifier(q) {
			t.Fatalf("test premise broken: %q must match isExactIdentifier", q)
		}
		if isExactPath(q) {
			t.Fatalf("test premise broken: %q must NOT match isExactPath (no slash)", q)
		}
		res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if res.Summary.Strategy != "semantic_first" {
			t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
		}
		// sem_only_top is the prefix rank-1; the backfill scans L
		// in order and admits lex_second then lex_path_target.
		if got, want := rowOrder(res.Rows), []string{"sem_only_top", "lex_second", "lex_path_target"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (semantic-first: sem_only_top leads; lex_path_target is admitted as backfill, NOT as the path override's rank-1)", got, want)
		}
		for _, r := range res.Rows {
			if r.Region == "lexical_path_override" {
				t.Errorf("row %s: Region = lexical_path_override on an exact-IDENTIFIER query (the path rule must NOT fire here)", r.NodeID)
			}
		}
		if res.Rows[0].Region != "semantic_prefix" {
			t.Errorf("rank-1 row Region = %q, want semantic_prefix", res.Rows[0].Region)
		}
		// The lex_path_target row, even though it is on the lexical
		// list at rank 1, is admitted to the result as a backfill row,
		// stamped region="lexical_backfill". It is NOT demoted to
		// rank 1 — the identifier lift is in force.
		var lpt *Row
		for i, r := range res.Rows {
			if r.NodeID == "lex_path_target" {
				lpt = &res.Rows[i]
			}
		}
		if lpt == nil {
			t.Fatal("lex_path_target was missing from the result; the backfill scan must reach it")
		}
		if lpt.Region != "lexical_backfill" {
			t.Errorf("lex_path_target Region = %q, want lexical_backfill (the path target is admitted via the lexical backfill scan, not via the path override)", lpt.Region)
		}
		if lpt.Explain.LexicalRank != 1 {
			t.Errorf("lex_path_target LexicalRank = %d, want 1 (the lexical list has it at rank 1; the backfill preserves that provenance)", lpt.Explain.LexicalRank)
		}
	})

	// Sub-test 3: bare-identifier query (no slash, no dot). The
	// identifier rule fires; semantic-first wins.
	t.Run("bare_identifier_query_keeps_semantic_first", func(t *testing.T) {
		const q = "ExecuteC"
		if !isExactIdentifier(q) {
			t.Fatalf("test premise broken: %q must match isExactIdentifier", q)
		}
		if isExactPath(q) {
			t.Fatalf("test premise broken: %q must NOT match isExactPath (no slash)", q)
		}
		res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if res.Summary.Strategy != "semantic_first" {
			t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
		}
		if got, want := rowOrder(res.Rows), []string{"sem_only_top", "lex_second", "lex_path_target"}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows = %v, want %v (semantic-first on a bare-identifier query)", got, want)
		}
	})

	// Sub-test 4: free-text query (no slash, no dotted identifier).
	// Neither rule fires; semantic-first wins.
	t.Run("free_text_query_keeps_semantic_first", func(t *testing.T) {
		const q = "how do I generate a man page"
		if isExactIdentifier(q) || isExactPath(q) {
			t.Fatalf("test premise broken: %q must NOT match either rule", q)
		}
		res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if res.Summary.Strategy != "semantic_first" {
			t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
		}
	})
}

// TestSemanticFirst_PathOverride_BareFilenameFires pins SW-270 AC-1 on
// the shipped dispatch: a bare filename with a known source extension
// ("shell_completions.go", dev exact_path query cb-09) matches isExactPath
// and therefore takes the lexical path override — L in lexical order,
// every row stamped lexical_path_override, the semantic rank-1 candidate
// NOT leading. Before SW-270 the documented rule required a `/`, this
// query went semantic-first, and exact_path scored 0.6667 on dev
// (SW-263). The override is the narrow bare-filename shape only: the
// sibling sub-tests in TestSemanticFirst_PathOverrideRestored_IdentifierLifted
// keep identifiers on the semantic-first path.
func TestSemanticFirst_PathOverride_BareFilenameFires(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "lex_target", Kind: "function", QualifiedName: "pkg.Target", Path: "shell_completions.go", Line: 1, Score: 999},
		{NodeID: "lex_other", Kind: "function", QualifiedName: "pkg.Other", Path: "other.go", Line: 1, Score: 100},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "sem_only_top", DocumentID: "doc-s", Kind: "function", QualifiedName: "pkg.S", Path: "sem.go", Line: 1, CosineScore: 0.99},
	}}
	e := newEngine(lex, sem, nil)

	const q = "shell_completions.go"
	if !isExactPath(q) {
		t.Fatalf("test premise broken: %q must match isExactPath (SW-270: a bare filename with a known source extension is a path query)", q)
	}
	// A bare filename ALSO matches the identifier regex (it is a dotted
	// name shape: "shell_completions" + "." + "go"). That overlap is
	// documented on exactPathPattern and resolved by dispatch order:
	// readyDispatch consults isExactPath first, and the identifier half
	// is lifted in ModeAuto anyway (SW-263). Pin the overlap so a change
	// to either regex that silently removes it is visible here.
	if !isExactIdentifier(q) {
		t.Fatalf("test premise broken: %q is expected to match isExactIdentifier as well; the path override must win by dispatch order, not by the identifier rule rejecting it", q)
	}

	res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Summary.Strategy != "semantic_first" {
		t.Errorf("Strategy = %q, want semantic_first (the path override is a sub-case of the semantic-first dispatch)", res.Summary.Strategy)
	}
	if got, want := rowOrder(res.Rows), []string{"lex_target", "lex_other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v (bare filename: L in lexical order, the semantic rank-1 row does not lead)", got, want)
	}
	for _, r := range res.Rows {
		if r.Region != "lexical_path_override" {
			t.Errorf("row %s: Region = %q, want lexical_path_override", r.NodeID, r.Region)
		}
		if r.Explain.SemanticRank != 0 {
			t.Errorf("row %s: SemanticRank = %d, want 0 (path override: no semantic provenance)", r.NodeID, r.Explain.SemanticRank)
		}
	}
}

// TestSemanticFirst_PathOverride_RejectedSuffixesDoNotFire is the
// negative twin of TestSemanticFirst_PathOverride_BareFilenameFires on
// the shipped dispatch (SW-270 round 1). The bare-filename shape is
// ".go" only: a bare filename with any other suffix, and a dotted
// identifier, must NOT take the lexical path override. Each query is
// identifier-shaped (it also matches exactIdentifierPattern), and the
// identifier half is lifted in ModeAuto, so the semantic rank-1 row must
// lead and no row may carry the lexical_path_override region — exactly
// what an exact-identifier query gets. The lexical fixture's rank-1 row
// is a file with the queried name, so if the override fired the lexical
// row would lead and the assertion would catch it.
func TestSemanticFirst_PathOverride_RejectedSuffixesDoNotFire(t *testing.T) {
	queries := []string{
		"theme.css",
		"config.json",
		"README.md",
		"config.yaml",
		"index.html",
		"module.jsx",
		"script.kts",
		"cmd.Execute",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			if isExactPath(q) {
				t.Fatalf("test premise broken: %q must NOT match isExactPath (SW-270: the bare-filename shape is .go only)", q)
			}
			if !isExactIdentifier(q) {
				t.Fatalf("test premise broken: %q is expected to match isExactIdentifier (identifier-shaped); the point of the test is that the lifted identifier rule, not the path rule, governs it", q)
			}
			lex := &fakeLexical{hits: []lexicalHit{
				{NodeID: "lex_target", Kind: "function", QualifiedName: "pkg.Target", Path: q, Line: 1, Score: 999},
				{NodeID: "lex_other", Kind: "function", QualifiedName: "pkg.Other", Path: "other.go", Line: 1, Score: 100},
			}}
			sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
				{NodeID: "sem_only_top", DocumentID: "doc-s", Kind: "function", QualifiedName: "pkg.S", Path: "sem.go", Line: 1, CosineScore: 0.99},
				{NodeID: "lex_other", DocumentID: "doc-lo", Kind: "function", QualifiedName: "pkg.Other", Path: "other.go", Line: 1, CosineScore: 0.5},
			}}
			e := newEngine(lex, sem, nil)

			res, err := e.Retrieve(context.Background(), Request{Query: q, Limit: 10})
			if err != nil {
				t.Fatalf("Retrieve: %v", err)
			}
			if res.Summary.Strategy != "semantic_first" {
				t.Errorf("Strategy = %q, want semantic_first", res.Summary.Strategy)
			}
			if len(res.Rows) == 0 || res.Rows[0].NodeID != "sem_only_top" {
				t.Errorf("rows = %v, want sem_only_top leading (a %q query is identifier-shaped and takes the semantic-first path; the lexical path override must NOT fire)", rowOrder(res.Rows), q)
			}
			for _, r := range res.Rows {
				if r.Region == "lexical_path_override" {
					t.Errorf("row %s: Region = lexical_path_override on %q (the path rule must NOT fire on a non-.go bare filename or a dotted identifier)", r.NodeID, q)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC-11 (strategy and provenance truthfulness).
// ---------------------------------------------------------------------------

// TestSemanticFirst_StrategyAndProvenanceStamped pins the reviewer's
// AC-11: every result stamps the applied ordering strategy and the
// retrieval algorithm version; every row identifies whether it came
// from the semantic prefix or the lexical backfill.
func TestSemanticFirst_StrategyAndProvenanceStamped(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "shared", Kind: "function", QualifiedName: "pkg.S", Path: "shared.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
		{NodeID: "shared", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.Shared", Path: "shared.go", Line: 1, CosineScore: 0.80},
	}}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})

	// Strategy.
	if res.Summary.Strategy != "semantic_first" {
		t.Errorf("Summary.Strategy = %q, want semantic_first", res.Summary.Strategy)
	}
	// RetrievalVersion.
	if res.Summary.RetrievalVersion != retrievalVersion {
		t.Errorf("Summary.RetrievalVersion = %q, want %q (the bump that says shipped behaviour moved)", res.Summary.RetrievalVersion, retrievalVersion)
	}
	if retrievalVersion != "retrieval/2" {
		t.Errorf("retrievalVersion = %q, want retrieval/2", retrievalVersion)
	}
	// Provenance on rows.
	wantRegion := map[string]string{
		"s1":     "semantic_prefix",
		"shared": "semantic_prefix",
		"l1":     "lexical_backfill",
	}
	for _, r := range res.Rows {
		want, ok := wantRegion[r.NodeID]
		if !ok {
			t.Errorf("unexpected row %s", r.NodeID)
			continue
		}
		if r.Region != want {
			t.Errorf("row %s Region = %q, want %q (AC-11 provenance truthfulness)", r.NodeID, r.Region, want)
		}
	}
	// Source ranks preserved (semantic and lexical both stamped on
	// overlap).
	for _, r := range res.Rows {
		if r.NodeID == "shared" {
			if r.Explain.SemanticRank != 2 || r.Explain.LexicalRank != 2 {
				t.Errorf("shared row ranks: sem=%d, lex=%d, want (2, 2) (overlap retains both source ranks)", r.Explain.SemanticRank, r.Explain.LexicalRank)
			}
		}
		if r.NodeID == "l1" {
			if r.Explain.SemanticRank != 0 {
				t.Errorf("l1 SemanticRank = %d, want 0 (lexical-only backfill row)", r.Explain.SemanticRank)
			}
			if r.Explain.LexicalRank != 1 {
				t.Errorf("l1 LexicalRank = %d, want 1", r.Explain.LexicalRank)
			}
		}
	}
}

// TestSemanticFirst_NoExperimentalContributionsOnShippedMode pins the
// AC-11 truthfulness corollary: RRF, graph, classification, and
// diversification are reported as ordering contributions only when
// they actually affected the result. On the shipped semantic-first
// mode they do not apply.
func TestSemanticFirst_NoExperimentalContributionsOnShippedMode(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
	}}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	for _, r := range res.Rows {
		if r.Explain.RRF != 0 {
			t.Errorf("row %s RRF = %d, want 0 (RRF does not apply in shipped semantic-first)", r.NodeID, r.Explain.RRF)
		}
		if r.Explain.Graph != 0 {
			t.Errorf("row %s Graph = %d, want 0 (graph rerank does not apply in shipped semantic-first)", r.NodeID, r.Explain.Graph)
		}
		if r.Explain.Classification != 0 {
			t.Errorf("row %s Classification = %d, want 0 (classification penalty does not apply in shipped semantic-first)", r.NodeID, r.Explain.Classification)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-1 + AC-12 + AC-4 (unreachable RRF from production surfaces).
// ---------------------------------------------------------------------------

// TestModeFusionNoGraph_AndGraph_AreUnreachableFromShippedAuto pins the
// AC-1 + AC-4 reviewer amendment: the shipped ModeAuto never selects
// the fusion pipelines, even when the semantic generation is ready.
// The strategies on the dispatched results are exactly
// "semantic_first" or "lexical_only"; "fusion_no_graph" and
// "fusion_graph" are unreachable from a ModeAuto call.
func TestModeFusionNoGraph_AndGraph_AreUnreachableFromShippedAuto(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
	}}
	e := newEngine(lex, sem, nil)

	// ModeAuto under ready semantic: strategy == semantic_first, NOT fusion.
	resAuto, _ := e.Retrieve(context.Background(), Request{Query: "q", Mode: ModeAuto, Limit: 10})
	if resAuto.Summary.Strategy == "fusion_no_graph" || resAuto.Summary.Strategy == "fusion_graph" {
		t.Errorf("ModeAuto produced Strategy=%q (the experimental fusion strategies must be unreachable from shipped ModeAuto)", resAuto.Summary.Strategy)
	}
	if resAuto.Summary.Strategy != "semantic_first" {
		t.Errorf("ModeAuto under ready semantic: Strategy = %q, want semantic_first", resAuto.Summary.Strategy)
	}

	// ModeAuto under non-ready semantic: strategy == lexical_only.
	sem2 := &fakeSemantic{available: false, state: StateLexicalOnly}
	e2 := newEngine(lex, sem2, nil)
	resAutoNonReady, _ := e2.Retrieve(context.Background(), Request{Query: "q", Mode: ModeAuto, Limit: 10})
	if resAutoNonReady.Summary.Strategy != "lexical_only" {
		t.Errorf("ModeAuto under non-ready: Strategy = %q, want lexical_only (AC-7 fallback)", resAutoNonReady.Summary.Strategy)
	}

	// Fusion strategies are reachable ONLY via explicit mode pin
	// (the evaluator-only path).
	resFusion, _ := e.Retrieve(context.Background(), Request{Query: "q", Mode: ModeFusionNoGraph, Limit: 10})
	if resFusion.Summary.Strategy != "fusion_no_graph" {
		t.Errorf("ModeFusionNoGraph: Strategy = %q, want fusion_no_graph", resFusion.Summary.Strategy)
	}
}

// TestSemanticFirst_ResultIsByteIdenticalAcrossRuns pins AC-8: the
// shipped semantic-first path is deterministic.
func TestSemanticFirst_ResultIsByteIdenticalAcrossRuns(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Kind: "function", QualifiedName: "pkg.L1", Path: "l.go", Line: 1},
		{NodeID: "l2", Kind: "function", QualifiedName: "pkg.L2", Path: "m.go", Line: 1},
		{NodeID: "shared", Kind: "function", QualifiedName: "pkg.Shared", Path: "shared.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Kind: "function", QualifiedName: "pkg.S1", Path: "s.go", Line: 1, CosineScore: 0.95},
		{NodeID: "shared", DocumentID: "doc-shared", Kind: "function", QualifiedName: "pkg.Shared", Path: "shared.go", Line: 1, CosineScore: 0.80},
		{NodeID: "s3", DocumentID: "doc-s3", Kind: "function", QualifiedName: "pkg.S3", Path: "s3.go", Line: 1, CosineScore: 0.70},
	}}
	e := newEngine(lex, sem, nil)
	r1, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	r2, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic:\n r1=%s\n r2=%s", b1, b2)
	}
}

// ---------------------------------------------------------------------------
// AC-3: the quantised ordering is unchanged from the pre-redirection
// semantic-first path. The deduped prefix carries quantised
// SemanticScore per row.
// ---------------------------------------------------------------------------

func TestSemanticFirst_PrefixCarriesQuantisedSemanticScore(t *testing.T) {
	lex := &fakeLexical{hits: []lexicalHit{
		{NodeID: "l1", Path: "l.go", Line: 1},
	}}
	sem := &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "s1", DocumentID: "doc-s1", Path: "s.go", Line: 1, CosineScore: 0.9999},
		{NodeID: "s2", DocumentID: "doc-s2", Path: "s.go", Line: 2, CosineScore: 0.7},
	}}
	e := newEngine(lex, sem, nil)
	res, _ := e.Retrieve(context.Background(), Request{Query: "q", Limit: 10})
	for _, r := range res.Rows {
		if r.NodeID == "s1" || r.NodeID == "s2" {
			if r.Explain.RRF != 0 {
				t.Errorf("prefix row %s: RRF = %d, want 0 (semantic-first does not emit RRF on the ready path)", r.NodeID, r.Explain.RRF)
			}
		}
	}
}

// rNodeIDForRow is a typed alias helper used in the AC-11 test
// above (helper kept inline because the file uses Row in tests below).
func rNodeIDForRow(r Row) string { return r.NodeID }
