package graphstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// This file began as the SW-114 (SP-11) selective-read spike's executable
// evidence and is now the PERMANENT PLAN GATE promised by ADR 0003 D3/D7
// (promoted in SW-115 / CORE-01, which added the two node indexes to the
// production DDL — the documented baseline flip: the spike's "SCAN nodes
// today" pin became the index-usage assertion below). It opens the real
// SQLiteStore, seeds it through the production API, and pins what the SQLite
// query planner does for the ports' statement shapes:
//
//  1. bounded endpoint reads use the two endpoint+kind+EdgeId composites with
//     bound parameters and no degree-sized temporary sort;
//  2. symbol lookups by qualified_name / source_path use the CORE-01
//     nodes_qualified_name / nodes_source_path indexes;
//  3. the legacy listing path still materializes the whole graph into the hot
//     cache — the pinned reason the ports bypass it (ADR 0003 D5).
//
// If the production schema or planner behavior drifts (an index dropped or
// no longer chosen), the affected assertion fails and must be re-reviewed.

// spikeStore opens a real on-disk SQLiteStore seeded with a small graph via the
// production API and returns it with its underlying *sql.DB for EXPLAIN probes.
func spikeStore(t *testing.T) *SQLiteStore {
	t.Helper()
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "spike.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mkNode := func(name string) model.Node {
		n, err := model.NewNode("function", "pkg."+name, "pkg/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
		return n
	}
	hub := mkNode("Hub")
	for i := 0; i < 3; i++ {
		caller := mkNode(fmt.Sprintf("Caller%d", i))
		e, err := model.NewEdge(caller.ID(), hub.ID(), "calls", model.TierDerived, 0.9, "call", []string{"x:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	return st
}

// explainPlan returns the concatenated `detail` column of EXPLAIN QUERY PLAN
// for the statement (bound parameters get placeholder values — the plan does
// not depend on the bound value, only on the statement shape).
func explainPlan(t *testing.T, s *SQLiteStore, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %s: %v", query, err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	plan := strings.Join(details, " | ")
	t.Logf("plan[%s] = %s", query, plan)
	return plan
}

// TestSpike_EndpointEdgeReads_UseCompositeIndexes is SP-11 evidence (1): the
// bound-parameter bounded Incoming/Outgoing statement shapes ADR 0003 specifies
// use exactly the endpoint+kind+EdgeId composite indexes. Filtered and
// unfiltered variants can stop in their specified deterministic order without
// scanning or sorting the complete adjacency set.
func TestSpike_EndpointEdgeReads_UseCompositeIndexes(t *testing.T) {
	s := spikeStore(t)

	incoming := explainPlan(t, s,
		"SELECT id FROM edges WHERE to_id = ? ORDER BY kind, id LIMIT ?", "nid", 17)
	if !strings.Contains(incoming, "edges_to_kind_id_edge_id") || strings.Contains(incoming, "USE TEMP B-TREE") {
		t.Errorf("unfiltered bounded Incoming shape is not endpoint+kind+id ordered:\n%s", incoming)
	}

	outgoing := explainPlan(t, s,
		"SELECT id FROM edges WHERE from_id = ? ORDER BY kind, id LIMIT ?", "nid", 17)
	if !strings.Contains(outgoing, "edges_from_kind_id_edge_id") || strings.Contains(outgoing, "USE TEMP B-TREE") {
		t.Errorf("unfiltered bounded Outgoing shape is not endpoint+kind+id ordered:\n%s", outgoing)
	}

	// A sparse kind filter must not walk unrelated endpoint edges.
	kindFiltered := explainPlan(t, s,
		"SELECT id FROM edges WHERE to_id = ? AND kind = ? ORDER BY id LIMIT ?", "nid", "calls", 17)
	if !strings.Contains(kindFiltered, "edges_to_kind_id_edge_id") || strings.Contains(kindFiltered, "USE TEMP B-TREE") {
		t.Errorf("bounded kind-filtered Incoming shape is not index-ordered:\n%s", kindFiltered)
	}

	// The exact unfiltered bounded full-row read (join to the interned reason)
	// keeps the endpoint+kind+EdgeId index and needs no temporary sort.
	fullRow := explainPlan(t, s,
		`SELECT e.id, e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
		 FROM edges e JOIN reasons r ON r.id = e.reason_id
		 WHERE e.to_id = ? ORDER BY e.kind, e.id LIMIT ?`, "nid", 17)
	if !strings.Contains(fullRow, "edges_to_kind_id_edge_id") || strings.Contains(fullRow, "USE TEMP B-TREE") {
		t.Errorf("bounded full-row Incoming join is not index-ordered:\n%s", fullRow)
	}
}

// TestSpike_SymbolLookups_UseProductionIndexes is the CORE-01 plan gate for
// SymbolLookupPort: on the production schema (which since SW-115 carries the
// nodes_qualified_name / nodes_source_path indexes the SP-11 spike proved out
// in a scratch DB), both exact-equality lookup shapes are index searches, and
// the exact statement lookupNodes issues (full projection) keeps the index.
func TestSpike_SymbolLookups_UseProductionIndexes(t *testing.T) {
	s := spikeStore(t)

	byQN := explainPlan(t, s,
		"SELECT id FROM nodes WHERE qualified_name = ? ORDER BY id", "pkg.Hub")
	if !strings.Contains(byQN, "USING INDEX nodes_qualified_name") {
		t.Errorf("qualified_name lookup does not use nodes_qualified_name:\n%s", byQN)
	}
	byPath := explainPlan(t, s,
		"SELECT id FROM nodes WHERE source_path = ? ORDER BY id", "pkg/Hub.go")
	if !strings.Contains(byPath, "USING INDEX nodes_source_path") {
		t.Errorf("source_path lookup does not use nodes_source_path:\n%s", byPath)
	}

	// The full-projection shape SymbolLookupPort actually issues.
	fullRow := explainPlan(t, s,
		"SELECT kind, qualified_name, source_path, line, col, meta FROM nodes WHERE qualified_name = ? ORDER BY id", "pkg.Hub")
	if !strings.Contains(fullRow, "USING INDEX nodes_qualified_name") {
		t.Errorf("full-projection qualified_name lookup does not use nodes_qualified_name:\n%s", fullRow)
	}
}

// TestSpike_TodaysReadPath_LoadsWholeGraphIntoCache is SP-11 evidence for the
// cache-bypass decision (ADR 0003 D5): on today's SQLiteStore EVERY listing
// read — however narrow its filter — goes through ensureCache, which loads the
// ENTIRE graph from SQLite into the in-memory memGraph before filtering in Go.
// A selective read that merely reuses Edges(Query{...}) therefore cannot fix
// the hotpath; GraphLookup must query SQLite directly with bound parameters.
func TestSpike_TodaysReadPath_LoadsWholeGraphIntoCache(t *testing.T) {
	s := spikeStore(t)
	ctx := context.Background()

	if err := s.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	before := s.CacheRebuilds()

	// The narrowest supported edge listing (one kind) …
	if _, err := s.Edges(ctx, Query{EdgeKind: "calls"}); err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if got := s.CacheRebuilds(); got != before+1 {
		t.Fatalf("expected the kind-filtered Edges read to trigger exactly one whole-graph cache rebuild, got %d→%d", before, got)
	}

	// … rebuilt the FULL graph: the cache now holds every node and edge.
	s.cacheMu.RLock()
	nodes, edges := len(s.cache.nodes), len(s.cache.edges)
	s.cacheMu.RUnlock()
	if nodes != 4 || edges != 3 { // 1 hub + 3 callers; 3 call edges (spikeStore)
		t.Fatalf("whole-graph cache pin drifted: cache holds %d nodes / %d edges, seeded 4/3", nodes, edges)
	}
	t.Logf("cache-bypass evidence: Edges(Query{EdgeKind}) rebuilt the whole graph (%d nodes, %d edges) to answer a filtered listing", nodes, edges)
}
