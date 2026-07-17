# Real-World Report Card

Two independent external testers ran graphi against real projects and found two
serious gaps. This page is the honest before/after record — and, per graphi's
"measured, not asserted" rule, **every number here is reproducible from a
checked-in test or gate**, not a hand-maintained figure. The command that
produces each row is in the last column.

## The two field findings

1. **vuln-go (a ground-truth security app).** `graphi analyze taint` found
   **0 of 4** real injections and reported `solved: true, flows: []` — i.e. a
   confident "all clear" on a demonstrably vulnerable app. Root cause:
   unresolved external call targets (`os/exec.Command`, `database/sql.DB.Query`,
   …) were dropped during linking and never became graph nodes, so the config's
   sinks could never match, and even once materialized there was no
   source→sink propagation path.
2. **Spring-Boot monorepo (11,746 files).** A 4m48s index, a 2.3 GB SQLite DB,
   4.27M edges over 55k nodes, minutes of silence in the link phase, and a
   `diagnose` output so noisy it was unusable (`dead_symbol` firing on every
   `@Test`/`@Bean`, `unresolved_reference` once per edge).

## Scorecard — before → after

| # | Metric | Before | After | Reproduce |
|---|--------|--------|-------|-----------|
| 1 | Taint recall (vuln-go) | **0/4**, silent all-clear | **5/5**, 0 false positives, precision ~1.0 | `go test ./engine/ingest/ -run TestTaintE2E_VulnGoRecall -v` |
| 2 | Import edges/node (fan-out) | **15.56** (→ 4.27M edges on Spring) | **0.96** (budget < 8) | `go test ./engine/ingest/ -run TestLinkFanout_EdgeExplosionBudget -v` |
| 3 | Storage bytes/edge | **~500** (→ 2.3 GB) | **226.7** (budget < 360) | `go test ./engine/ingest/ -run TestStorageBudget_BytesPerEdge -v` |
| 4 | Full index time | 4m48s | proxied¹ | `go run ./cmd/bench -budget bench/bench-budget.yml` |
| 5 | Link-phase progress | minutes of silence | **5 incremental events** (0→200) | `go test ./engine/ingest/ -run TestLinkProgress_Incremental -v` |
| 6 | dead_symbol false positives | "very many" (@Test/@Bean/main) | **0 warnings** on entry points | `go test ./engine/ingest/ -run TestDiagnose_EntryPointsNotDead -v` |
| 7 | unresolved_reference diagnostics | O(edges) — one per edge | **1 per target** (with a count) | `go test ./engine/diagnostic/ -run TestUnresolvedRef_AggregatedByTarget -v` |

¹ Full-index wall-clock time is machine-bound and flaky as a checked-in
assertion, so it is not a synthetic gate. It is covered by the bench harness's
`full_index_ms` budget plus metric 5 (the link phase is no longer a silent
block), and is dominated by metrics 2+3 — the Spring index's cost was the 4.27M
edges (metric 2 cuts that ~16×) and their storage (metric 3 halves the per-edge
bytes).

## How the fixes work (one line each)

- **Taint 0/4 → 5/5.** Materialize dropped external call targets as interned
  `external` nodes (import-alias selectors + syntactic receiver-type inference
  for `db.Query` → `database/sql.DB.Query`), then a new intra-procedural
  dataflow that roots taint at source-typed parameters, propagates use-based
  through local assignments with sanitizer kills, and flags a tainted sink
  argument. Precision is by construction: `exec.Command("uptime")` (constant
  arg) and a `strconv.Atoi`-sanitized path produce no finding.
- **Edge explosion.** A Java `import` no longer fans out to every same-basename
  directory repo-wide; it resolves to a single interned `package` node.
- **DB size.** Edges are no longer FTS-indexed (nothing full-text-searches
  edges), and the highly-repetitive edge `reason` is interned into a dictionary.
- **Link silence.** The link phase emits incremental progress per file batch.
- **Diagnose noise.** A non-identity node `Meta` (annotations + flags, read from
  the Java `modifiers` node) lets `dead_symbol` exempt entry points
  (`@Test`/`@Bean`/`main`/test paths) and `safe-delete` refuse to remove a live
  bean; `unresolved_reference` is aggregated to one diagnostic per target.

## Reproduce everything

The package-level gates run as normal `go test` checks under the CGo-free
default build; the full-index measurement is executed by the benchmark command:

```sh
CGO_ENABLED=0 go test ./engine/... ./core/...
CGO_ENABLED=0 go run ./cmd/bench
# or the whole suite with the same fail-closed stream/status validation as CI:
CGO_ENABLED=0 go run ./cmd/testgate
```

The commands remeasure the behavior and the gates hard-fail when their declared
boundaries regress. Exact table values are historical snapshots and may move
inside those budgets; they are not pinned as byte-exact performance claims. The
design and per-package details are in
[`docs/plan/superseded/2026-07-produkt-brief-agent-pipeline.md`](plan/superseded/2026-07-produkt-brief-agent-pipeline.md)
and the gate scorecard in
[`docs/plan/superseded/wp00-red-gates.md`](plan/superseded/wp00-red-gates.md). Both were
superseded on 2026-07-17 (SW-117) by
[`docs/plan/2026-07-graphi-9of10-execution-plan.md`](plan/2026-07-graphi-9of10-execution-plan.md)
and are kept as the archived record of that design, not as current instruction.
