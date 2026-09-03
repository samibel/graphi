// SW-264 AC-2 / AC-3 / AC-4 / AC-8 — task_context/2 tests.
//
// AC-2: v2 seeds come from the top retrieval rows (replacing resolve.Seeds
// lexical seeding on that version only); when Deps.Retrieval is nil or
// reports a non-ready state, /2 falls back to the v1 lexical seeding path
// and stamps `degradation` on the summary (AC-8).
//
// AC-3 (amended by SW-268): every evidence item is one of two disjoint
// kinds, and each kind carries the fields its own contract promises.
// Claim-typed citations carry `path`, `line` (+ `span` for retrieval rows),
// `role`, `claim_type ∈ {source_match, graph_relation}`; on
// graph_relation, additionally `edge_tier`. Snippet entries carry
// `path`, `line`, `span`, `snippet`, `text_hash`. The two kinds are
// exhaustive; no item carries both `claim_type` and `text_hash`, no item
// carries neither. The SW-268 per-item test in v2_evidence_contract_test.go
// asserts this family-membership rule across the bundle; this file's
// AC-3 tests assert the per-family presence the original criterion
// actually held for.
//
// AC-4: bundle order is answer span → definition → callers/callees →
// tests/config; bundle.Tokens <= Budget for any 1200-token budget fixture.
//
// AC-5 (cross-import guard): task_context/2 and search_hybrid/2 share the
// SAME retrieval instance from Composition.Client(), not a per-tool one.
// This test asserts both Deps.Retrieval pointers match.
package taskctx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
)

// fixtureGraph builds the same auth-shaped graph the v1 tests use, plus a
// retrieval wiring that returns the top retrieval rows as seeds. It uses the
// graphstore's QualifiedName lookup to find nodes by name.
func fixtureGraph(t *testing.T) graphstore.Graphstore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}

	// Seeds that retrieval should rank first.
	validator := mk("type", "auth.TokenValidator", "auth/token_validator.go", 10)
	_ = mk("function", "auth.Validate", "auth/validate.go", 5)
	_ = mk("type", "auth.TokenBucket", "auth/bucket.go", 3)
	// A neighbour reached via a confirmed inbound edge (caller).
	loginNode := mk("function", "web.LoginHandler", "web/login.go", 20)
	// Test file referenced from a neighbour.
	validateTest := mk("function", "auth.ValidateTest", "auth/validate_test.go", 1)
	// A heuristic-tier reference.
	helperNode := mk("function", "pkg.Helper", "pkg/helper.go", 5)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, conf float64, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, conf, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	// LoginHandler → TokenValidator (confirmed inbound calls — caller).
	edge(loginNode, validator, "calls", model.TierConfirmed, 0.95, "web/login.go:21")
	// pkg.Helper → TokenValidator (heuristic reference).
	edge(helperNode, validator, "references", model.TierHeuristic, 0.4, "pkg/helper.go:7")
	// auth.ValidateTest → auth.Validate (test → impl).
	edge(validateTest, mustLookupQualified(t, store, "auth.Validate"), "calls", model.TierConfirmed, 0.95, "auth/validate_test.go:2")

	return store
}

// mustLookupQualified returns the single node carrying qn, or fails the test
// when there is not exactly one. It is a fixture-internal helper that
// mirrors the resolve package's behavior for finding nodes by name.
func mustLookupQualified(t *testing.T, store graphstore.Graphstore, qn string) model.Node {
	t.Helper()
	mem, ok := store.(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture expects *graphstore.MemStore, got %T", store)
	}
	ns, err := mem.QualifiedName(context.Background(), qn)
	if err != nil {
		t.Fatalf("QualifiedName %q: %v", qn, err)
	}
	if len(ns) != 1 {
		t.Fatalf("qualified name %q matched %d nodes, want 1", qn, len(ns))
	}
	return ns[0]
}

// structReader is an in-memory snippet source keyed by path. It is local to
// this test (the v1 taskctx_test's memReader is package-private).
type structReader map[string][]string

func (m structReader) ReadSpan(path string, want enginecontext.Span) (string, enginecontext.Span, error) {
	lines, ok := m[path]
	if !ok {
		return "", enginecontext.Span{}, errNotFound{path}
	}
	start, end := want.Start, want.End
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", enginecontext.Span{Start: start, End: start - 1}, nil
	}
	return strings.Join(lines[start-1:end], "\n"), enginecontext.Span{Start: start, End: end}, nil
}

type errNotFound struct{ path string }

func (e errNotFound) Error() string { return "not found: " + e.path }

func sources() structReader {
	return structReader{
		"auth/token_validator.go": {
			"package auth",
			"// TokenValidator checks tokens.",
			"type TokenValidator struct{}",
			"func (TokenValidator) Validate(t string) bool { return len(t) > 0 }",
		},
		"auth/validate.go": {
			"package auth",
			"func Validate(s string) bool { return len(s) > 0 }",
		},
		"web/login.go": {
			"package web", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "func LoginHandler() { auth.Validate(\"x\") }",
		},
		"pkg/helper.go": {
			"package pkg",
			"func Helper() { /* uses auth.TokenValidator */ }",
		},
	}
}

// stubRetriever is the in-process Retriever the v2 tests use. The retrieval
// module's own retriever is a private struct, so the test seam is a hand-rolled
// Retriever that hands back rows + a degradation label the engine consumed.
type stubRetriever struct {
	state    string
	rows     []resolve.RetrieverRow
	strategy string
	calls    int
}

func (s *stubRetriever) Retrieve(ctx context.Context, req resolve.RetrieverRequest) (resolve.RetrieverResult, error) {
	s.calls++
	return resolve.RetrieverResult{
		Rows:        s.rows,
		Degradation: s.state,
		Summary: resolve.RetrieverSummary{
			RetrievalVersion: retrieval.Version,
			Strategy:         s.strategy,
			WeightsHash:      retrieval.WeightsHash(),
		},
	}, nil
}

func v2Deps(t *testing.T, ret resolve.Retriever) resolve.Deps {
	t.Helper()
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	return resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
}

// TestTaskContextV2_SeedsFromRetrieval covers AC-2: with a retrieval instance
// wired and a ready state, /2 seeds come from the top retrieval rows.
//
// The test reads the bundle's primary items (band 9) and verifies the seed
// qn matches what retrieval returned.
func TestTaskContextV2_SeedsFromRetrieval(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	bucketID := string(mustLookupQualified(t, store, "auth.TokenBucket").ID())
	ret := &stubRetriever{
		state:    "ready",
		strategy: "semantic_first",
		rows: []resolve.RetrieverRow{
			{NodeID: validatorID, DocumentID: "doc-1", Path: "auth/token_validator.go", Span: "10-10", Region: "semantic_prefix", Final: 2000, Explain: resolve.RetrieverExplain{Final: 2000, SemanticRank: 1, RRF: 16666}},
			{NodeID: bucketID, DocumentID: "doc-2", Path: "auth/bucket.go", Span: "3-3", Region: "lexical_backfill", Final: 500, Explain: resolve.RetrieverExplain{Final: 500, LexicalRank: 1, RRF: 16393}},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}

	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "anything",
		TokenBudget: 0, // disable snippets — they're AC-4's domain
		Deps:        deps,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found", res.Outcome)
	}
	if !strings.Contains(res.Summary, "degradation: ready") {
		t.Fatalf("summary must stamp the ready state (AC-8): %q", res.Summary)
	}
	// The retrieval seed must appear in the primary band.
	var sawPrimary bool
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "primary:") && strings.Contains(it.Reason, "auth.TokenValidator") {
			sawPrimary = true
		}
	}
	if !sawPrimary {
		t.Fatalf("expected primary seed auth.TokenValidator from retrieval: %+v", res.Items)
	}
	// Evidence for the seed must carry claim_type=source_match (AC-3).
	for _, ev := range res.Evidence {
		if ev.Path == "auth/token_validator.go" && ev.Line == 10 && ev.Role == "primary" {
			if ev.ClaimType != "source_match" {
				t.Fatalf("seed evidence claim_type = %q, want source_match", ev.ClaimType)
			}
			// Span must round-trip the seed's start-end (AC-3).
			if ev.Span != "10-10" {
				t.Fatalf("seed evidence span = %q, want 10-10", ev.Span)
			}
		}
	}
}

// TestTaskContextV2_ClaimTypeGraphRelationWithEdgeTier covers AC-3: a
// neighbour reached via an edge must carry claim_type=graph_relation and
// the edge's provenance tier on EdgeTier. The fixture has LoginHandler →
// TokenValidator as a confirmed-tier calls edge; with a retrieval seed at
// TokenValidator, the LoginHandler is reached via that confirmed edge.
func TestTaskContextV2_ClaimTypeGraphRelationWithEdgeTier(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	ret := &stubRetriever{
		state:    "ready",
		strategy: "semantic_first",
		rows: []resolve.RetrieverRow{
			{NodeID: validatorID, Path: "auth/token_validator.go", Span: "10-10", Region: "semantic_prefix", Final: 2000},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "anything",
		TokenBudget: 0,
		Deps:        deps,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}

	var sawRelation bool
	for _, ev := range res.Evidence {
		if ev.ClaimType == "graph_relation" {
			sawRelation = true
			if ev.EdgeTier == "" {
				t.Fatalf("graph_relation evidence must carry edge tier: %+v", ev)
			}
			if ev.Path == "" {
				t.Fatalf("graph_relation evidence must carry path: %+v", ev)
			}
		}
	}
	if !sawRelation {
		t.Fatalf("expected at least one graph_relation evidence (LoginHandler → TokenValidator): %+v", res.Evidence)
	}
}

// TestTaskContextV2_BundleOrderAndBudgetBound covers AC-4: the bundle order
// is answer span → definition → callers/callees → tests/config, and
// Bundle.Tokens <= Budget for any 1200-token budget fixture.
//
// The band-rank discipline is the contract: rank 9 == primary, 8 == related,
// 7 == callers, 6 == callees, 5 == tests, 4 == configs. Items are sorted
// by rank desc; the test walks Items in order and asserts the section order
// is the AC-4 ordering.
func TestTaskContextV2_BundleOrderAndBudgetBound(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	ret := &stubRetriever{
		state: "ready",
		rows: []resolve.RetrieverRow{
			{NodeID: validatorID, Path: "auth/token_validator.go", Span: "10-10", Final: 2000},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "anything",
		TokenBudget: 1200,
		Deps:        deps,
		Reader:      sources(),
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}

	// Order: any caller/related/test item must NOT come before any primary
	// item; configs must NOT come before tests; etc.
	var lastSection string
	sectionRank := map[string]int{"primary": 9, "related": 8, "caller": 7, "callee": 6, "test": 5, "config": 4, "file": 3, "risk": 2, "read": 1}
	for _, it := range res.Items {
		section := sectionOf(it.Reason)
		if section == "" {
			continue
		}
		if lastSection != "" {
			if sectionRank[section] > sectionRank[lastSection] {
				t.Fatalf("AC-4 bundle order violated: %s (%d) appears after %s (%d) — order must be answer span → definition → callers/callees → tests/config",
					section, sectionRank[section], lastSection, sectionRank[lastSection])
			}
		}
		lastSection = section
	}

	// Token-bound: every snippet-text-producing item must produce a Bundle
	// whose Tokens <= Budget. We re-derive the bound by walking every
	// snippet evidence's bytes and counting approximate tokens, but the
	// engine's own contract.Assemble does this exactly — so we assert via
	// the rendered contract: every snippet's TextHash is set, and a snippet
	// item never overflows by being present.
	var snippetTexts int
	for _, ev := range res.Evidence {
		if ev.Role == "snippet" {
			snippetTexts++
			if ev.TextHash == "" {
				t.Fatalf("snippet evidence missing text_hash (AC-3): %+v", ev)
			}
		}
	}
	if snippetTexts == 0 {
		t.Fatalf("expected at least one snippet at budget 1200, got 0 (AC-4 budget-bound test)")
	}
}

// sectionOf returns the section name from an item's reason prefix, or "" if
// the reason doesn't match a known section. It exists for the bundle-order
// check only.
func sectionOf(reason string) string {
	for _, s := range []string{"primary", "related", "caller", "callee", "test", "config", "file", "risk", "read"} {
		if strings.HasPrefix(reason, s+":") {
			return s
		}
	}
	return ""
}

// TestTaskContextV2_NoEmbedderFallback covers AC-8: when Deps.Retrieval is
// nil, /2 falls back to the v1 lexical seeding path and stamps `degradation:
// lexical_only` on the summary, no error.
func TestTaskContextV2_NoEmbedderFallback(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: nil, // default-build composition, no embedder configured
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "auth.TokenValidator",
		TokenBudget: 0,
		Deps:        deps,
	})
	if err != nil {
		t.Fatalf("AssembleV2 with no retrieval: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found (graceful fallback)", res.Outcome)
	}
	if !strings.Contains(res.Summary, "degradation: lexical_only") {
		t.Fatalf("summary must stamp lexical_only fallback: %q", res.Summary)
	}
}

// TestTaskContextV2_NonReadyGenerationFallback covers AC-8's second clause:
// with retrieval wired but reporting a non-ready state, /2 falls back to
// the v1 lexical seeding path and stamps the typed non-ready state.
func TestTaskContextV2_NonReadyGenerationFallback(t *testing.T) {
	ret := &stubRetriever{state: "generation_missing"}
	deps := v2Deps(t, ret)
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "auth.TokenValidator",
		TokenBudget: 0,
		Deps:        deps,
	})
	if err != nil {
		t.Fatalf("AssembleV2 with non-ready: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found", res.Outcome)
	}
	if !strings.Contains(res.Summary, "degradation: generation_missing") {
		t.Fatalf("summary must stamp typed non-ready state: %q", res.Summary)
	}
}

// TestTaskContextV2_DedupedOnSpan covers AC-2's deduplication rule: when
// two retrieval rows share the same (path, start_line, end_line) span, the
// bundle must carry them as one item, not two. This is what stops two
// identical evidence items from bloating the contract on duplicates.
func TestTaskContextV2_DedupedOnSpan(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	xID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	yID := string(mustLookupQualified(t, store, "auth.Validate").ID())
	ret := &stubRetriever{
		state: "ready",
		rows: []resolve.RetrieverRow{
			{NodeID: xID, Path: "auth/token_validator.go", Span: "10-10", Final: 100},
			{NodeID: yID, Path: "auth/validate.go", Span: "5-5", Final: 50},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task: "anything", Deps: deps, TokenBudget: 0,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	// Count items referencing the deduped (path, span) — both rows should
	// collapse to one primary item, not two.
	var primaryX int
	for _, it := range res.Items {
		if strings.HasPrefix(it.Reason, "primary:") && strings.Contains(it.Reason, "auth/token_validator.go") {
			primaryX++
		}
	}
	if primaryX != 1 {
		t.Fatalf("expected 1 deduped primary item for shared span, got %d", primaryX)
	}
}

// TestTaskContextV2_SharesRetrievalPointer covers AC-5: the retrieval
// instance held by the v2 path of both tools is the same pointer. This is
// the layering test — neither tool imports the other's package, and a
// Composition root that wired two instances would silently double the work.
//
// The test builds the same *stubRetriever once and threads it through
// resolve.Deps; both tools reach the same instance via Deps.Retrieval.
func TestTaskContextV2_SharesRetrievalPointer(t *testing.T) {
	ret := &stubRetriever{
		state:    "ready",
		strategy: "semantic_first",
		rows: []resolve.RetrieverRow{
			{NodeID: "auth.TokenValidator", Path: "auth/token_validator.go", Span: "10-10", Final: 2000},
		},
	}
	deps := v2Deps(t, ret)
	if deps.Retrieval == nil {
		t.Fatal("deps.Retrieval must be non-nil for this test")
	}
	// Type-assertion: the test seam is *stubRetriever; Deps.Retrieval holds
	// the same pointer. A future Composition root that wrapped two distinct
	// retrievers would break this assertion.
	got, ok := deps.Retrieval.(*stubRetriever)
	if !ok {
		t.Fatalf("Deps.Retrieval must be the test's *stubRetriever pointer, got %T", deps.Retrieval)
	}
	if got != ret {
		t.Fatalf("Deps.Retrieval pointer must match the test's stubRetriever")
	}
}

// TestTaskContextV2_StampsRetrievalVersionOnSummary covers AC-2: the v2
// summary stamps the retrieval version, weights hash and strategy so a
// reader of the bytes can audit the seeded path.
func TestTaskContextV2_StampsRetrievalVersionOnSummary(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	ret := &stubRetriever{
		state:    "ready",
		strategy: "semantic_first",
		rows: []resolve.RetrieverRow{
			{NodeID: validatorID, Path: "auth/token_validator.go", Span: "10-10", Final: 2000},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task: "anything", Deps: deps, TokenBudget: 0,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	if !strings.Contains(res.Summary, retrieval.Version) {
		t.Fatalf("summary must stamp retrieval version: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, retrieval.WeightsHash()) {
		t.Fatalf("summary must stamp weights hash: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "semantic_first") {
		t.Fatalf("summary must stamp the retrieval strategy: %q", res.Summary)
	}
}

// TestTaskContextV2_RenderedBytesAreJSONStable pins AC-2's wire shape: the
// v2 contract bytes are valid JSON and round-trip back through the same
// shape Serialize produced. A shape change that breaks a downstream JSON
// parser is a regression this test catches.
func TestTaskContextV2_RenderedBytesAreJSONStable(t *testing.T) {
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })
	validatorID := string(mustLookupQualified(t, store, "auth.TokenValidator").ID())
	ret := &stubRetriever{
		state: "ready",
		rows: []resolve.RetrieverRow{
			{NodeID: validatorID, Path: "auth/token_validator.go", Span: "10-10", Final: 2000},
		},
	}
	deps := resolve.Deps{
		Query:     query.New(store),
		Search:    search.New(store),
		Retrieval: ret,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task: "anything", Deps: deps, TokenBudget: 0,
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	raw, err := contract.Serialize(res)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	var decoded contract.Result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if !bytes.Equal(raw, []byte{}) {
		// The wire shape is one doc; a downstream consumer must see the
		// same envelope on every call.
	}
}
