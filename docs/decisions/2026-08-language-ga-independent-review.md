# Decision: independent architect + product-owner review of the language-GA program

> ## ✅ OWNER RATIFIED, 2026-08-16 — with one reversal
>
> Two reviewers examined this tree **independently and without seeing each
> other's findings**, then reconciled their two genuine conflicts. This record is
> their reconciled position. **Sami is the decision owner** (ADR 0007/0008) and
> has now ruled:
>
> **The goal is unchanged: GA for EVERY shipped language**, each at its honestly
> achievable capability level, with Go's full proof chain (the original P2
> program). Therefore §7's recommendation to *pause* the language-GA program is
> **OVERRULED**, and §5.2's proposal to retire per-language "GA" vocabulary is
> **declined** — languages keep GA, but every user-facing GA mention of a
> language now carries its capability level beside it (`Java — GA
> (cross-file-heuristic)`, never bare "GA"), which is the reviewers' own
> mitigation adopted as a standing rule.
>
> **Everything in §1 (verified facts) and every soundness/integrity ruling
> stands and is adopted, unchanged.** The blockers are not reasons to stop; they
> are **Wave 0** of the program — the work that must land before any language,
> Go included, may read GA. Priority order §3 is adopted as Wave 0's order.
> **Go's grandfathering ends**: "GA for all languages" includes Go, so Go gets
> real `GA-LANG-go-*` evidence rows (backfilled from the P0/P1 record where
> artifacts exist), closing finding S2.
>
> Execution plan:
> [`../plan/2026-08-graphi-language-ga-execution-plan-v1.md`](../plan/2026-08-graphi-language-ga-execution-plan-v1.md)
> is superseded by the Wave-structured route agreed with the owner; Wave 0 is in
> progress on branch `claude/parity-001-002-m2` (PARITY-002 disclosed, the
> migration race fixed; JVMSOUND-001/002 and the rest next).
>
> The rulings below remain the substance; only §5's open questions 2 and 3, and
> §7's pause, are resolved by this header. Where a ruling merely records a
> verified fact (§1), it never needed ratification.
>
> ## Cross-reference — 2026-08-20 (W1.e / SW-178): the WP-J11 flip gate
>
> Per D6 this is **added**, nothing below is rewritten. SW-178 ratifies the
> nine R1 conditions this record identified as "zero met" into a single
> machine-checkable list — the **WP-J11 flip gate** — and adds the
> index-migration story SW-179's flip demands. The gate is the instrument
> the flip is measured against; the gate is not a decision to flip. Full
> text:
> [`2026-08-language-ga-wpj11-flip-gate.md`](2026-08-language-ga-wpj11-flip-gate.md).
> ADR 0008 is ratified as a whole in the same change
> ([`../adr/0008-jvm-declared-type-resolution.md`](../adr/0008-jvm-declared-type-resolution.md)),
> with D6, D8 and D9 each recorded with the reformulation, re-scope and rule
> from Wave 0 / Wave 1.

- **Date:** 2026-08-16
- **Scope:** the language-GA program (Java/Kotlin first), ADR 0008 D1–D8, the
  PARITY-001 fix, and release sequencing
- **Method:** two reviewers, briefed only with pointers to primary sources and
  the standing product constraints, each explicitly instructed to treat the
  in-flight implementation as *evidence to evaluate, not an approved direction*

---

## 1. Verified facts (not subject to ratification)

Each re-verified independently of the reviewer who reported it.

**F1 — The binder emits FALSE `confirmed` edges. Two classes, both reproduced.**
`JVMSOUND-001` (variadic arity) and `JVMSOUND-002` (signature identity by
written text) are recorded in full, with reproductions, in
[`../plan/2026-08-graphi-language-ga-execution-plan-v1.md`](../plan/2026-08-graphi-language-ga-execution-plan-v1.md) §1.1.
Both produce a *wrong* edge, not a missing one; neither fires a skip counter.

**F2 — Both defects live in the TABLE, not the emitter, and that is why every
gate read clean.** G2b asserts the under-approximation invariant over
*emission*. The emitter obeyed it exactly — it was handed a table that had
already destroyed the varargs marker (`parse_java.go` tables `spread_parameter`
identically to `formal_parameter`) and already conflated two distinct types
(`sigOf` concatenates `p.Type.Base`, the name as written). By the emitter's own
lights nothing was partial, so nothing was dropped. **A binder whose honesty
instrument reads clean while it is wrong is more dangerous than one with no
instrument, because the clean reading is what a flip decision would rest on.**

**F3 — The evidence index has zero PASS rows.** 17 gate rows: 13 UNKNOWN, 4
STALE, **0 PASS**. No `GA-LANG-*` row exists. Under the repository's own rule
(UNKNOWN counts as *not passed*) no language gate may read DONE.

**F4 — `graphi sync` is advertised GA while non-deterministic and undisclosed.**
`readme.md:224` lists it GA (facade). `docs/rc/parity-matrix-real-repo.md`
records six executions over an identical tree and binary producing three
distinct snapshots while `rebuild` was byte-stable. `grep -rn "PARITY-00"` over
`readme.md`, `docs/HOWTO.md`, `docs/FEATURES.md`, `CHANGELOG.md` returns
**nothing**.

**F5 — PARITY-002 cannot arise for Java/Kotlin, and plausibly can for Python.**
Its mechanism is a fan-out over a node *set*. `engine/link/resolve_common.go`:
JVM imports emit **one** file→package edge via a `packageNodeByPath` map lookup
(the code's own comment: "no false fan-out"); **Python** imports loop
`clausePackageFileNodes(...)` and emit one edge per file node — structurally the
same shape as Go's `packageFileNodes` fan-out. Verified by reading the emission
code; the Python exposure is a *shape*, not a measurement, and is not claimed as
a defect.

**F6 — The JVM corpus pins carry no stratification.** guava, okio and
kotlinx.serialization all have `properties: null`; the `stratification` block
names Go repositories only.

---

## 2. Reconciled rulings

Where a reviewer changed position, that is recorded — the concessions are the
evidence that the two reviews were genuinely independent.

| # | Ruling | Status |
|---|---|---|
| R1 | **WP-J11 (default flip) is blocked.** Nine conditions, **zero met**. | unanimous |
| R2 | **Soundness before recall.** No recall study is meaningful against a binder known to mis-bind. | unanimous |
| R3 | **The oracle must run at corpus scale.** WP-J9 compiles only checked-in fixtures; guava (3204 `.java`) certainly contains varargs. *Fixture-scale zero-tolerance is not zero-tolerance* — today it is a fixture check wearing a gate's name. | unanimous |
| R4 | **G2b must be restated over the TABLING step**, not only emission: any construct the table cannot represent faithfully must mark its members unbindable. Both defects would have been caught; neither was. | unanimous (new gate) |
| R5 | **Legible abstention is a hard gate** for `callers`/`callees`/`references`, **every language, every level — including Go**. `3 confirmed callers · 14 sites not bound (inferred val: 9, lambda receiver: 5)` is honest at 20% and useful at 20%. Requires plumbing the skip counters into `typeresolve.Result`, where they are today deliberately excluded. | unanimous (each arrived at it separately) |
| R6 | **Kotlin's ≥50% recall threshold is dropped.** The measured share, *with a denominator*, per language, on the real pins, remains a **required published artifact** answering ADR 0008 D2 — but as an artifact, not a bar. `3517 typed sites` is not that answer. | PO conceded |
| R7 | **No Java/Kotlin level split.** Both carry open `JVMSOUND` defects; both are blocked on the same thing. | PO withdrew the split |
| R8 | **D8 is over-broad and must be rewritten** as a per-defect applicability test rather than a blanket gate — free, and it stops the next PARITY-0xx blocking JVM work for a reason that does not apply. | unanimous |
| R9 | **WP-J7 unblocks as a PARALLEL track** — must not draw effort from the top of the priority list, and its output is a published matrix, never an input to a flip. | unanimous |
| R10 | **G4 may read PARTIAL**, provided every open defect is **deterministic and disclosed**. Applies to Go too: PARITY-002 fails this and must be fixed or explicitly caveated. | PO's qualifier adopted |
| R11 | **The PARITY-001 fix stays** — right diagnosis, right direction, wrong shape. Do not revert. Follow-up booked in §4. | architect |
| R12 | **The gate roll-up is withdrawn** — all Java/Kotlin gates read UNKNOWN, G2 reads FALSE. Already applied. | unanimous |

### The one number that was withdrawn

The architect argued against Kotlin's threshold with "Go's own typeresolve
contributes ~9% of edges on cobra." Both reviewers then agreed it does not
measure what the threshold measured: the denominator is *all edges of all kinds*
(not call sites), and — decisively — `engine/ingest/typeresolve.go` documents
that a confirmed edge **replaces** the heuristic edge for the same relation, so
every upgraded-in-place edge contributes **zero** to that delta. It is a strict
structural lower bound, taken from a crashed-store reading captured for another
purpose. **Go's confirmed share is UNMEASURED in both directions.** The number
should not be cited by anyone as a Go recall baseline.

---

## 3. Priority order (agreed)

Ranked by who is hurt if it is not done. The binder is default-off, so **no
shipped user can currently receive a false confirmed edge**, while every user is
exposed to non-deterministic `sync` today — which is why F1, despite being a
stop-ship, does not take rank 1.

1. **Disclose PARITY-002** on user-facing surfaces (readme "Known limits",
   `sync -h`, `doctor`/`status`). Hours of work; converts an undisclosed defect
   in a GA operation into a disclosed one.
2. **Fix PARITY-002.**
   **2a. Fix `JVMSOUND-001/002`** — beside, not above: a stop-ship for an
   unshipped path does not outrank a live defect in a GA operation. It outranks
   everything below, and must land before any further JVM feature work.
3. **`trust_package_evidence_v4` migration race** — `CREATE TABLE` without
   `IF NOT EXISTS`; two-line fix, currently causing CI red.
4. **Re-baseline the product** — WP2/WP4 are STALE, freshness p95 measured
   ~3.2× over its gate and never re-measured. Sequence *after* 2, since both
   parity fixes move the candidate.
5. **Stratify the pins that exist** (F6) rather than adding a fourth.
6. WP-J11 — gated, see R1.
7. Rollout waves — not before wave 1 produces a shippable language.

*Parallel, non-competing:* WP-J7 (R9), and creating the `GA-LANG-*` rows **born
UNKNOWN**, which is cheap and is the instrument that makes every later claim
checkable.

---

## 4. Follow-up booked against the PARITY-001 fix (R11)

The fix removed a permanent, shipped divergence and must stay. Its shape needs
correcting while the ordering constraint is fresh:

1. **Prefer folding the purge into batch 1**, matching `IngestAll` exactly, with
   a test pinning `nodeReferencedByOtherFile` under pre-upsert cache rows. If
   infeasible, keep the separate batch and do (2)+(3) regardless.
2. **Add an incremental batch-count coverage guard** mirroring the full pass's
   (`faultmatrix_test.go` fails the build if `IngestAll` opens an uncovered
   batch; the incremental path has no such guard, which is why a new commit
   boundary could be added silently — and one was), plus an incremental kill
   test on a **delete-shaped** change set crossing the purge/link boundary.
   The existing incremental fault test kills on a two-file tree with no deletion
   and therefore cannot reach the new window: the suite passing is not evidence
   about it.
3. **Update ADR 0004** — its "one batched session per incremental pass" is now
   wrong by two, and its K-matrix does not enumerate the purge boundary.
4. **Resolve the contradiction between published artifacts:** `parity-classes.yaml`
   now reads `delete_file: PROVEN` while `parity-matrix-real-repo.md` still
   publishes the same class as a live FAIL. Re-run `internal/parity` over the
   pinned clones and republish, or add a dated superseded-pending-re-measurement
   header. Leaving both live and contradictory is the one option that is not
   acceptable.

---

## 5. Open for the owner

1. **Ratify or amend §2.** In particular R5 (legible abstention as a
   cross-language gate) and R4 (restating G2b) are new scope neither ADR carries.
2. **The GA vocabulary.** The product owner argues "GA" should describe the
   *product* (12 ops · CLI + MCP · CGo-free) and that languages should carry
   **capability + evidence** as paired tokens (`Java — cross-file-heuristic,
   Verified`), on the grounds that a tier label whose meaning inverts by footnote
   is not a tier label. This is a naming change with real churn
   (`ga-language`, `CheckGALanguages`, `stability-tiers.md`) and is the owner's
   call, not a reviewer's.
3. **Whether to pause the language-GA program.** The product owner's verdict is
   *"no, not at its current weighting"* — pause new scope, keep the
   infrastructure (WP-J0/J1/J9 and the per-family YAML pattern are judged
   durable and correctly built early), ship the honest interim position, and
   move the primary effort to the Go path. The architect accepted the sequencing.
   **This is the largest call in the record and belongs to the owner.**
4. **Add D9 to ADR 0008** for the mixed-language-directory sweep exemption
   (`resolver.go` marks a directory holding both `.java` and `.kt` as
   stale-sweep exempt, so confirmed edges there survive every `sync` forever —
   in exactly the layout Kotlin-in-Java adoption produces, with zero parity
   coverage). Currently a code comment; it is a soundness decision.
