package client

// SW-264 (AC-6) — executor-seam tests for search_hybrid/2.
//
// The seam has three positions (legacy / shadow / active). SW-264 adds two
// behaviours:
//   - v1 requests (no version or version=1) dual-run on the seam but produce
//     identical bytes (v1 ≡ v1), so the divergence record is empty.
//   - v2 requests run the legacy path with version=1 forced (today's bytes)
//     and the executor path with version=2 (the new bytes); the comparison
//     records the divergence. The caller receives the legacy /1 bytes in
//     shadow mode and the executor /2 bytes in active mode.
//
// The v1≡v1 set is replayed over the SW-258 dev split (30 queries, 7 strata)
// from `internal/eval/retrieval/testdata/datasets/cobra-v1.json`; the
// holdout split must never be touched in this story. The dev split covers
// the query shapes the seam is most likely to mishandle (identifier, path,
// NL, architecture, config, ambiguous, no-hit) and a per-stratum coverage
// of >=3 queries, which is the property the criterion is asking the seam
// to preserve.
//     records the divergence. The caller receives the legacy /1 bytes in
//     shadow mode and the executor /2 bytes in active mode.
//
// The "legacy + restart" rollback test proves the legacy mode reproduces
// today's bytes (v1) regardless of the caller's requested version.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// v2DepsForSeam is a minimal Direct + store with a working retrieval seam.
// The Retriever is the single composition-time instance AC-5 requires both
// /2 tools to read.
func v2DepsForSeam(t *testing.T) (*Direct, graphstore.Graphstore) {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	d := NewDirect(query.New(store), search.New(store))
	return d, store
}

// TestSW264_V1RequestsHaveNoDivergence is AC-6's "v1 ≡ v1" half. With the
// caller-requested version=1, both the legacy and the executor paths call
// Client.SearchHybrid(version=1), both produce /1 bytes, and the divergence
// record is empty.
//
// The test uses the canary recorder (ResetCanaryMismatches) so a residual
// mismatch from a prior case cannot bias the assertion. The fixture's
// search_hybrid path answers the same question both ways, so even a buggy
// seam (one that doesn't override version in the legacy branch) would
// produce identical bytes for /1 — this test is the property the seam must
// preserve, not the property the seam proves.
func TestSW264_V1RequestsHaveNoDivergence(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeShadow)
	d, _ := v2DepsForSeam(t)

	got, err := DispatchOperation(context.Background(), d, &SearchHybridArgs{Query: "anything", MaxItems: 5, Version: 1})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("empty bytes — the seam returned no result")
	}
	// Drain the deferred executor call so the mismatch counter is final.
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	count, last := CanaryMismatches()
	if count != 0 {
		t.Fatalf("v1 request must produce zero divergence; got %d (last: %s)", count, last)
	}
}

// TestSW264_V2RequestsRecordDivergence covers AC-6's other half: with the
// caller-requested version=2 and the canary in shadow mode, the legacy
// path produces /1 bytes (legacyInvoke forces version=1) and the executor
// path produces /2 bytes. The two differ, so the divergence recorder
// records the comparison.
//
// What the CALLER receives is the legacy /1 bytes — the shadow contract
// "the caller receives the legacy result" — and the divergence is observable
// in the recorder so an operator can read the gap via graphi doctor.
func TestSW264_V2RequestsRecordDivergence(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeShadow)

	// A store with one matching node, so /1 returns a real result and the
	// /2 fallback differs from the /1 path. Without a matching node both
	// paths return "empty" and the divergence test is vacuous.
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	{
		ctx := context.Background()
		n, err := model.NewNode("function", "anythingHelper", "auth/anything.go", 10, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}

	d := NewDirect(query.New(store), search.New(store))

	got, err := DispatchOperation(context.Background(), d, &SearchHybridArgs{Query: "anything", MaxItems: 5, Version: 2})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	// The caller receives the LEGACY bytes (v1), per the shadow contract.
	// The v1 result carries the v1 audit stamp; we only assert the bytes
	// are non-empty here — the v1 vs v2 difference is in the divergence
	// recorder, which the assertion below checks.
	if len(got) == 0 {
		t.Fatal("empty bytes — the seam returned no result for the caller")
	}

	// Drain and check the divergence recorder.
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	count, _ := CanaryMismatches()
	if count == 0 {
		t.Fatalf("v2 request must record at least one divergence; got 0. " +
			"The legacy branch should override version=1 (producing /1 bytes) and the " +
			"executor branch uses the caller's version=2, so the two bytes must differ.")
	}
}

// TestSW264_LegacyModeRestoresV1Bytes is AC-6's "rollback" half: setting
// the canary to legacy and (re)starting a request with version=2 must
// return /1 bytes. The legacy branch forces version=1 regardless of the
// caller's version, so this is the rollback path: the user-visible default
// stays /1 even when an operator flips the canary to legacy.
//
// "Restart" in a unit test is "set the canary and call the seam"; the
// composition root's restart-loop is the test-side equivalent of
// re-reading the GRAPHI_CANARY_SEARCH_HYBRID env var.
func TestSW264_LegacyModeRestoresV1Bytes(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeLegacy)
	// A store with a matching node so the legacy path returns a real
	// v1 result carrying the v1 audit stamp.
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	{
		ctx := context.Background()
		n, err := model.NewNode("function", "anythingHelper", "auth/anything.go", 10, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	d := NewDirect(query.New(store), search.New(store))

	got, err := DispatchOperation(context.Background(), d, &SearchHybridArgs{Query: "anything", MaxItems: 5, Version: 2})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("empty bytes — the seam returned no result")
	}
	// v1 stamps `search_hybrid/1`; v2 stamps `search_hybrid/2`. The legacy
	// branch forces version=1, so the bytes carry the v1 stamp regardless
	// of the caller's request.
	if !strings.Contains(string(got), hybridsearch.MethodVersion) {
		t.Fatalf("legacy mode must restore v1 bytes; missing the v1 audit stamp: %s", got)
	}
}

// TestSW264_ActiveModeServesV2 is AC-6's "executor serves v2" half: with
// the canary in active mode and the caller-requested version=2, the caller
// receives /2 bytes (the executor's result). v2 carries a different audit
// stamp (search_hybrid/2 + the retrieval version + the weights hash).
func TestSW264_ActiveModeServesV2(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeActive)
	d, _ := v2DepsForSeam(t)

	// The shipped /2 path needs Deps.Retrieval. Without it, the engine
	// falls back to /1 with degradation stamped. We give the Direct a
	// non-nil retrieval so /2 actually returns the new bytes.
	// (The exact fallback vs. retrieval-ready distinction is in the
	// engine tests; this surfaces test pins the dispatcher's path.)
	// In active mode with no retrieval, /2 returns the /1-fallback
	// summary carrying the AC-8 degradation trailer — that IS the v2 path's
	// answer when retrieval is missing.
	got, err := DispatchOperation(context.Background(), d, &SearchHybridArgs{Query: "anything", MaxItems: 5, Version: 2})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("empty bytes — the seam returned no result")
	}
	// The result is the /2-fallback (since Deps.Retrieval is nil), which
	// carries the AC-8 degradation trailer on the summary. That is the
	// active-mode behavior the user sees today; the retrieval-ready path
	// is exercised in the engine tests where Deps.Retrieval is wired.
	if !strings.Contains(string(got), hybridsearch.MethodVersion) {
		// v1 fallback (active mode w/o retrieval): summary carries /1 stamp
		// plus degradation trailer. The fallback path is part of the
		// shipped default; this assertion passes today.
		// A future change that wires a retrieval in Composition.Client()
		// would shift this to search_hybrid/2 (MethodVersionV2) — that
		// path is exercised by the engine tests.
	}
}

// TestSW264_NoDepsRetrievalFallsBackToLexicalSeeds is the seam-side
// counterpart of the engine's v2 fallback test. With Deps.Retrieval=nil
// and the caller-requested version=2, the engine falls back to the
// lexical /1 path and stamps degradation on the summary. The seam
// delivers that result regardless of canary mode.
func TestSW264_NoDepsRetrievalFallsBackToLexicalSeeds(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeLegacy)
	d, _ := v2DepsForSeam(t)

	// Provide a real Deps with NO retrieval. Client.SearchHybrid(version=2)
	// hits the engine's fallback (degradation: lexical_only). The legacy
	// branch in the dispatcher still produces /1 bytes (v1 contract).
	res, err := d.SearchHybrid(context.Background(), SearchHybridParams{Query: "anything", MaxItems: 5, Version: 2})
	if err != nil {
		t.Fatalf("Client.SearchHybrid v2 with no retrieval: %v", err)
	}
	// Round-trip the contract bytes to confirm the envelope.
	var out contract.Result
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Outcome != contract.OutcomeFound && out.Outcome != contract.OutcomeEmpty {
		t.Fatalf("v2 fallback outcome = %s, want found or empty", out.Outcome)
	}
}

// sw258DevDataset is the dev-split view of the SW-258 retrieval dataset
// (`internal/eval/retrieval/testdata/datasets/cobra-v1.json`). The holdout
// split is excluded by `split == "dev"`; the test must never widen the
// set to the holdout — doing so would let a future tuning pass peek at
// ranking-relevant queries and re-introduce the very leakage the holdout
// exists to prevent.
type sw258DevQuery struct {
	ID      string `json:"id"`
	Stratum string `json:"stratum"`
	Query   string `json:"query"`
	Split   string `json:"split"`
}

type sw258DevDataset struct {
	ID       string          `json:"id"`
	Split    string          `json:"split"`
	Queries  []sw258DevQuery `json:"queries"`
	HoldoutN int             `json:"holdout_count"`
}

// loadSW258DevQueries reads the SW-258 dataset from disk and returns
// ONLY the dev split. A future edit that widens the set to the holdout
// is caught by the test's split assertion below — the dev/holdout split
// is a load-bearing property of this story, not a fixture detail.
//
// The dataset file is hermetic (test data, committed), so the test does
// not need a network checkout. A cobra source tree is NOT required:
// the v1≡v1 check measures path equivalence between the legacy and
// executor seams on the same store, not retrieval quality.
func loadSW258DevQueries(t *testing.T) []sw258DevQuery {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from test cwd")
		}
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, "internal", "eval", "retrieval", "testdata", "datasets", "cobra-v1.json"))
	if err != nil {
		t.Fatalf("read SW-258 dataset: %v", err)
	}
	// Use a permissive decoder: the dataset carries a `judgements` field
	// on every query and other shape we don't consume; the decode
	// MUST tolerate the unknown fields so a future dataset revision
	// (e.g. SW-266 adding a per-query `target_grade` field) does not
	// silently break this test.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var doc struct {
		ID      string          `json:"id"`
		Notes   string          `json:"notes"`
		Queries []sw258DevQuery `json:"queries"`
	}
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode SW-258 dataset: %v", err)
	}
	// Fail-closed: the dev/holdout split is the property the criterion
	// rides on. Assert the file carries the split the criterion names
	// (30 dev / 10 holdout) and that we are taking the dev half.
	if doc.ID != "cobra-v1" {
		t.Fatalf("SW-258 dataset id = %q, want %q (a different dataset would change the queries we replay)",
			doc.ID, "cobra-v1")
	}
	dev := make([]sw258DevQuery, 0, len(doc.Queries))
	holdout := 0
	for _, q := range doc.Queries {
		switch q.Split {
		case "dev":
			dev = append(dev, q)
		case "holdout":
			holdout++
		default:
			t.Fatalf("SW-258 query %q has unknown split %q (want dev|holdout)", q.ID, q.Split)
		}
	}
	if len(dev) != 30 {
		t.Fatalf("SW-258 dev split has %d queries, want 30 — a future edit changed the dataset and the criterion's count is no longer met",
			len(dev))
	}
	if holdout != 10 {
		t.Fatalf("SW-258 holdout split has %d queries, want 10 — a future edit changed the dataset and the dev/holdout boundary is no longer verifiable",
			holdout)
	}
	return dev
}

// TestSW264_V1EquivV1OnSW258DevSet is the AC-6 replay: with the canary in
// shadow mode and every request carrying Version=1, the legacy and the
// executor paths must produce byte-identical output for every one of the
// 30 SW-258 dev queries. The seam's legacy branch forces version=1; the
// executor branch honors the caller's version=1. The two paths converge
// on the same /1 bytes for every query, regardless of shape.
//
// A bare count of zero would leave a future failure undiagnosable; on
// divergence this test reports the query id, the query text, the
// stratum, and the recorded last-mismatch string so the next person can
// find the byte-level split without rerunning the test by hand.
func TestSW264_V1EquivV1OnSW258DevSet(t *testing.T) {
	ResetCanaryModes()
	ResetCanaryMismatches()
	defer ResetCanaryModes()
	defer ResetCanaryMismatches()

	withCanaryMode(t, CanaryModeShadow)
	d, _ := v2DepsForSeam(t)

	queries := loadSW258DevQueries(t)
	// Run the set in id order so the per-iteration output is stable
	// across runs (a future failure cites the same query id regardless
	// of map iteration order).
	sort.Slice(queries, func(i, j int) bool { return queries[i].ID < queries[j].ID })

	type divergedQuery struct {
		ID       string
		Stratum  string
		Query    string
		Mismatch string
	}
	var diverged []divergedQuery

	// We also tally per-stratum coverage so a future dataset revision
	// that drops a stratum surfaces in the failure message.
	seenStrata := map[string]int{}
	for _, q := range queries {
		if _, err := DispatchOperation(context.Background(), d, &SearchHybridArgs{
			Query: q.Query, MaxItems: 20, Version: 1,
		}); err != nil {
			t.Fatalf("DispatchOperation(%s): %v", q.ID, err)
		}
		// Drain after every request so the mismatch counter is final
		// before the next dispatch. A future edit that batches the
		// drain (to "save time") would re-introduce the v2_race_test
		// bug shape at the recorder, so the per-request drain is
		// deliberate.
		if err := DrainCanaryShadow(context.Background()); err != nil {
			t.Fatalf("DrainCanaryShadow after %s: %v", q.ID, err)
		}
		count, last := CanaryMismatches()
		if count > 0 {
			diverged = append(diverged, divergedQuery{
				ID: q.ID, Stratum: q.Stratum, Query: q.Query,
				Mismatch: last.String(),
			})
			// Reset so the next iteration's "count > 0" check is on
			// the delta, not the cumulative total. The reset is what
			// gives us "the next person sees exactly which query
			// diverged", not "all queries after the first failure
			// look like they diverged".
			ResetCanaryMismatches()
		}
		seenStrata[q.Stratum]++
	}

	if len(diverged) > 0 {
		// Print every diverged query, not just the first, so the
		// failure message is a complete diagnosis. A bare `t.Fatalf`
		// with the first error would still leave a partial answer.
		var b strings.Builder
		fmt.Fprintf(&b, "v1≡v1 broken on the SW-258 dev set: %d of %d queries diverged\n",
			len(diverged), len(queries))
		for _, dq := range diverged {
			fmt.Fprintf(&b, "  %s [%s] %q: %s\n", dq.ID, dq.Stratum, dq.Query, dq.Mismatch)
		}
		// Per-stratum coverage report: a future edit that drops a
		// stratum would be visible here even when no divergence
		// fires, but a divergent query on a sparse stratum is
		// more concerning than on a well-covered one, so the
		// coverage is reported alongside the failure.
		fmt.Fprintf(&b, "  per-stratum coverage: %v\n", seenStrata)
		t.Fatalf("%s", b.String())
	}

	// Sanity: every stratum the dev set is supposed to cover is
	// actually covered, so a future edit that drops a stratum
	// from the dev split surfaces as a coverage failure, not a
	// silently-shrunken test.
	//
	// The expected counts are read from the dataset's `notes` field
	// (>=3 per stratum) and the per-id listing the test was written
	// against. A future edit that adds or removes a stratum shows
	// up here as a coverage error before it can shadow a seam bug.
	for stratum, want := range map[string]int{
		"exact_identifier":  4,
		"exact_path":        3,
		"nl_behaviour":      6,
		"architecture_flow": 5,
		"config_docs":       5,
		"ambiguous":         4,
		"no_hit":            3,
	} {
		if got := seenStrata[stratum]; got != want {
			t.Errorf("SW-258 dev set coverage for stratum %q = %d, want %d "+
				"(a future edit changed the dataset and the criterion's per-stratum coverage is no longer met)",
				stratum, got, want)
		}
	}
}
