package watch

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/ingest"
)

// trackingParser records the live and peak concurrency observed across its
// ParseFile calls, independent of the pool's own instrumentation, so the cap
// test cross-checks both.
type trackingParser struct {
	mu       sync.Mutex
	cur, max int
	count    int
}

func (p *trackingParser) ParseFile(ctx context.Context, root, rel string) (*ingest.ParsedFile, error) {
	p.mu.Lock()
	p.cur++
	p.count++
	if p.cur > p.max {
		p.max = p.cur
	}
	p.mu.Unlock()
	// Hold the slot briefly to force overlap when more work than workers exists.
	time.Sleep(3 * time.Millisecond)
	p.mu.Lock()
	p.cur--
	p.mu.Unlock()
	return &ingest.ParsedFile{RelPath: rel}, nil
}

// TestPool_ConcurrencyCap is AC-5: concurrent parse calls never exceed the
// configured pool bound; excess work queues rather than spawning unbounded
// goroutines.
func TestPool_ConcurrencyCap(t *testing.T) {
	const workers = 4
	const jobs = 50
	cfg := Config{PoolSize: workers, PoolHardCap: workers}
	pool := NewPool(cfg)
	if pool.Workers() != workers {
		t.Fatalf("Workers() = %d, want %d", pool.Workers(), workers)
	}

	parser := &trackingParser{}
	paths := make([]string, jobs)
	for i := range paths {
		paths[i] = fmt.Sprintf("f%02d.go", i)
	}

	baseline := runtime.NumGoroutine()
	results, err := pool.ParseBatch(context.Background(), parser, "/root", paths)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(results) != jobs {
		t.Fatalf("got %d results, want %d", len(results), jobs)
	}
	if parser.count != jobs {
		t.Fatalf("parsed %d files, want %d", parser.count, jobs)
	}

	if pool.MaxInFlight() > workers {
		t.Fatalf("pool observed max in-flight %d exceeds bound %d", pool.MaxInFlight(), workers)
	}
	if parser.max > workers {
		t.Fatalf("parser observed max concurrency %d exceeds bound %d", parser.max, workers)
	}
	// Sanity: with 50 jobs and 4 workers holding slots, we expect real parallelism.
	if parser.max < 2 {
		t.Fatalf("expected concurrent parsing (max>=2), got %d", parser.max)
	}

	// No goroutine leak: ParseBatch joins all workers before returning.
	settleGoroutines()
	if grew := runtime.NumGoroutine() - baseline; grew > 1 {
		t.Fatalf("goroutine leak: grew by %d after ParseBatch", grew)
	}

	// Results are returned in canonical relPath-sorted order regardless of
	// completion order.
	for i := 1; i < len(results); i++ {
		if results[i-1].RelPath >= results[i].RelPath {
			t.Fatalf("results not sorted at %d: %q >= %q", i, results[i-1].RelPath, results[i].RelPath)
		}
	}
}

// TestPool_ScheduleHookPerturbsCompletionOrder is the AC-2/AC-3 test seam check:
// injecting per-file jitter changes COMPLETION order but the pool still returns
// canonical-sorted results, so downstream determinism is preserved.
func TestPool_ScheduleHookPerturbsCompletionOrder(t *testing.T) {
	cfg := Config{PoolSize: 4, PoolHardCap: 4}
	pool := NewPool(cfg)
	// Reverse-ordered jitter: later-named files return fastest, scrambling
	// completion order relative to input order.
	pool.SetScheduleHook(func(rel string) {
		// crude inverse delay keyed on first char
		d := time.Duration(20-int(rel[len(rel)-4])) * time.Microsecond
		if d < 0 {
			d = 0
		}
		time.Sleep(d)
	})
	parser := &trackingParser{}
	paths := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	results, err := pool.ParseBatch(context.Background(), parser, "/r", paths)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(results) != len(paths) {
		t.Fatalf("got %d, want %d", len(results), len(paths))
	}
	for i := 1; i < len(results); i++ {
		if results[i-1].RelPath >= results[i].RelPath {
			t.Fatalf("pool output not canonical-sorted despite jitter")
		}
	}
}

func settleGoroutines() {
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(2 * time.Millisecond)
	}
}
