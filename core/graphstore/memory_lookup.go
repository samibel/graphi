package graphstore

// MemStore's GraphLookup + SymbolLookupPort implementations (CORE-01, ADR 0003
// D4). All reads are served from the adjacency/lookup indexes maintained by the
// write paths in memory.go, so cost is proportional to the node's degree (or
// the name-collision set), never to the graph. Canonical ordering (edges by
// EdgeId, nodes by NodeId) is applied on the matched set only.

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// Incoming implements GraphLookup: edges whose To endpoint equals id, kind-
// filtered, canonical EdgeId order, provenance intact.
func (m *MemStore) Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return m.incident(ctx, m.inSet, id, kinds)
}

// Outgoing implements GraphLookup: Incoming's mirror for the From endpoint.
func (m *MemStore) Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return m.incident(ctx, m.outSet, id, kinds)
}

func (m *MemStore) inSet(id model.NodeId) map[model.EdgeId]struct{}  { return m.in[id] }
func (m *MemStore) outSet(id model.NodeId) map[model.EdgeId]struct{} { return m.out[id] }

func (m *MemStore) incident(ctx context.Context, side func(model.NodeId) map[model.EdgeId]struct{}, id model.NodeId, kinds []model.EdgeKind) ([]model.Edge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	set := side(id)
	out := make([]model.Edge, 0, len(set))
	for eid := range set {
		e, ok := m.edges[eid]
		if !ok || !kindMatches(e.Kind(), kinds) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func kindMatches(kind string, kinds []model.EdgeKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if kind == k {
			return true
		}
	}
	return false
}

// NodesByID implements GraphLookup: found nodes in canonical NodeId order,
// missing ids skipped, duplicates collapsed (set semantics).
func (m *MemStore) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	seen := make(map[model.NodeId]struct{}, len(ids))
	out := make([]model.Node, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if n, ok := m.nodes[id]; ok {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// QualifiedName implements SymbolLookupPort: exact qualified-name matches in
// canonical NodeId order.
func (m *MemStore) QualifiedName(ctx context.Context, qn string) ([]model.Node, error) {
	return m.lookupNodes(ctx, m.qnSet, qn)
}

// SourcePath implements SymbolLookupPort: exact (caller-normalized) source-path
// matches in canonical NodeId order.
func (m *MemStore) SourcePath(ctx context.Context, path string) ([]model.Node, error) {
	return m.lookupNodes(ctx, m.pathSet, path)
}

func (m *MemStore) qnSet(k string) map[model.NodeId]struct{}   { return m.byQN[k] }
func (m *MemStore) pathSet(k string) map[model.NodeId]struct{} { return m.byPath[k] }

func (m *MemStore) lookupNodes(ctx context.Context, side func(string) map[model.NodeId]struct{}, key string) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	set := side(key)
	out := make([]model.Node, 0, len(set))
	for id := range set {
		if n, ok := m.nodes[id]; ok {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// Search implements SymbolLookupPort by delegating to the existing SearchNodes
// (the port exposes the same contract on one read surface).
func (m *MemStore) Search(ctx context.Context, text string, limit int) ([]RankedNode, error) {
	return m.SearchNodes(ctx, text, limit)
}
