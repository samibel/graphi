package graphstore

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/core/model"
)

// MemStore is an in-memory Graphstore: the pluggable test double that proves the
// contract is real by passing the same suite as the SQLite backend. It has no
// durable layer and no separate cache, so EvictCache is a no-op (which the
// contract suite explicitly tolerates). It is safe for concurrent use.
//
// MemStore intentionally has no full-text engine beyond a simple substring scan;
// the contract suite only asserts that Text queries return a deterministic,
// correct subset, which substring matching satisfies for the searchable fields
// (node name/qualified name, edge reason).
type MemStore struct {
	mu     sync.RWMutex
	closed bool
	nodes  map[model.NodeId]model.Node
	edges  map[model.EdgeId]model.Edge
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		nodes: make(map[model.NodeId]model.Node),
		edges: make(map[model.EdgeId]model.Edge),
	}
}

// MemFactory is a Factory for the in-memory backend. The dir argument is ignored.
func MemFactory(_ string) (Graphstore, error) { return NewMemStore(), nil }

var _ Graphstore = (*MemStore)(nil)

func (m *MemStore) PutNode(ctx context.Context, n model.Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.nodes[n.ID()] = n
	return nil
}

func (m *MemStore) PutEdge(ctx context.Context, e model.Edge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if _, ok := m.nodes[e.From()]; !ok {
		return ErrUnknownEdgeEndpoint
	}
	if _, ok := m.nodes[e.To()]; !ok {
		return ErrUnknownEdgeEndpoint
	}
	m.edges[e.ID()] = e
	return nil
}

// DeleteNode removes the node and every edge incident to it (From or To). It is
// idempotent: deleting a missing node is a no-op. MemStore has no separate
// durable layer, so the in-memory maps ARE the source of truth; the deletion is
// applied atomically under the write lock.
func (m *MemStore) DeleteNode(ctx context.Context, id model.NodeId) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	delete(m.nodes, id)
	// Cascade: drop every edge touching the removed node so no dangling edge
	// survives (the PutEdge endpoint invariant in reverse).
	for eid, e := range m.edges {
		if e.From() == id || e.To() == id {
			delete(m.edges, eid)
		}
	}
	return nil
}

// DeleteEdge removes the edge with the given ID. Idempotent: deleting a missing
// edge is a no-op.
func (m *MemStore) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	delete(m.edges, id)
	return nil
}

func (m *MemStore) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	if err := ctx.Err(); err != nil {
		return model.Node{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return model.Node{}, ErrClosed
	}
	n, ok := m.nodes[id]
	if !ok {
		return model.Node{}, ErrNotFound
	}
	return n, nil
}

func (m *MemStore) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	if err := ctx.Err(); err != nil {
		return model.Edge{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return model.Edge{}, ErrClosed
	}
	e, ok := m.edges[id]
	if !ok {
		return model.Edge{}, ErrNotFound
	}
	return e, nil
}

func (m *MemStore) Nodes(ctx context.Context, q Query) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := make([]model.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		if q.NodeKind != "" && n.Kind() != q.NodeKind {
			continue
		}
		if q.Text != "" && !nodeMatchesText(n, q.Text) {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// SearchNodes performs a simple substring search over qualified names and
// returns deterministic results. rank is always 0 because MemStore has no
// full-text engine; ordering is by qualified_name then node id ascending.
func (m *MemStore) SearchNodes(ctx context.Context, text string, limit int) ([]RankedNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return nil, nil
	}
	out := make([]RankedNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		if !strings.Contains(strings.ToLower(n.QualifiedName()), t) {
			continue
		}
		out = append(out, RankedNode{Node: n, Rank: 0})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Node.QualifiedName() != out[j].Node.QualifiedName() {
			return out[i].Node.QualifiedName() < out[j].Node.QualifiedName()
		}
		return out[i].Node.ID() < out[j].Node.ID()
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) Edges(ctx context.Context, q Query) ([]model.Edge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := make([]model.Edge, 0, len(m.edges))
	for _, e := range m.edges {
		if q.EdgeKind != "" && e.Kind() != q.EdgeKind {
			continue
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(e.Reason()), strings.ToLower(q.Text)) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func nodeMatchesText(n model.Node, text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(strings.ToLower(n.QualifiedName()), t)
}

func (m *MemStore) Snapshot(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safe, err := safeSnapshotPath(path)
	if err != nil {
		return err
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrClosed
	}
	nodes := make([]model.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	edges := make([]model.Edge, 0, len(m.edges))
	for _, e := range m.edges {
		edges = append(edges, e)
	}
	m.mu.RUnlock()

	data, err := encodeSnapshot(nodes, edges)
	if err != nil {
		return err
	}
	return writeFileAtomic(safe, data)
}

func (m *MemStore) Load(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safe, err := safeSnapshotPath(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(safe) //nolint:gosec // path sanitized by safeSnapshotPath
	if err != nil {
		return err
	}
	g, err := decodeSnapshot(data)
	if err != nil {
		return err
	}
	// Build the replacement state fully before swapping (atomic, fail-closed).
	newNodes := make(map[model.NodeId]model.Node, len(g.Nodes()))
	for _, n := range g.Nodes() {
		newNodes[n.ID()] = n
	}
	newEdges := make(map[model.EdgeId]model.Edge, len(g.Edges()))
	for _, e := range g.Edges() {
		newEdges[e.ID()] = e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.nodes = newNodes
	m.edges = newEdges
	return nil
}

// EvictCache is a no-op: MemStore has no separate cache layer.
func (m *MemStore) EvictCache(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

func (m *MemStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.nodes = nil
	m.edges = nil
	return nil
}
