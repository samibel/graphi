# graphi — Language-GA execution plan v1 (the ordered path to Java/Kotlin GA, then the rest)

- **Status:** EXECUTION — the strategic program
  ([P2 language-GA program v1](2026-08-graphi-p2-language-ga-program-v1.md))
  is largely built; this document sequences what *remains* into ordered
  milestones and names the critical path. Where this document and the
  machine-checked matrices disagree, the matrices win and this document changes.
- **Date:** 2026-08-15
- **Authority:** the **language axis only**. The 12 frozen operations, the two
  GA surfaces (CLI, MCP stdio) and the CGo-free build are untouched.
- **Decision owner:** Sami. Every row this plan marks *owner* is a sign-off or
  a stop-ship judgement that cannot be honestly fabricated — a number without a
  checked-in artifact is UNSUPPORTED, and UNKNOWN counts as *not passed*.
- **Companion to:** the program doc (G1–G9 gates, WP-J0…WP-J11) and
  [ADR 0008](../adr/0008-jvm-declared-type-resolution.md) (the binder's
  decision points, still *Proposed*).

## 1. Where we are — the status ledger

Read against the program doc's work packages (`WP-Jn`) and per-language gates
(`Gn`). State is one of: **DONE** (built + green), **BUILDABLE** (I can land it
here with a local proof), **BLOCKED** (a dependency must clear first),
**OWNER** (a sign-off or measurement only Sami can settle).

| WP | What | State | Note |
|---|---|---|---|
| WP-J0 | Semantic-resolver seam (registry-driven 3rd ingest phase) | **DONE** | `engine/semantic`, default-off `GRAPHI_JVM_TYPERESOLVE` |
| WP-J1 | Machine-encode the GA language axis | **DONE** | `ga-language` matrix + `CheckGALanguages`; set is `{go}` |
| WP-J2 | Extractor deepening + `qn.go` identity map | **DONE** | byte-exact golden cross-test |
| WP-J3 | Java binder (phases A–C), dark | **DONE** | named skip counters, zero external confirmed edges |
| WP-J4 | Kotlin binder on the shared table | **DONE** | D2 inferred/declared boundary proven |
| WP-J5 | Hermetic change-class parity | **DONE** | **11** required classes, both stores, byte parity; **no deferred rows left** (M2.2) |
| WP-J6 | Corpus deepening (pins + stratification) | **PARTIAL** | okio pinned (G5); guava→v3 + more pins outstanding |
| WP-J7 | Real-repo parity over JVM pins (**G4**) | **BLOCKED** | ADR 0008 D8 entry criterion: PARITY-001 ✅ fixed (M2.1), **PARITY-002 still open** |
| WP-J8 | Hero-JVM (~20 scenarios × 12 ops) | **DONE** | `corpus/fixtures/hero-jvm`, hero gate green |
| WP-J9 | Differential bytecode ground-truth (soundness) | **DONE** (Java) | live JDK, 4 dispatch forms; **Kotlin live path runs in CI only** (no local `kotlinc`) |
| WP-J10 | Perf + budget runs; create `GA-LANG-*` rows (born UNKNOWN) | **OWNER** | needs an attested candidate + reproducible CI runs |
| WP-J11 | The flip: registries report java/kotlin `typed-confirmed` | **OWNER** | stop-ship gated; one line in `engine/semantic/semantic.go` |

Gate roll-up for Java/Kotlin: **G1, G2, G3, G6, G8** DONE (capability wiring is
verified end-to-end — `TestCapabilities_JVMOptInFlip` pins what the flip
produces). **G5** partial. **G4, G7, G9** not yet green.

## 2. The critical path — and it is not JVM-specific

The long pole to Java/Kotlin GA runs through **Go-level** defects, not the
binder. **PARITY-001 is now FIXED (M2.1); PARITY-002 remains open**, so the path
is shorter but not clear:

```
PARITY-001  ✅ FIXED 2026-08-16 (M2.1) — deleted-path purge now commits before linkFiles
PARITY-002  ⛔ OPEN — re-link `imports` edge-set divergence, NON-DETERMINISTIC
      │  language-independent, ingest-level
      ▼
WP-J7  (real-repo parity over JVM pins)   ← ADR 0008 D8 makes the fixes the ENTRY CRITERION
      ▼
G4     (real-repository parity gate)  ── one of the nine GA gates
      ▼
GA-LANG-java/kotlin-G4 = PASS
```

Everything else is either DONE, BUILDABLE by me now, or an OWNER sign-off.
**If G4 is required for GA-at-declared-capability, PARITY-002 is now the
schedule.** The one open ADR question that could shorten this path is whether
Java/Kotlin may reach GA with G4 published as an honest **PARTIAL** (defects
filed, pinned, never hidden — exactly how Go's own real-repo matrix reads
today) rather than a full PASS. That is ADR 0008 D8's ruling to make (§4).

**What PARITY-002 will take, from the published characterisation.** It is *not*
a second instance of PARITY-001 — different edge kind (`imports` only), different
trigger (`sync` re-linking *any* file, including a comment-only edit; a no-op
`sync` converges), and bidirectional rather than one-way. `imports` edges are
file→file and fan out over `idx.packageFileNodes(imp.Path)`, so what varies is
*which file nodes were in the index when the loop ran*. The hard part is not the
fan-out — it is that grpc-go produced **three distinct incremental snapshots over
six executions of an identical tree and binary** while the full side read the same
number every time. Any fix must first make `sync` *reproducible*, because a
non-deterministic baseline cannot be shown to have converged. That is a genuine
root-cause investigation, not a reordering, and it should not be attempted by
pattern-matching it to PARITY-001's fix.

## 3. Milestones — ordered, with the Claude/owner split

### M1 — Evidence deepening (BUILDABLE, no dependencies) — *Claude — ✅ DONE 2026-08-15*
Raise recall and harden the oracle while everything downstream waits. Each item
shipped with a local proof; none touched the shipped default.
- **M1.1 ✅** Binder recall forms, each proven by the live-JDK gate:
  `super.method()` (invokespecial to a regular method) and `new Foo().bar()`
  compositions pinned hermetically + live; field-access and declared-return
  chains were already covered. Live gate now spans invokevirtual, invokespecial
  (`<init>` and regular), and invokestatic — SOUND, recall 5/7.
- **M1.2 ✅** Corpus pins (WP-J6 / G5): guava→v3 measured standard (full sha +
  census, 3204 .java) and a second Kotlin pin, kotlinx.serialization v1.6.3
  (615 JVM files, binder resolves 3517 typed sites, zero crashes at pin time).
- **M1.3 ✅** Kotlin ground-truth e2e test written; SKIPS locally (no `kotlinc`),
  proven for the first time by `jvm-groundtruth.yml`. The graphi side is
  validated locally (exactly 2 confirmed calls with the keys the bytecode
  carries); only the bytecode half waits for CI.
- **Gate:** touched packages + layerguard green; live-JDK gate green on the new
  forms; drift guards green. (Full-suite: two pre-existing load-induced flakes
  in `surfaces` MCP journey tests — unrelated to M1, pass in isolation; see the
  note under §6.)

### M2 — Clear the critical path — *partially done; PARITY-002 remains*
- **M2.1a ✅ PARITY-001 FIXED** (2026-08-16). The incremental path now runs the
  deleted-path purge **and commits it** before `linkFiles` — the order
  `IngestAll` always used. Both published pins flipped in the same commit: the
  hermetic `engine/conformance` row now asserts real parity with a strengthened
  witness, and `internal/parity` (which drives the **built binary**) went from
  `want FAIL` to `want PASS`. The SQLite trap SW-157 recorded is *avoided, not
  disproved* — purging into the link batch is what breaks it; a separate,
  committed batch means `linkFiles` never sees a purged node.
- **M2.1b ⛔ PARITY-002 still open** — see §2 for what it will take. Deliberately
  not attempted alongside M2.1: it is a different defect with a
  non-deterministic baseline, and bundling them would make neither provable.
- **M2.2 ✅ DONE** — `jvm_delete_file` promoted from deferred to required; the
  JVM matrix is now **11 required rows and no deferred rows**. Recorded honestly:
  it converges *with and without* the PARITY-001 fix (verified by reverting
  `engine/ingest`), so the ADR 0008 D8 deferral was a precaution rather than a
  masked JVM defect.
- **Gate (still open):** `internal/parity` real-repo matrix converges
  **deterministically** on the Go corpus; the two published dispatches agree on
  every verdict. Blocked on M2.1b.

### M3 — Real-repo parity + perf for JVM (unblocked by M2) — *Claude builds, CI measures*
- **M3.1** WP-J7: run `internal/parity` over the JVM pins; publish
  `parity-matrix-real-repo.md`; file any JVM defects honestly.
- **M3.2** WP-J10: the four perf suites over the JVM corpus; raw runs under
  `docs/eval/runs/…`; Kotlin's 337 KB grammar blob inside the budget gate.
- **M3.3** Create the `GA-LANG-java-G<n>` / `GA-LANG-kotlin-G<n>` evidence rows
  — **born UNKNOWN**, each naming its artifact URI + sha.
- **Gate:** every GA-LANG-* row exists; each carries a real measurement or an
  explicit UNKNOWN. No row reads PASS without a checked-in artifact.

### M4 — The cutover (OWNER, stop-ship gated) — *Sami*
This is WP-J11. See the checklist in §4. It is a one-line default flip **behind
green evidence** — never a side effect of anything else.

### M5 — Rollout waves for the remaining ~19 languages — *after M4*
The JVM path becomes the reusable template (§5).

## 4. WP-J11 cutover checklist (the stop-ship gates)

The flip may proceed only when **all** of these hold — any one UNKNOWN/STALE or
any open `JVMSOUND-0xx` is a stop-ship:

- [ ] ADR 0007 and ADR 0008 → **Accepted** (D1–D8 ruled).
- [ ] Every `GA-LANG-java-*` and `GA-LANG-kotlin-*` evidence row reads **PASS**
      with evidence URI + sha (G9 honesty rule).
- [ ] Ground-truth soundness: **zero** counterexamples, Java *and* Kotlin, on
      the attested candidate (WP-J9 nightly green).
- [ ] Real-repo parity (G4): PASS, **or** a ruled PARTIAL with defects filed
      and pinned (D8).
- [ ] The flip itself: one line in `engine/semantic/semantic.go` (register
      java/kotlin by default); `TestCapabilities_JVMOptInFlip` already pins the
      surface output, so the diff is provable, not a leap.
- [ ] `cmd/coverage -check` green with `ga-language` rows for `java` and
      `kotlin`; prose in `stability-tiers.md` + `language-support.md` updated to
      match the machine matrices (matrices win).

## 5. Rollout-wave template (M5, the other languages)

Each language L repeats the JVM path at its **honestly-achievable** capability —
`typed-confirmed` where a declared-type binder is possible, else
`cross-file-heuristic`, else lower. The template, per L:

1. Declare L's target level (G1); make the live derivation equal it (G8).
2. If `typed-confirmed`: a declared-type binder behind the WP-J0 seam + a
   differential ground-truth oracle (G2). Else stop at the heuristic linker.
3. Parity rows (G3), corpus pins measured at pin time (G5), hero scenarios (G6),
   perf (G7), `GA-LANG-<L>-*` rows born UNKNOWN (G9).
4. Flip only behind green evidence.

Wave ordering is a §6-of-the-program-doc question; a sensible first wave is the
languages that already have a cross-file resolver (Python, TypeScript) reaching
a *proven* `cross-file-heuristic` GA, since they need no binder — a shorter path
that exercises the template before the next `typed-confirmed` language.

## 6. What proceeds without further input

Milestone **M1 is DONE** (2026-08-15, merged as #123). **M2 is half done**
(2026-08-16, branch `claude/parity-001-002-m2`): PARITY-001 is fixed and
`jvm_delete_file` is promoted, so the JVM matrix has no deferred rows left.
**PARITY-002 is the remaining blocker** on the critical path — a separate,
harder defect with a non-deterministic baseline (§2). M3 depends on it; M4 is
the owner's cutover. This plan is the map from *here* to that flip.

Both PARITY-001 fixes were product-byte changes, so they move the candidate —
the perf/evidence rows measured against the previous candidate stay STALE until
re-measured, exactly as the claims discipline requires.

### Note — a full-suite flake surfaced while gating M1 (pre-existing, not M1)

Running `go test ./...` under full parallelism, two `surfaces` MCP
session-journey subprocess tests fail and **pass in isolation**:
- `TestSessionProfile_MCPRepositoryJourney` — the documented `-32002
  "repository is not bound … retry in a moment"` 2s bind race (known flake).
- `TestSessionProfile_MCPRootsListJourney` — a distinct, sharper variant: the
  bind fails wrapping a **SQL migration race**, `table
  trust_package_evidence_v4 already exists`. The migration at
  `engine/ingest/schema.go:249` does `CREATE TABLE trust_package_evidence_v4`
  **without `IF NOT EXISTS`**, so two subprocesses opening the shared test
  state home can both attempt it. In production the ingest lock serializes
  access, so this is a test-isolation robustness gap, not a shipped-path
  correctness bug — but it is a genuine CI-red risk. It predates M1 (it comes
  with the per-language trust-evidence migration) and is a candidate for a
  small, separate hardening change (`IF NOT EXISTS` + a transactional guard, or
  test-side state-home isolation).
