package watch_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/watch"
)

// stubParser produces one node per file so the watch integration tests exercise
// the real ingest apply path without depending on a heavy language grammar.
type stubParser struct{}

func (stubParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	n, err := model.NewNode("function", "pkg/fn"+filepath.Base(path), path, 1, 1)
	if err != nil {
		return nil, err
	}
	return &parse.ParseResult{
		Meta:  parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes: []model.Node{n},
	}, nil
}

func newIng(t *testing.T, store graphstore.Graphstore) *ingest.Ingester {
	t.Helper()
	i, err := ingest.New(store, stubParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = i.Close() })
	return i
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nodeCount(t *testing.T, store graphstore.Graphstore) int {
	t.Helper()
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	return len(nodes)
}

// TestService_RefreshesGraphWithoutReindex is AC-1: with the daemon watching a
// workspace, creating/modifying/deleting a tracked file refreshes the
// incremental graph with NO explicit re-index command.
func TestService_RefreshesGraphWithoutReindex(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	store := graphstore.NewMemStore()
	defer store.Close()
	ing := newIng(t, store)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("seed IngestAll: %v", err)
	}
	if got := nodeCount(t, store); got != 1 {
		t.Fatalf("seed nodes = %d, want 1", got)
	}

	applied := make(chan int, 8)
	cfg := watch.Config{DebounceMs: 20, PoolSize: 2, PoolHardCap: 4, ReconcileInterval: time.Hour}
	svc, err := watch.NewService(root, ing, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetApplyHook(func(int) { applied <- 1 })
	svc.SetErrorHook(func(e error) { t.Errorf("watch error: %v", e) })
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	waitApply := func() {
		select {
		case <-applied:
		case <-time.After(5 * time.Second):
			t.Fatal("graph was not refreshed within timeout")
		}
	}

	// Create a new tracked file → graph grows to 2 nodes.
	writeFile(t, root, "b.go", "package b\n")
	waitApply()
	if got := nodeCount(t, store); got != 2 {
		t.Fatalf("after create, nodes = %d, want 2", got)
	}

	// Delete a file → graph shrinks back to 1 node.
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	waitApply()
	if got := nodeCount(t, store); got != 1 {
		t.Fatalf("after delete, nodes = %d, want 1", got)
	}
}

// TestService_ByteIdenticalThroughService is AC-2 end-to-end through the watch
// Service: the graph it produces equals a full single-threaded parse.
func TestService_ByteIdenticalThroughService(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")

	store := graphstore.NewMemStore()
	defer store.Close()
	ing := newIng(t, store)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("seed: %v", err)
	}

	applied := make(chan int, 16)
	cfg := watch.Config{DebounceMs: 20, PoolSize: 3, PoolHardCap: 8, ReconcileInterval: time.Hour}
	svc, err := watch.NewService(root, ing, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetApplyHook(func(int) { applied <- 1 })
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// A burst of changes.
	writeFile(t, root, "a.go", "package a\n//edit\n")
	writeFile(t, root, "c.go", "package c\n")
	writeFile(t, root, "d.go", "package d\n")
	select {
	case <-applied:
	case <-time.After(5 * time.Second):
		t.Fatal("no apply observed")
	}
	// Drain any trailing applies so the on-disk state is fully reflected.
	drain(applied, 500*time.Millisecond)

	// Full reindex of the resulting on-disk state.
	full := graphstore.NewMemStore()
	defer full.Close()
	iFull := newIng(t, full)
	if err := iFull.IngestAll(ctx, root); err != nil {
		t.Fatalf("full: %v", err)
	}
	if !bytes.Equal(snap(t, store), snap(t, full)) {
		t.Fatalf("watch-service graph not byte-identical to full parse")
	}
}

// TestService_ReconcileRepairsLostEvent covers R-2: when a filesystem event is
// lost (here simulated by mutating disk and invoking Reconcile directly), the
// periodic reconcile converges the graph to the correct state.
func TestService_ReconcileRepairsLostEvent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	store := graphstore.NewMemStore()
	defer store.Close()
	ing := newIng(t, store)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := watch.Config{DebounceMs: 20, PoolSize: 2, PoolHardCap: 4, ReconcileInterval: time.Hour}
	svc, err := watch.NewService(root, ing, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// Do NOT Start the watcher: simulate that fsnotify missed these events.
	writeFile(t, root, "b.go", "package b\n")
	writeFile(t, root, "c.go", "package c\n")

	// Reconcile must detect the drift and converge.
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := nodeCount(t, store); got != 3 {
		t.Fatalf("after reconcile, nodes = %d, want 3", got)
	}

	// Byte-identical to a full parse.
	full := graphstore.NewMemStore()
	defer full.Close()
	iFull := newIng(t, full)
	if err := iFull.IngestAll(ctx, root); err != nil {
		t.Fatalf("full: %v", err)
	}
	if !bytes.Equal(snap(t, store), snap(t, full)) {
		t.Fatalf("reconciled graph not byte-identical to full parse")
	}
}

func drain(ch <-chan int, quiet time.Duration) {
	for {
		select {
		case <-ch:
		case <-time.After(quiet):
			return
		}
	}
}

func snap(t *testing.T, store graphstore.Graphstore) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap")
	if err := store.Snapshot(context.Background(), p); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read snap: %v", err)
	}
	return b
}
