package ingest_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// fuzzRepo builds a fixture large enough that worker interleavings actually
// vary: cross-referencing files across several directories.
func fuzzRepo(t *testing.T) string {
	t.Helper()
	files := make(map[string]string)
	for d := 0; d < 4; d++ {
		for f := 0; f < 8; f++ {
			name := fmt.Sprintf("d%d/f%d.go", d, f)
			body := fmt.Sprintf("package d%d\n", d)
			if f > 0 {
				body += fmt.Sprintf("use:d%d/f%d.go\n", d, f-1)
			}
			files[name] = body
		}
	}
	return writeRepo(t, files)
}

// snapshotOf runs a full ingest into a fresh SQLite store and returns the
// deterministic snapshot bytes.
func snapshotOf(t *testing.T, root string, configure func(*ingest.Ingester)) []byte {
	t.Helper()
	dir := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	if configure != nil {
		configure(i)
	}
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	snap := filepath.Join(dir, "graph.snap")
	if err := store.Snapshot(context.Background(), snap); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// TestParallelParse_SchedulingParity pins the core SW-101 invariant for the
// full-ingest pool: jittered worker scheduling must never reach the committed
// bytes. Reference = forced-serial run; 10 jittered parallel runs must match
// it byte-for-byte.
func TestParallelParse_SchedulingParity(t *testing.T) {
	root := fuzzRepo(t)
	want := snapshotOf(t, root, func(i *ingest.Ingester) { i.SetParseWorkers(1) })

	for run := 0; run < 10; run++ {
		rng := rand.New(rand.NewSource(int64(run)))
		var mu sync.Mutex
		got := snapshotOf(t, root, func(i *ingest.Ingester) {
			i.SetParseWorkers(4)
			i.SetParseScheduleHook(func(string) {
				mu.Lock()
				d := time.Duration(rng.Intn(3)) * time.Millisecond
				mu.Unlock()
				time.Sleep(d)
			})
		})
		if string(got) != string(want) {
			t.Fatalf("run %d: parallel snapshot differs from serial reference", run)
		}
	}
}

// TestParallelParse_WorkerCap pins that in-flight parses never exceed the
// configured pool width.
func TestParallelParse_WorkerCap(t *testing.T) {
	root := fuzzRepo(t)
	const cap = 3
	var inFlight, maxSeen atomic.Int64

	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	i.SetParseWorkers(cap)
	i.SetParseScheduleHook(func(string) {
		cur := inFlight.Add(1)
		for {
			m := maxSeen.Load()
			if cur <= m || maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
	})
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if maxSeen.Load() > cap {
		t.Fatalf("max in-flight parses = %d, want <= %d", maxSeen.Load(), cap)
	}
}

// TestParallelParse_CancelAborts pins that a parent-context cancel during the
// parallel phase aborts the pass with an error and commits nothing.
func TestParallelParse_CancelAborts(t *testing.T) {
	root := fuzzRepo(t)
	dir := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	i := newIngester(t, store, &stubParser{})
	i.SetParseWorkers(4)
	var once sync.Once
	i.SetParseScheduleHook(func(string) {
		once.Do(cancel)
		time.Sleep(time.Millisecond)
	})
	if err := i.IngestAll(ctx, root); err == nil {
		t.Fatal("IngestAll with cancelled context must fail")
	}
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("cancelled pass committed %d nodes, want 0", len(nodes))
	}
}

// TestParallelParse_ProgressMonotonicWithPaths pins the pool-driven progress
// contract: Done increments by one per completion up to Total, every per-file
// event carries a real path, and delivery is single-goroutine (the existing
// notifyProgress state is unsynchronized by design).
func TestParallelParse_ProgressMonotonicWithPaths(t *testing.T) {
	root := fuzzRepo(t)
	store := graphstore.NewMemStore()
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	i.SetParseWorkers(4)

	var parseDones []int
	paths := make(map[string]bool)
	i.WithProgress(func(ev ingest.ProgressEvent) {
		if ev.Phase == ingest.PhaseParse && ev.Done > 0 {
			parseDones = append(parseDones, ev.Done)
			if ev.Path == "" {
				t.Errorf("per-file parse event without Path: %+v", ev)
			}
			paths[ev.Path] = true
		}
	})
	if err := i.IngestAll(context.Background(), root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if len(parseDones) != 32 {
		t.Fatalf("parse events = %d, want 32", len(parseDones))
	}
	for k, d := range parseDones {
		if d != k+1 {
			t.Fatalf("Done sequence not monotonic: %v", parseDones)
		}
	}
	if len(paths) != 32 {
		t.Fatalf("distinct paths = %d, want 32 (each file reported once)", len(paths))
	}
}
