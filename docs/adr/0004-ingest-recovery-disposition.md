# ADR 0004 — Ingest Cross-DB Recovery Disposition (SW-118 / ING-DEC)

> ## ADDED 2026-08-19 (SW-169) — the incremental write-batch ordering invariant
>
> Per D6 nothing below is rewritten; this section is **added on top** and names
> what it supersedes. It records the PARITY-001 *hardening* ruling: the defect
> was closed by measurement on 2026-08-16 (`d8f1fbb`), but nothing structural
> stopped the ordering drifting back. It also answers the reverse-dependency
> ordering question carried from ADR 0009 review round 2, finding 6, and the
> product question PARITY-001's record left open.
>
> **Correction to §"The two-store problem" below, stated rather than edited.**
> That section says the graphstore takes "one batched session per incremental
> pass". At the time of writing it did. It does not now: a delete-shaped
> incremental pass opens **four, or five when it orphans an interned external
> node** — measured, not read off the source, by instrumenting the store
> (`engine/ingest/purge_ordering_test.go`). The kill-point matrix's K5/K6 rows
> are unaffected, because they classify *when* the graph can be ahead of the meta
> transaction, not how many batches carry it.
>
> ### D1 — Disposition: the deleted-path purge KEEPS ITS OWN COMMITTED BATCH
>
> The declared shape of an incremental pass over a Go change set that deletes a
> file, enforced by `TestIncrementalBatchShape_DeleteShapedOrderingGuard`:
>
> | # | batch | content |
> |---|---|---|
> | 1 | parse-write | nodes and intra-file edges of every reprocessed file. It **may** delete a node a reprocessed file no longer declares (`commitParsed`'s identity-not-reproduced delete); it may **not** delete a node of a purged path |
> | 2 | **purge** | `DeleteNode` of exactly the vanished files' nodes, **and nothing else**, committed before batch 3 opens |
> | 3 | link | the cross-file re-link: stale-edge sweep, the interned external node, cross-file edges; **no deletes of nodes** |
> | 4 | orphan-sweep | **conditional** — present only when this pass orphaned an interned external node. Pure node deletes, none of them a purged path's |
> | 5 | typeresolve | whole-repo confirmed-tier edge upserts |
>
> Rows 1 and 4 are stated this precisely because an adversarial review defeated
> the first, strictly positional version of the guard with two ordinary change
> sets against **correct** product code — a surviving file that loses a symbol,
> and a delete that orphans a stdlib reference. Both are now permanent fixtures
> rather than caveats.
>
> **What the guard cannot see, recorded so it is not assumed away.** It observes
> WRITES; PARITY-001 was caused by a READ (`linkFiles` streaming the live store).
> A reordering that leaves the write shape intact is invisible to it — verified:
> hoisting `sweepOrphanExternalNodes` to just after the purge commit passes the
> guard and is caught instead by `TestPurgeOrdering_DeleteThenReaddConverges` and
> the pre-existing `TestLink_GoExternalNode_InterningLifecycle`; sharing the
> reverse-dep pass's `SymbolIndex` with `linkFiles` reintroduces PARITY-001 with
> an unchanged batch shape and is caught by the kill test and by the graphstore's
> own `edge references unknown node` endpoint check. The guard is one layer of
> three, not the whole net.
>
> The alternative — folding the purge into batch 1, the way `IngestAll` folds
> it — is **rejected**, and the reason is measured rather than argued. A folded
> prototype was built and run: the full suite passes, `engine/conformance`
> passes, and the delete-shaped kill test passes. What flips is a **two-step**
> sequence: delete a file that another package imports, then restore it. With
> the separate purge batch that sequence converges with a full rebuild; with the
> purge folded into batch 1 it **diverges permanently**
> (`TestPurgeOrdering_DeleteThenReaddConverges`, red on MemStore and SQLite
> under the fold).
>
> The mechanism is the reverse-dependency translation, which sits between batch
> 1's commit and the purge. `reverseDepKeys` translates the importer's forward
> ref into the directory key space through `SymbolIndex.DirsForImport`, and both
> of that function's bases read the **live index**. Folding the purge into batch
> 1 moves the translation to the far side of the purge, so the deleted
> package's directory is already gone, `DirsForImport` returns nothing, and the
> importer's cascade row is stored under the raw import path
> `example.com/m/tax`. `dependentsOf` only ever looks a row up by the changed
> file's **directory** or its **file path**, so that row is unreachable forever
> and the importer is never re-linked. The fold therefore imports PARITY-004
> (D3 below) into the incremental path.
>
> Second reason, stated because it is real and not merely procedural: under
> AC-7 this story is test-and-docs-only, and folding is a product-byte change
> that would need its own ADR, a candidate move and a two-dispatch
> re-measurement (D7) that is currently not publishable at all.
>
> ### D2 — Reverse-dependency translation ordering: DELIBERATELY ASYMMETRIC
>
> The incremental pass runs the translation **before** the purge; the full pass
> runs it **after**. ADR 0009 review round 2 finding 6 recorded that asymmetry
> as meta-only and harmless "because the translation is metadata rather than
> graph bytes". **That justification is refuted by measurement and must not be
> re-used.** The translation does write only metadata, but the metadata is the
> reverse-dependency index, and that index decides which files a LATER pass
> re-links — which decides graph bytes. The two orders produce different keys
> for the same tree (`tax` vs `example.com/m/tax`), and only one of them is
> reachable by `dependentsOf`.
>
> The asymmetry is therefore retained **deliberately, and for the opposite
> reason to the one previously given**: the incremental order is the one that
> produces the reachable key. Aligning it with the full pass would spread a
> defect, not remove one.
>
> The invariant that DOES hold, and that is pinned rather than asserted
> (`TestReverseDepTranslation_WritesNoGraphBytes`): within the pass that runs
> it, the translation issues **no graph write at all** — no unbatched write, and
> no batch open across its window — so its position relative to the purge cannot
> move a graph byte in that pass. Its effect is entirely on the NEXT pass.
>
> ### D3 — PARITY-004, found while ruling on D2, filed and NOT fixed
>
> A **full** pass over a tree containing a dangling intra-module import writes
> the importer's reverse-dependency row under the unresolvable import path,
> where `dependentsOf` can never find it. Restore the missing package and
> `graphi sync`, and the importer is never cascaded: the stale interned external
> node and its `heuristic` `calls` edge survive beside the now-correct
> `confirmed` edge, and the `imports` edge a rebuild emits is missing.
> Reproduced through the built CLI on SQLite — `graphi sync` 7 nodes against
> `graphi rebuild` 6, unrepaired by three further syncs — and pinned as
> executable data by `TestParity004_DanglingIntraModuleImportBreaksTheCascade`,
> which fails **with instructions** the moment the defect is fixed. Filed on
> `projects/graphi/backlog.md`; disclosed in readme "Known limits".
>
> Its user-visible cost is **measured, and smaller than the node-count
> difference suggests**: on the same fixture `neighborhood` loses the `imports`
> edge (5 edges against a rebuild's 6), `related_files` returns the same files
> in a different rank order with weaker evidence, and `callers`, `callees`,
> `impact` and `search` are identical — interned external nodes are excluded
> from those operations anyway. One fixture, one profile, one shape of dangling
> import; that is not a frequency estimate.
>
> **Escalation, not a decision.** The surviving heuristic edge is a WRONG edge,
> which D5 (zero-tolerance soundness) calls stop-ship unqualified — and whether
> D5 binds `heuristic`-tier edges is the same owner question LINK-002 §9 already
> raised and that is still open. The doctor `known-defects` half of the D8
> disclosure contract is **not** landed here, because `internal/doctor/checks.go`
> is compiled and AC-7 pins this story to a byte-unchanged product tree; that is
> the same D8-versus-bytes tension the Wave 0 handoff already escalated, and it
> is named here rather than quietly resolved.
>
> ### D4 — The carried product question (PARITY-001's open item), answered
>
> *"Is minting an external node for a vanished intra-module symbol the right
> behaviour at all?"* Ruling: **keep it, and stop treating the question as
> open** — with one modelling defect recorded rather than fixed.
>
> Keeping it is right because the fact the node records is TRUE: the call site
> still exists in the source, and its target is no longer in the indexed graph.
> Dropping the edge instead would make `callers`/`callees`/`impact` silently
> under-report a live call site, which is the failure mode this programme treats
> as worse than an honest heuristic. The alternative ("emit nothing") also
> cannot be reached without diverging from the full pass, which is the reference
> by definition.
>
> What is NOT right, and is recorded as a modelling defect rather than fixed: a
> **dangling intra-module** target and a **third-party/stdlib** target are the
> same fact to the node model (`kind: "external"`), and they are not the same
> fact. Only the edge's `reason` string distinguishes them today. D3's measured
> consequence is the first evidence that the conflation costs something: the
> external node's edge is exactly what keeps the stale node alive across a
> re-add, because the orphan sweep reaps only externals with **no** incident
> edge. Splitting the kinds is a product-byte change with its own ceremony and
> its own ADR, and it is the owner's call, not this story's.

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
