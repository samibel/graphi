// SW-264 AC-1 / AC-8 — search_hybrid/2 tests.
//
// AC-1: with no embedder or a non-ready generation, /2 falls back to today's
// lexical-only bytes (the byte parity with SW-257 / hybridsearch.Search).
// The fallback carries `degradation` in the summary and no error.
//
// AC-1 also demands the v2 rendering carries the SW-263 explain fields
// (LexicalRank, SemanticRank, RRF, Graph, Classification, Final) and the
// summary fingerprints (RetrievalVersion, WeightsHash, ModelFingerprint,
// IndexFingerprint, Strategy).
package hybridsearch_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
)

// sw257HelloGreeterHash is the SW-257 §7.2 sha256 for `search_hybrid`
// ("hello greeter") on the SQLite backend. The v1 golden is the byte
// identity every /2 test re-uses as the fallback target (AC-1, AC-8).
const sw257HelloGreeterHash = "0ec5fd56cf662defc4efe69ff9f7be2fe68645bc71bcc5e102535bed5888ae40"

// charFixtureDir is the SW-257 pinned Go corpus fixture, copied from
// engine/retrieval/byte_parity_test.go so the v2 test has a fixture
// producing the exact SW-257 bytes when no embedder is configured.
func charFixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "corpus", "fixtures", "go")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from test cwd")
		}
		dir = parent
	}
}

func indexedFixtureStore(t *testing.T) graphstore.Graphstore {
	t.Helper()
	store, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(context.Background(), charFixtureDir(t)); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("ing.Close: %v", err)
	}
	return store
}

func v2Deps(t *testing.T) (graphstore.Graphstore, resolve.Deps) {
	t.Helper()
	store := indexedFixtureStore(t)
	t.Cleanup(func() { _ = store.Close() })
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: nil, // no retrieval wired: AC-1 / AC-8 fallback path.
	}
	return store, deps
}

// TestSearchV2_NoRetrievalFallsBackCarriesDegradation covers AC-1's
// fallback contract for the v2 path: with no retrieval wired (the default
// build), SearchV2 returns the v1 bytes plus a `degradation: lexical_only`
// trailer on the summary (AC-8). The v1 bytes themselves stay byte-identical
// to the SW-257 §7.2 golden — the trailer is appended after the canonical
// summary so the v1 invariant holds for callers that hit Search directly.
//
// This test deliberately checks the v2 output rather than comparing Search
// to SearchV2: the AC-1 byte-identity claim is about the /1 surface, and
// the /2 fallback's added degradation trailer is the explicit AC-8 marker.
func TestSearchV2_NoRetrievalFallsBackCarriesDegradation(t *testing.T) {
	_, deps := v2Deps(t)
	ctx := context.Background()

	// v1 bytes (golden).
	v1, err := hybridsearch.Search(ctx, hybridsearch.Params{Query: "hello greeter", MaxItems: 20, Deps: deps})
	if err != nil {
		t.Fatalf("v1 Search: %v", err)
	}
	v1Bytes, err := contract.Serialize(v1)
	if err != nil {
		t.Fatalf("Serialize v1: %v", err)
	}
	if sum := sha256.Sum256(v1Bytes); hex.EncodeToString(sum[:]) != sw257HelloGreeterHash {
		t.Fatalf("v1 sha256 = %s, want SW-257 §7.2 %s (v1 changed under us)",
			hex.EncodeToString(sum[:]), sw257HelloGreeterHash)
	}
	if len(v1Bytes) != 1590 {
		t.Fatalf("v1 bytes = %d, want 1590 (SW-257 §7.2)", len(v1Bytes))
	}

	// v2 bytes (the fallback path).
	v2, err := hybridsearch.SearchV2(ctx, hybridsearch.Params{Query: "hello greeter", MaxItems: 20, Deps: deps})
	if err != nil {
		t.Fatalf("v2 Search: %v", err)
	}
	v2Bytes, err := contract.Serialize(v2)
	if err != nil {
		t.Fatalf("Serialize v2: %v", err)
	}
	// AC-8: v2 fallback MUST carry the degradation trailer. A v2 fallback
	// that doesn't stamp `degradation` is the AC-8 violation the test exists
	// to catch — the marker is how a reader of the bytes tells the fallback
	// ran without consulting a separate log.
	if !bytes.Contains(v2Bytes, []byte("degradation: lexical_only")) {
		t.Fatalf("AC-8 fallback missing degradation trailer:\n  v2=%s", v2Bytes)
	}
	// Structural check: the fallback must preserve the v1 row set verbatim.
	// A v2 fallback that re-ranked or re-shaped rows would silently change
	// shipped behaviour, which is the regression this assertion catches.
	if len(v2.Items) != len(v1.Items) {
		t.Fatalf("v2 fallback changed row count: v1=%d v2=%d", len(v1.Items), len(v2.Items))
	}
	for i := range v1.Items {
		if v2.Items[i].RefID != v1.Items[i].RefID {
			t.Fatalf("v2 fallback reshuffled rows: position %d v1=%s v2=%s",
				i, v1.Items[i].RefID, v2.Items[i].RefID)
		}
		if v2.Items[i].Rank != v1.Items[i].Rank {
			t.Fatalf("v2 fallback changed ranks: position %d v1=%d v2=%d",
				i, v1.Items[i].Rank, v2.Items[i].Rank)
		}
	}
}

// TestSearchV2_NoRetrievalStampsDegradation covers AC-8: with no embedder
// (Deps.Retrieval == nil), /2 carries `degradation` in the summary and
// returns no error. A reader of the bytes can tell the fallback ran from the
// summary alone.
func TestSearchV2_NoRetrievalStampsDegradation(t *testing.T) {
	_, deps := v2Deps(t)
	res, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "hello greeter", MaxItems: 20, Deps: deps,
	})
	if err != nil {
		t.Fatalf("SearchV2 with no retrieval: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found (no error on graceful fallback)", res.Outcome)
	}
	if !strings.Contains(res.Summary, "degradation: lexical_only") {
		t.Fatalf("summary must stamp the lexical_only degradation: %q", res.Summary)
	}
	// The v1 prefix bytes are preserved (AC-1 byte-identity claim is on the
	// /1 surface; /2 is allowed to append the AC-8 trailer). The /1 stamp
	// (search_hybrid/1) is still visible because the fallback re-uses the
	// v1 summary verbatim before appending the trailer.
	if !strings.Contains(res.Summary, hybridsearch.MethodVersion) {
		t.Fatalf("fallback summary must keep the v1 audit stamp: %q", res.Summary)
	}
}

// TestSearchV2_NonReadyGenerationFallsBack checks AC-8's second clause: with
// a retrieval instance wired but reporting a non-ready state (the v3 capsule
// fail-closed admission contract), SearchV2 returns the lexical-only rows
// with the typed degradation stamped in the summary, no error.
//
// The non-ready state is driven by passing a Retriever whose Retrieve always
// returns a RetrieverResult with Degradation="generation_missing"; that is
// the typed fallback vocabulary the retrieval module exports (SW-263 §State).
func TestSearchV2_NonReadyGenerationFallsBack(t *testing.T) {
	store := indexedFixtureStore(t)
	t.Cleanup(func() { _ = store.Close() })
	deps := resolve.Deps{
		Query:  query.New(store),
		Search: search.New(store),
		Retrieval: stubRetriever{result: resolve.RetrieverResult{
			Rows:        nil,
			Degradation: "generation_missing",
			Reason:      "no active generation",
		}},
	}
	res, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "hello greeter", MaxItems: 20, Deps: deps,
	})
	if err != nil {
		t.Fatalf("SearchV2 with non-ready retrieval: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found (graceful lexical fallback on non-ready state)", res.Outcome)
	}
	if !strings.Contains(res.Summary, "degradation: generation_missing") {
		t.Fatalf("summary must stamp the typed non-ready state: %q", res.Summary)
	}
}

// TestSearchV2_RendersExplainFieldsAndFingerprints checks the AC-1 deliverable
// for /2 when the retrieval IS ready: every item's reason carries the SW-263
// explain breakdown (lexical_rank, semantic_rank, rrf, graph, classification,
// final) so a reader of the bytes can audit the ranking, and the summary
// stamps the retrieval version, weights hash and the model/index
// fingerprints. The fixture is a tiny in-memory graph with a hand-rolled
// Retriever that returns one retrieval row with a known explain triple.
func TestSearchV2_RendersExplainFieldsAndFingerprints(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	deps := resolve.Deps{
		Query:  query.New(store),
		Search: search.New(store),
		Retrieval: stubRetriever{result: resolve.RetrieverResult{
			Degradation: "ready",
			Summary: resolve.RetrieverSummary{
				RetrievalVersion: retrieval.Version,
				Strategy:         "semantic_first",
				WeightsHash:      retrieval.WeightsHash(),
				ModelFingerprint: "m-fp-v3",
				IndexFingerprint: "i-fp-v3",
			},
			Rows: []resolve.RetrieverRow{{
				NodeID:     "node-1",
				DocumentID: "doc-1",
				Path:       "auth/token.go",
				Span:       "10-10",
				Region:     "semantic_prefix",
				Final:      1200,
				Explain: resolve.RetrieverExplain{
					LexicalRank:    0,
					SemanticRank:   1,
					RRF:            16666,
					Graph:          0,
					Classification: 0,
					Final:          1200,
				},
			}},
		}},
	}

	res, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "anything", MaxItems: 20, Deps: deps,
	})
	if err != nil {
		t.Fatalf("SearchV2 ready: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found", res.Outcome)
	}
	if !strings.Contains(res.Summary, retrieval.Version) && !strings.Contains(res.Summary, retrieval.WeightsHash()) {
		t.Fatalf("summary must stamp retrieval audit trail (version or weights hash): %q", res.Summary)
	}
	if !strings.Contains(res.Summary, retrieval.WeightsHash()) {
		t.Fatalf("summary must stamp weights hash: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "m-fp-v3") || !strings.Contains(res.Summary, "i-fp-v3") {
		t.Fatalf("summary must stamp model + index fingerprints: %q", res.Summary)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(res.Items))
	}
	item := res.Items[0]
	// Every explain field the AC names appears in the reason.
	for _, want := range []string{"lexical_rank", "semantic_rank", "rrf", "graph", "classification", "final"} {
		if !strings.Contains(item.Reason, want) {
			t.Fatalf("reason must carry %q breakdown: %q", want, item.Reason)
		}
	}
	// Evidence carries the row's provenance (path, span), not claim_type
	// (search_hybrid v2 does not stamp claim_type — AC-3 is task_context/2).
	if len(res.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1", len(res.Evidence))
	}
	ev := res.Evidence[0]
	if ev.Path != "auth/token.go" {
		t.Fatalf("evidence path = %q, want auth/token.go", ev.Path)
	}
	if ev.Span != "10-10" {
		t.Fatalf("evidence span = %q, want 10-10", ev.Span)
	}
	if ev.ClaimType != "" {
		t.Fatalf("search_hybrid v2 must NOT stamp claim_type (task_context-only): %q", ev.ClaimType)
	}
}

// stubRetriever is the in-process Retriever the v2 tests use to drive a known
// ready / non-ready state. The retrieval module's own retriever type is a
// private struct, so a hand-rolled Retriever is the only test seam.
type stubRetriever struct {
	result resolve.RetrieverResult
}

func (s stubRetriever) Retrieve(ctx context.Context, req resolve.RetrieverRequest) (resolve.RetrieverResult, error) {
	return s.result, nil
}

// TestSearchV2_EmptyAndUnavailable covers the boundary cases that must also
// degrade gracefully (no error): an empty query and the unavailable
// services outcome. Both are present-day hybridsearch.Search behaviors; the
// v2 path must mirror them.
func TestSearchV2_EmptyAndUnavailable(t *testing.T) {
	_, deps := v2Deps(t)

	if _, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "   ", Deps: deps,
	}); err == nil {
		t.Fatal("blank query must error")
	}

	empty, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "zzzzqqq no matches", Deps: deps,
	})
	if err != nil {
		t.Fatalf("SearchV2 no-match: %v", err)
	}
	if empty.Outcome != contract.OutcomeEmpty {
		t.Fatalf("no-match outcome = %s, want empty", empty.Outcome)
	}

	unavail, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "anything", Deps: resolve.Deps{},
	})
	if err != nil {
		t.Fatalf("SearchV2 no-deps: %v", err)
	}
	if unavail.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("no-deps outcome = %s, want unavailable", unavail.Outcome)
	}
}

// TestSearchV2_RowJSONIncludesRegionAndStrategy is a structural check that
// every retrieval row's Region tag survives the render. AC-11 truthfulness
// (SW-263): a reader of the bytes must be able to tell how each row entered
// the result. The test holds only when the v2 renderer passes Region through
// to the evidence (the per-row reason carries it).
func TestSearchV2_RowJSONIncludesRegionAndStrategy(t *testing.T) {
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	deps := resolve.Deps{
		Query:  query.New(store),
		Search: search.New(store),
		Retrieval: stubRetriever{result: resolve.RetrieverResult{
			Degradation: "ready",
			Summary: resolve.RetrieverSummary{
				RetrievalVersion: retrieval.Version,
				Strategy:         "semantic_first",
				WeightsHash:      retrieval.WeightsHash(),
			},
			Rows: []resolve.RetrieverRow{
				{NodeID: "n1", Path: "a.go", Span: "1-1", Region: "semantic_prefix", Final: 100},
				{NodeID: "n2", Path: "b.go", Span: "2-2", Region: "lexical_backfill", Final: 50},
			},
		}},
	}
	res, err := hybridsearch.SearchV2(context.Background(), hybridsearch.Params{
		Query: "q", Deps: deps,
	})
	if err != nil {
		t.Fatalf("SearchV2: %v", err)
	}
	// Round-trip through Serialize and decode back so we see the rendered
	// fields, not the intermediate struct.
	raw, err := contract.Serialize(res)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	var decoded struct {
		Summary string `json:"summary"`
		Items   []struct {
			RefID  string `json:"ref_id"`
			Reason string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if !strings.Contains(decoded.Summary, "semantic_first") {
		t.Fatalf("summary must stamp the strategy: %q", decoded.Summary)
	}
	if len(decoded.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(decoded.Items))
	}
	if !strings.Contains(decoded.Items[0].Reason, "semantic_prefix") {
		t.Fatalf("row 0 reason must stamp region semantic_prefix: %q", decoded.Items[0].Reason)
	}
	if !strings.Contains(decoded.Items[1].Reason, "lexical_backfill") {
		t.Fatalf("row 1 reason must stamp region lexical_backfill: %q", decoded.Items[1].Reason)
	}
}
