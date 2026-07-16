package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// BriefStats implements BriefAggregatePort over MemStore. It walks the
// in-memory maps under one read lock but returns only file-level aggregates and
// the requested top symbols; it never copies the full node/edge catalogs.
func (m *MemStore) BriefStats(ctx context.Context, topSymbols int) (BriefStats, error) {
	if err := ctx.Err(); err != nil {
		return BriefStats{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return BriefStats{}, ErrClosed
	}

	type fileCounts struct{ symbols, endpoints int }
	files := make(map[string]fileCounts)
	inbound := make(map[model.NodeId]int)
	out := BriefStats{
		TotalNodes: len(m.nodes),
		TotalEdges: len(m.edges),
		TierCounts: make(map[model.ConfidenceTier]int),
	}
	scanned := 0
	for _, n := range m.nodes {
		if path := n.SourcePath(); path != "" {
			fc := files[path]
			fc.symbols++
			files[path] = fc
		}
		scanned++
		if scanned&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return BriefStats{}, err
			}
		}
	}
	for _, e := range m.edges {
		out.TierCounts[e.Tier()]++
		inbound[e.To()]++
		if n, ok := m.nodes[e.From()]; ok && n.SourcePath() != "" {
			fc := files[n.SourcePath()]
			fc.endpoints++
			files[n.SourcePath()] = fc
		}
		if n, ok := m.nodes[e.To()]; ok && n.SourcePath() != "" {
			fc := files[n.SourcePath()]
			fc.endpoints++
			files[n.SourcePath()] = fc
		}
		scanned++
		if scanned&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return BriefStats{}, err
			}
		}
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out.Files = make([]BriefFileStats, 0, len(paths))
	for _, path := range paths {
		fc := files[path]
		out.Files = append(out.Files, BriefFileStats{Path: path, SymbolCount: fc.symbols, EdgeEndpoints: fc.endpoints})
	}

	if topSymbols > 0 {
		out.TopInbound = make([]BriefSymbolStats, 0, len(inbound))
		for id, degree := range inbound {
			if n, ok := m.nodes[id]; ok {
				out.TopInbound = append(out.TopInbound, BriefSymbolStats{Node: n, InboundEdges: degree})
			}
		}
		sort.Slice(out.TopInbound, func(i, j int) bool {
			if out.TopInbound[i].InboundEdges != out.TopInbound[j].InboundEdges {
				return out.TopInbound[i].InboundEdges > out.TopInbound[j].InboundEdges
			}
			return out.TopInbound[i].Node.ID() < out.TopInbound[j].Node.ID()
		})
		if len(out.TopInbound) > topSymbols {
			out.TopInbound = out.TopInbound[:topSymbols]
		}
	}
	return out, nil
}

// BriefStats implements BriefAggregatePort with a consistent read transaction.
// GROUP BY executes next to the data; only O(files + topSymbols) rows cross the
// SQL boundary and the legacy whole-graph cache remains untouched.
func (s *SQLiteStore) BriefStats(ctx context.Context, topSymbols int) (BriefStats, error) {
	if err := ctx.Err(); err != nil {
		return BriefStats{}, err
	}
	if s.closed.Load() {
		return BriefStats{}, ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: begin brief aggregate: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	out := BriefStats{TierCounts: make(map[model.ConfidenceTier]int)}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes").Scan(&out.TotalNodes); err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: count brief nodes: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM edges").Scan(&out.TotalEdges); err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: count brief edges: %w", err)
	}

	tierRows, err := tx.QueryContext(ctx, `SELECT confidence_tier, COUNT(*) FROM edges GROUP BY confidence_tier`)
	if err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: brief tier counts: %w", err)
	}
	for tierRows.Next() {
		var tier string
		var count int
		if err := tierRows.Scan(&tier, &count); err != nil {
			tierRows.Close()
			return BriefStats{}, fmt.Errorf("graphstore: scan brief tier count: %w", err)
		}
		out.TierCounts[model.ConfidenceTier(tier)] = count
	}
	if err := tierRows.Err(); err != nil {
		tierRows.Close()
		return BriefStats{}, fmt.Errorf("graphstore: iterate brief tier counts: %w", err)
	}
	if err := tierRows.Close(); err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: close brief tier counts: %w", err)
	}

	fileRows, err := tx.QueryContext(ctx, `
WITH file_symbols AS (
  SELECT source_path AS path, COUNT(*) AS symbol_count
  FROM nodes WHERE source_path <> '' GROUP BY source_path
), file_degree AS (
  SELECT n.source_path AS path, COUNT(*) AS endpoint_count
  FROM (
    SELECT from_id AS node_id FROM edges
    UNION ALL
    SELECT to_id AS node_id FROM edges
  ) endpoints
  JOIN nodes n ON n.id = endpoints.node_id
  WHERE n.source_path <> ''
  GROUP BY n.source_path
)
SELECT s.path, s.symbol_count, COALESCE(d.endpoint_count, 0)
FROM file_symbols s LEFT JOIN file_degree d ON d.path = s.path
ORDER BY s.path`)
	if err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: brief file aggregates: %w", err)
	}
	for fileRows.Next() {
		var fs BriefFileStats
		if err := fileRows.Scan(&fs.Path, &fs.SymbolCount, &fs.EdgeEndpoints); err != nil {
			fileRows.Close()
			return BriefStats{}, fmt.Errorf("graphstore: scan brief file aggregate: %w", err)
		}
		out.Files = append(out.Files, fs)
	}
	if err := fileRows.Err(); err != nil {
		fileRows.Close()
		return BriefStats{}, fmt.Errorf("graphstore: iterate brief file aggregates: %w", err)
	}
	if err := fileRows.Close(); err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: close brief file aggregates: %w", err)
	}

	if topSymbols > 0 {
		topRows, err := tx.QueryContext(ctx, `
SELECT n.kind, n.qualified_name, n.source_path, n.line, n.col, n.meta, COUNT(e.id) AS inbound_count
FROM edges e JOIN nodes n ON n.id = e.to_id
GROUP BY n.id, n.kind, n.qualified_name, n.source_path, n.line, n.col, n.meta
ORDER BY inbound_count DESC, n.id ASC
LIMIT ?`, topSymbols)
		if err != nil {
			return BriefStats{}, fmt.Errorf("graphstore: brief top inbound: %w", err)
		}
		for topRows.Next() {
			var kind, qn, path, metaJSON string
			var line, col, degree int
			if err := topRows.Scan(&kind, &qn, &path, &line, &col, &metaJSON, &degree); err != nil {
				topRows.Close()
				return BriefStats{}, fmt.Errorf("graphstore: scan brief top inbound: %w", err)
			}
			n, err := model.NewNode(kind, qn, path, line, col)
			if err != nil {
				topRows.Close()
				return BriefStats{}, fmt.Errorf("graphstore: reconstruct brief node: %w", err)
			}
			meta, err := decodeNodeMeta(metaJSON)
			if err != nil {
				topRows.Close()
				return BriefStats{}, err
			}
			if !meta.IsZero() {
				n = n.WithMeta(meta)
			}
			out.TopInbound = append(out.TopInbound, BriefSymbolStats{Node: n, InboundEdges: degree})
		}
		if err := topRows.Err(); err != nil {
			topRows.Close()
			return BriefStats{}, fmt.Errorf("graphstore: iterate brief top inbound: %w", err)
		}
		if err := topRows.Close(); err != nil {
			return BriefStats{}, fmt.Errorf("graphstore: close brief top inbound: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return BriefStats{}, fmt.Errorf("graphstore: commit brief aggregate read: %w", err)
	}
	return out, nil
}
