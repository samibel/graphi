package graphstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// This file is the SW-114 (SP-11) selective-read spike's EXECUTABLE EVIDENCE.
// It changes no production code and no production schema: it opens the real
// SQLiteStore, seeds it through the production API, and pins what the SQLite
// query planner does for the read shapes ADR 0003 specifies for CORE-01:
//
//  1. endpoint-selective edge reads (Incoming/Outgoing) CAN use the existing
//     edges_from_id / edges_to_id indexes with bound parameters — no schema
//     change needed for the GraphLookup hotpath;
//  2. symbol lookups by qualified_name / source_path are FULL TABLE SCANS on
//     today's schema (nodes has no secondary index) — the measured gap;
//  3. adding the two content-neutral indexes ADR 0003 proposes (here created
//     only inside this test's scratch DB) flips those lookups to index
//     searches, proving CORE-01's fix is index-only.
//
// If the production schema changes underneath these pins (an index added or
// dropped), the affected assertion fails and the baseline must be re-recorded.

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

// TestSpike_EndpointEdgeReads_UseExistingIndexes is SP-11 evidence (1): the
// bound-parameter Incoming/Outgoing statement shapes ADR 0003 specifies for
// GraphLookup are served by the EXISTING edges_to_id / edges_from_id indexes —
// including with an additional kind filter and the canonical ORDER BY id — so
// CORE-01's edge hotpath needs no schema change at all.
func TestSpike_EndpointEdgeReads_UseExistingIndexes(t *testing.T) {
	s := spikeStore(t)

	incoming := explainPlan(t, s,
		"SELECT id FROM edges WHERE to_id = ? ORDER BY id", "nid")
	if !strings.Contains(incoming, "USING INDEX edges_to_id") {
		t.Errorf("Incoming shape does not use edges_to_id:\n%s", incoming)
	}

	outgoing := explainPlan(t, s,
		"SELECT id FROM edges WHERE from_id = ? ORDER BY id", "nid")
	if !strings.Contains(outgoing, "USING INDEX edges_from_id") {
		t.Errorf("Outgoing shape does not use edges_from_id:\n%s", outgoing)
	}

	// The kind filter (Incoming(id, kind...)) stays on the same index: kind is
	// applied as a residual filter after the endpoint probe.
	kindFiltered := explainPlan(t, s,
		"SELECT id FROM edges WHERE to_id = ? AND kind = ? ORDER BY id", "nid", "calls")
	if !strings.Contains(kindFiltered, "USING INDEX edges_to_id") {
		t.Errorf("kind-filtered Incoming shape does not use edges_to_id:\n%s", kindFiltered)
	}

	// The full-row read (join to the interned reason) keeps the endpoint index.
	fullRow := explainPlan(t, s,
		`SELECT e.id, e.from_id, e.to_id, e.kind, e.confidence_tier, e.confidence, r.text, e.evidence
		 FROM edges e JOIN reasons r ON r.id = e.reason_id
		 WHERE e.to_id = ? ORDER BY e.id`, "nid")
	if !strings.Contains(fullRow, "USING INDEX edges_to_id") {
		t.Errorf("full-row Incoming join does not use edges_to_id:\n%s", fullRow)
	}
}

// TestSpike_SymbolLookups_FullScanToday_IndexedWithProposedIndexes is SP-11
// evidence (2) and (3): on today's production schema a qualified_name or
// source_path equality lookup SCANS the whole nodes table (no secondary index
// exists — the pinned gap resolveExact's full Nodes() read papers over), and
// the two content-neutral indexes ADR 0003 proposes for CORE-01 — created here
// ONLY in this test's scratch DB — flip both lookups to index searches.
func TestSpike_SymbolLookups_FullScanToday_IndexedWithProposedIndexes(t *testing.T) {
	s := spikeStore(t)

	// (2) Today: full table scan for both symbol-lookup shapes.
	byQN := explainPlan(t, s,
		"SELECT id FROM nodes WHERE qualified_name = ? ORDER BY id", "pkg.Hub")
	if !strings.Contains(byQN, "SCAN nodes") {
		t.Errorf("BASELINE DRIFT: qualified_name lookup no longer scans (an index landed?) — re-record this baseline:\n%s", byQN)
	}
	byPath := explainPlan(t, s,
		"SELECT id FROM nodes WHERE source_path = ? ORDER BY id", "pkg/Hub.go")
	if !strings.Contains(byPath, "SCAN nodes") {
		t.Errorf("BASELINE DRIFT: source_path lookup no longer scans (an index landed?) — re-record this baseline:\n%s", byPath)
	}

	// (3) With ADR 0003's proposed content-neutral indexes (scratch-DB only —
	// CORE-01 adds them to the production DDL), both shapes become searches.
	for _, ddl := range []string{
		"CREATE INDEX nodes_qualified_name ON nodes(qualified_name)",
		"CREATE INDEX nodes_source_path ON nodes(source_path)",
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}

	byQN = explainPlan(t, s,
		"SELECT id FROM nodes WHERE qualified_name = ? ORDER BY id", "pkg.Hub")
	if !strings.Contains(byQN, "USING INDEX nodes_qualified_name") {
		t.Errorf("qualified_name lookup does not use the proposed index:\n%s", byQN)
	}
	byPath = explainPlan(t, s,
		"SELECT id FROM nodes WHERE source_path = ? ORDER BY id", "pkg/Hub.go")
	if !strings.Contains(byPath, "USING INDEX nodes_source_path") {
		t.Errorf("source_path lookup does not use the proposed index:\n%s", byPath)
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
