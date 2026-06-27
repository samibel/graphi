package ingest_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// parseAll is a helper that purely parses the given changed paths via the
// SW-101 ParseFile entry point, returning the isolated results in a caller-chosen
// order (the order does NOT affect the apply — that is the property under test).
func parseAll(t *testing.T, i *ingest.Ingester, root string, paths []string) []*ingest.ParsedFile {
	t.Helper()
	out := make([]*ingest.ParsedFile, 0, len(paths))
	for _, p := range paths {
		// Mirror the Service: a path that no longer exists on disk is a deletion,
		// not a parse job — the pool only parses existing files.
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			continue
		}
		pf, err := i.ParseFile(context.Background(), root, p)
		if err != nil {
			t.Fatalf("ParseFile %s: %v", p, err)
		}
		if pf != nil {
			out = append(out, pf)
		}
	}
	return out
}

func snapshotBytes(t *testing.T, store graphstore.Graphstore) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap")
	if err := store.Snapshot(context.Background(), p); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// TestApplyChangedParsed_ByteIdenticalToFull is the SW-101 KEY AC (AC-2):
// applying pre-computed parse results through ApplyChangedParsed yields a graph
// byte-identical to a full single-threaded IngestAll of the same on-disk state.
func TestApplyChangedParsed_ByteIdenticalToFull(t *testing.T) {
	ctx := context.Background()

	storeInc := graphstore.NewMemStore()
	defer storeInc.Close()
	iInc := newIngester(t, storeInc, &stubParser{})

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go",
		"c.go": "package c\n",
	})
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Mutate: edit a.go, add d.go, delete c.go, edit b.go.
	mustWrite(t, repo, "a.go", "package a\n//changed\n")
	mustWrite(t, repo, "d.go", "package d\nuse:b.go\n")
	if err := os.Remove(filepath.Join(repo, "c.go")); err != nil {
		t.Fatalf("rm c.go: %v", err)
	}
	mustWrite(t, repo, "b.go", "package b\nuse:a.go\n//changed\n")

	// Parallel path: pure-parse the changed set, apply (deletes carried in the
	// changed slice).
	changed := []string{"a.go", "b.go", "c.go", "d.go"}
	parsed := parseAll(t, iInc, repo, changed)
	if err := iInc.ApplyChangedParsed(ctx, repo, changed, parsed); err != nil {
		t.Fatalf("ApplyChangedParsed: %v", err)
	}

	// Full single-threaded reindex of the SAME on-disk state.
	storeFull := graphstore.NewMemStore()
	defer storeFull.Close()
	iFull := newIngester(t, storeFull, &stubParser{})
	if err := iFull.IngestAll(ctx, repo); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	if !bytes.Equal(snapshotBytes(t, storeInc), snapshotBytes(t, storeFull)) {
		t.Fatalf("parallel ApplyChangedParsed is NOT byte-identical to full parse")
	}
}

// TestApplyChangedParsed_CompletionOrderIndependent is AC-3: feeding the SAME
// parse results in many randomized orders produces an identical graph
// serialization every run (the apply re-sorts to canonical path order).
func TestApplyChangedParsed_CompletionOrderIndependent(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"a.go": "package a\nuse:b.go",
		"b.go": "package b\nuse:c.go",
		"c.go": "package c\n",
		"d.go": "package d\nuse:a.go",
		"e.go": "package e\n",
	}
	repo := writeRepo(t, files)
	changed := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}

	var golden []byte
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20; iter++ {
		store := graphstore.NewMemStore()
		i := newIngester(t, store, &stubParser{})
		if err := i.IngestAll(ctx, repo); err != nil {
			t.Fatalf("IngestAll: %v", err)
		}
		parsed := parseAll(t, i, repo, changed)
		// Shuffle the result-arrival order to simulate randomized worker
		// completion order.
		rng.Shuffle(len(parsed), func(a, b int) { parsed[a], parsed[b] = parsed[b], parsed[a] })
		// Also shuffle the changed slice order.
		order := append([]string(nil), changed...)
		rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
		if err := i.ApplyChangedParsed(ctx, repo, order, parsed); err != nil {
			t.Fatalf("ApplyChangedParsed: %v", err)
		}
		snap := snapshotBytes(t, store)
		if golden == nil {
			golden = snap
		} else if !bytes.Equal(golden, snap) {
			t.Fatalf("iteration %d: serialization differs under shuffled completion order", iter)
		}
		store.Close()
	}
}

// TestApplyChangedParsed_FuzzByteIdentical is the scheduling-fuzz property test
// (AC-2/AC-3): for randomized change-sets over a fixture corpus, applying through
// the parallel path with randomized result-arrival order is byte-identical to a
// full single-threaded parse of the resulting on-disk state, every seed.
func TestApplyChangedParsed_FuzzByteIdentical(t *testing.T) {
	ctx := context.Background()

	const nFiles = 12
	base := make(map[string]string, nFiles)
	names := make([]string, nFiles)
	for k := 0; k < nFiles; k++ {
		name := fmt.Sprintf("f%02d.go", k)
		names[k] = name
		// Each file uses the next one, forming a dependency chain so the cascade
		// is exercised.
		body := fmt.Sprintf("package f%02d\n", k)
		if k+1 < nFiles {
			body += fmt.Sprintf("use:f%02d.go\n", k+1)
		}
		base[name] = body
	}
	repo := writeRepo(t, base)

	storeInc := graphstore.NewMemStore()
	defer storeInc.Close()
	iInc := newIngester(t, storeInc, &stubParser{})
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("seed IngestAll: %v", err)
	}

	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))

		// Build a randomized change-set: edits, adds, and deletes.
		changedSet := map[string]struct{}{}
		for k := 0; k < nFiles; k++ {
			switch rng.Intn(3) {
			case 0: // edit
				name := names[k]
				mustWrite(t, repo, name, base[name]+fmt.Sprintf("//edit-%d\n", seed))
				changedSet[name] = struct{}{}
			case 1: // delete (then it may be re-added by a later add path)
				name := names[k]
				_ = os.Remove(filepath.Join(repo, name))
				changedSet[name] = struct{}{}
			}
		}
		// Occasionally add a brand-new file.
		if rng.Intn(2) == 0 {
			extra := fmt.Sprintf("x%02d.go", seed)
			mustWrite(t, repo, extra, fmt.Sprintf("package x%02d\nuse:f00.go\n", seed))
			changedSet[extra] = struct{}{}
		}
		// Restore any deleted base files for the NEXT iteration's determinism by
		// re-creating them when absent is NOT done here — each seed builds on the
		// prior on-disk state, which is exactly the realistic incremental scenario.

		changed := make([]string, 0, len(changedSet))
		for p := range changedSet {
			changed = append(changed, p)
		}
		sort.Strings(changed)
		if len(changed) == 0 {
			continue
		}

		parsed := parseAll(t, iInc, repo, changed)
		rng.Shuffle(len(parsed), func(a, b int) { parsed[a], parsed[b] = parsed[b], parsed[a] })
		if err := iInc.ApplyChangedParsed(ctx, repo, changed, parsed); err != nil {
			t.Fatalf("seed %d ApplyChangedParsed: %v", seed, err)
		}

		// Full reindex of the resulting on-disk state in a fresh store.
		storeFull := graphstore.NewMemStore()
		iFull := newIngester(t, storeFull, &stubParser{})
		if err := iFull.IngestAll(ctx, repo); err != nil {
			t.Fatalf("seed %d full IngestAll: %v", seed, err)
		}
		if !bytes.Equal(snapshotBytes(t, storeInc), snapshotBytes(t, storeFull)) {
			t.Fatalf("seed %d: parallel apply diverged from full parse", seed)
		}
		storeFull.Close()
	}
}
