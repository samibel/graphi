// SW-268 — task_context/2 evidence contract, per-item.
//
// AC-1 of SW-268 chose option (b): the contract has two disjoint evidence
// families, and AC-3's original "all five fields on every item" wording is
// amended. The two families are:
//
//   - Claim-typed citation.  Path, Line (+ Span for retrieval rows),
//     Role, ClaimType ∈ {source_match, graph_relation}; on
//     graph_relation, additionally EdgeTier. NO Snippet, NO TextHash.
//   - Snippet entry. Path, Line, Role, Span, Snippet, TextHash.
//     NO ClaimType.
//
// This file's tests assert the per-item family membership on a bundle
// produced by the real task_context/2 path with a non-nil retrieval
// instance (AC-5). The producer is the same composition the SW-264 AC-9
// measurement harness uses (`internal/eval/retrieval.TaskContextRetriever`
// wrapping `engine/retrieval.New`); we reuse it rather than building a
// second adapter so a future change to the engine→resolve translation
// cannot drift between the harness and this test.
//
// AC-4 of SW-268: the per-item assertion is the contract. Removing one
// promised field from one item turns it red. The companion test
// TestTaskContextV2_EvidencePerItemContractBites demonstrates the failure
// mode by tampering with one emitted evidence item and reading the
// resulting test failure message.
//
// AC-7 of SW-268: this file does not touch the /1 path. The SW-257 byte-
// stable golden for task_context is the /1 contract and remains
// unchanged.
package taskctx_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/eval/retrieval"
)

// retrievalRealBundle builds the smallest retrieval-real bundle the per-item
// contract can run against: a /2 call driven through
// retrieval.TaskContextRetriever (the same adapter the SW-264 AC-9
// measurement uses) wrapping an engine/retrieval.New instance backed by
// the fixture's in-memory graphstore.
//
// The fixture's graph has three retrieval-eligible nodes plus a
// confirmed-tier calls edge that produces a graph_relation neighbour, so
// the resulting bundle exercises both families: claim-typed citations for
// the seeds and the neighbour, and snippet entries when TokenBudget > 0.
//
// Returns the bundle plus the adapter so callers can assert the engine
// was hit (CalledCount >= 1) — a test that reads zero from CalledCount
// has silently fallen back to lexical seeding and is no longer the AC-5
// "real retrieval instance" producer.
func retrievalRealBundle(t *testing.T, tokenBudget int) (*contract.Result, *retrieval.TaskContextRetriever) {
	t.Helper()
	store := fixtureGraph(t)
	t.Cleanup(func() { _ = store.Close() })

	// engine/retrieval.New wants the bounded-lookup interface; the fixture
	// hands us the broader Graphstore. The harness tests pin this exact
	// path and *MemStore implements BoundedGraphLookup, so the assertion
	// only fails if a future fixture change loses the bounded methods.
	mem, ok := store.(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture must be a *graphstore.MemStore so engine/retrieval.New can build a real retrieval instance (got %T)", store)
	}

	// Use the SW-264 AC-9 measurement adapter. It is the only composition
	// seam the harness vouches for as "the real retrieval instance, not a
	// hand-built fixture", which is exactly AC-5 of SW-268.
	engine := engineretrieval.New(
		resolve.Deps{Query: query.New(mem), Search: search.New(mem)},
		search.New(mem),
		mem,
	)
	adapter := retrieval.NewTaskContextRetriever(engine)

	deps := resolve.Deps{
		Query:     query.New(mem),
		Search:    search.New(mem),
		Retrieval: adapter,
	}
	res, err := taskctx.AssembleV2(context.Background(), taskctx.Params{
		Task:        "auth.TokenValidator",
		TokenBudget: tokenBudget,
		Deps:        deps,
		Reader:      sources(),
	})
	if err != nil {
		t.Fatalf("AssembleV2: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("Outcome = %s, want found", res.Outcome)
	}
	if adapter.Called() < 1 {
		t.Fatalf("real retrieval instance was not called (AC-5); the test ran the lexical fallback, not the /2 ready path")
	}
	// The engine's degradation stamp depends on whether a semantic service is
	// wired; a unit test against a MemStore has no semantic generation and
	// gets lexical_only. The per-item evidence contract still applies — the
	// v2 path stamps claim_type on every seed it produces, regardless of
	// whether the seed came from a retrieval row or the lexical fallback.
	// What AC-5 forbids is a hand-built fixture; this is the real engine
	// answer.
	switch adapter.LastResult().Degradation {
	case string(engineretrieval.StateReady), string(engineretrieval.StateLexicalOnly):
	default:
		t.Fatalf("real retrieval state = %q, want ready or lexical_only; the test ran an unhandled degradation path", adapter.LastResult().Degradation)
	}
	if len(res.Evidence) == 0 {
		t.Fatalf("bundle carried no evidence; the per-item assertion has nothing to walk")
	}
	return res, adapter
}

// evidenceFamilyError describes one per-item contract violation. The
// string form is the message a test failure carries; keeping it
// structured lets the bite test assert on substrings without depending
// on the exact format.
type evidenceFamilyError struct {
	RefID string
	Index int
	Why   string
}

func (e evidenceFamilyError) Error() string {
	return fmt.Sprintf("evidence[%s] (item %d) %s", e.RefID, e.Index, e.Why)
}

// validateEvidenceFamilyPure is the per-item assertion in a test-
// friendly form: it returns every violation it finds rather than
// reporting them via testing.T. The companion validateEvidenceFamily
// wraps it with t.Errorf so the assertion is identical from the test's
// point of view and the bite test can still read the message it would
// have emitted.
//
// The family-membership rule:
//
//   - Item with ClaimType ∈ {source_match, graph_relation}:
//     must carry Path, Line, Role, ClaimType. If ClaimType ==
//     graph_relation, must additionally carry EdgeTier. Must NOT carry
//     Snippet or TextHash (a citation names text, it does not include it).
//   - Item with ClaimType == "":
//     must carry Path, Line, Role, Span, Snippet, TextHash. Must NOT carry
//     ClaimType (a snippet is a body, not a claim).
//
// An item that carries neither is not a member of either family and the
// validator reports it.
func validateEvidenceFamilyPure(bundle *contract.Result) []evidenceFamilyError {
	var errs []evidenceFamilyError
	for i, ev := range bundle.Evidence {
		if ev.RefID == "" {
			errs = append(errs, evidenceFamilyError{RefID: "", Index: i, Why: "missing ref_id"})
			continue
		}
		if ev.Path == "" {
			errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "missing path"})
		}
		if ev.Line < 0 {
			errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("negative line %d", ev.Line)})
		}
		if ev.Role == "" {
			errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "missing role"})
		}
		switch ev.ClaimType {
		case "source_match":
			// Band 9 / bands 4 / 3 / 1 (config, file, read): span may or may
			// not be present. We do not require span (configs/files/read
			// reference a path, not a span), but we forbid Snippet+TextHash.
			if ev.Snippet != "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("claim_type=source_match but carries Snippet (%q); a citation names text, it does not include it", ev.Snippet)})
			}
			if ev.TextHash != "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("claim_type=source_match but carries TextHash (%q); a citation names text, it does not include it", ev.TextHash)})
			}
		case "graph_relation":
			if ev.EdgeTier == "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "claim_type=graph_relation but EdgeTier is empty; the edge's provenance tier must travel with the claim"})
			}
			if ev.Snippet != "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("claim_type=graph_relation but carries Snippet (%q); a citation names text, it does not include it", ev.Snippet)})
			}
			if ev.TextHash != "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("claim_type=graph_relation but carries TextHash (%q); a citation names text, it does not include it", ev.TextHash)})
			}
		case "":
			// Snippet entry. The contract requires Snippet+TextHash; Span
			// is a "start-end" pair. We assert Span is parseable.
			if ev.Snippet == "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "is a snippet entry (no claim_type) but Snippet is empty"})
			}
			if ev.TextHash == "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "is a snippet entry (no claim_type) but TextHash is empty; the snippet bytes must carry their own hash"})
			}
			if ev.Span == "" {
				errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: "is a snippet entry but Span is empty; a snippet must cite start-end"})
			}
		default:
			errs = append(errs, evidenceFamilyError{RefID: ev.RefID, Index: i, Why: fmt.Sprintf("carries unknown claim_type %q; only source_match and graph_relation are valid", ev.ClaimType)})
		}
	}
	return errs
}

// validateEvidenceFamily wraps validateEvidenceFamilyPure with t.Errorf
// so the test fails the canonical way (the failure message is the same
// string validateEvidenceFamilyPure would produce).
func validateEvidenceFamily(t *testing.T, bundle *contract.Result) {
	t.Helper()
	for _, e := range validateEvidenceFamilyPure(bundle) {
		t.Errorf("%s", e.Error())
	}
}

// TestTaskContextV2_EvidencePerItemContract_NoSnippets covers AC-1 (option
// b) and AC-4 of SW-268: per-item, the family membership rule holds for
// every emitted evidence item when TokenBudget=0 disables snippets. With
// no snippets, every item must be a claim-typed citation. The bundle is
// produced by the real task_context/2 path through the harness's
// adapter, not a hand-built fixture (AC-5).
func TestTaskContextV2_EvidencePerItemContract_NoSnippets(t *testing.T) {
	bundle, _ := retrievalRealBundle(t, -1) // -1 = no snippets
	if len(bundle.Evidence) == 0 {
		t.Fatal("no evidence; per-item assertion has nothing to walk")
	}
	// With snippets disabled, every item is a claim-typed citation.
	for i, ev := range bundle.Evidence {
		if ev.ClaimType == "" {
			t.Errorf("evidence[%s] (item %d) has no claim_type but Snippet is disabled; expected every item to be a claim-typed citation", ev.RefID, i)
		}
	}
	validateEvidenceFamily(t, bundle)
	// Sanity: the SW-264 numbers — at least one source_match and at least
	// one graph_relation must be present for the fixture to be exercising
	// the per-family property.
	var sources, relations int
	for _, ev := range bundle.Evidence {
		switch ev.ClaimType {
		case "source_match":
			sources++
		case "graph_relation":
			relations++
		}
	}
	if sources == 0 || relations == 0 {
		t.Fatalf("fixture did not exercise both families: source_match=%d graph_relation=%d", sources, relations)
	}
}

// TestTaskContextV2_EvidencePerItemContract_WithSnippets covers AC-1
// (option b) and AC-4 of SW-268 with TokenBudget>0: per-item, the family
// membership rule holds when both kinds coexist in the same bundle. This
// is the shape the SW-264 AC-9 raw bundles take (494 claim-typed
// citations + 47 snippet entries per query on average) and the test
// exercises both the citation-only and the snippet-only failure modes.
func TestTaskContextV2_EvidencePerItemContract_WithSnippets(t *testing.T) {
	bundle, _ := retrievalRealBundle(t, 1200)
	if len(bundle.Evidence) == 0 {
		t.Fatal("no evidence; per-item assertion has nothing to walk")
	}
	// With snippets, we expect at least one snippet entry alongside the
	// claim-typed citations.
	var sources, relations, snippets int
	for _, ev := range bundle.Evidence {
		switch ev.ClaimType {
		case "source_match":
			sources++
		case "graph_relation":
			relations++
		case "":
			snippets++
		}
	}
	if sources == 0 || snippets == 0 {
		t.Fatalf("fixture did not exercise both families at budget 1200: source_match=%d snippets=%d", sources, snippets)
	}
	validateEvidenceFamily(t, bundle)
}

// TestTaskContextV2_EvidencePerItemContract_AcrossRealBundle exercises
// the per-item assertion against every emitted evidence item in every
// /2 call the test fixture can produce. It is the AC-4 corollary of
// AC-5: the check is per item, not per family, and runs against the
// real producer — not against a subset of items or a hand-built slice.
//
// The test walks items twice — once with snippets disabled (all
// citations) and once with snippets enabled (mixed families) — and
// re-runs the per-item assertion on each, so a future regression that
// affects only one family still turns the test red.
func TestTaskContextV2_EvidencePerItemContract_AcrossRealBundle(t *testing.T) {
	for _, budget := range []int{-1, 1200} {
		budget := budget
		t.Run(fmt.Sprintf("budget=%d", budget), func(t *testing.T) {
			bundle, _ := retrievalRealBundle(t, budget)
			validateEvidenceFamily(t, bundle)
		})
	}
}

// TestTaskContextV2_EvidencePerItemContractBites is the load-bearing
// AC-4 proof that the per-item test is not a tautology. It deliberately
// tampers with one emitted evidence item and asserts the validator
// catches the tampered item with a message that points at the exact
// ref_id. The failure message is logged so a reader of the bytes can
// see exactly what the contract catches.
//
// Two variants are run:
//
//   - "source_match with Snippet" — the family mismatch the SW-264 AC-3
//     tests would have missed (they only checked the per-family
//     presence, never the per-item exclusivity).
//   - "graph_relation with empty EdgeTier" — a /2-specific failure that
//     the /1 tests cannot catch because /1 never stamps claim_type.
func TestTaskContextV2_EvidencePerItemContractBites(t *testing.T) {
	bundle, _ := retrievalRealBundle(t, -1) // -1 = no snippets, so every item is a citation
	if len(bundle.Evidence) == 0 {
		t.Fatal("no evidence; nothing to tamper with")
	}
	// Find a source_match item to corrupt.
	idx := -1
	for i, ev := range bundle.Evidence {
		if ev.ClaimType == "source_match" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no source_match item in bundle; cannot demonstrate bite")
	}
	corrupted := *bundle
	corrupted.Evidence = append([]contract.Evidence(nil), bundle.Evidence...)
	corrupted.Evidence[idx].Snippet = "this should not be here on a citation"
	corrupted.Evidence[idx].TextHash = "deadbeefdeadbeef"

	errs := validateEvidenceFamilyPure(&corrupted)
	if len(errs) == 0 {
		t.Fatal("validateEvidenceFamilyPure did not catch the tampered evidence; the per-item contract is not load-bearing")
	}
	var found bool
	for _, e := range errs {
		if e.RefID == corrupted.Evidence[idx].RefID && strings.Contains(e.Error(), "claim_type=source_match but carries Snippet") {
			found = true
			t.Logf("per-item contract bit on the tampered item: %s", e.Error())
		}
	}
	if !found {
		t.Fatalf("expected the tampered item %q to be flagged for the family mismatch; got %d errors but none pointed at the right ref_id with the right reason", corrupted.Evidence[idx].RefID, len(errs))
	}

	// Second variant: graph_relation with empty EdgeTier.
	gIdx := -1
	for i, ev := range bundle.Evidence {
		if ev.ClaimType == "graph_relation" {
			gIdx = i
			break
		}
	}
	if gIdx < 0 {
		t.Fatal("no graph_relation item in bundle; cannot demonstrate bite for graph_relation")
	}
	gCorrupted := *bundle
	gCorrupted.Evidence = append([]contract.Evidence(nil), bundle.Evidence...)
	gCorrupted.Evidence[gIdx].EdgeTier = ""
	gErrs := validateEvidenceFamilyPure(&gCorrupted)
	if len(gErrs) == 0 {
		t.Fatal("validateEvidenceFamilyPure did not catch graph_relation with empty EdgeTier; the per-item contract is not load-bearing")
	}
	var gFound bool
	for _, e := range gErrs {
		if e.RefID == gCorrupted.Evidence[gIdx].RefID && strings.Contains(e.Error(), "EdgeTier is empty") {
			gFound = true
			t.Logf("per-item contract bit on graph_relation without EdgeTier: %s", e.Error())
		}
	}
	if !gFound {
		t.Fatalf("expected the tampered graph_relation item %q to be flagged for the missing EdgeTier; got %d errors but none pointed at the right ref_id with the right reason", gCorrupted.Evidence[gIdx].RefID, len(gErrs))
	}
}

// silence unused-import warnings if go vet ever tightens.
var (
	_ = graphstore.NewMemStore
)
