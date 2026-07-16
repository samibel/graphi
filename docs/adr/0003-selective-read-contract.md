# ADR 0003 — Selective-Read Contract and Stable Hotpaths

- Status: **Accepted and implemented for the Stable tier** (`CORE-01`/`CORE-02` green)
- Original decision: 2026-07-14
- Implementation status reconciled: 2026-07-15
- Scope: endpoint reads, symbol lookup, bounded hydration, aggregate reads, and Stable consumers
- Canonical implementation: `core/graphstore/lookup.go`

## Status of this document

This ADR is no longer a future selective-read design. Both reference backends implement
the ports, the Stable consumers use them, and the old whole-catalog baselines have been
flipped into must-be-selective gates.

Performance evidence is less complete than the implementation evidence. The committed
2026-07-15 `ubuntu-latest` reports were produced by the previous harness. They remain
historical raw data, but they are not directly comparable with the current harness,
which now includes all 12 Stable operations, degree-stratified samples, semantic outcome
checks, and a post-Stable-suite MAXRSS sample. A new reference run is required before
claiming a measured post-change speed or memory improvement.

## Historical problem

Before `CORE-01`/`CORE-02`, degree-shaped requests used graph-shaped reads:

- directed queries loaded an entire edge kind and filtered it in Go;
- neighborhood loaded all edges;
- exact agent-tool resolution loaded all nodes;
- search results were hydrated one node at a time;
- `agent_brief` loaded full node and edge catalogs;
- SQLite legacy listings populated the whole-graph `memGraph` cache before filtering.

The original evidence remains in
`engine/query/baseline_characterization_test.go`,
`engine/agenttools/resolve/baseline_characterization_test.go`,
`engine/agenttools/brief/baseline_characterization_test.go`, and
`core/graphstore/selective_read_spike_test.go`. Those files now assert the corrected
behavior rather than preserving the regression.

## Decision implemented

### D1. Endpoint-selective graph reads

`graphstore.GraphLookup` is the Stable graph-read seam:

```go
type GraphLookup interface {
    GetNode(context.Context, model.NodeId) (model.Node, error)
    NodesByID(context.Context, []model.NodeId) ([]model.Node, error)
    Incoming(context.Context, model.NodeId, ...model.EdgeKind) ([]model.Edge, error)
    Outgoing(context.Context, model.NodeId, ...model.EdgeKind) ([]model.Edge, error)
}

type BoundedGraphLookup interface {
    IncomingBounded(context.Context, model.NodeId, int, ...model.EdgeKind) ([]model.Edge, bool, error)
    OutgoingBounded(context.Context, model.NodeId, int, ...model.EdgeKind) ([]model.Edge, bool, error)
}
```

The contract is deterministic and backend-independent:

- full incident edges preserve provenance and sort by `EdgeId`;
- bounded incident reads return at most the positive limit and report whether
  matching edges were omitted. Explicit kind filters sort by `EdgeId`; zero
  kinds means all kinds and sorts by `(EdgeKind, EdgeId)`. A non-positive limit
  returns `ErrInvalidLimit`;
- `NodesByID` collapses duplicate IDs, skips missing IDs, and sorts by `NodeId`;
- `GetNode` retains the strict `ErrNotFound` behavior;
- all methods take a context and propagate cancellation/errors.

SQLite `NodesByID` sorts and deduplicates before issuing bound `IN` queries in chunks of
900, below SQLite's common 999-variable floor. The >33k-ID regression is covered; a
large traversal cannot fail merely because hydration exceeded one SQL statement's host
parameter limit.

**Evidence:** `core/graphstore/lookup.go`,
`core/graphstore/sqlite_lookup.go`,
`TestLookupContract_IncomingOutgoing`,
`TestLookupContract_BoundedIncomingOutgoing`,
`TestLookupContract_NodesByID`,
`TestSQLiteNodesByID_ChunksBeyondHostParameterLimit`.

### D2. Selective symbol resolution

`graphstore.SymbolLookupPort` provides:

- exact `QualifiedName` lookup;
- exact normalized `SourcePath` lookup;
- ranked lexical `Search`.

SQLite executes the exact lookups through `nodes_qualified_name` and
`nodes_source_path`. The plan gates require index search rather than a node-table scan.
Memory uses maps maintained under the store's write lock. Writes, deletes, and snapshot
loads rebuild or update the indexes so selective reads cannot become stale.

`ExactName` is intentionally absent. There is no Stable consumer with a defined bare-name
equality contract; adding an API and index without one would invent semantics.

**Evidence:** `core/graphstore/sqlite_lookup.go`, `core/graphstore/memory.go`,
`TestSpike_SymbolLookups_UseProductionIndexes`,
`TestLookupContract_SymbolLookups`,
`TestLookupContract_WritesKeepIndexesFresh`,
`TestLookupContract_MemLoadRebuildsIndexes`.

### D3. Direct SQLite reads bypass `memGraph`

SQLite `Incoming`, `Outgoing`, their bounded variants, `NodesByID`, `QualifiedName`, and
`SourcePath` query the database directly with bound parameters. They neither consult nor
populate the legacy whole-graph cache. Exactly two `(endpoint, kind, id)` indexes — one
per direction — let bounded incident reads stop in deterministic order after `limit+1`
rows per retained kind; sparse kind filters do not scan unrelated edge kinds. Redundant
endpoint-only and `(endpoint,id)` indexes are deliberately absent because they breach the
armed per-edge storage budget and amplify ingest writes. Memory uses deterministic
endpoint+kind treap indexes: expected-logarithmic writes avoid the quadratic ingest cost
of inserting random EdgeIds into sorted slices. A second ordered distinct-kind treap per
endpoint lets an unfiltered `limit=1` read avoid enumerating or sorting every stored kind;
prefix reads therefore do not scan the full degree even for one unique kind per edge.

The legacy `Nodes(Query)`/`Edges(Query)` and `memGraph` remain for catalog-shaped Labs or
internal consumers. Their presence is not evidence that a Stable hotpath uses them.
Conversely, the selective gates do not prove that every Labs path is memory-bounded.

**Evidence:** `core/graphstore/sqlite_lookup.go`,
`core/graphstore/selective_read_spike_test.go`.

### D4. Compact aggregate reads for `agent_brief`

`BriefAggregatePort.BriefStats` is an explicit aggregate seam. It returns total counts,
confidence-tier counts, per-file symbol/edge-endpoint counts, and a caller-bounded list of
top inbound symbols.

- SQLite performs `COUNT`, `GROUP BY`, and bounded top-inbound queries in one read
  transaction. Only O(files + requested top symbols) rows cross the SQL boundary.
- Memory computes the same result under one read lock without copying the full catalogs.
- File rows and top symbols have fixed deterministic ordering.

`agent_brief` reports the graph view as unavailable when the aggregate seam is absent; it
never falls back to `Nodes(Query{})`/`Edges(Query{})`.

**Evidence:** `core/graphstore/brief_aggregate.go`,
`engine/agenttools/brief/brief.go`,
`TestBriefAggregateContract_BackendParityAndNoCacheRebuild`,
`TestSelectiveGate_Brief_UsesCompactAggregate`.

### D5. Stable consumer migration

The implemented Stable paths are:

1. `callers`, `callees`, `references`, and `definition` use one incident lookup plus one
   batched `NodesByID` hydration. `definition` follows incoming `defines` because ingest
   emits definer/container → defined symbol.
2. `neighborhood` expands only reached endpoints and batch-hydrates nodes.
3. Stable `impact` expands through `IncomingBounded`/`OutgoingBounded`, batch-hydrates a
   bounded candidate window, and derives independent node and returned-edge work caps
   from `MaxNodes`. Distinct requested kinds are deduplicated and capped at
   `min(2× MaxNodes, 16)` before backend probes. Canonical selection retains only the
   cap plus one truncation-proof value, so a transport-sized untrusted kind list cannot
   cause input-proportional auxiliary allocation. Any omitted kind, edge, node, or
   hydration window is reported as `truncated`, never complete.
4. exact agent-tool resolution uses `QualifiedName`/`SourcePath`; lexical fallback
   hydration is one `NodesByID` batch.
5. `explain_symbol`, `related_files`, and `change_risk` reuse those selective seams and
   propagate read errors instead of converting them into plausible empty answers.
6. `agent_brief` uses `BriefAggregatePort` plus bounded topic lookups.

**Evidence:** `engine/query/service.go`, `engine/analysis/impact.go`,
`engine/agenttools/{resolve,related,risk,brief}`, and their `TestSelectiveGate_*` tests.

### D6. Evaluation-only degree sampling

`DegreeSamplePort.DegreeStratifiedSymbols` returns a deterministic function/method sample
ranked by incident degree and distributed across quantile buckets. It exists so the
current evaluation harness does not benchmark only a low-entropy `NodeId` prefix. It is
not a product operation and does not widen the Stable client capability seam.

**Evidence:** `core/graphstore/degree_sample.go`, `cmd/eval/fullrun.go`.

### D7. Cross-backend and error semantics are gates

Memory and SQLite must return the same ordered nodes, edges, metadata, aggregates, and
missing-ID behavior. Selective read errors are returned to the operation caller. Silent
fallback to a whole graph or a successful empty answer is rejected.

**Evidence:** `TestLookupContract_CrossBackendIdentity`,
`core/graphstore/lookup_contract_test.go`, agent-tool injected-error tests.

## Performance evidence: what is and is not proven

The committed reports in `docs/eval/runs/2026-07-15-ubuntu-latest/` belong to commit
`71353f90720e079b84b7a0549bd51fc632bcfe37` and the previous harness. They prove the
numbers recorded in those files only:

- guava indexing reported 13,216 ms, a 33.4 MB database, and 11,821 MB process MAXRSS;
- the MAXRSS sample was taken **immediately after `IngestAll`**, before the warm operation
  suite;
- guava `agent_brief` p95 was 183,933 µs in that historical run;
- the historical structural pool omitted `impact`, did not record semantic checks for
  all 12 Stable operations, and did not take a post-Stable-suite MAXRSS sample.

It is therefore false to attribute the 11,821 MB peak to `agent_brief`, Stable reads, or
whole-cache materialization. The measurement occurred before those operations. The root
cause of that peak is **UNKNOWN**. It is also false to compare the historical p95 pools
directly with a current-harness result as if only the implementation changed.

`docs/eval/hero-budgets.json` retains the historical numeric ceilings so runs fail closed,
but they are provisional compatibility limits, not a validated post-change ratchet. A
fresh `ubuntu-latest` run on the current commit and current harness must establish the
next comparable baseline.

## Explicit UNKNOWNs

- **U1 — current reference performance.** Accuracy and selective-read shape are gated,
  but current `ubuntu-latest` latency and post-Stable-suite RSS for cobra/flask/guava are
  **UNKNOWN** until a new full matrix run is pinned.
- **U2 — 11.8 GB historical MAXRSS cause.** It was observed immediately after ingest.
  Parser allocation, linker state, SQLite behavior, Go heap policy, runner variance, and
  other causes have not been isolated. Causality is **UNKNOWN**.
- **U3 — multi-source incident batching.** Impact and neighborhood use single-source
  incident probes per frontier with batched hydration. Whether a multi-source SQL edge
  query materially improves high-degree workloads under the current harness is
  **UNKNOWN**.
- **U4 — Labs cache exposure.** Stable hotpaths bypass `memGraph`; peak memory and cache
  policy for catalog-shaped Labs consumers remain **UNKNOWN** at production scale.
- **U5 — bare-name resolution.** No Stable contract currently needs `ExactName`. Its
  matching and ambiguity semantics remain **UNKNOWN** until a real consumer requires it.

## Consequences

The architectural decision is closed for the Stable tier: retain the graphstore and its
selective ports, not rewrite them. The next work is evidence, not another read-port
redesign: run the current harness on the pinned reference repositories, diagnose ingest
RSS independently, and only then tighten comparable budgets.
