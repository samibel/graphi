// Byte-parity tests against the SW-257 search_hybrid golden (AC-7).
//
// AC-7 requires that with no embedder configured, the retrieval module's
// rows and their explain fields render to bytes identical to today's
// search_hybrid output. The retrieval module's lexical pipeline DELEGATES
// to engine/agenttools/hybridsearch (the only production LexicalProvider,
// HybridSearchBridge), so the underlying row data is the SAME data
// search_hybrid produces. The byte-parity test renders both as a minimal
// JSON projection of the row payload (the row's node_id, path, line, and
// rank — the fields AC-7 calls "rows and their explain fields") and
// compares the resulting byte slices. If they differ, the test fails with
// both byte strings so the orchestrator sees the exact divergence.
package retrieval_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// SW-257 §7.2 captured bytes for `search_hybrid` `hello greeter` on the
// SQLite backend at the candidate. The retrieval's byte-parity path
// asserts the same bytes are produced today (the row-set pipeline
// has not moved). The hash is a second assertion: a structural change
// in hybridsearch would move the hash, and AC-7 forbids that move.
const sw257HelloGreeterHash = "0ec5fd56cf662defc4efe69ff9f7be2fe68645bc71bcc5e102535bed5888ae40"
const sw257HelloGreeterBytes = 1590

// charFixtureDir is the SW-257 pinned Go corpus fixture.
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

func indexedFixture(t *testing.T) graphstore.Graphstore {
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

// rowPayload is the structural projection of a search_hybrid item and a
// retrieval row that AC-7 calls "the rows and their explain fields":
// the node identity, the embedding-space document id (SW-260), the
// source path and line, and the audit score. The five-field projection
// is the FULL row payload the AC-7 contract names — DocumentID is the
// AC-2 identity the previous 4-field projection omitted, so a structural
// change in either pipeline that moves any of these fields is a defect
// the test fails on.
//
// search_hybrid's Rank is the audit score (an integer), and on the
// lexical-only path retrieval's Explain.Final IS that same audit score
// (the rerank's bonus/penalty signals are skipped on the lexical-only
// path, per the SW-263 review's AC-4-vs-AC-7 resolution). LexicalRank is
// retrieval-internal: search_hybrid's items don't expose the lexical
// position, so it is not part of the byte-parity projection — a
// retrieval-side change to LexicalRank does not break AC-7 conformance.
type rowPayload struct {
	NodeID     string `json:"node_id"`
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Rank       int    `json:"rank"`
}

func payloadFromItem(it contract.Item, ev []contract.Evidence) rowPayload {
	p := rowPayload{NodeID: it.RefID, Rank: it.Rank}
	for _, e := range ev {
		if e.RefID == it.EvidenceRefIDs[0] {
			p.Path = e.Path
			p.Line = e.Line
			break
		}
	}
	return p
}

func payloadFromRow(r retrieval.Row) rowPayload {
	return rowPayload{
		NodeID:     r.NodeID,
		DocumentID: r.DocumentID,
		Path:       r.Path,
		Line:       lineFromSpan(r.Span),
		Rank:       r.Explain.Final,
	}
}

func lineFromSpan(s string) int {
	if s == "" {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				c := s[j]
				if c < '0' || c > '9' {
					return 0
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

// TestByteParity_NoEmbedderEqualsSearchHybridGolden is AC-7. The
// retrieval's lexical pipeline delegates to search_hybrid (the only
// production LexicalProvider, HybridSearchBridge), so the underlying
// row data is the SAME data search_hybrid produces. Both render the
// rows into a minimal projection (node_id, path, line, rank) and the
// test compares the byte slices plus the sha256 from SW-257 §7.2. A
// structural change in either pipeline that moves the bytes is a
// defect the test fails on.
func TestByteParity_NoEmbedderEqualsSearchHybridGolden(t *testing.T) {
	store := indexedFixture(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}

	// search_hybrid path.
	shRes, err := hybridsearch.Search(ctx, hybridsearch.Params{
		Query:    "hello greeter",
		MaxItems: 20,
		Deps:     deps,
	})
	if err != nil {
		t.Fatalf("hybridsearch.Search: %v", err)
	}
	shBytes, err := contract.Serialize(shRes)
	if err != nil {
		t.Fatalf("contract.Serialize: %v", err)
	}
	if len(shBytes) != sw257HelloGreeterBytes {
		t.Fatalf("search_hybrid bytes = %d, want SW-257 §7.2 %d (regression: search_hybrid changed)",
			len(shBytes), sw257HelloGreeterBytes)
	}
	if sum := sha256.Sum256(shBytes); hex.EncodeToString(sum[:]) != sw257HelloGreeterHash {
		t.Fatalf("search_hybrid sha256 = %s, want SW-257 §7.2 %s (regression: search_hybrid changed)",
			hex.EncodeToString(sum[:]), sw257HelloGreeterHash)
	}

	// Retrieval path with no embedder. New hides the hybrid-search adapter;
	// the retrieval pipeline adds no extra
	// signals in the default weights so the byte projection is the
	// SAME as search_hybrid's.
	graph, _ := store.(graphstore.BoundedGraphLookup)
	r := retrieval.New(deps, deps.Search, graph)
	res, err := r.Retrieve(ctx, retrieval.Request{Query: "hello greeter", Limit: 20})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	// Render both into the minimal row payload projection.
	shPayload := make([]rowPayload, len(shRes.Items))
	for i, it := range shRes.Items {
		shPayload[i] = payloadFromItem(it, shRes.Evidence)
	}
	retPayload := make([]rowPayload, len(res.Rows))
	for i, row := range res.Rows {
		retPayload[i] = payloadFromRow(row)
	}

	shJSON, _ := json.Marshal(shPayload)
	retJSON, _ := json.Marshal(retPayload)

	if string(shJSON) != string(retJSON) {
		// First-difference display: byte parity is the AC-7 invariant,
		// the orchestrator needs the actual divergence.
		diff := firstDiff(string(shJSON), string(retJSON), 240)
		t.Fatalf("AC-7 byte parity violated:\n  search_hybrid = %s\n  retrieval     = %s\n  diff          = %s\n  sh payload    = %s\n  ret payload   = %s",
			string(shJSON), string(retJSON), diff, shJSON, retJSON)
	}

	// AC-1 + AC-2 + AC-7 invariants on the lexical-only path: every row
	// is reachable from the lexical candidate list, no semantic candidates
	// were consulted, and the integer RRF formula collapses to 0 (single
	// source, identity RRF — see engine/retrieval/rrf.go) so Final equals
	// the rerank + classification contributions only. The previous "_ =
	// sum" placeholder claimed to check "present and integer-only" but did
	// nothing; these assertions are the structural checks AC-7 and AC-2
	// require on this path.
	for i, row := range res.Rows {
		if row.Explain.SemanticRank != 0 {
			t.Errorf("row %d (%s): SemanticRank = %d, want 0 (lexical-only path)",
				i, row.NodeID, row.Explain.SemanticRank)
		}
		wantLexical := i + 1
		if row.Explain.LexicalRank != wantLexical {
			t.Errorf("row %d (%s): LexicalRank = %d, want %d (1-based lexical position)",
				i, row.NodeID, row.Explain.LexicalRank, wantLexical)
		}
		// Single-source RRF is the identity (rrf.go), so RRF is 0 and
		// Final = Graph + Classification exactly. AC-7 relies on this:
		// the lexical-only Final is the same integer the byte projection
		// of search_hybrid carries.
		if row.Explain.RRF != 0 {
			t.Errorf("row %d (%s): RRF = %d, want 0 (single-source RRF is identity)",
				i, row.NodeID, row.Explain.RRF)
		}
		wantFinal := row.Explain.Graph + row.Explain.Classification
		if row.Explain.Final != wantFinal {
			t.Errorf("row %d (%s): Final = %d, want %d (Final = Graph + Classification when RRF = 0)",
				i, row.NodeID, row.Explain.Final, wantFinal)
		}
		if row.Explain.Final <= 0 {
			t.Errorf("row %d (%s): Final = %d, want > 0 (ranked row must have a positive score)",
				i, row.NodeID, row.Explain.Final)
		}
	}

	if res.Degradation != retrieval.StateLexicalOnly {
		t.Errorf("Degradation = %q, want %q (no embedder)",
			res.Degradation, retrieval.StateLexicalOnly)
	}

	// The retrieval's rows must be a subset of the search_hybrid rows
	// in the same order — a structural assertion that holds even when
	// the rendering shape differs.
	shIDs := make([]string, len(shRes.Items))
	for i, it := range shRes.Items {
		shIDs[i] = it.RefID
	}
	retIDs := make([]string, len(res.Rows))
	for i, row := range res.Rows {
		retIDs[i] = row.NodeID
	}
	if !sameOrder(shIDs, retIDs) {
		t.Errorf("node_id order differs:\n  search_hybrid=%v\n  retrieval   =%v", shIDs, retIDs)
	}
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// firstDiff returns a short summary of the first byte position where a
// and b differ, plus the surrounding bytes for context.
func firstDiff(a, b string, around int) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - around
			if lo < 0 {
				lo = 0
			}
			hi := i + around
			if hi > n {
				hi = n
			}
			return fmt.Sprintf("first differing offset %d: a=%q b=%q (surrounding: a=%q b=%q)",
				i, string(a[i]), string(b[i]),
				a[lo:hi], b[lo:hi])
		}
	}
	return "identical up to common length"
}

// TestLexicalOnly_DegradationStateIsTyped asserts the AC-7 invariant at
// the engine integration boundary: a retrieval constructed over a
// vanilla search.Service (no WithSemantic, no WithSemanticState) returns
// Result.Degradation == StateLexicalOnly and never an error — exactly
// the S0 default-build contract SW-257 §8.1 freezes.
func TestLexicalOnly_DegradationStateIsTyped(t *testing.T) {
	store := indexedFixture(t)
	defer func() { _ = store.Close() }()

	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	graph, _ := store.(graphstore.BoundedGraphLookup)
	r := retrieval.New(deps, deps.Search, graph)
	res, err := r.Retrieve(context.Background(), retrieval.Request{Query: "hello greeter"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res.Degradation != retrieval.StateLexicalOnly {
		t.Errorf("Degradation = %q, want %q", res.Degradation, retrieval.StateLexicalOnly)
	}
}

// TestNew_SemanticAvailabilityReflectsGenerationState drives the public deep
// module over a real search.Service whose semantic path is unconfigured.
func TestNew_SemanticAvailabilityReflectsGenerationState(t *testing.T) {
	store := indexedFixture(t)
	defer func() { _ = store.Close() }()

	svc := search.New(store)
	deps := resolve.Deps{Query: query.New(store), Search: svc}
	graph, _ := store.(graphstore.BoundedGraphLookup)
	r := retrieval.New(deps, svc, graph)
	out, err := r.Retrieve(context.Background(), retrieval.Request{Query: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if out.Degradation != retrieval.StateLexicalOnly {
		t.Errorf("Degradation = %q, want %q", out.Degradation, retrieval.StateLexicalOnly)
	}
}

// TestNew_DelegatingLexicalPathProducesIdenticalNodeIDs is the dedicated
// structural assertion through the public seam: New's hidden lexical adapter
// returns the same node ids as search_hybrid in the same order.
func TestNew_DelegatingLexicalPathProducesIdenticalNodeIDs(t *testing.T) {
	store := indexedFixture(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}

	// Direct call to search_hybrid.
	shRes, _ := hybridsearch.Search(ctx, hybridsearch.Params{
		Query: "hello greeter", MaxItems: 20, Deps: deps,
	})
	graph, _ := store.(graphstore.BoundedGraphLookup)
	r := retrieval.New(deps, deps.Search, graph)
	res, err := r.Retrieve(ctx, retrieval.Request{Query: "hello greeter", Limit: 20, Mode: retrieval.ModeLexicalOnly})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(res.Rows) != len(shRes.Items) {
		t.Fatalf("retrieval returned %d rows, search_hybrid %d", len(res.Rows), len(shRes.Items))
	}
	for i := range res.Rows {
		if res.Rows[i].NodeID != shRes.Items[i].RefID {
			t.Errorf("rank %d: retrieval NodeID=%s, search_hybrid RefID=%s", i, res.Rows[i].NodeID, shRes.Items[i].RefID)
		}
	}
}
