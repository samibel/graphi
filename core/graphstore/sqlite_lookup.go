package graphstore

// SQLiteStore's GraphLookup + SymbolLookupPort implementations (CORE-01,
// ADR 0003 D3/D5). Every read here goes DIRECTLY to SQLite with bound
// parameters — it neither consults nor populates the whole-graph memGraph hot
// cache (the cache "hit" costs a full-graph materialization first, which is
// exactly the scan problem these ports exist to avoid). The statement shapes
// are the ones the SP-11 spike proved against the query planner:
//
//	WHERE endpoint = ? [AND kind = ?]
//		→ SEARCH edges USING INDEX edges_*_kind_id_edge_id
//	WHERE qualified_name = ? / source_path = ? → SEARCH nodes USING INDEX …
//
// Filtered bounded reads satisfy ORDER BY id; unfiltered bounded reads satisfy
// ORDER BY kind,id. No degree-sized temporary sort is needed. The plan-gate
// tests in selective_read_spike_test.go pin these plans in CI.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// sqliteNodesByIDChunkSize stays below SQLite's lowest commonly supported
// host-parameter limit (999). A graph traversal may hydrate tens of thousands
// of nodes; one placeholder per id would otherwise fail with "too many SQL
// variables" instead of returning a partial graph.
const sqliteNodesByIDChunkSize = 900

// Incoming implements GraphLookup: edges whose To endpoint equals id, kind-
// filtered, canonical EdgeId order, provenance intact.
func (s *SQLiteStore) Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return s.incident(ctx, "to_id", id, kinds)
}

// Outgoing implements GraphLookup: Incoming's mirror for the From endpoint.
func (s *SQLiteStore) Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return s.incident(ctx, "from_id", id, kinds)
}

// IncomingBounded implements BoundedGraphLookup through the composite
// (to_id,kind,id) index. It never materializes a complete high-degree adjacency
// list before applying limit.
func (s *SQLiteStore) IncomingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	return s.incidentBounded(ctx, "to_id", id, limit, kinds)
}

// OutgoingBounded is IncomingBounded's mirror for the From endpoint.
func (s *SQLiteStore) OutgoingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	return s.incidentBounded(ctx, "from_id", id, limit, kinds)
}

// incidentBounded reads at most limit+1 rows for an unfiltered endpoint. For a
// kind filter it runs one composite-index probe per distinct kind, each capped
// at limit+1, then performs a canonical k-way union in memory. Consequently DB
// row work is bounded by the explicit request, not endpoint degree; sparse kind
// filters cannot force a scan through unrelated incident edges.
func (s *SQLiteStore) incidentBounded(ctx context.Context, endpointCol string, id model.NodeId, limit int, kinds []model.EdgeKind) ([]model.Edge, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		return nil, false, ErrInvalidLimit
	}
	if s.closed.Load() {
		return nil, false, ErrClosed
	}

	probeLimit := boundedProbeLimit(limit)
	if len(kinds) == 0 {
		edges, err := s.incidentWindow(ctx, endpointCol, id, "", false, true, probeLimit)
		if err != nil {
			return nil, false, err
		}
		truncated := len(edges) > limit
		if truncated {
			edges = edges[:limit]
		}
		return edges, truncated, nil
	}

	var merged []model.Edge
	truncated := false
	for _, kind := range uniqueEdgeKinds(kinds) {
		edges, err := s.incidentWindow(ctx, endpointCol, id, kind, true, false, probeLimit)
		if err != nil {
			return nil, false, err
		}
		if len(edges) > limit {
			edges = edges[:limit]
			truncated = true
		}
		merged = append(merged, edges...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID() < merged[j].ID() })
	if len(merged) > limit {
		merged = merged[:limit]
		truncated = true
	}
	if merged == nil {
		merged = []model.Edge{}
	}
	return merged, truncated, nil
}

func (s *SQLiteStore) incidentWindow(ctx context.Context, endpointCol string, id model.NodeId, kind string, filterKind, orderByKind bool, limit int) ([]model.Edge, error) {
	q := `SELECT e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
FROM edges e JOIN reasons r ON r.id = e.reason_id
WHERE e.` + endpointCol + ` = ?`
	args := []any{string(id)}
	if filterKind {
		q += " AND e.kind = ?"
		args = append(args, kind)
	}
	if orderByKind {
		q += " ORDER BY e.kind, e.id LIMIT ?"
	} else {
		q += " ORDER BY e.id LIMIT ?"
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graphstore: bounded %s lookup: %w", endpointCol, err)
	}
	defer rows.Close()
	return scanEdgeRows(rows)
}

// incident runs the endpoint-selective edge read. endpointCol is one of the
// two fixed identifiers "to_id"/"from_id" (never caller input); every VALUE is
// a bound parameter.
func (s *SQLiteStore) incident(ctx context.Context, endpointCol string, id model.NodeId, kinds []model.EdgeKind) ([]model.Edge, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	q := `SELECT e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
FROM edges e JOIN reasons r ON r.id = e.reason_id
WHERE e.` + endpointCol + ` = ?`
	args := []any{string(id)}
	if len(kinds) > 0 {
		q += " AND e.kind IN (?" + strings.Repeat(",?", len(kinds)-1) + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += " ORDER BY e.id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graphstore: %s lookup: %w", endpointCol, err)
	}
	defer rows.Close()
	return scanEdgeRows(rows)
}

// scanEdgeRows reconstructs provenance-intact edges from the canonical edge
// projection (from, to, kind, tier, confidence, reason text, evidence JSON) —
// the same reconstruction loadAllFromDB performs.
func scanEdgeRows(rows *sql.Rows) ([]model.Edge, error) {
	var out []model.Edge
	for rows.Next() {
		var from, to, kind, tier, reason, evJSON string
		var conf float64
		if err := rows.Scan(&from, &to, &kind, &tier, &conf, &reason, &evJSON); err != nil {
			return nil, fmt.Errorf("graphstore: scan edge: %w", err)
		}
		var evidence []string
		if err := json.Unmarshal([]byte(evJSON), &evidence); err != nil {
			return nil, fmt.Errorf("graphstore: decode evidence: %w", err)
		}
		e, err := model.NewEdge(model.NodeId(from), model.NodeId(to), kind,
			model.ConfidenceTier(tier), conf, reason, evidence)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate edges: %w", err)
	}
	if out == nil {
		out = []model.Edge{}
	}
	return out, nil
}

// NodesByID implements GraphLookup: found nodes in canonical NodeId order,
// missing ids skipped, duplicates collapsed (the IN probe is naturally a set).
func (s *SQLiteStore) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if len(ids) == 0 {
		return []model.Node{}, nil
	}

	// Collapse duplicates before hitting SQLite and sort the ids so concatenated
	// chunk results retain the GraphLookup contract's global NodeId ordering.
	seen := make(map[model.NodeId]struct{}, len(ids))
	unique := make([]model.NodeId, 0, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })

	out := make([]model.Node, 0, len(unique))
	for start := 0; start < len(unique); start += sqliteNodesByIDChunkSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + sqliteNodesByIDChunkSize
		if end > len(unique) {
			end = len(unique)
		}
		chunk := unique[start:end]
		q := `SELECT kind, qualified_name, source_path, line, col, meta FROM nodes
WHERE id IN (?` + strings.Repeat(",?", len(chunk)-1) + `) ORDER BY id`
		args := make([]any, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, string(id))
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("graphstore: nodes by id chunk %d: %w", start/sqliteNodesByIDChunkSize, err)
		}
		nodes, scanErr := scanNodeRows(rows)
		closeErr := rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("graphstore: close nodes by id rows: %w", closeErr)
		}
		out = append(out, nodes...)
	}
	return out, nil
}

// QualifiedName implements SymbolLookupPort via the nodes_qualified_name index.
func (s *SQLiteStore) QualifiedName(ctx context.Context, qn string) ([]model.Node, error) {
	return s.lookupNodes(ctx, "qualified_name", qn)
}

// SourcePath implements SymbolLookupPort via the nodes_source_path index.
func (s *SQLiteStore) SourcePath(ctx context.Context, path string) ([]model.Node, error) {
	return s.lookupNodes(ctx, "source_path", path)
}

// lookupNodes runs an exact-equality node lookup. col is one of the two fixed
// identifiers "qualified_name"/"source_path" (never caller input).
func (s *SQLiteStore) lookupNodes(ctx context.Context, col, value string) ([]model.Node, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, qualified_name, source_path, line, col, meta FROM nodes
WHERE `+col+` = ? ORDER BY id`, value)
	if err != nil {
		return nil, fmt.Errorf("graphstore: %s lookup: %w", col, err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
}

// scanNodeRows reconstructs nodes (meta included) from the canonical node
// projection — the same reconstruction loadAllFromDB performs.
func scanNodeRows(rows *sql.Rows) ([]model.Node, error) {
	out := []model.Node{}
	for rows.Next() {
		var kind, qn, sp, metaJSON string
		var line, col int
		if err := rows.Scan(&kind, &qn, &sp, &line, &col, &metaJSON); err != nil {
			return nil, fmt.Errorf("graphstore: scan node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return nil, fmt.Errorf("graphstore: reconstruct node: %w", err)
		}
		meta, err := decodeNodeMeta(metaJSON)
		if err != nil {
			return nil, err
		}
		if !meta.IsZero() {
			n = n.WithMeta(meta)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate nodes: %w", err)
	}
	return out, nil
}

// Search implements SymbolLookupPort by delegating to the existing SearchNodes
// (FTS5-ranked; already selective).
func (s *SQLiteStore) Search(ctx context.Context, text string, limit int) ([]RankedNode, error) {
	return s.SearchNodes(ctx, text, limit)
}
