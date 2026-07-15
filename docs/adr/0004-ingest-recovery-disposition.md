# ADR 0004 — Ingest Cross-DB Recovery Disposition (SW-118 / ING-DEC)

- Status: Accepted (disposition of record for the `ING-REWRITE` trigger)
- Date: 2026-07-14
- Story: SW-118 — ING-DEC: cross-DB fault injection and recovery disposition
- Spec / Gate: master WBS `ING-DEC`; exit evidence "every commit/kill point
  classified; fix or documented harmlessness"
- Depends on: SW-110 (byte-parity oracles), the existing dirty-unit recovery
  machinery (`markDirtyTx`/`clearDirtyTx`/`RecoverWithRoot`)
- Feeds: `RC-01` (recovery disposition input) and the master plan's
  `ING-REWRITE` stopping rule ("kein Rewrite ohne reproduzierbaren Fehler")

## The two-store problem

One ingest pass commits to TWO databases that cannot share a transaction:

- the **graphstore** (SQLite graph or MemStore) — three batched sessions per
  full pass (write → link → typeresolve; `engine/ingest/ingest.go` IngestAll),
  one batched session per incremental pass;
- the **meta sidecar** (`ingest-meta.db`) — ONE transaction per pass carrying
  the file-content cache, reverse-deps, dirty flags, edit provenance and the
  warm-start semantics stamp.

Graph batches commit **inside** the meta transaction's lifetime, so a process
death between a durable graph commit and the meta commit leaves the two stores
at different generations. This ADR classifies every such kill point, with an
executable proof per class, and records the disposition the master plan's
`ING-REWRITE` trigger consumes.

## Kill-point matrix

| # | Kill point | State at crash | Disposition | Proof |
|---|---|---|---|---|
| K1 | Full pass, before any graph batch | graph untouched, meta rolled back | **SAFE (inherent)** — nothing diverged; retry is a plain cold pass | `TestFaultMatrix_FullPass_KillAtEveryBatchBoundary/kill-before-batch-1` |
| K2 | Full pass, after WRITE batch commit, before LINK | nodes+parser edges durable (incl. purge), no links; meta (cache/stamp) rolled back | **FIXED (this story)** — see "store-derived purge" below; retry full pass converges to fresh-index bytes even when the tree changed in between | `…/kill-before-batch-2` |
| K3 | Full pass, after LINK batch commit, before TYPERESOLVE | as K2 plus link edges | **FIXED (this story)** — same mechanism | `…/kill-before-batch-3` |
| K4 | Full pass, after meta commit (stamp durable), before taint persist / profile metadata / WAL checkpoint | graph+meta consistent and warm-startable; taint findings and `index.profile` metadata stale or absent; WAL not truncated | **DOCUMENTED HARMLESS (Labs tier)** — the graph the 12 stable ops read is complete and stamped. Stale/missing intra-proc taint findings affect only the Labs `analyze taint` readout and self-heal on the next full pass; `index.profile` is cosmetic metadata; a skipped WAL TRUNCATE is reclaimed by SQLite normally | code order `IngestAll` lines after the meta tx; no test needed — no stable-scope state involved |
| K5 | Incremental pass, after phase-1 dirty-mark commit, before any graph write | dirty rows durable, graph+cache untouched | **SAFE (by design, pre-existing)** — recovery replays the units, provenance-idempotent | `TestIngest_CrashRecovery`, `TestProvenance_CrashRecoveryIsIdempotent` (pre-existing) |
| K6 | Incremental pass, after a DURABLE graph write mid-phase-2, before the meta commit | graph partially ahead (e.g. old node deleted, replacement missing); cache/provenance/clear-dirty rolled back; dirty rows still set | **SAFE (by design, now proven)** — replaying the durable dirty rows converges byte-identically; content-addressed IDs make the replay idempotent | `TestFaultMatrix_Incremental_KillAfterDurableGraphWrite` |
| K7 | Any crashed incremental followed by a session open | as K6, plus: nothing ever CALLED the recovery | **FIXED (this story)** — `RecoverWithRoot` existed but had ZERO production callers; the dirty rows would sit forever and a warm start served the divergent graph. `warmOrFullIngest` (the zeroconfig/session seam) now recovers BEFORE the warm/drift decision; a recovery failure falls back to the tolerant full pass | `TestWarmOrFullIngest_ReplaysDirtyUnitsBeforeTrustingTheStore` — constructs the divergence drift cannot see (crash between durable delete and re-put, then disk revert ⇒ disk == cache ⇒ drift silent) |
| K8 | Edit-saga crash points (source write / re-index / rollback) | per edit saga | **SAFE (pre-existing)** — the edit saga has its own snapshot/rollback + provenance replay, exercised by the engine/edit fault-stage suite | `engine/edit` AC-2/AC-3 fault tests (pre-existing) |

## Fixes applied in this story

### Store-derived purge (K2/K3)

`IngestAll` derived its purge set (prior nodes to delete when not re-produced)
from the meta **cache**. After a K2/K3 crash the cache has rolled back while
the graph kept the interrupted pass's nodes — so on a FRESH store the cache is
empty, the purge set is empty, and any node the retry does not re-produce
(renamed symbol, deleted file — the tree changing between crash and retry)
survives as a permanent orphan: the retry is no longer "full" and
fresh-index byte-identity breaks silently.

Fix: the purge set is now derived from the **authoritative store**
(`i.store.Nodes(…)` at the start of the pass). For an uninterrupted store the
two sets are identical (happy-path bytes unchanged — the whole pre-existing
golden suite pins this); for a crashed store the full pass becomes
self-healing from ANY partial graph state. `DeleteNode`'s cascade removes the
orphans' incident edges with them.

### Recovery wired at session open (K7)

`warmOrFullIngest` now calls `RecoverWithRoot(root)` before `CanWarmStart`.
Rationale for the placement: the drift pass already heals every divergence
that shows up as a disk-vs-cache hash difference; what it CANNOT see is a
dirty unit whose current disk content matches the cache while the graph is
mid-edit (K7's revert construction), and it replays no edit provenance. The
dirty rows are precisely the durable record of "the graph may be ahead of the
meta state for these files" — they must be replayed before any trust decision.

## Residual scope (assigned, not forgotten)

- **RUN-01**: read-only session opens (per ADR 0002, sessions may open an
  existing DB without ingesting) must run the same recovery once the
  composition root owns session open. Today the only production ingest-capable
  session seam is `warmOrFullIngest` (wired); direct `IngestAll` callers are
  full passes (self-healing per K2/K3). The Runtime should make
  "open → recover → ready" the single ordering for every session kind.
- **Watch/daemon sessions** are Labs (not in the Focused Core RC); their
  long-running incremental loop rides the same `ingestChanged` machinery
  (K5/K6 safe), and a restart goes through a session open.

## Disposition for the `ING-REWRITE` trigger

**No reproducible, unfixable recovery fault exists.** Every kill point is
either inherently safe, safe-by-design with executable proof, fixed in this
story with a red-without-fix test, or documented Labs-tier staleness. The
master plan's `ING-REWRITE` bet ("Scanner/Parse/Commit/Link/Checkpoint-Phasen,
Journal und Recovery", P80 6–12 PW) therefore has **no trigger** from recovery
correctness: per the stopping rule ("kein Rewrite allein wegen Dateigröße oder
Architekturästhetik"), ING-REWRITE stays untriggered unless the EVAL-02
real-repo gates surface resource/recovery failures the synthetic matrix
cannot.
