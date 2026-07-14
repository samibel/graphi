package graphstore

// SQLiteStore's GraphLookup + SymbolLookupPort implementations (CORE-01,
// ADR 0003 D3/D5). Every read here goes DIRECTLY to SQLite with bound
// parameters — it neither consults nor populates the whole-graph memGraph hot
// cache (the cache "hit" costs a full-graph materialization first, which is
// exactly the scan problem these ports exist to avoid). The statement shapes
// are the ones the SP-11 spike proved against the query planner:
//
//	WHERE e.to_id = ?   → SEARCH edges USING INDEX edges_to_id
//	WHERE e.from_id = ? → SEARCH edges USING INDEX edges_from_id
//	WHERE qualified_name = ? / source_path = ? → SEARCH nodes USING INDEX …
//
// The trailing ORDER BY sorts only the matched (degree-bounded) set. The
// plan-gate tests in selective_read_spike_test.go pin these plans in CI.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// Incoming implements GraphLookup: edges whose To endpoint equals id, kind-
// filtered, canonical EdgeId order, provenance intact.
func (s *SQLiteStore) Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return s.incident(ctx, "to_id", id, kinds)
}

// Outgoing implements GraphLookup: Incoming's mirror for the From endpoint.
func (s *SQLiteStore) Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return s.incident(ctx, "from_id", id, kinds)
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
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if len(ids) == 0 {
		return []model.Node{}, nil
	}
	q := `SELECT kind, qualified_name, source_path, line, col, meta FROM nodes
WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `) ORDER BY id`
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, string(id))
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graphstore: nodes by id: %w", err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
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
