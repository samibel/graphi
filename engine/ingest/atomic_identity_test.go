package ingest_test

// SW-261 CRITICAL 2a — the graph identity must never lag behind the graph.
//
// index.commit_generation is what tells a reload whether the persisted vectors
// belong to the graph it is looking at. If a mutation can land while that key
// still names the previous graph, a reload serves vectors built for the old
// graph as `ready` — the one outcome the field exists to prevent.
//
// Both writers therefore publish the identity BEFORE the step that makes the
// mutation irreversible: the full pass bumps before clearing its marker, and
// the incremental pass bumps before the Phase-2 transaction that clears the
// dirty state. That makes the failure modes safe in one direction — a failed
// write means nothing landed, a failed commit means the identity is merely
// ahead and forces a rebuild.
//
// Round 2 fixed only the full pass, and its incremental test said so in its own
// body ("we can't easily swap the store mid-Ingester") before returning without
// asserting anything; round 3 caught the path still open. These tests force a
// SetMetadata failure on each writer and assert that nothing was mutated behind
// it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// failOnCommitGenGraphstore is a graphstore wrapper that fails every
// SetMetadata call whose key is the commit_generation key, succeeds
// everywhere else. Used to force the post-mutation bump to fail without
// disturbing the rest of the store's behaviour. The full Graphstore
// interface is satisfied by embedding the inner store; only SetMetadata
// is overridden.
type failOnCommitGenGraphstore struct {
	graphstore.Graphstore
	setCalls  int
	failError error
}

func (f *failOnCommitGenGraphstore) SetMetadata(ctx context.Context, key, value string) error {
	f.setCalls++
	if key == "index.commit_generation" {
		return f.failError
	}
	return f.Graphstore.SetMetadata(ctx, key, value)
}

// TestFullPass_BumpFailsKeepsMarkerOpen pins CRITICAL 2a for the full
// pass: when the commit_generation bump fails after the graph mutation,
// the full-pass marker MUST stay open so the next session forces a
// rebuild. The pre-fix shape cleared the marker first; the test asserts
// the marker is still present.
func TestFullPass_BumpFailsKeepsMarkerOpen(t *testing.T) {
	ctx := context.Background()
	inner := graphstore.NewMemStore()
	t.Cleanup(func() { _ = inner.Close() })
	wrapped := &failOnCommitGenGraphstore{
		Graphstore: inner,
		failError:  errors.New("simulated disk full"),
	}
	ing := newIngester(t, wrapped, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err == nil {
		t.Fatal("IngestAll succeeded despite the bump failing; want error")
	}

	// The marker MUST stay open. A subsequent CanWarmStart probe must
	// refuse warm-start (forces a rebuild), otherwise a later reload
	// would classify the just-built-but-unbumped vectors as ready.
	if _, ok, err := ing.CanWarmStart(ctx, root); err != nil {
		t.Fatalf("CanWarmStart: %v", err)
	} else if ok {
		t.Fatalf("CanWarmStart = true after a failed bump; want false (the marker must stay open)")
	}
}

// failAfterNCommitGenGraphstore fails the commit_generation write only after
// the first n successful ones, so a priming full pass can succeed and the
// NEXT identity write — the incremental one — is the one that fails. The
// round-2 test could not do this ("we can't easily swap the store mid-Ingester")
// and settled for re-running the full pass, which left the incremental path
// untested; counting instead of swapping is what makes it testable.
type failAfterNCommitGenGraphstore struct {
	graphstore.Graphstore
	allow     int // this many commit_generation writes succeed first
	seen      int
	failError error
}

func (f *failAfterNCommitGenGraphstore) SetMetadata(ctx context.Context, key, value string) error {
	if key == "index.commit_generation" {
		f.seen++
		if f.seen > f.allow {
			return f.failError
		}
	}
	return f.Graphstore.SetMetadata(ctx, key, value)
}

// TestIncremental_BumpFailureLeavesNothingMutated pins CRITICAL 2a for the
// INCREMENTAL path, which round 2 fixed only for the full pass and round 3
// caught still open.
//
// The property is an ordering one. The Phase-2 transaction is what clears the
// dirty state, so it is the last moment at which anything remembers a mutation
// is outstanding. Publishing the identity BEFORE that transaction makes the two
// failure modes safe:
//
//   - the identity write fails  → the transaction never runs, the file stays
//     dirty, and the graph is untouched, so the vectors built for it are still
//     legitimately current;
//   - the identity write succeeds and the transaction fails → the identity is
//     ahead of the graph, so a reload reads stale and rebuilds.
//
// The unsafe ordering is the reverse: mutate, clear dirty, then fail to publish
// — a durably changed graph still naming the previous identity, which a reload
// serves as `ready`. This test fails under that ordering because the mutation
// lands despite the error.
func TestIncremental_BumpFailureLeavesNothingMutated(t *testing.T) {
	ctx := context.Background()
	inner := graphstore.NewMemStore()
	t.Cleanup(func() { _ = inner.Close() })
	// One write is allowed: the priming full pass. The incremental bump is
	// the second and fails.
	wrapped := &failAfterNCommitGenGraphstore{
		Graphstore: inner,
		allow:      1,
		failError:  errors.New("simulated disk full"),
	}
	ing := newIngester(t, wrapped, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("priming IngestAll: %v", err)
	}
	before := countNodes(ctx, t, inner)
	if before == 0 {
		t.Fatal("priming pass stored no nodes; the fixture is not exercising anything")
	}

	// A real incremental mutation: a new file with a new symbol.
	addFile(t, root, "added.go", "package p\n\nfunc AddedSymbol() {}\n")

	err := ing.IngestChanged(ctx, root, []string{"added.go"})
	if err == nil {
		t.Fatal("IngestChanged succeeded although the identity write failed; want a loud error")
	}

	// The mutation must not have landed. Under the unsafe ordering the node
	// would be in the graph and the file marked clean, with the identity still
	// naming the previous graph — the exact state that serves stale vectors as
	// ready.
	if after := countNodes(ctx, t, inner); after != before {
		t.Fatalf("node count moved from %d to %d despite the failed identity write: "+
			"the graph was mutated while commit_generation still names the previous graph, "+
			"which is the state a reload would serve as `ready`", before, after)
	}
}

// TestIncremental_SuccessfulMutationAdvancesIdentity is the positive half: a
// committed incremental mutation must move index.commit_generation, or a
// semantic generation built for the previous graph would keep reading `ready`.
// Without this, an identity that never advanced would satisfy the negative test
// above just as well.
func TestIncremental_SuccessfulMutationAdvancesIdentity(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	first, err := store.Metadata(ctx, "index.commit_generation")
	if err != nil {
		t.Fatalf("read identity after full pass: %v", err)
	}
	if first == "" {
		t.Fatal("full pass left index.commit_generation empty")
	}

	addFile(t, root, "added.go", "package p\n\nfunc AddedSymbol() {}\n")
	if err := ing.IngestChanged(ctx, root, []string{"added.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	second, err := store.Metadata(ctx, "index.commit_generation")
	if err != nil {
		t.Fatalf("read identity after incremental: %v", err)
	}
	if second == first {
		t.Fatalf("index.commit_generation did not move across an incremental mutation (%q): "+
			"vectors built for the previous graph would keep reading ready", first)
	}
}

// countNodes reports how many nodes the store holds, so a test can assert that
// a failed pass mutated nothing.
func countNodes(ctx context.Context, t *testing.T, gs graphstore.Graphstore) int {
	t.Helper()
	nodes, err := gs.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	return len(nodes)
}

// addFile writes one new file into the fixture repo.
func addFile(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
