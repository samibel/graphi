package watch

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// IndexerFactory produces the Indexer (and an optional cleanup) for a workspace
// root the first time it is tracked. It lets the daemon own per-workspace store
// + ingester construction while the watch Manager owns watcher lifecycle.
type IndexerFactory func(root string) (idx Indexer, cleanup func(), err error)

// Manager owns a set of per-workspace watch Services keyed by workspace id. It
// structurally satisfies surfaces/daemon.WatchManager (StartWatch/StopWatch), so
// the daemon can drive filesystem watching without surfaces depending on this
// engine package directly (the dependency is injected by cmd). It is safe for
// concurrent use.
type Manager struct {
	factory IndexerFactory
	cfg     Config

	mu       sync.Mutex
	services map[string]*managed
}

type managed struct {
	svc     *Service
	cleanup func()
}

// NewManager constructs a watch Manager. factory builds the Indexer for each
// newly tracked root; cfg tunes every Service it starts.
func NewManager(factory IndexerFactory, cfg Config) *Manager {
	return &Manager{
		factory:  factory,
		cfg:      cfg.Normalize(),
		services: make(map[string]*managed),
	}
}

// StartWatch begins watching root under id. Idempotent: re-tracking an already
// watched id is a no-op.
func (m *Manager) StartWatch(id, root string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[id]; ok {
		return nil
	}
	idx, cleanup, err := m.factory(root)
	if err != nil {
		return fmt.Errorf("watch: build indexer for %s: %w", root, err)
	}
	svc, err := NewService(root, idx, m.cfg)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return err
	}
	if err := svc.Start(context.Background()); err != nil {
		if cleanup != nil {
			cleanup()
		}
		return err
	}
	m.services[id] = &managed{svc: svc, cleanup: cleanup}
	return nil
}

// StopWatch stops and forgets the watcher for id. No-op if absent.
func (m *Manager) StopWatch(id string) {
	m.mu.Lock()
	mg, ok := m.services[id]
	delete(m.services, id)
	m.mu.Unlock()
	if !ok {
		return
	}
	_ = mg.svc.Stop()
	if mg.cleanup != nil {
		mg.cleanup()
	}
}

// Statuses returns the read-only health snapshot of every managed watcher
// (SW-104), ordered by root path for deterministic reporting. It powers the
// daemon's `watcher-status` operation, surfacing per-root health (including the
// SW-101 Reconcile error) honestly.
func (m *Manager) Statuses() []ServiceStatus {
	m.mu.Lock()
	svcs := make([]*Service, 0, len(m.services))
	for _, mg := range m.services {
		svcs = append(svcs, mg.svc)
	}
	m.mu.Unlock()
	out := make([]ServiceStatus, 0, len(svcs))
	for _, svc := range svcs {
		out = append(out, svc.Status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

// StopAll stops every managed watcher (daemon shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	all := m.services
	m.services = make(map[string]*managed)
	m.mu.Unlock()
	for _, mg := range all {
		_ = mg.svc.Stop()
		if mg.cleanup != nil {
			mg.cleanup()
		}
	}
}
