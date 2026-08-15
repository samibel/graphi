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
| WP-J5 | Hermetic change-class parity | **DONE** | **10** required classes, both stores, byte parity |
| WP-J6 | Corpus deepening (pins + stratification) | **PARTIAL** | okio pinned (G5); guava→v3 + more pins outstanding |
| WP-J7 | Real-repo parity over JVM pins (**G4**) | **BLOCKED** | ADR 0008 D8: PARITY-001/002 fixes are the entry criterion |
| WP-J8 | Hero-JVM (~20 scenarios × 12 ops) | **DONE** | `corpus/fixtures/hero-jvm`, hero gate green |
| WP-J9 | Differential bytecode ground-truth (soundness) | **DONE** (Java) | live JDK, 4 dispatch forms; **Kotlin live path runs in CI only** (no local `kotlinc`) |
| WP-J10 | Perf + budget runs; create `GA-LANG-*` rows (born UNKNOWN) | **OWNER** | needs an attested candidate + reproducible CI runs |
| WP-J11 | The flip: registries report java/kotlin `typed-confirmed` | **OWNER** | stop-ship gated; one line in `engine/semantic/semantic.go` |

Gate roll-up for Java/Kotlin: **G1, G2, G3, G6, G8** DONE (capability wiring is
verified end-to-end — `TestCapabilities_JVMOptInFlip` pins what the flip
produces). **G5** partial. **G4, G7, G9** not yet green.

## 2. The critical path — and it is not JVM-specific

The long pole to Java/Kotlin GA runs through a **Go-level** defect, not the
binder:

```
PARITY-001 / PARITY-002   (open, unfixed, scheduled v0.7.2 — docs/rc/parity-matrix-real-repo.md)
      │  re-link non-determinism + phase-ordering; language-independent, ingest-level
      ▼
WP-J7  (real-repo parity over JVM pins)   ← ADR 0008 D8 makes the fixes the ENTRY CRITERION
      ▼
G4     (real-repository parity gate)  ── one of the nine GA gates
      ▼
GA-LANG-java/kotlin-G4 = PASS
```

Everything else is either DONE, BUILDABLE by me now, or an OWNER sign-off.
**If G4 is required for GA-at-declared-capability, PARITY-001/002 is the
schedule.** The one open ADR question that could shorten this path is whether
Java/Kotlin may reach GA with G4 published as an honest **PARTIAL** (defects
filed, pinned, never hidden — exactly how Go's own real-repo matrix reads
today) rather than a full PASS. That is ADR 0008 D8's ruling to make (§4).

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

### M2 — Clear the critical path (BLOCKED → the real work) — *owner-scheduled*
- **M2.1** Fix **PARITY-001** (phase-ordering / delete path) and **PARITY-002**
  (re-link edge-set non-determinism). Product-byte changes → a new candidate.
- **M2.2** Land the deferred conformance class `jvm_delete_file` (its entry
  criterion is exactly M2.1) — flips the JVM matrix from 10 required + 1
  deferred to 11 required.
- **Gate:** `internal/parity` real-repo matrix converges deterministically on
  the Go corpus; the two published dispatches agree on every verdict.

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

Milestone **M1 is DONE** (2026-08-15) — landed on `claude/jvm-java-constructor-calls`,
each slice with its proof, no PR. **M2 is next and owner-scheduled** (it is a
product-byte change to a shipped defect); M3 depends on M2; M4 is the owner's
cutover. This plan is the map from *here* to that flip.

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
