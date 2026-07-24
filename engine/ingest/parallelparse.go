package ingest

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
)

// parseUnitsParallel purely parses units with min(GOMAXPROCS, len(units))
// workers and returns results positionally aligned with units (results[k]
// belongs to units[k]). It mutates no graph state — parseUnit is the pure
// SW-101 parse phase — so worker scheduling cannot influence the committed
// graph: the caller applies results serially in the units order exactly as
// before. Units carry no bytes; each worker reads its file through one shared
// root handle inside parseUnit, so resident source is bounded by the pool
// width. onDone, when non-nil, is invoked from the CALLING goroutine (never
// a worker) once per completed unit with the running completion count, the
// unit's index k (units[k]/pf just completed), and its ParsedFile — the seam
// where IngestAll runs the per-file taint analysis and releases the Go AST,
// so progress reporting AND the release keep the single-goroutine contract.
// The first hard parse error cancels the pool and is returned; per-file skip
// diagnostics remain fail-closed non-errors.
func (i *Ingester) parseUnitsParallel(ctx context.Context, root string, units []fileUnit, onDone func(done, k int, pf *ParsedFile)) ([]*ParsedFile, error) {
	results := make([]*ParsedFile, len(units))
	if len(units) == 0 {
		return results, nil
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: open root: %w", err)
	}
	defer rootHandle.Close()
	workers := runtime.GOMAXPROCS(0)
	if i.parseWorkers > 0 {
		workers = i.parseWorkers
	}
	if workers > len(units) {
		workers = len(units)
	}
	if workers <= 1 {
		for k, u := range units {
			pf, err := i.parseUnit(ctx, rootHandle, u)
			if err != nil {
				return nil, err
			}
			results[k] = pf
			if onDone != nil {
				onDone(k+1, k, pf)
			}
		}
		return results, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan int)
	completions := make(chan int)
	var (
		errMu    sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		cancel()
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range work {
				if hook := i.parseScheduleHook(); hook != nil {
					hook(units[k].relPath)
				}
				pf, err := i.parseUnit(ctx, rootHandle, units[k])
				if err != nil {
					fail(err)
					return
				}
				results[k] = pf // disjoint slot per unit; no lock needed
				select {
				case completions <- k:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for k := range units {
			select {
			case work <- k:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(completions)
	}()

	// Drain completions on the calling goroutine: onDone stays serial.
	done := 0
	for k := range completions {
		done++
		if onDone != nil {
			onDone(done, k, results[k])
		}
	}
	errMu.Lock()
	defer errMu.Unlock()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil && done < len(units) {
		return nil, err
	}
	return results, nil
}

// SetParseWorkers overrides the parse-pool width (0 = GOMAXPROCS). Test seam
// for the scheduling-parity fuzz — forcing 1 reproduces the serial path.
func (i *Ingester) SetParseWorkers(n int) { i.parseWorkers = n }

// SetParseScheduleHook installs a test-only hook invoked by each pool worker
// before it parses a unit (same seam engine/watch's pool exposes): jitter
// injected here fuzzes worker interleavings so tests can pin that scheduling
// never reaches the committed bytes.
func (i *Ingester) SetParseScheduleHook(fn func(relPath string)) {
	i.hookMu.Lock()
	i.scheduleHook = fn
	i.hookMu.Unlock()
}

func (i *Ingester) parseScheduleHook() func(string) {
	i.hookMu.Lock()
	defer i.hookMu.Unlock()
	return i.scheduleHook
}
