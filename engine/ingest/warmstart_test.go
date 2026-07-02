package ingest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// TestCanWarmStart_Lifecycle pins the warm-start gate: a fresh store is cold,
// a completed full pass certifies it, and a semantics stamp from a DIFFERENT
// binary generation makes it cold again — content hashes cannot see a binary
// upgrade, only the stamp can.
func TestCanWarmStart_Lifecycle(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if files, ok, err := ing.CanWarmStart(ctx); err != nil || ok || files != 0 {
		t.Fatalf("fresh store: CanWarmStart = (%d, %v, %v), want (0, false, nil)", files, ok, err)
	}

	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	files, ok, err := ing.CanWarmStart(ctx)
	if err != nil || !ok || files == 0 {
		t.Fatalf("after full pass: CanWarmStart = (%d, %v, %v), want (>0, true, nil)", files, ok, err)
	}

	// Simulate a store written by an older binary: rewrite the stamp.
	if _, err := ing.MetaDB().ExecContext(ctx, "UPDATE ingest_semantics SET value = '0' WHERE key = 'semantics_version'"); err != nil {
		t.Fatalf("tamper stamp: %v", err)
	}
	if _, ok, err := ing.CanWarmStart(ctx); err != nil || ok {
		t.Fatalf("stale semantics stamp: CanWarmStart ok = %v (err %v), want false — an upgraded binary must re-index", ok, err)
	}

	// A full pass under the current binary re-certifies.
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("re-IngestAll: %v", err)
	}
	if _, ok, err := ing.CanWarmStart(ctx); err != nil || !ok {
		t.Fatalf("after re-index: CanWarmStart ok = %v (err %v), want true", ok, err)
	}
}

// TestIncrementalPaths_SilentWithoutExplicitProgress pins watcher safety: even
// with a WithProgress callback attached to the Ingester, the plain incremental
// entry points emit nothing — progress on the incremental path exists ONLY via
// the explicitly-scoped IngestChangedWithProgress.
func TestIncrementalPaths_SilentWithoutExplicitProgress(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	var events []ingest.ProgressEvent
	ing.WithProgress(func(ev ingest.ProgressEvent) { events = append(events, ev) })
	rewrite(t, root, "util/util.go", "package util\n\nfunc Answer() int { return 43 }\n")
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("IngestChanged emitted %d events through the ATTACHED callback, want 0 (background passes must stay silent): %+v", len(events), events)
	}
}

// TestIngestChangedWithProgress_Events pins the scoped progress contract of
// the warm-start delta pass: parse events carry the current path and a known
// total (the changed files plus their cascade), phases stay ordered, and done
// arrives exactly once with Done == Total.
func TestIngestChangedWithProgress_Events(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	rewrite(t, root, "util/util.go", "package util\n\nfunc Answer() int { return 43 }\n")
	var events []ingest.ProgressEvent
	if err := ing.IngestChangedWithProgress(ctx, root, []string{"util/util.go"}, func(ev ingest.ProgressEvent) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("IngestChangedWithProgress: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no progress events delivered")
	}

	order := map[ingest.Phase]int{ingest.PhaseParse: 0, ingest.PhaseLink: 1, ingest.PhaseResolve: 2, ingest.PhaseDone: 3}
	prev, dones, total := -1, 0, 0
	sawPath := false
	for _, ev := range events {
		ord, known := order[ev.Phase]
		if !known {
			t.Fatalf("unexpected phase %q in incremental progress: %+v", ev.Phase, events)
		}
		if ord < prev {
			t.Fatalf("phase %q after a later phase: %+v", ev.Phase, events)
		}
		prev = ord
		if ev.Phase == ingest.PhaseParse {
			total = ev.Total
			sawPath = sawPath || ev.Path != ""
		}
		if ev.Phase == ingest.PhaseDone {
			dones++
			if ev.Done != ev.Total || ev.Done == 0 {
				t.Fatalf("done event = %+v, want Done == Total > 0", ev)
			}
		}
	}
	if dones != 1 {
		t.Fatalf("done emitted %d times, want exactly once", dones)
	}
	// The cascade pulls in main.go (imports util), so the delta is 2 files.
	if total != 2 {
		t.Fatalf("parse Total = %d, want 2 (changed file + cascaded importer)", total)
	}
	if !sawPath {
		t.Fatal("no parse event carried the current file path")
	}
}

// TestWarmDeltaMatchesFullReindex is the warm-start correctness anchor: a
// drift-then-incremental update of an existing store must produce a graph
// byte-identical to a fresh full index of the final state — tiers, evidence,
// everything (the SW-101 convergence the warm start leans on).
func TestWarmDeltaMatchesFullReindex(t *testing.T) {
	ctx := context.Background()
	finalUtil := "package util\n\nfunc Answer() int { return 43 }\n\nfunc Extra() int { return Answer() }\n"

	// Path A: full pass, mutate, drift, incremental delta.
	storeA := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeA.Close() })
	ingA := newIngester(t, storeA, parse.NewDefaultRegistry())
	rootA := writeRepo(t, typeresolveFixture())
	if err := ingA.IngestAll(ctx, rootA); err != nil {
		t.Fatalf("IngestAll A: %v", err)
	}
	rewrite(t, rootA, "util/util.go", finalUtil)
	changed, deleted, err := ingA.DriftSetWithProgress(ctx, rootA, nil)
	if err != nil {
		t.Fatalf("DriftSetWithProgress: %v", err)
	}
	if len(changed) != 1 || changed[0] != "util/util.go" || len(deleted) != 0 {
		t.Fatalf("drift = (%v, %v), want ([util/util.go], [])", changed, deleted)
	}
	if err := ingA.IngestChangedWithProgress(ctx, rootA, append(changed, deleted...), func(ingest.ProgressEvent) {}); err != nil {
		t.Fatalf("IngestChangedWithProgress: %v", err)
	}

	// Path B: fresh full index of the final state.
	files := typeresolveFixture()
	files["util/util.go"] = finalUtil
	storeB := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeB.Close() })
	ingB := newIngester(t, storeB, parse.NewDefaultRegistry())
	rootB := writeRepo(t, files)
	if err := ingB.IngestAll(ctx, rootB); err != nil {
		t.Fatalf("IngestAll B: %v", err)
	}

	a, b := dumpGraph(t, storeA), dumpGraph(t, storeB)
	if a != b {
		t.Errorf("warm delta diverges from a fresh full index:\n--- warm ---\n%s\n--- full ---\n%s", a, b)
	}
	if !strings.Contains(a, "confirmed") {
		t.Error("parity fixture produced no confirmed edges — the invariant would be vacuous")
	}
}
