package conformance_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/watch"
)

// ep017Ops are the four EP-017 canonical operations surfaced by SW-104. Their
// envelopes against the full-parse graph and the incremental-parallel-parse graph
// must be byte-for-byte identical.
var ep017Ops = []string{"notebook-ingest", "watcher-status", "taint-query", "communities"}

// sampleNotebook is a minimal valid nbformat >=4.5 notebook with one python code
// cell, so the SW-100 NotebookParser commits notebook_cell provenance and the
// notebook-ingest envelope is non-trivial.
const sampleNotebook = `{
  "cells": [
    {"cell_type": "markdown", "source": ["# title\n"]},
    {"cell_type": "code", "id": "c1", "source": ["def nb_func():\n", "    return 1\n"]}
  ],
  "metadata": {"kernelspec": {"language": "python"}},
  "nbformat": 4,
  "nbformat_minor": 5
}
`

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func removeFile(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

func newIngester(t *testing.T, store graphstore.Graphstore) *ingest.Ingester {
	t.Helper()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	return ing
}

// snapshot serializes the whole graph to canonical bytes (the engine's own
// byte-stable store snapshot), the unit the full-vs-incremental graph-parity
// assertion compares.
func snapshot(t *testing.T, store graphstore.Graphstore) []byte {
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

// envelope dispatches one EP-017 operation through the SAME single dispatch path
// + shared encoder every surface uses (analysis.Service.Dispatch ->
// analysis.Marshal) and returns the canonical serialized envelope bytes.
func envelope(t *testing.T, store graphstore.Graphstore, op string) []byte {
	t.Helper()
	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(context.Background(), op, analysis.Params{})
	if err != nil {
		t.Fatalf("dispatch %s: %v", op, err)
	}
	b, err := analysis.Marshal(res)
	if err != nil {
		t.Fatalf("marshal %s: %v", op, err)
	}
	return b
}

// buildIncrementalParallel builds a graph by applying the mutation steps
// incrementally through the watcher-driven, bounded-worker-pool parallel parse
// path (watch.Service.Reconcile -> Pool.ParseBatch -> ApplyChangedParsed). A
// schedule hook perturbs worker COMPLETION order so the test exercises arrival
// nondeterminism the canonical-ordered apply must defeat (AC-4 permuted arrival).
func buildIncrementalParallel(t *testing.T, root string, steps []func()) *graphstore.MemStore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ing := newIngester(t, store)
	cfg := watch.Config{DebounceMs: 20, PoolSize: 4, PoolHardCap: 8, ReconcileInterval: time.Hour}
	svc, err := watch.NewService(root, ing, cfg)
	if err != nil {
		t.Fatalf("watch.NewService: %v", err)
	}
	var n int64
	svc.Pool().SetScheduleHook(func(string) {
		// Perturb completion order without changing the input set.
		if atomic.AddInt64(&n, 1)%2 == 0 {
			time.Sleep(time.Millisecond)
		}
	})
	for i, step := range steps {
		step()
		if err := svc.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile step %d: %v", i, err)
		}
	}
	return store
}

// TestFullVsIncremental_ByteParity is the AC-3 conformance gate: a graph built by
// a single full parse and a graph built by an incremental watcher-driven parallel
// parse over the SAME mutation sequence serialize identically, AND every EP-017
// operation's envelope is byte-identical against each graph. This proves P10's
// parallelism did not leak into output.
func TestFullVsIncremental_ByteParity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	steps := []func(){
		func() {
			writeFile(t, root, "a/a.go", "package a\n\nfunc Source() string { return helper() }\nfunc helper() string { return \"x\" }\n")
			writeFile(t, root, "b/b.go", "package b\n\nfunc Sink(s string) {}\n")
		},
		func() {
			writeFile(t, root, "a/a.go", "package a\n\nfunc Source() string { return helper() }\nfunc helper() string { return \"y\" }\nfunc Extra() {}\n")
			writeFile(t, root, "c/c.go", "package c\n\nfunc C() {}\n")
		},
		func() {
			writeFile(t, root, "nb.ipynb", sampleNotebook)
		},
		func() {
			removeFile(t, root, "b/b.go")
		},
	}

	incStore := buildIncrementalParallel(t, root, steps)
	defer incStore.Close()

	// Full parse of the FINAL on-disk state.
	fullStore := graphstore.NewMemStore()
	defer fullStore.Close()
	fullIng := newIngester(t, fullStore)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	// (a) Serialized graphs byte-identical.
	if !bytes.Equal(snapshot(t, incStore), snapshot(t, fullStore)) {
		t.Fatal("full-vs-incremental: serialized graphs are NOT byte-identical")
	}

	// (b) All four EP-017 operations' envelopes byte-identical against each graph.
	for _, op := range ep017Ops {
		inc := envelope(t, incStore, op)
		full := envelope(t, fullStore, op)
		if !bytes.Equal(inc, full) {
			t.Fatalf("full-vs-incremental: %q envelope differs:\n incremental: %s\n full:        %s", op, inc, full)
		}
	}

	// Sanity: the notebook-ingest envelope actually surfaced cells (the harness
	// would otherwise prove parity over an empty payload).
	if !bytes.Contains(envelope(t, fullStore, "notebook-ingest"), []byte(`"cells":[{`)) {
		t.Fatalf("notebook-ingest surfaced no cells; envelope=%s", envelope(t, fullStore, "notebook-ingest"))
	}
}

// TestRepeatRun_Determinism is the AC-4 gate: repeated dispatch of each EP-017
// operation against the same graph (built via the parallel parse path) yields
// byte-identical envelopes run-to-run — no map-iteration / goroutine-order /
// wall-clock dependence.
func TestRepeatRun_Determinism(t *testing.T) {
	root := t.TempDir()
	steps := []func(){
		func() {
			writeFile(t, root, "a/a.go", "package a\n\nfunc F1() string { return F2() }\nfunc F2() string { return \"x\" }\n")
			writeFile(t, root, "b/b.go", "package b\n\nfunc G() {}\n")
			writeFile(t, root, "nb.ipynb", sampleNotebook)
		},
	}
	store := buildIncrementalParallel(t, root, steps)
	defer store.Close()

	for _, op := range ep017Ops {
		first := envelope(t, store, op)
		for i := 0; i < 8; i++ {
			again := envelope(t, store, op)
			if !bytes.Equal(first, again) {
				t.Fatalf("op %q not deterministic across runs:\n run0: %s\n run%d: %s", op, first, i+1, again)
			}
		}
	}
}
