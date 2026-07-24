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
	meta   map[string]string

	// Selective-read indexes (CORE-01, ADR 0003 D4): per-node adjacency for
	// GraphLookup and exact-match lookup maps for SymbolLookupPort. They are
	// maintained atomically inside the SAME write lock as nodes/edges, so a
	// reader can never observe the maps and the indexes out of sync.
	in     map[model.NodeId]map[model.EdgeId]struct{} // To endpoint → incident edge ids
	out    map[model.NodeId]map[model.EdgeId]struct{} // From endpoint → incident edge ids
	byQN   map[string]map[model.NodeId]struct{}       // qualified_name → node ids
	byPath map[string]map[model.NodeId]struct{}       // source_path → node ids

	// The endpoint+kind ordered adjacency indexes back BoundedGraphLookup. A
	// bounded read touches only requested kind prefixes; it never scans a
	// high-degree map merely to discover the first N deterministic edges. The
	// companion ordered kind indexes let zero-kind reads take only the first
	// required distinct kinds without scanning every endpoint+kind key.
	inKindOrdered  map[incidentKindKey]*edgeIDTree
	outKindOrdered map[incidentKindKey]*edgeIDTree
	inKinds        map[model.NodeId]*kindTree
	outKinds       map[model.NodeId]*kindTree
}

type incidentKindKey struct {
	id   model.NodeId
	kind string
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		nodes:  make(map[model.NodeId]model.Node),
		edges:  make(map[model.EdgeId]model.Edge),
		meta:   make(map[string]string),
		in:     make(map[model.NodeId]map[model.EdgeId]struct{}),
		out:    make(map[model.NodeId]map[model.EdgeId]struct{}),
		byQN:   make(map[string]map[model.NodeId]struct{}),
		byPath: make(map[string]map[model.NodeId]struct{}),

		inKindOrdered:  make(map[incidentKindKey]*edgeIDTree),
		outKindOrdered: make(map[incidentKindKey]*edgeIDTree),
		inKinds:        make(map[model.NodeId]*kindTree),
		outKinds:       make(map[model.NodeId]*kindTree),
	}
}

// indexNode/unindexNode/indexEdge/unindexEdge maintain the selective-read
// indexes. All four MUST be called with m.mu held for writing.

func (m *MemStore) indexNode(n model.Node) {
	addToSet(m.byQN, n.QualifiedName(), n.ID())
	addToSet(m.byPath, n.SourcePath(), n.ID())
}

func (m *MemStore) unindexNode(n model.Node) {
	dropFromSet(m.byQN, n.QualifiedName(), n.ID())
	dropFromSet(m.byPath, n.SourcePath(), n.ID())
}

func (m *MemStore) indexEdge(e model.Edge) {
	addToSet(m.out, e.From(), e.ID())
	addToSet(m.in, e.To(), e.ID())
	addToEdgeIDTree(m.outKindOrdered, incidentKindKey{id: e.From(), kind: e.Kind()}, e.ID())
	addToEdgeIDTree(m.inKindOrdered, incidentKindKey{id: e.To(), kind: e.Kind()}, e.ID())
	addToKindTree(m.outKinds, e.From(), e.Kind())
	addToKindTree(m.inKinds, e.To(), e.Kind())
}

func (m *MemStore) unindexEdge(e model.Edge) {
	dropFromSet(m.out, e.From(), e.ID())
	dropFromSet(m.in, e.To(), e.ID())
	if dropFromEdgeIDTree(m.outKindOrdered, incidentKindKey{id: e.From(), kind: e.Kind()}, e.ID()) {
		dropFromKindTree(m.outKinds, e.From(), e.Kind())
	}
	if dropFromEdgeIDTree(m.inKindOrdered, incidentKindKey{id: e.To(), kind: e.Kind()}, e.ID()) {
		dropFromKindTree(m.inKinds, e.To(), e.Kind())
	}
}

func addToEdgeIDTree[K comparable](idx map[K]*edgeIDTree, key K, id model.EdgeId) {
	tree := idx[key]
	if tree == nil {
		tree = &edgeIDTree{}
		idx[key] = tree
	}
	tree.insert(id)
}

func dropFromEdgeIDTree[K comparable](idx map[K]*edgeIDTree, key K, id model.EdgeId) bool {
	tree := idx[key]
	if tree == nil {
		return false
	}
	tree.delete(id)
	if tree.len() == 0 {
		delete(idx, key)
		return true
	}
	return false
}

func addToKindTree(idx map[model.NodeId]*kindTree, id model.NodeId, kind string) {
	tree := idx[id]
	if tree == nil {
		tree = &kindTree{}
		idx[id] = tree
	}
	tree.insert(kind)
}

func dropFromKindTree(idx map[model.NodeId]*kindTree, id model.NodeId, kind string) {
	tree := idx[id]
	if tree == nil {
		return
	}
	tree.delete(kind)
	if tree.len() == 0 {
		delete(idx, id)
	}
}

func addToSet[K comparable, V comparable](idx map[K]map[V]struct{}, k K, v V) {
	set, ok := idx[k]
	if !ok {
		set = make(map[V]struct{})
		idx[k] = set
	}
	set[v] = struct{}{}
}

func dropFromSet[K comparable, V comparable](idx map[K]map[V]struct{}, k K, v V) {
	set, ok := idx[k]
	if !ok {
		return
	}
	delete(set, v)
	if len(set) == 0 {
		delete(idx, k)
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
	// NodeId is content-addressed over Kind+QualifiedName+SourcePath, so a
	// replace under the same ID cannot change the indexed keys — indexNode is
	// idempotent for it.
	m.nodes[n.ID()] = n
	m.indexNode(n)
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
	// EdgeId is content-addressed over its endpoints (among other fields), so a
	// replace under the same ID cannot move the edge — indexEdge is idempotent.
	m.edges[e.ID()] = e
	m.indexEdge(e)
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
	if n, ok := m.nodes[id]; ok {
		m.unindexNode(n)
	}
	delete(m.nodes, id)
	// Cascade: drop every edge touching the removed node so no dangling edge
	// survives (the PutEdge endpoint invariant in reverse). The adjacency
	// indexes make the cascade degree-proportional instead of a full edge scan;
	// collecting ids first keeps the iteration separate from the unindexing.
	var incident []model.EdgeId
	for eid := range m.in[id] {
		incident = append(incident, eid)
	}
	for eid := range m.out[id] {
		incident = append(incident, eid)
	}
	for _, eid := range incident {
		if e, ok := m.edges[eid]; ok {
			m.unindexEdge(e)
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
	if e, ok := m.edges[id]; ok {
		m.unindexEdge(e)
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
	// The selective-read indexes are re-derived from the snapshot content, never
	// trusted from the file — mirroring how SQLite re-derives its FTS index.
	newNodes := make(map[model.NodeId]model.Node, len(g.Nodes()))
	newQN := make(map[string]map[model.NodeId]struct{})
	newPath := make(map[string]map[model.NodeId]struct{})
	for _, n := range g.Nodes() {
		newNodes[n.ID()] = n
		addToSet(newQN, n.QualifiedName(), n.ID())
		addToSet(newPath, n.SourcePath(), n.ID())
	}
	newEdges := make(map[model.EdgeId]model.Edge, len(g.Edges()))
	newIn := make(map[model.NodeId]map[model.EdgeId]struct{})
	newOut := make(map[model.NodeId]map[model.EdgeId]struct{})
	newInKindOrdered := make(map[incidentKindKey]*edgeIDTree)
	newOutKindOrdered := make(map[incidentKindKey]*edgeIDTree)
	newInKinds := make(map[model.NodeId]*kindTree)
	newOutKinds := make(map[model.NodeId]*kindTree)
	for _, e := range g.Edges() {
		newEdges[e.ID()] = e
		addToSet(newOut, e.From(), e.ID())
		addToSet(newIn, e.To(), e.ID())
		addToEdgeIDTree(newOutKindOrdered, incidentKindKey{id: e.From(), kind: e.Kind()}, e.ID())
		addToEdgeIDTree(newInKindOrdered, incidentKindKey{id: e.To(), kind: e.Kind()}, e.ID())
		addToKindTree(newOutKinds, e.From(), e.Kind())
		addToKindTree(newInKinds, e.To(), e.Kind())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.nodes = newNodes
	m.edges = newEdges
	m.byQN = newQN
	m.byPath = newPath
	m.in = newIn
	m.out = newOut
	m.inKindOrdered = newInKindOrdered
	m.outKindOrdered = newOutKindOrdered
	m.inKinds = newInKinds
	m.outKinds = newOutKinds
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

func (m *MemStore) SetMetadata(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.meta[key] = value
	return nil
}

func (m *MemStore) Metadata(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return "", ErrClosed
	}
	v, ok := m.meta[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *MemStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.nodes = nil
	m.edges = nil
	m.meta = nil
	m.in = nil
	m.out = nil
	m.inKindOrdered = nil
	m.outKindOrdered = nil
	m.inKinds = nil
	m.outKinds = nil
	m.byQN = nil
	m.byPath = nil
	return nil
}

// ---- GraphScanner (streaming bulk reads) ----
//
// MemStore holds the whole graph in memory by construction, so its scans
// snapshot the sorted element set under the read lock and then release it
// before invoking callbacks — the capability's value here is contract parity
// with the SQLite backend (same order, same fn-error semantics), not memory
// bounding, and not holding the lock across fn keeps callback code free to
// read the store.

// NodeIDs implements GraphScanner: every node id in ascending order.
func (m *MemStore) NodeIDs(ctx context.Context) ([]model.NodeId, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrClosed
	}
	ids := make([]model.NodeId, 0, len(m.nodes))
	for id := range m.nodes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// ScanNodes implements GraphScanner: every node in canonical NodeId order.
func (m *MemStore) ScanNodes(ctx context.Context, fn func(model.Node) error) error {
	nodes, err := m.Nodes(ctx, Query{})
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if err := fn(n); err != nil {
			return err
		}
	}
	return nil
}

// ScanEdges implements GraphScanner: every edge in canonical EdgeId order.
func (m *MemStore) ScanEdges(ctx context.Context, fn func(model.Edge) error) error {
	edges, err := m.Edges(ctx, Query{})
	if err != nil {
		return err
	}
	for _, e := range edges {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// Interface conformance for the optional scan capability.
var (
	_ GraphScanner = (*MemStore)(nil)
	_ GraphScanner = (*SQLiteStore)(nil)
)
