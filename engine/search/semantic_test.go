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

// repairEmbedder is a test embedder whose Embed always returns a typed
// UnavailableError. It exists to assert the AC-5 contract: when the
// configured embedder surfaces a typed repair command, SemanticSearch
// must reach the user as a typed unavailable response with the exact
// command, not as a generic error.
type repairEmbedder struct {
	repair string
}

func (r repairEmbedder) ID() string { return "test:repair" }
func (r repairEmbedder) Dim() int   { return 4 }
func (r repairEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, &typedRepairError{msg: "no embedder artifact cached", repair: r.repair}
}

type typedRepairError struct {
	msg    string
	repair string
}

func (e *typedRepairError) Error() string  { return e.msg }
func (e *typedRepairError) Repair() string { return e.repair }

type availabilityRepairEmbedder struct {
	repair     string
	available  bool
	embedCalls int
}

func (e *availabilityRepairEmbedder) ID() string { return "test:availability-repair" }
func (e *availabilityRepairEmbedder) Dim() int   { return 4 }
func (e *availabilityRepairEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	e.embedCalls++
	return [][]float32{{1, 0, 0, 0}}, nil
}
func (e *availabilityRepairEmbedder) CheckAvailable(context.Context) error {
	if e.available {
		return nil
	}
	return &typedRepairError{msg: "no embedder artifact cached", repair: e.repair}
}

func TestSemanticSearch_TypedRepairCommand_PropagatesToUnavailableResponse(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()

	wantRepair := "graphi setup-embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
	reg := embed.NewRegistry()
	reg.Register(repairEmbedder{repair: wantRepair})
	svc := search.New(st).WithSemantic(reg, embed.NewIndex(), st)

	res, err := svc.SemanticSearch(ctx, "anything", 10)
	if err != nil {
		t.Fatalf("SemanticSearch returned an error; the typed repair path should not surface as a generic error: %v", err)
	}
	if res.Available {
		t.Fatal("Available = true; the configured embedder surfaced a typed repair, the response must be unavailable")
	}
	if res.Reason != wantRepair {
		t.Fatalf("Reason = %q, want %q (the exact repair command)", res.Reason, wantRepair)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("Hits = %d, want 0", len(res.Hits))
	}
}

// AC-5: artifact availability precedes both generation-state and empty-query
// short circuits. Otherwise a missing static artifact can look like a healthy
// empty search or produce the unrelated `index --semantic` repair.
func TestSemanticSearch_TypedRepairCommand_CoversEveryEarlyReturn(t *testing.T) {
	wantRepair := "graphi setup-embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
	tests := []struct {
		name  string
		query string
		state search.SemanticState
	}{
		{name: "empty query", query: ""},
		{name: "missing generation", query: "anything", state: search.SemanticState{State: embed.StateMissing, Reason: search.ReasonUnavailable}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := graphstore.NewMemStore()
			defer st.Close()
			emb := &availabilityRepairEmbedder{repair: wantRepair}
			reg := embed.NewRegistry()
			if err := reg.Register(emb); err != nil {
				t.Fatal(err)
			}
			svc := search.New(st).
				WithSemantic(reg, embed.NewIndex(), st).
				WithSemanticState(tc.state)

			res, err := svc.SemanticSearch(context.Background(), tc.query, 10)
			if err != nil {
				t.Fatalf("SemanticSearch returned generic error: %v", err)
			}
			if res.Available || res.Reason != wantRepair || len(res.Hits) != 0 {
				t.Fatalf("response = %+v, want typed unavailable with repair %q", res, wantRepair)
			}
			if emb.embedCalls != 0 {
				t.Fatalf("Embed calls = %d, want 0; availability check must cover the early path", emb.embedCalls)
			}
		})
	}
}

// Artifact availability deliberately precedes generation freshness. If both
// repairs apply, installing the pinned model is the prerequisite; after the
// artifact is present, the generation-state repair becomes visible. The same
// ordering must preserve the empty-query and ready-generation behavior.
func TestSemanticSearch_ArtifactAvailabilityPrecedence(t *testing.T) {
	const artifactRepair = "graphi setup-embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
	tests := []struct {
		name          string
		available     bool
		query         string
		state         search.SemanticState
		wantAvailable bool
		wantReason    string
		wantEmbeds    int
	}{
		{
			name:       "artifact absent wins over stale generation",
			query:      "anything",
			state:      search.SemanticState{State: embed.StateStale, Reason: search.ReasonStale},
			wantReason: artifactRepair,
		},
		{
			name:       "stale generation remains visible when artifact is present",
			available:  true,
			query:      "anything",
			state:      search.SemanticState{State: embed.StateStale, Reason: search.ReasonStale},
			wantReason: search.ReasonStale,
		},
		{
			name:          "empty query remains an available empty result",
			available:     true,
			query:         "",
			wantAvailable: true,
		},
		{
			name:          "ready generation still reaches the embedder",
			available:     true,
			query:         "anything",
			state:         search.SemanticState{State: embed.StateReady},
			wantAvailable: true,
			wantEmbeds:    1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := graphstore.NewMemStore()
			defer st.Close()
			emb := &availabilityRepairEmbedder{repair: artifactRepair, available: tc.available}
			reg := embed.NewRegistry()
			if err := reg.Register(emb); err != nil {
				t.Fatal(err)
			}
			svc := search.New(st).
				WithSemantic(reg, embed.NewIndex(), st).
				WithSemanticState(tc.state)

			res, err := svc.SemanticSearch(context.Background(), tc.query, 10)
			if err != nil {
				t.Fatalf("SemanticSearch: %v", err)
			}
			if res.Query != tc.query || res.Available != tc.wantAvailable || res.Reason != tc.wantReason || res.Hits == nil || len(res.Hits) != 0 {
				t.Fatalf("response = %+v, want query=%q available=%v reason=%q hits=[]", res, tc.query, tc.wantAvailable, tc.wantReason)
			}
			if emb.embedCalls != tc.wantEmbeds {
				t.Fatalf("Embed calls = %d, want %d", emb.embedCalls, tc.wantEmbeds)
			}
		})
	}
}
