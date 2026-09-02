package search_test

// SW-265 AC-4: zero hits on a `ready` index is byte-distinguishable from
// the `unavailable` response. An agent seeing an empty hit list MUST be
// able to tell whether retrieval is unusable vs the index has no
// matches — and the wire documents for the two cases must differ.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
)

// TestSemanticSearch_AC4_ReadyZeroHitsVsUnavailable exercises the two
// paths the agent MUST be able to distinguish:
//
//   - path A: a `ready` index on a configured embedder, but the query
//     has no ranked matches in the index. Available=true, Hits=[].
//   - path B: no embedder configured at all. Available=false, Reason set.
//
// The two responses MUST be byte-distinct. A regression that conflated
// "ready but no matches" with "no embedder" would fail this assertion.
func TestSemanticSearch_AC4_ReadyZeroHitsVsUnavailable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer st.Close()

	// Path A: ready index, but a query that doesn't match anything.
	mock := embed.NewMockEmbedder(8)
	regA := embed.NewRegistry()
	if err := regA.Register(mock); err != nil {
		t.Fatalf("register mock: %v", err)
	}
	regA.Freeze()
	svcReady := search.New(st).
		WithSemantic(regA, embed.NewIndex(), st).
		WithSemanticState(search.SemanticState{State: embed.StateReady})
	ready, err := svcReady.SemanticSearch(ctx, "ZZZ_NO_MATCH_IN_MOCK_INDEX_ZZZ", 10)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}

	// Path B: no embedder at all (the default graceful skip).
	svcUnconfigured := search.New(st)
	unavail, err := svcUnconfigured.SemanticSearch(ctx, "ZZZ_NO_MATCH_IN_MOCK_INDEX_ZZZ", 10)
	if err != nil {
		t.Fatalf("unavailable: %v", err)
	}

	// The wire documents MUST be byte-distinct.
	readyB, err := search.MarshalSemantic(ready)
	if err != nil {
		t.Fatal(err)
	}
	unavailB, err := search.MarshalSemantic(unavail)
	if err != nil {
		t.Fatal(err)
	}
	readyGolden, err := os.ReadFile(filepath.Join("testdata", "semantic-ready-empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	unavailableGolden, err := os.ReadFile(filepath.Join("testdata", "semantic-unavailable.json"))
	if err != nil {
		t.Fatal(err)
	}
	readyGolden = bytes.TrimSuffix(readyGolden, []byte("\n"))
	unavailableGolden = bytes.TrimSuffix(unavailableGolden, []byte("\n"))
	if !bytes.Equal(readyB, readyGolden) {
		t.Fatalf("ready bytes:\n got %s\nwant %s", readyB, readyGolden)
	}
	if !bytes.Equal(unavailB, unavailableGolden) {
		t.Fatalf("unavailable bytes:\n got %s\nwant %s", unavailB, unavailableGolden)
	}
	if bytes.Equal(readyB, unavailB) {
		t.Fatalf("AC-4 broken: ready and unavailable bytes match:\n%s\n==\n%s", readyB, unavailB)
	}
	// Available MUST differ.
	if ready.Available == unavail.Available {
		t.Errorf("AC-4: ready.Available (%v) == unavailable.Available (%v)", ready.Available, unavail.Available)
	}
	if !ready.Available {
		t.Errorf("AC-4: ready.Available = false, want true (the configured path returns available=true even when zero hits)")
	}
	if ready.State != embed.StateReady || unavail.State != embed.StateMissing {
		t.Fatalf("states ready=%s unavailable=%s, want ready/missing", ready.State, unavail.State)
	}
	if unavail.Available {
		t.Errorf("AC-4: unavailable.Available = true, want false")
	}
	// Reason MUST be empty on the ready path; populated on unavailable.
	if ready.Reason != "" {
		t.Errorf("AC-4: ready.Reason = %q, want \"\" (ready returns no reason)", ready.Reason)
	}
	if unavail.Reason == "" {
		t.Errorf("AC-4: unavailable.Reason = \"\", want non-empty")
	}
}
