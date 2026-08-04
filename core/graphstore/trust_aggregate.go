package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// kindExternal is the interned external-symbol node kind (WP-03): heuristic
// linker artifacts minted by engine/link with an empty source path. Mirrors
// core/parse.KindExternal without importing parse.
const kindExternal = "external"

// TrustStats implements TrustAggregatePort over MemStore. One read lock,
// two passes (nodes then edges), no full catalog copies — mirroring
// BriefStats, including the periodic ctx checks every 1024 scanned entries.
func (m *MemStore) TrustStats(ctx context.Context, topN int) (TrustStats, error) {
	if err := ctx.Err(); err != nil {
		return TrustStats{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return TrustStats{}, ErrClosed
	}

	out := TrustStats{
		NodesTotal:    len(m.nodes),
		EdgesTotal:    len(m.edges),
		EdgesByKind:   make(map[string]int),
		EdgesByTier:   make(map[model.ConfidenceTier]int),
		TopBoundaries: []ExternalBoundary{},
	}
	extQN := make(map[model.NodeId]string)
	scanned := 0
	for _, n := range m.nodes {
		if n.Kind() == kindExternal {
			out.ExternalNodes++
			extQN[n.ID()] = n.QualifiedName()
		}
		scanned++
		if scanned&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return TrustStats{}, err
			}
		}
	}
	// Boundaries aggregate by qualified name, not node id: external nodes are
	// interned per unique QN, and the name is the boundary's identity in the
	// struct — keying by name keeps both backends identical even for stores
	// built by direct PutNode.
	incident := make(map[string]int)
	for _, e := range m.edges {
		out.EdgesByKind[e.Kind()]++
		out.EdgesByTier[e.Tier()]++
		if qn, ok := extQN[e.To()]; ok {
			out.ExternalEdges++
			incident[qn]++
		}
		scanned++
		if scanned&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return TrustStats{}, err
			}
		}
	}

	if topN > 0 && len(incident) > 0 {
		boundaries := make([]ExternalBoundary, 0, len(incident))
		for qn, count := range incident {
			boundaries = append(boundaries, ExternalBoundary{QualifiedName: qn, IncidentEdges: count})
		}
		sort.Slice(boundaries, func(i, j int) bool {
			if boundaries[i].IncidentEdges != boundaries[j].IncidentEdges {
				return boundaries[i].IncidentEdges > boundaries[j].IncidentEdges
			}
			return boundaries[i].QualifiedName < boundaries[j].QualifiedName
		})
		if len(boundaries) > topN {
			boundaries = boundaries[:topN]
		}
		out.TopBoundaries = boundaries
	}
	return out, nil
}

// TrustStats implements TrustAggregatePort with a consistent read transaction.
// GROUP BY executes next to the data; only O(kinds + tiers + topN) rows cross
// the SQL boundary and the legacy whole-graph cache remains untouched.
func (s *SQLiteStore) TrustStats(ctx context.Context, topN int) (TrustStats, error) {
	if err := ctx.Err(); err != nil {
		return TrustStats{}, err
	}
	if s.closed.Load() {
		return TrustStats{}, ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: begin trust aggregate: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	out := TrustStats{
		EdgesByKind:   make(map[string]int),
		EdgesByTier:   make(map[model.ConfidenceTier]int),
		TopBoundaries: []ExternalBoundary{},
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&out.NodesTotal); err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: count trust nodes: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM edges").Scan(&out.EdgesTotal); err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: count trust edges: %w", err)
	}

	if err := func() (retErr error) {
		kindRows, err := tx.QueryContext(ctx, `SELECT kind, COUNT(*) FROM edges GROUP BY kind`)
		if err != nil {
			return fmt.Errorf("graphstore: trust kind counts: %w", err)
		}
		defer func() {
			if err := kindRows.Close(); retErr == nil && err != nil {
				retErr = fmt.Errorf("graphstore: close trust kind counts: %w", err)
			}
		}()

		for kindRows.Next() {
			var kind string
			var count int
			if err := kindRows.Scan(&kind, &count); err != nil {
				return fmt.Errorf("graphstore: scan trust kind count: %w", err)
			}
			out.EdgesByKind[kind] = count
		}
		if err := kindRows.Err(); err != nil {
			return fmt.Errorf("graphstore: iterate trust kind counts: %w", err)
		}
		return nil
	}(); err != nil {
		return TrustStats{}, err
	}

	if err := func() (retErr error) {
		tierRows, err := tx.QueryContext(ctx, `SELECT confidence_tier, COUNT(*) FROM edges GROUP BY confidence_tier`)
		if err != nil {
			return fmt.Errorf("graphstore: trust tier counts: %w", err)
		}
		defer func() {
			if err := tierRows.Close(); retErr == nil && err != nil {
				retErr = fmt.Errorf("graphstore: close trust tier counts: %w", err)
			}
		}()

		for tierRows.Next() {
			var tier string
			var count int
			if err := tierRows.Scan(&tier, &count); err != nil {
				return fmt.Errorf("graphstore: scan trust tier count: %w", err)
			}
			out.EdgesByTier[model.ConfidenceTier(tier)] = count
		}
		if err := tierRows.Err(); err != nil {
			return fmt.Errorf("graphstore: iterate trust tier counts: %w", err)
		}
		return nil
	}(); err != nil {
		return TrustStats{}, err
	}

	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes WHERE kind = ?", kindExternal).Scan(&out.ExternalNodes); err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: count trust external nodes: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM edges e JOIN nodes n ON n.id = e.to_id WHERE n.kind = ?",
		kindExternal).Scan(&out.ExternalEdges); err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: count trust external edges: %w", err)
	}

	if topN > 0 {
		if err := func() (retErr error) {
			// GROUP BY qualified name, matching MemStore: the name is the
			// boundary's identity (external nodes are interned per unique QN).
			topRows, err := tx.QueryContext(ctx, `
SELECT n.qualified_name, COUNT(*) AS incident_count
FROM edges e JOIN nodes n ON n.id = e.to_id
WHERE n.kind = ?
GROUP BY n.qualified_name
ORDER BY incident_count DESC, n.qualified_name ASC
LIMIT ?`, kindExternal, topN)
			if err != nil {
				return fmt.Errorf("graphstore: trust top boundaries: %w", err)
			}
			defer func() {
				if err := topRows.Close(); retErr == nil && err != nil {
					retErr = fmt.Errorf("graphstore: close trust top boundaries: %w", err)
				}
			}()

			for topRows.Next() {
				var b ExternalBoundary
				if err := topRows.Scan(&b.QualifiedName, &b.IncidentEdges); err != nil {
					return fmt.Errorf("graphstore: scan trust top boundary: %w", err)
				}
				out.TopBoundaries = append(out.TopBoundaries, b)
			}
			if err := topRows.Err(); err != nil {
				return fmt.Errorf("graphstore: iterate trust top boundaries: %w", err)
			}
			return nil
		}(); err != nil {
			return TrustStats{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return TrustStats{}, fmt.Errorf("graphstore: commit trust aggregate read: %w", err)
	}
	return out, nil
}
