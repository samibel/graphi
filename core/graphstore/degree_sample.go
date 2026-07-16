package graphstore

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

type degreeCandidate struct {
	node   model.Node
	degree int
}

// DegreeStratifiedSymbols implements DegreeSamplePort over MemStore without
// copying the graph catalogs. It computes incident degree under one read lock
// and returns at most maxSymbols representative quantiles.
func (m *MemStore) DegreeStratifiedSymbols(ctx context.Context, maxSymbols int) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxSymbols <= 0 {
		return []model.Node{}, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrClosed
	}
	degree := make(map[model.NodeId]int)
	for _, e := range m.edges {
		degree[e.From()]++
		degree[e.To()]++
	}
	candidates := make([]degreeCandidate, 0)
	for _, n := range m.nodes {
		if n.Kind() == "function" || n.Kind() == "method" {
			candidates = append(candidates, degreeCandidate{node: n, degree: degree[n.ID()]})
		}
	}
	return stratifyDegreeCandidates(candidates, maxSymbols), nil
}

func stratifyDegreeCandidates(candidates []degreeCandidate, maxSymbols int) []model.Node {
	if len(candidates) == 0 || maxSymbols <= 0 {
		return []model.Node{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].degree != candidates[j].degree {
			return candidates[i].degree > candidates[j].degree
		}
		return candidates[i].node.ID() < candidates[j].node.ID()
	})
	if len(candidates) <= maxSymbols {
		out := make([]model.Node, 0, len(candidates))
		for _, candidate := range candidates {
			out = append(out, candidate.node)
		}
		return out
	}
	out := make([]model.Node, 0, maxSymbols)
	lastBucket := -1
	for i, candidate := range candidates {
		bucket := i * maxSymbols / len(candidates)
		if bucket == lastBucket {
			continue
		}
		lastBucket = bucket
		out = append(out, candidate.node)
	}
	return out
}

// DegreeStratifiedSymbols implements DegreeSamplePort in SQL. Window
// functions rank candidates and pick one per quantile bucket, so only the
// bounded sample crosses into Go even for monorepos.
func (s *SQLiteStore) DegreeStratifiedSymbols(ctx context.Context, maxSymbols int) ([]model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if maxSymbols <= 0 {
		return []model.Node{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
WITH endpoint_degree AS (
  SELECT node_id, COUNT(*) AS degree
  FROM (
    SELECT from_id AS node_id FROM edges
    UNION ALL
    SELECT to_id AS node_id FROM edges
  ) endpoints
  GROUP BY node_id
), candidates AS (
  SELECT n.id, n.kind, n.qualified_name, n.source_path, n.line, n.col, n.meta,
         COALESCE(d.degree, 0) AS degree
  FROM nodes n LEFT JOIN endpoint_degree d ON d.node_id = n.id
  WHERE n.kind IN ('function', 'method')
), ranked AS (
  SELECT *,
         ROW_NUMBER() OVER (ORDER BY degree DESC, id ASC) AS rn,
         COUNT(*) OVER () AS total
  FROM candidates
), bucketed AS (
  SELECT *, CAST(((rn - 1) * ?) / total AS INTEGER) AS bucket
  FROM ranked
), picked AS (
  SELECT *, ROW_NUMBER() OVER (PARTITION BY bucket ORDER BY rn) AS bucket_rank
  FROM bucketed
)
SELECT kind, qualified_name, source_path, line, col, meta
FROM picked
WHERE bucket_rank = 1
ORDER BY bucket
LIMIT ?`, maxSymbols, maxSymbols)
	if err != nil {
		return nil, fmt.Errorf("graphstore: degree-stratified symbols: %w", err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
}
