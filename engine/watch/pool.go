package watch

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/samibel/graphi/engine/ingest"
)

// Parser is the inward dependency the pool needs for the PURE parse phase: it
// reads and parses one file into an isolated ingest.ParsedFile with no
// graphstore mutation. *ingest.Ingester satisfies it. Keeping this a narrow
// interface lets the determinism/cap tests inject a fake that perturbs timing.
type Parser interface {
	ParseFile(ctx context.Context, root, relPath string) (*ingest.ParsedFile, error)
}

// Pool is a bounded worker-pool that parses files in parallel into isolated
// results. It NEVER spawns more than its configured worker count regardless of
// how many files are submitted — excess work queues on an internal channel. The
// pool holds no graphstore handle; it only drives the pure parse phase.
//
// Determinism note: the pool returns results in canonical relPath-sorted order,
// not completion order, and the downstream serialized apply re-sorts by the
// walked unit order anyway — so neither the pool's output order nor worker
// scheduling can influence the resulting graph.
type Pool struct {
	workers int

	// onParse is an optional test seam invoked inside each worker immediately
	// before ParseFile. Tests use it to inject scheduling jitter so the
	// scheduling-fuzz property test exercises randomized COMPLETION order, not
	// merely randomized input order. Nil in production (zero overhead).
	onParse func(relPath string)

	// inFlight is the live count of concurrently-running parse calls; maxInFlight
	// is the high-water mark. Both are the AC-5 concurrency-cap instrumentation.
	inFlight    int64
	maxInFlight int64
}

// NewPool constructs a pool with the bounded worker count derived from cfg.
func NewPool(cfg Config) *Pool {
	return &Pool{workers: cfg.Workers()}
}

// Workers returns the bounded worker count.
func (p *Pool) Workers() int { return p.workers }

// MaxInFlight returns the high-water mark of concurrently-running parse calls
// observed across this pool's lifetime. The AC-5 cap test asserts it never
// exceeds Workers().
func (p *Pool) MaxInFlight() int { return int(atomic.LoadInt64(&p.maxInFlight)) }

// SetScheduleHook installs the test-only completion-order jitter hook. It must
// only be called before ParseBatch (it is not concurrency-safe with a running
// batch). Passing nil clears it.
func (p *Pool) SetScheduleHook(fn func(relPath string)) { p.onParse = fn }

// ParseBatch parses every path in the batch through the bounded pool and returns
// the isolated results in canonical relPath-sorted order. Paths are processed by
// exactly p.workers goroutines; any excess queues on the job channel rather than
// spawning new goroutines. On the first hard parse error (a genuine parse
// failure, not a fail-closed resource skip) the batch is cancelled and that
// error returned. ctx cancellation aborts the batch.
func (p *Pool) ParseBatch(ctx context.Context, parser Parser, root string, paths []string) ([]*ingest.ParsedFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string, len(paths))
	for _, pth := range paths {
		jobs <- pth
	}
	close(jobs)

	results := make([]*ingest.ParsedFile, 0, len(paths))
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)

	worker := func() {
		defer wg.Done()
		for relPath := range jobs {
			if ctx.Err() != nil {
				return
			}
			// AC-5 instrumentation: track concurrent parse depth.
			cur := atomic.AddInt64(&p.inFlight, 1)
			for {
				max := atomic.LoadInt64(&p.maxInFlight)
				if cur <= max || atomic.CompareAndSwapInt64(&p.maxInFlight, max, cur) {
					break
				}
			}
			if p.onParse != nil {
				p.onParse(relPath)
			}
			pf, err := parser.ParseFile(ctx, root, relPath)
			atomic.AddInt64(&p.inFlight, -1)

			mu.Lock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				continue
			}
			if pf != nil {
				results = append(results, pf)
			}
			mu.Unlock()
		}
	}

	wg.Add(p.workers)
	for w := 0; w < p.workers; w++ {
		go worker()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Canonical order: sort by relPath so the pool's output is independent of
	// worker completion order (defense in depth — the serialized apply re-sorts
	// by walked unit order regardless).
	sort.Slice(results, func(a, b int) bool { return results[a].RelPath < results[b].RelPath })
	return results, nil
}
