# ADR 0003 — Selective-Read Contract: `GraphLookup` and `SymbolLookupPort` (SW-114 / SP-11)

- Status: Accepted (decision spike — contract of record for `CORE-01` / `CORE-02`)
- Date: 2026-07-14
- Story: SW-114 — SP-11 (spike): selective-read contract — endpoints, symbol indexes, cache
  bypass, brief aggregates
- Spec / Gate: `focused-core-rc-g0-g1` — gate **G1** (contract freeze)
- Master WBS: `SP-11`; master plan §4 "Core Read Ports"
- Depends on: SW-110 (TEST-01 rows-scanned baselines), SW-111 (frozen 12 stable ops)
- Governs (implemented later, NOT here): `CORE-01` — the `GraphLookup` / `SymbolLookupPort`
  backends for Memory and SQLite; `CORE-02` — the migration of every stable hotpath onto them.

## Status of this document

This is a **decision spike**. It is accepted as a **written contract**, not as running code.
It defines the read-port API and backend strategy `CORE-01` implements against, and the
migration targets `CORE-02` drives to. Its companion executable evidence is
`core/graphstore/selective_read_spike_test.go` (green, no production change): real
`EXPLAIN QUERY PLAN` output over the production schema, plus a pin of today's
whole-graph-cache read path. Where a decision cannot be made from current evidence it is an
explicit **UNKNOWN** with the experiment that resolves it — not a silent assumption, and not an
invented number.

## Context — the measured problem (evidence)

Every stable read hotpath answers a **degree-shaped question with a graph-shaped scan**:

1. **Directed traversals scan a whole edge class.** `Service.directedLookup`
   (`engine/query/service.go:145`) — the shared body of `callers`/`callees`/`references`/
   `definition` — issues `Edges(Query{EdgeKind})`, receives EVERY edge of that kind, and filters
   to the symbol's endpoint in Go, because `graphstore.Query` (`core/graphstore/graphstore.go:45`)
   cannot express an endpoint. The TEST-01 baseline
   (`engine/query/baseline_characterization_test.go`) pins the over-scan: rows scanned = all
   edges of the kind, not the matched set.
2. **Neighborhood loads the entire edge set** (`Edges(Query{})`, no filter at all) to build
   adjacency for one seed (same baseline file).
3. **Symbol resolution scans every node.** `resolveExact`
   (`engine/agenttools/resolve/resolve.go:160`) loads `Nodes(Query{})` — the full node set — and
   compares `SourcePath()`/`QualifiedName()` in Go (baseline:
   `engine/agenttools/resolve/baseline_characterization_test.go`).
4. **agent_brief digests the whole graph.** `buildView`
   (`engine/agenttools/brief/brief.go:198-203`) reads ALL nodes and ALL edges to compute file
   degrees and hotspots (baseline: `engine/agenttools/brief/baseline_characterization_test.go`).
5. **The SQLite backend cannot serve ANY listing selectively today.** Every `Nodes`/`Edges`
   call goes through `ensureCache` → `loadAllFromDB` (`core/graphstore/sqlite.go:687-771`),
   which materializes the ENTIRE graph in memory, then filters in Go
   (`core/graphstore/sqlite.go:841-866`). The spike pins this: a one-kind edge listing triggers
   exactly one whole-graph cache rebuild
   (`TestSpike_TodaysReadPath_LoadsWholeGraphIntoCache`).
6. **The endpoint indexes already exist but the read path never uses them.**
   `edges_from_id`/`edges_to_id` (`core/graphstore/sqlite.go:187-192`) were added for
   DeleteNode's cascade. The spike proves they serve the selective shapes directly:

   ```text
   SELECT id FROM edges WHERE to_id = ?            → SEARCH edges USING INDEX edges_to_id (to_id=?)
   … WHERE to_id = ? AND kind = ?                  → SEARCH edges USING INDEX edges_to_id (to_id=?)
   … JOIN reasons … WHERE e.to_id = ?              → SEARCH e USING INDEX edges_to_id (to_id=?)
   ```

7. **Symbol lookups have no index to use.** `nodes` has no secondary index; equality lookups by
   `qualified_name` or `source_path` are full scans today
   (`SCAN nodes USING INDEX sqlite_autoindex_nodes_1`), and flip to
   `SEARCH nodes USING INDEX …` with the two indexes this ADR proposes
   (`TestSpike_SymbolLookups_FullScanToday_IndexedWithProposedIndexes`).

## Decision

### D1. New core read port: `GraphLookup` (endpoint-selective, in `core/graphstore`)

```go
// core/graphstore — CORE-01
type GraphLookup interface {
    GetNode(ctx context.Context, id model.NodeId) (model.Node, error)
    NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error)
    Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)
    Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)
}
```

- **`Incoming`/`Outgoing`** return the edges whose `To`/`From` equals `id`, optionally
  restricted to the given kinds (zero kinds = all kinds), **provenance intact**, in canonical
  `EdgeId`-ascending order. Cost must be proportional to the node's degree, never to the size of
  an edge class (the G2 gate).
- **`NodesByID`** is the anti-N+1 companion: it returns the found nodes for the given ids in
  canonical `NodeId`-ascending order and **silently skips missing ids** (callers that need
  strictness use `GetNode`). Rationale: `directedLookup` collects opposite endpoints and then
  drops interned externals anyway (`engine/query/service.go:175-190`); a hard error on a missing
  endpoint would make the port unusable for exactly its first consumer.
- **`GetNode`** keeps `Graphstore.GetNode`'s exact contract (`ErrNotFound` sentinel).
- **Canonical order is part of the port contract**, not an implementation courtesy — the
  byte-identical golden outputs (SW-110 AC1/AC2) are the migration gate.
- **No multi-source/batch traversal methods yet** (master §4: batch variants are added only when
  a measured baseline proves the need — see U3).

### D2. New core read port: `SymbolLookupPort` (in `core/graphstore`)

```go
// core/graphstore — CORE-01
type SymbolLookupPort interface {
    QualifiedName(ctx context.Context, qn string) ([]model.Node, error)
    SourcePath(ctx context.Context, path string) ([]model.Node, error)
    Search(ctx context.Context, text string, limit int) ([]RankedNode, error)
    // ExactName(ctx, name) is RESERVED, not part of v1 — see U1.
}
```

- **`QualifiedName`** and **`SourcePath`** are exact-equality lookups returning canonical
  `NodeId`-ascending order — precisely the two comparisons `resolveExact` does in Go over the
  full node set today (`resolve.go:165-183`). Callers pass `model.NormalizePath`-normalized
  paths (normalization stays in the caller, as today).
- **`Search`** is the existing ranked FTS5 lexical search — `Graphstore.SearchNodes`'s contract
  verbatim (it is already selective; it moves into the port so stable consumers depend on one
  read surface).
- **`ExactName`** (bare final-segment name) is **reserved, not part of the v1 Go interface**: no
  stable hotpath consumes a bare-name equality today (resolution falls through to ranked
  `Search`), and `model.Node` carries no bare-name field, so its derivation rule would be
  invented without a consumer. It is added to the interface only when `CORE-02`'s migration
  surfaces one — see **U1**. This keeps the master-plan port shape without inventing an unneeded
  index or semantics.

### D3. SQLite implementation: bound-parameter reads, two new content-neutral indexes

- `Incoming`/`Outgoing` execute the spike's proven statement shape — bound `WHERE to_id = ?` /
  `WHERE from_id = ?` (+ `kind IN (…)` as residual filter) with the `reasons` join — against the
  EXISTING `edges_to_id`/`edges_from_id` indexes. **No schema change for edges.**
- `QualifiedName`/`SourcePath` require two new indexes, proven by the spike to flip the plan
  from `SCAN nodes` to an index search:

  ```sql
  CREATE INDEX IF NOT EXISTS nodes_qualified_name ON nodes(qualified_name);
  CREATE INDEX IF NOT EXISTS nodes_source_path    ON nodes(source_path);
  ```

  They are **content-neutral** (indexes never change row content; listings stay ordered by id, so
  graph bytes, snapshots, and golden outputs are unaffected) and are added to `initSchema`'s DDL
  with `IF NOT EXISTS` — the same pattern as the endpoint indexes, **no
  `graphstoreSchemaVersion` bump** (that stamp versions the physical row layout, which does not
  change).
- The `ORDER BY id` sort appears as `USE TEMP B-TREE FOR ORDER BY` in the plan: it sorts the
  **matched set** (degree-bounded / name-collision-bounded), not the table. Accepted; no covering
  index gymnastics without a measured need.
- **Cancellation:** all port reads take `ctx` and use `QueryContext` (unlike today's cache path,
  which ignores ctx after the initial load).

### D4. Memory backend: adjacency + lookup maps with identical semantics

`MemStore` implements both ports with per-node adjacency indexes (`from → []EdgeId`,
`to → []EdgeId`) and lookup maps (`qualified_name → []NodeId`, `source_path → []NodeId`),
maintained **atomically inside the existing Put/Delete write lock**. Contract identity with
SQLite (same results, same canonical order, same `ErrNotFound`/skip semantics) is enforced by a
shared **conformance suite run against both backends via `Factory`** — the same pattern the
existing contract tests use. Backend-divergence anywhere = red gate (G2: "Memory/SQLite liefern
deterministisch identische Semantik").

### D5. Cache bypass — selective reads never touch the whole-graph cache

- `GraphLookup`/`SymbolLookupPort` reads on SQLite go **directly to SQL with bound parameters**;
  they neither consult nor populate the `memGraph` hot cache. Rationale (pinned by the spike): a
  cache "hit" on today's path costs a whole-graph materialization first — the cache IS the
  full-scan problem on cold/large stores.
- The whole-graph cache and the legacy `Nodes(Query)`/`Edges(Query)` listings **remain unchanged**
  as catalog/export/diagnostic paths (snapshot, wiki, community, conformance). Removing, bounding,
  or opt-in-flagging the cache is a **measured** decision deferred to `CORE-02`'s budget work on
  the EVAL-01 pinned repos (master §6: no full-graph-cache rewrite without measurement) — see U4.
- The TEST-01 AC5 baselines stay armed during `CORE-01` (which adds ports but migrates no
  consumer) and are **flipped one hotpath at a time in `CORE-02`** into "must-be-selective"
  gates, exactly as their drift messages instruct.

### D6. Brief/related aggregates — no N+1 rewrite, measured choice

`agent_brief`'s whole-graph digest (file degrees, hotspots) is a genuine **aggregate**, not a
traversal: replacing it with per-node `Incoming`/`Outgoing` calls would trade one full scan for
N index probes (an N+1 regression). `CORE-02` chooses per measurement on the EVAL-01 repos
between:

1. keeping ONE bounded catalog read for the digest (status quo, honest cost), or
2. adding explicit SQL aggregates (`GROUP BY` degree/file counts) as a **separate, additive**
   port method once the measured need is proven (see U2).

The contract fixed here: brief's aggregates never force `GraphLookup` to grow scan-shaped
methods; aggregate needs get aggregate queries.

### D7. Consumer migration order and gates (`CORE-02`)

1. `directedLookup` (callers/callees/references/definition) → `Incoming`/`Outgoing` +
   `NodesByID`;
2. `Neighborhood`/`impact` → per-hop `Incoming`+`Outgoing` over the frontier (batch variants
   only per U3);
3. `resolveExact` → `QualifiedName`/`SourcePath` (drops the full `Nodes(Query{})` read);
4. `explain_symbol`/`related_files`/`change_risk` → whatever mix of 1–3 they consume;
5. `agent_brief` → per D6.

Per-slice gates: SW-110 golden bytes (AC1) and backend parity (AC2) stay green; the slice's AC5
baseline flips to its selective assertion; `EXPLAIN QUERY PLAN` gates (the spike's helpers) pin
index usage in CI.

## Explicit UNKNOWNs (each with the experiment that resolves it)

- **U1 — `ExactName` consumer.** No stable hotpath needs bare-name equality today. *Experiment:*
  during the `CORE-02` migration, record every lookup shape each of the 12 ops actually issues;
  if a bare-name shape appears, implement `ExactName` (index or expression-index decision then),
  else leave it specified-but-unimplemented and record that in the port doc. Resolve in
  `CORE-02`.
- **U2 — brief aggregate strategy.** Catalog read vs. SQL aggregates. *Experiment:* on the three
  EVAL-01 pinned repos measure brief's wall-clock + peak-RSS with the status-quo digest; adopt
  SQL aggregates only if the digest breaches the (then-measured) budget. Resolve in `CORE-02`.
- **U3 — frontier batching for neighborhood/impact.** Per-hop loops may need
  `Incoming(ids []NodeId)` batch variants on high-degree frontiers. *Experiment:* the CORE-02
  1M-edge fixture (master `MVP-04` heritage) measures per-hop probe counts; add batch variants
  only if the loop shape breaches budget. Until then the port keeps single-source methods only.
- **U4 — whole-graph cache disposition.** Keep, bound, or opt-in. *Experiment:* after all stable
  hotpaths bypass it (D7), measure RSS + latency with and without the cache on the EVAL-01 repos;
  decide delete/bound/flag from data. Resolve in `CORE-02`/`RC-01` window.
- **U5 — latency/rows budgets.** Absolute p95 and rows-scanned budgets are **not invented here**
  (master: "keine Scheingenauigkeit"). *Experiment:* first reproducible EVAL-01 baseline run
  fixes the numbers; they are then versioned as gates (EVAL-02).

## Governed code seams (so `CORE-01`/`CORE-02` implement against this)

- `core/graphstore/graphstore.go` — `Query` (`:45`, stays endpoint-free; the new ports live
  beside it), `Graphstore`/`Factory` (conformance-suite pattern to extend).
- `core/graphstore/sqlite.go` — `initSchema` (`:137`, gains the two node indexes),
  `ensureCache`/`loadAllFromDB` (`:687-771`, bypassed by the ports), `Edges` (`:841`, untouched
  legacy catalog path), endpoint indexes (`:187-192`).
- `core/graphstore/memstore.go` — adjacency/lookup index home (D4).
- `engine/query/service.go` — `directedLookup` (`:136-192`), `collectNodes` (`:224`),
  `Neighborhood`; `engine/query/reader.go` — `Reader` (`:34`, the seam that grows the new ports
  for consumers).
- `engine/agenttools/resolve/resolve.go` — `resolveExact` (`:139-196`).
- `engine/agenttools/brief/brief.go` — `buildView` (`:186-210`, D6).
- Baselines to flip in `CORE-02`: `engine/query/baseline_characterization_test.go`,
  `engine/agenttools/resolve/baseline_characterization_test.go`,
  `engine/agenttools/brief/baseline_characterization_test.go`.
- Spike evidence (this story): `core/graphstore/selective_read_spike_test.go`.

## Consequences

- `CORE-01` has a fixed target: implement D1–D4 behind a shared conformance suite, with the two
  new indexes and the spike's `EXPLAIN` assertions promoted to permanent plan gates. No consumer
  changes in `CORE-01`.
- `CORE-02` has a fixed migration order (D7) with byte-parity and baseline-flip gates per slice,
  and four measured decisions (U1–U4) it must close with data.
- No behavior changes on `main` from this story: the spike test is evidence, not production
  code; today's read paths, cache, and schema are untouched.
- The master plan's P50/P80 for `CORE-01`/`CORE-02` can now be re-based on a known statement
  shape and schema delta (two indexes, zero migrations) — input to the SP-11-gated re-estimation
  checkpoint (master §5).
