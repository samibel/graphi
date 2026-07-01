package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// TestInMemorySidecarsAreIsolated guards the fix for the in-memory meta sidecar:
// each Ingester created with an empty metaDir must get its OWN shared-cache
// database, not a single process-global one. An unnamed
// "file::memory:?cache=shared" URI resolves to ONE database shared by every such
// connection in the process, so two in-memory Ingesters alive at once (e.g.
// makeEditorClient's primary ingester plus the parser-consistency checker's
// throwaway ingester) would silently share one content cache and
// cross-contaminate. Naming the database per-Ingester keeps the cache shared
// across a single Ingester's pooled connections while isolating Ingesters from
// each other.
func TestInMemorySidecarsAreIsolated(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"a.go": "package a",
		"b.go": "package b",
	})
	ctx := context.Background()

	// First in-memory ingester fully indexes the repo, populating ITS meta cache
	// with a content-hash row per file.
	iA, err := ingest.New(graphstore.NewMemStore(), &stubParser{}, "")
	if err != nil {
		t.Fatalf("ingest.New A: %v", err)
	}
	t.Cleanup(func() { _ = iA.Close() })
	if err := iA.IngestAll(ctx, repo); err != nil {
		t.Fatalf("A IngestAll: %v", err)
	}

	// A second, independent in-memory ingester must observe an EMPTY cache: with
	// a leaked (shared) meta DB it would see A's cached hashes and report zero
	// drift; with proper isolation every file is reported as new/changed.
	iB, err := ingest.New(graphstore.NewMemStore(), &stubParser{}, "")
	if err != nil {
		t.Fatalf("ingest.New B: %v", err)
	}
	t.Cleanup(func() { _ = iB.Close() })

	changed, _, err := iB.DriftSet(ctx, repo)
	if err != nil {
		t.Fatalf("B DriftSet: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("expected 2 changed files against a fresh isolated sidecar, got %d (%v) — the in-memory meta DB leaked across Ingesters", len(changed), changed)
	}
}
