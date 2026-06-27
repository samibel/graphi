package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/samibel/graphi/engine/ingest"
)

// Indexer is the inward dependency the watch Service drives. *ingest.Ingester
// satisfies it. The Service NEVER mutates the graph directly; it parses in the
// bounded pool (ParseFile) and applies through the serialized canonical path
// (ApplyChangedParsed), with DriftSet powering the reconcile backstop.
type Indexer interface {
	ParseFile(ctx context.Context, root, relPath string) (*ingest.ParsedFile, error)
	ApplyChangedParsed(ctx context.Context, root string, changed []string, parsed []*ingest.ParsedFile) error
	DriftSet(ctx context.Context, root string) (changed, deleted []string, err error)
}

// Service ties the filesystem watcher, debounce/coalesce layer, bounded
// worker-pool, and serialized canonical apply together for one workspace root.
// It refreshes the incremental graph in response to on-disk changes with NO
// explicit re-index command (AC-1), coalesces bursts (AC-4), parses in parallel
// under a hard concurrency bound (AC-5), and applies results in canonical order
// so the graph stays byte-identical to a full single-threaded parse (AC-2/AC-3).
// A periodic reconcile repairs drift from any lost fsnotify event.
type Service struct {
	root string
	cfg  Config
	idx  Indexer
	pool *Pool

	w  *watcher
	co *coalescer

	// applyMu serializes the merge/apply phase: it is the single-writer guarantee
	// that the only graph-mutating authority runs one batch at a time, so a
	// reconcile pass and a debounced batch can never interleave their applies.
	applyMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// onError, when set, receives non-fatal apply/reconcile errors for
	// observability. Nil by default (errors are swallowed — the watcher is a
	// best-effort freshness loop, and reconcile is the eventual-consistency net).
	onError func(error)

	// onApply, when set, is invoked after each successful apply with the changed
	// path count. Test seam used to observe graph-refresh completion (AC-1)
	// without polling wall-clock.
	onApply func(changed int)
}

// NewService constructs a Service for root using idx and the (normalized) cfg.
func NewService(root string, idx Indexer, cfg Config) (*Service, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("watch: abs root: %w", err)
	}
	abs = filepath.Clean(abs)
	cfg = cfg.Normalize()
	return &Service{
		root: abs,
		cfg:  cfg,
		idx:  idx,
		pool: NewPool(cfg),
	}, nil
}

// Pool exposes the bounded pool (for the AC-5 concurrency-cap assertion).
func (s *Service) Pool() *Pool { return s.pool }

// SetErrorHook installs an observability hook for non-fatal errors. Call before Start.
func (s *Service) SetErrorHook(fn func(error)) { s.onError = fn }

// SetApplyHook installs a post-apply observability hook. Call before Start.
func (s *Service) SetApplyHook(fn func(changed int)) { s.onApply = fn }

// Start begins watching. It registers the fsnotify watcher, arms the coalescer,
// and launches the periodic reconcile loop. Start does NOT perform an initial
// full index — the caller is expected to have already indexed the root (the
// daemon ingests on track); the watcher keeps it fresh from there.
func (s *Service) Start(parent context.Context) error {
	s.ctx, s.cancel = context.WithCancel(parent)
	s.co = newCoalescer(s.cfg.Debounce(), s.handleBatch)

	w, err := newWatcher(s.root, func(ev Event) { s.co.Add(ev.RelPath) })
	if err != nil {
		s.cancel()
		return err
	}
	s.w = w

	s.wg.Add(1)
	go s.reconcileLoop()
	return nil
}

// Stop tears down the watcher, flushes any pending batch, and waits for the
// reconcile loop to exit. It is idempotent.
func (s *Service) Stop() error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	var err error
	if s.w != nil {
		err = s.w.Close()
	}
	if s.co != nil {
		s.co.Stop()
	}
	s.wg.Wait()
	return err
}

// handleBatch is the coalescer fire callback: it resolves the final on-disk
// state of each coalesced path, parses the survivors in the bounded pool, and
// applies the merged result in canonical order. It runs on the coalescer's timer
// goroutine; applyMu serializes it against reconcile.
func (s *Service) handleBatch(paths []string) {
	if s.ctx.Err() != nil {
		return
	}
	if err := s.applyPaths(s.ctx, paths); err != nil && s.onError != nil {
		s.onError(err)
	}
}

// applyPaths partitions paths into existing (to parse) and missing (deletions),
// parses the existing set in the bounded pool, and applies the merged result.
// This is where "create-then-delete within the window nets to delete" resolves:
// a path that no longer exists on disk is treated as a deletion regardless of
// the raw event sequence that produced it.
func (s *Service) applyPaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var toParse, deleted []string
	for _, p := range paths {
		abs := filepath.Join(s.root, filepath.FromSlash(p))
		info, err := os.Stat(abs)
		switch {
		case err == nil && info.IsDir():
			// A directory path is not a file change; its children surface as their
			// own events / via reconcile.
			continue
		case err == nil:
			toParse = append(toParse, p)
		default:
			// Missing (deleted/renamed-away) — resolve to a delete.
			deleted = append(deleted, p)
		}
	}

	parsed, err := s.pool.ParseBatch(ctx, s.idx, s.root, toParse)
	if err != nil {
		return err
	}

	// changed = parseable survivors (untracked file types yield no result and are
	// dropped) ∪ deletions. Deterministically ordered for clarity; the serialized
	// apply re-sorts by walked unit order regardless.
	changedSet := make(map[string]struct{}, len(parsed)+len(deleted))
	for _, pf := range parsed {
		changedSet[pf.RelPath] = struct{}{}
	}
	for _, d := range deleted {
		changedSet[d] = struct{}{}
	}
	if len(changedSet) == 0 {
		return nil
	}
	changed := make([]string, 0, len(changedSet))
	for p := range changedSet {
		changed = append(changed, p)
	}
	sort.Strings(changed)

	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if err := s.idx.ApplyChangedParsed(ctx, s.root, changed, parsed); err != nil {
		return err
	}
	if s.onApply != nil {
		s.onApply(len(changed))
	}
	return nil
}

// reconcileLoop periodically rescans the workspace and repairs any drift between
// on-disk content and the cache — the safety net for fsnotify events lost on
// network mounts, under high-volume bursts, or to macOS coalescing.
func (s *Service) reconcileLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			if err := s.Reconcile(s.ctx); err != nil && s.onError != nil {
				s.onError(err)
			}
		}
	}
}

// Reconcile runs one drift-repair pass: it computes the changed/deleted set from
// on-disk hashes vs the cache and applies it through the same bounded-pool +
// serialized-apply path. Exposed for the reconcile test and for an explicit
// caller-driven sync. A no-op when there is no drift.
func (s *Service) Reconcile(ctx context.Context) error {
	changed, deleted, err := s.idx.DriftSet(ctx, s.root)
	if err != nil {
		return err
	}
	if len(changed) == 0 && len(deleted) == 0 {
		return nil
	}

	parsed, err := s.pool.ParseBatch(ctx, s.idx, s.root, changed)
	if err != nil {
		return err
	}
	all := make([]string, 0, len(changed)+len(deleted))
	all = append(all, changed...)
	all = append(all, deleted...)
	sort.Strings(all)

	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if err := s.idx.ApplyChangedParsed(ctx, s.root, all, parsed); err != nil {
		return err
	}
	if s.onApply != nil {
		s.onApply(len(all))
	}
	return nil
}
