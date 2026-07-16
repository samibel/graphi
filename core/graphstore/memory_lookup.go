package graphstore

// MemStore's GraphLookup + SymbolLookupPort implementations (CORE-01, ADR 0003
// D4). All reads are served from the adjacency/lookup indexes maintained by the
// write paths in memory.go, so cost is proportional to the node's degree (or
// the name-collision set), never to the graph. Canonical ordering (edges by
// EdgeId, nodes by NodeId) is applied on the matched set only.

import (
	"container/heap"
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

// IncomingBounded implements BoundedGraphLookup without scanning the complete
// adjacency set. The ordered per-endpoint indexes are maintained with each
// write under the same lock as the edge catalog.
func (m *MemStore) IncomingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	return m.incidentBounded(ctx, id, limit, true, kinds)
}

// OutgoingBounded is IncomingBounded's mirror for the From endpoint.
func (m *MemStore) OutgoingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	return m.incidentBounded(ctx, id, limit, false, kinds)
}

func (m *MemStore) incidentBounded(
	ctx context.Context,
	id model.NodeId,
	limit int,
	incoming bool,
	kinds []model.EdgeKind,
) ([]model.Edge, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		return nil, false, ErrInvalidLimit
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	byKind := m.outKindOrdered
	knownKinds := m.outKinds
	if incoming {
		byKind = m.inKindOrdered
		knownKinds = m.inKinds
	}

	probeLimit := boundedProbeLimit(limit)
	var ids []model.EdgeId
	if len(kinds) == 0 {
		// Zero kinds means every stored kind. Canonical order is (kind, EdgeId),
		// matching SQLite's endpoint+kind+id index order while touching only the
		// prefix needed from each successive kind tree.
		orderedKinds := knownKinds[id].first(probeLimit)
		ids = make([]model.EdgeId, 0, min(probeLimit, len(m.edges)))
		for _, kind := range orderedKinds {
			remaining := probeLimit - len(ids)
			if remaining <= 0 {
				break
			}
			ids = append(ids, byKind[incidentKindKey{id: id, kind: kind}].first(remaining)...)
		}
	} else {
		// Merge requested endpoint+kind indexes directly. At most limit+1 IDs
		// are emitted regardless of endpoint degree; every individual kind prefix
		// is itself capped at limit+1.
		var cursors edgeIDCursorHeap
		for _, kind := range uniqueEdgeKinds(kinds) {
			ordered := byKind[incidentKindKey{id: id, kind: kind}].first(probeLimit)
			if len(ordered) > 0 {
				cursors = append(cursors, edgeIDCursor{ids: ordered})
			}
		}
		heap.Init(&cursors)
		ids = make([]model.EdgeId, 0, min(probeLimit, cursors.totalAvailable()))
		for len(ids) < probeLimit && cursors.Len() > 0 {
			cursor := heap.Pop(&cursors).(edgeIDCursor)
			ids = append(ids, cursor.ids[cursor.pos])
			cursor.pos++
			if cursor.pos < len(cursor.ids) {
				heap.Push(&cursors, cursor)
			}
		}
	}

	truncated := len(ids) > limit
	if truncated {
		ids = ids[:limit]
	}
	out := make([]model.Edge, 0, len(ids))
	for _, edgeID := range ids {
		if edge, ok := m.edges[edgeID]; ok {
			out = append(out, edge)
		}
	}
	return out, truncated, nil
}

type edgeIDCursor struct {
	ids []model.EdgeId
	pos int
}

type edgeIDCursorHeap []edgeIDCursor

func (h edgeIDCursorHeap) Len() int { return len(h) }
func (h edgeIDCursorHeap) Less(i, j int) bool {
	return h[i].ids[h[i].pos] < h[j].ids[h[j].pos]
}
func (h edgeIDCursorHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *edgeIDCursorHeap) Push(value any) {
	*h = append(*h, value.(edgeIDCursor))
}
func (h *edgeIDCursorHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func (h edgeIDCursorHeap) totalAvailable() int {
	total := 0
	maxInt := int(^uint(0) >> 1)
	for _, cursor := range h {
		remaining := len(cursor.ids) - cursor.pos
		if total > maxInt-remaining {
			return maxInt
		}
		total += remaining
	}
	return total
}

func boundedProbeLimit(limit int) int {
	if limit < int(^uint(0)>>1) {
		return limit + 1
	}
	return limit
}

func uniqueEdgeKinds(kinds []model.EdgeKind) []string {
	seen := make(map[string]struct{}, len(kinds))
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if _, exists := seen[kind]; exists {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
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
