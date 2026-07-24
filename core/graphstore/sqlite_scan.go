package graphstore

// SQLiteStore's GraphScanner implementation. Like the sqlite_lookup.go ports,
// every read here goes DIRECTLY to SQLite and neither consults nor populates
// the whole-graph memGraph hot cache — the point of these scans is that a
// bulk consumer (the ingest pipeline) can traverse the entire graph while
// holding exactly one reconstructed element at a time, instead of paying for
// the cache mirror plus a full slice. Row reconstruction is byte-identical to
// loadAllFromDB's; ordering is the canonical id-ascending listing order.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/core/model"
)

// NodeIDs implements GraphScanner: every node id, ascending, without
// reconstructing nodes.
func (s *SQLiteStore) NodeIDs(ctx context.Context) ([]model.NodeId, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM nodes ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("graphstore: scan node ids: %w", err)
	}
	defer rows.Close()
	var ids []model.NodeId
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("graphstore: scan node id: %w", err)
		}
		ids = append(ids, model.NodeId(id))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphstore: iterate node ids: %w", err)
	}
	return ids, nil
}

// ScanNodes implements GraphScanner: streams every node in canonical NodeId
// order, one reconstructed node per callback.
func (s *SQLiteStore) ScanNodes(ctx context.Context, fn func(model.Node) error) error {
	if s.closed.Load() {
		return ErrClosed
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT kind, qualified_name, source_path, line, col, meta FROM nodes ORDER BY id")
	if err != nil {
		return fmt.Errorf("graphstore: scan nodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, qn, sp, metaJSON string
		var line, col int
		if err := rows.Scan(&kind, &qn, &sp, &line, &col, &metaJSON); err != nil {
			return fmt.Errorf("graphstore: scan node: %w", err)
		}
		n, err := model.NewNode(kind, qn, sp, line, col)
		if err != nil {
			return fmt.Errorf("graphstore: reconstruct node: %w", err)
		}
		meta, err := decodeNodeMeta(metaJSON)
		if err != nil {
			return err
		}
		if !meta.IsZero() {
			n = n.WithMeta(meta)
		}
		if err := fn(n); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graphstore: iterate nodes: %w", err)
	}
	return nil
}

// ScanEdges implements GraphScanner: streams every edge (provenance intact)
// in canonical EdgeId order, one reconstructed edge per callback.
func (s *SQLiteStore) ScanEdges(ctx context.Context, fn func(model.Edge) error) error {
	if s.closed.Load() {
		return ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
FROM edges e
JOIN reasons r ON e.reason_id = r.id
ORDER BY e.id`)
	if err != nil {
		return fmt.Errorf("graphstore: scan edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var from, to, kind, tier, reason, evJSON string
		var conf float64
		if err := rows.Scan(&from, &to, &kind, &tier, &conf, &reason, &evJSON); err != nil {
			return fmt.Errorf("graphstore: scan edge: %w", err)
		}
		var evidence []string
		if err := json.Unmarshal([]byte(evJSON), &evidence); err != nil {
			return fmt.Errorf("graphstore: decode evidence: %w", err)
		}
		e, err := model.NewEdge(model.NodeId(from), model.NodeId(to), kind,
			model.ConfidenceTier(tier), conf, reason, evidence)
		if err != nil {
			return fmt.Errorf("graphstore: reconstruct edge: %w", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graphstore: iterate edges: %w", err)
	}
	return nil
}
