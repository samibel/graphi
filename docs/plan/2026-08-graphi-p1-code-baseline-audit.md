# P1 Trust Surface — Source-derived Baseline Audit

**Audited:** 2026-08-03, against working tree `4c455f4cc9f4ec12c6ffdb4310350ba4fd84f828`
(current `main` at audit time). The PRD names design baseline `afa1b68` (merge of PR #81);
that commit is an ancestor of the audited tree and every finding below was taken from the
audited tree, not from the older baseline.
**Method:** Go source only — grep and file reads over `*.go`. No markdown document was
used as evidence for any claim below; the PRD's own §3 claims were treated as assertions
to verify, not as facts.
**Subject:** [`docs/plan/2026-07-graphi-p1-trust-surface-prd.md`](2026-07-graphi-p1-trust-surface-prd.md)
(registered Draft — see its registration record).
**What this document is:** an observation of the code, recorded so the P1 plan starts from
measured ground. It is not the PRD's authority, not a contract freeze, and not a P1 start
decision.

---

## Verdict

**P1 is 0 % implemented.** No P1 artifact exists anywhere in the Go source:

- No `TrustSnapshot`, `TrustAssessment`, `trust-report`, `graph_health`, or
  `trust_report` identifier in any `.go` file (repo-wide grep: zero hits).
- No `engine/trust/` package (the `engine/` directory was listed; no trust entry).
- No `cmd/graphi/trust_report.go`; the CLI dispatch switch
  (`cmd/graphi/main.go:46-161`) has no trust case.
- No `graph_health` MCP tool (tool-name constants `surfaces/mcp/tools.go:19-127`).
- None of the three policies (`exploratory-v1`, `review-v1`, `automated-change-v1`),
  no finding codes, no verdict enum for trust.
- No confidence-tier filter and no strict mode anywhere on the query path:
  `client.Query(ctx, op, symbol, depth)` carries no filter parameter, compound `WHERE`
  accepts only `KIND` (`engine/query/compound/compound.go:72`), and tiers are used only
  for **ordering** (`engine/query/compare.go:5-50`).
- No trust persistence: no `trust.*` key, no trust table, no snapshot digest.

All 15 story slices (P1-001 … P1-015) and all work packages (WP1.0 … WP1.8) are open,
including WP1.0's organizational items — the PRD's own header still reads
"Owner: noch festzulegen".

---

## PRD §3 claims, verified against source

Every raw signal the PRD claims to exist does exist. The PRD's caveat — scattered, partly
in-memory, not generation-bound — is confirmed and is in fact **understated**: all four
signal accessors have **zero non-test callers**.

| PRD claim | Source finding |
|---|---|
| §3.1 `graphi status` versioned JSON | ✅ `statusJSONSchemaVersion = 1` (`cmd/graphi/status.go:23`); fields repo/git/db_path/node_count/profile/last_sync/index/drift/current/recommendation; `full_pass_in_progress` + `lock_held` separate "running now" from "aborted"; exit codes 0/1/2; strictly read-only. **But:** everything is unexported in `package main` — there is no status library to reuse (FR-4 requires extraction). |
| §3.2 linker evidence | ✅ `engine/link/link.go:71` `type Stats struct` with ResolvedDerived, ResolvedHeuristic, Skipped, Ambiguous, ResolvedExternal. |
| §3.2 "in-memory last run only" | ✅ confirmed, and sharper: `engine/ingest/ingester.go:45` `lastLinkStats`, accessor `LastLinkStats()` (`:123`) — **zero non-test callers**; never persisted, lost when the (one-shot CLI) process exits. |
| §3.3 type-resolver evidence | ✅ `engine/typeresolve/check.go:50` `Result` with per-unit `Degraded`/`TypeErrors` (`:106`), `SkippedFiles`, `DroppedIntents` (`:61`). **But:** the struct is consumed transiently in `engine/ingest/typeresolve.go:118-166` and discarded — degradation, type-error and dropped-intent counts survive nowhere; only the confirmed-tier edge rows themselves persist. |
| §3.4 parse-skip diagnostics | ✅ `engine/ingest/ingester.go:221-296`: `SkipReason` (oversize, timeout, max-depth, unreadable, parse-error), source-free `SkipDiagnostic{Path, Reason, Size}`. **But:** in-memory, reset at the top of every pass (`resetSkips`, called from `ingest.go:43`), zero non-test callers of `SkippedDiagnostics()`. |
| §3.5 diagnostic metrics | ✅ `engine/diagnostic/metrics.go:6` `Metrics` (TotalDiagnostics, SuppressedByCategory, DedupCollapsed). Derived on demand; only test callers. |
| edge confidence tier + evidence | ✅ complete and persisted: `core/model/edge.go:19-42` closed tier set {heuristic, derived, confirmed}; tier, confidence [0,1], reason and evidence **enforced at construction** (`NewEdge`, `edge.go:90-92`) — an unprovenanced edge is unrepresentable. Persisted per edge (`core/graphstore/sqlite.go:245-254`); evidence is canonical `file:line`, sorted, capped at 64 (`sqlite.go:815-858`). Typeresolve mints confirmed/1.0 (`engine/typeresolve/check.go:563-580`) with a stale-confirmed sweep (`engine/ingest/typeresolve.go:134-158`); the linker never emits confirmed (`engine/link/link.go:58-68`). |

---

## What exists beyond §3 and shortens the P1 build

- **Store-level tier counting already exists:** `core/graphstore/brief_aggregate.go:124`
  (`GROUP BY confidence_tier`) → `BriefStats.TierCounts`
  (`core/graphstore/lookup.go:106-121`, both backends). The natural seed of the
  §13.8 `GraphTrustFacts` collector.
- **A generation concept already exists:** `beginFullPass`/`finishFullPass`
  (`engine/ingest/warmstart.go:168-211`) mint a full-pass generation nonce binding the
  sidecar (`full_pass_generation`) to the graph (`index.full_ingest_generation`);
  `CanWarmStart` requires equality. Usable as the §13.2 `GraphGenerationRef` anchor.
  Caveats: a random nonce (not monotonic, not commit-derived), and not exposed on any
  surface today.
- **The interrupted-ingest marker exists** (`full_pass_in_progress`), read-only via
  `Ingester.FullPassInProgress` — the raw material for snapshot state INCOMPLETE.
- **Two persistence precedents:** metadata-only `kv_meta` writes are documented as
  snapshot/byte-parity safe (`engine/ingest/ingest.go:266-272`; working example
  `analysis.intraproc_taint.v1`, `engine/ingest/intraproctaint.go:143-170`) — the
  template for `trust.snapshot.v1`. And `engine/analysis/interproctaint/persist.go` is an
  atomic temp+rename, schema-versioned, content-hashed artifact store (currently without
  a production caller) — the template for generation-bound detail artifacts.
- **Labs gating is finished infrastructure:** frozen Stable-12
  (`surfaces/mcp/tools.go:145-158`), `WithLabs()` catalog switch
  (`surfaces/mcp/mcp.go:95`, CLI `-labs` in `cmd/graphi/serve.go:97-107`), dispatch-side
  enforcement (`surfaces/mcp/toolcalls.go:96-102`), `[labs]` description prefix,
  capability filtering. FR-3's "Labs-gated" requirement costs nothing new.
- **The surface template is uniform:** engine package → method on `client.Client` →
  `surfaces/client/direct.go` implementation returning canonical bytes →
  `surfaces/cli` runner → MCP descriptor + handler (worked example: `explain_symbol`
  end-to-end). A trust port follows it mechanically.
- **Verdict scaffolds to reuse:** `internal/doctor` (pass/warn/fail/info/unverified,
  Check/Registry/Runner, `WorstStatus`, `ExitCode` — the best skeleton for
  `trust-report`); `engine/review/gate.go` (pure threshold gate with hashed config — the
  closest structural relative of a trust policy); `internal/audit` (PASS/FAIL/UNVERIFIED,
  deliberately fail-closed); `engine/diagnostic/filter.go:180-222` (tier-threshold filter
  **with a `WithheldByConfidence` tally** — exactly FR-14's "filtered emptiness must not
  present as true emptiness" pattern).
- **Result-scoped tier tallies exist:** `engine/agenttools/shape/shape.go:72-88`
  (`TierTally`) and the heuristic-share risk line in
  `engine/agenttools/brief/brief.go:517-530` — reusable for result-set scope.

---

## Gaps that are prerequisites, not part of any P1 story as written

1. **Status extraction.** FR-4 forbids a second freshness implementation, but the status
   logic is CLI-embedded (`package main`). The composable primitives exist
   (`internal/state`, `internal/gitinfo`, `internal/ingestlock`,
   `cmd/internal/runtime/syncmeta.go`, `engine/ingest` read-only) — a shared read-only
   component must be extracted before FR-4 can be satisfied.
2. **Store aggregation.** `graphstore.Query` has no tier filter, there is no
   `CountEdges`, no kind×tier cross-tab, and no external-boundary count or listing (the
   only external-node counting in the tree is the orphan check in
   `internal/parity/snapshot.go:126-152`). External **edges** are ordinary
   heuristic-tier edges recognizable only via a node-kind join on the target — §13.9
   needs either that join or collect-time counting.
3. **Collector wiring.** The PRD says the raw signals are available "during or
   immediately after ingest"; in code they are gone when the ingest process exits, which
   for the one-shot CLI is immediately. The collector must run in-process, before
   `finishFullPass`, in the same pass that produced the signals.
4. **Snapshot atomicity.** There is no staged-DB/pointer-flip build commit: a full pass
   writes in place across three separate batch transactions
   (`engine/ingest/ingest.go:129-257`), and WAL readers can observe intermediate graphs.
   Crash safety is marker+generation, not isolation. Of §14.4's three permitted designs,
   variant 3 — post-commit write with fail-closed "snapshot unavailable" until complete —
   fits the existing architecture; variants 1–2 are rebuilds.
5. **CI drift guards.** A new CLI command and a new MCP tool each fail the build until
   `docs/coverage-matrix.yaml` gets a row (`internal/coverage/cli.go:26-33`,
   `internal/coverage/coverage.go:101`); the stable set is frozen at exactly 12
   (`internal/coverage/stable.go:41`), so `graph_health` must enter as a Labs tool —
   which is also what FR-3 requires.

---

## Standing constraint

The PRD's precondition — a documented P0 GO — is not met: per
[`docs/plan/2026-07-graphi-p0-completion-checklist.md`](2026-07-graphi-p0-completion-checklist.md),
17 of 18 P0 stories are open and no Go/No-Go has been held. This audit records what the
code contains; it does not authorize starting P1 ahead of that gate.
