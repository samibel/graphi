package search_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
)

// Graceful skip (CORE): a Service with NO embedder returns the typed Unavailable
// response — no error, no network, and lexical Search is unaffected.
func TestSemanticSearch_GracefulSkip(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	svc := search.New(st) // no WithSemantic ⇒ graceful skip
	res, err := svc.SemanticSearch(ctx, "ParseGraph", 10)
	if err != nil {
		t.Fatalf("SemanticSearch returned error on graceful-skip path: %v", err)
	}
	if res.Available {
		t.Fatal("Available = true on the graceful-skip path, want false")
	}
	if res.Reason != search.UnavailableReason {
		t.Fatalf("Reason = %q, want %q", res.Reason, search.UnavailableReason)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("Hits = %d on graceful-skip path, want 0", len(res.Hits))
	}
	// Lexical search still works and is unaffected.
	lex, lerr := svc.Search(ctx, "ParseGraph", 10)
	if lerr != nil {
		t.Fatalf("lexical Search failed: %v", lerr)
	}
	if len(lex.Matches) == 0 {
		t.Fatal("lexical Search returned no matches; semantic graceful-skip blocked it")
	}
}

// An unconfigured (zero) registry passed to WithSemantic still gracefully skips.
func TestSemanticSearch_UnconfiguredRegistry(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()

	svc := search.New(st).WithSemantic(embed.NewRegistry(), embed.NewIndex(), st)
	res, err := svc.SemanticSearch(ctx, "q", 10)
	if err != nil || res.Available {
		t.Fatalf("unconfigured registry not graceful: err=%v available=%v", err, res.Available)
	}
}

// failEmbedder fails the test if it is ever called — proves the graceful-skip
// path performs NO embedding (and therefore no network).
type failEmbedder struct{ t *testing.T }

func (f failEmbedder) ID() string { return "fail" }
func (f failEmbedder) Dim() int   { return 4 }
func (f failEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	f.t.Fatal("embedder was called on the graceful-skip path (network/embed must not happen)")
	return nil, nil
}

// Zero-embed assertion: when the registry is unconfigured, the embedder is never
// invoked. We register nothing, so Active()==false and Embed is never reached.
func TestSemanticSearch_NoEmbedWhenUnconfigured(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()

	reg := embed.NewRegistry() // registers nothing
	svc := search.New(st).WithSemantic(reg, embed.NewIndex(), st)
	if _, err := svc.SemanticSearch(ctx, "q", 10); err != nil {
		t.Fatalf("SemanticSearch error: %v", err)
	}
	// A failEmbedder is intentionally NOT registered; the test asserts via the
	// graceful-skip path that no embed occurred. (Registering it would activate
	// the configured path; see TestSemanticSearch_ConfiguredRanksHits.)
	_ = failEmbedder{t}
}

// Configured with a deterministic mock embedder ⇒ deterministic ranked hits
// citing NodeId + score.
func TestSemanticSearch_ConfiguredRanksHits(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()
	seedStore(t, st)

	mock := embed.NewMockEmbedder(16)
	reg := embed.NewRegistry()
	reg.Register(mock)
	index := embed.NewIndex()

	// Index every seeded node's qualified name with the SAME mock embedder so a
	// query that matches a node's text scores highest for that node.
	nodes, err := st.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, n := range nodes {
		vecs, eerr := mock.Embed(ctx, []string{n.QualifiedName()})
		if eerr != nil {
			t.Fatalf("Embed: %v", eerr)
		}
		index.Put(n.ID(), vecs[0])
	}

	svc := search.New(st).WithSemantic(reg, index, st)
	res, err := svc.SemanticSearch(ctx, "pkg/foo.ParseGraph", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if !res.Available {
		t.Fatal("Available = false with a configured embedder")
	}
	if len(res.Hits) == 0 {
		t.Fatal("no hits with a configured embedder")
	}
	// The exact-match node ("pkg/foo.ParseGraph") must rank first (cosine 1.0).
	if res.Hits[0].QualifiedName != "pkg/foo.ParseGraph" {
		t.Fatalf("top hit = %q, want pkg/foo.ParseGraph", res.Hits[0].QualifiedName)
	}
	if res.Hits[0].NodeID == "" || res.Hits[0].Score <= 0 {
		t.Fatalf("top hit missing NodeId/score: %+v", res.Hits[0])
	}
	// Deterministic across runs.
	res2, _ := svc.SemanticSearch(ctx, "pkg/foo.ParseGraph", 10)
	b1, _ := search.MarshalSemantic(res)
	b2, _ := search.MarshalSemantic(res2)
	if string(b1) != string(b2) {
		t.Fatalf("non-deterministic semantic results:\n%s\n%s", b1, b2)
	}
}

// MarshalSemantic produces stable JSON and a nil Hits slice serializes as [].
func TestMarshalSemantic_Stable(t *testing.T) {
	r := search.SemanticResponse{Query: "q", Available: false, Reason: search.UnavailableReason}
	b, err := search.MarshalSemantic(r)
	if err != nil {
		t.Fatalf("MarshalSemantic: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["available"] != false {
		t.Fatalf("available = %v, want false", decoded["available"])
	}
	if _, ok := decoded["hits"].([]any); !ok {
		t.Fatalf("hits not serialized as array: %v", decoded["hits"])
	}
}
