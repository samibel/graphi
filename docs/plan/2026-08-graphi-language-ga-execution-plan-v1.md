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
| WP-J7 | Real-repo parity over JVM pins (**G4**) | **READY** | All three parity defects CLOSED BY MEASUREMENT (W0.f-3 + W0.f-4): the Go matrix is 19/19 PASS over two dispatches identical at count granularity. ADR 0008 D8's entry criterion is met; what remains is running J7 over the JVM pins |
| WP-J8 | Hero-JVM (~20 scenarios × 12 ops) | **DONE** | `corpus/fixtures/hero-jvm`, hero gate green |
| WP-J9 | Differential bytecode ground-truth (soundness) | **DONE** (Java) | live JDK, 4 dispatch forms; **Kotlin live path runs in CI only** (no local `kotlinc`) |
| WP-J10 | Perf + budget runs; create `GA-LANG-*` rows (born UNKNOWN) | **OWNER** | needs an attested candidate + reproducible CI runs |
| WP-J11 | The flip: registries report java/kotlin `typed-confirmed` | **OWNER** | stop-ship gated; one line in `engine/semantic/semantic.go` |

Gate roll-up for Java/Kotlin: **every gate reads UNKNOWN.** This table
previously claimed "G1, G2, G3, G6, G8 DONE"; that claim was **withdrawn on
2026-08-16** after an independent architect and product-owner review, for two
independent reasons — either alone is sufficient:

1. **No `GA-LANG-*` evidence row exists.** `docs/rc/evidence-index.yaml` holds
   17 gate rows: 13 UNKNOWN, 4 STALE, **zero PASS**, and `grep GA-LANG` returns
   nothing. Under this repository's own claims discipline, **UNKNOWN counts as
   not passed**, so a gate cannot read DONE before its row exists and is green.
   The previous roll-up was self-assessed from green test suites — precisely the
   substitution `internal/coverage.CheckGALanguages` exists to prevent,
   sidestepped by never creating the rows.
2. **G2 is affirmatively FALSE, not merely unevidenced.** The binder emits
   *wrong* confirmed edges in two reproduced cases (see the D6 note in §3/M3),
   which is the stop-ship class, not a coverage gap.

The engineering behind these gates is real and largely green in CI; what was
wrong is the ledger's verdict vocabulary. Green tests are an input to a gate,
never the gate itself. **G5** partial. **G4, G7, G9** not started.

### 1.1 STOP-SHIP — the binder emits FALSE `confirmed` edges (found 2026-08-16)

Two defects in the D6 binding rule, **each reproduced against the live binder**
(found by an independent architect review, then re-reproduced independently
before being recorded here). Both produce a **wrong** edge, not a missing one —
the class the program doc defines as a stop-ship because it falsifies the tier's
meaning rather than a coverage claim. Neither fired a skip counter: the binder
believed its information was complete.

**JVMSOUND-001 — variadic arity.** `parse_java.go` tables `spread_parameter`
exactly like `formal_parameter`, so `void f(String... s)` records
`len(Params) == 1`, and `hierarchy.go` matches candidates on
`len(m.Params) == arity`. A variadic method is therefore invisible at every
arity except its declared one:

```java
void f(int a, int b) {}
void f(String... s) {}
void g() { f("a", "b"); }   // javac binds f(String...)
```
```
SITE f arity=2 BOUND-TO sig="int,int"   ← wrong; zero skips
```
Kotlin is worse: `parse_kotlin.go` records neither `vararg` nor default
parameter values, so `fun f(a: Int, b: Int = 0)` beside `fun f(s: String)`
mis-binds `f(1)` the same way.

**JVMSOUND-002 — signature identity compares written text.** `sigOf`
concatenates each parameter's type name **as written**, so two members whose
parameters are spelled alike but *resolve* differently are collapsed into an
override chain and the most-derived one wins:

```
q/Foo.java  package q; class Foo {}
r/Foo.java  package r; class Foo {}
s/A.java    import q.Foo;  class A       { void m(Foo x) {} }
s/B.java    import r.Foo;  class B extends A { void m(Foo x) {} }   // an OVERLOAD to javac
```
```
b.m(qFoo) → DECLARING s.B    ← wrong; javac binds A.m; zero skips
```
The index already has `ResolveTypeName`; it simply is not used for signature
identity.

**Why every existing gate missed them.** The G2b invariant tests are written
against the *intended* semantics ("partial information may only drop edges") and
cannot see a case where the binder wrongly believes it is complete. The WP-J9
bytecode oracle is zero-tolerance and would catch both instantly — but
`jvm-groundtruth.yml` compiles only checked-in fixtures, and those fixtures
contain no varargs. **Fixture-scale zero-tolerance is not zero-tolerance.**

**Consequences, unconditional and independent of any open decision:** the
WP-J11 default flip cannot proceed; **G2 reads FALSE, not UNKNOWN**; the D6 rule
as written in ADR 0008 (and as described in `hierarchy.go`'s own comment, which
claims identical-erased-signature matching the code does not do) must be
re-worded to what is actually true; and the oracle must be pointed at the pinned
corpus (guava for Java, okio + kotlinx.serialization for Kotlin), which is
CI-only and violates no product constraint.

**STATUS — FIXED 2026-08-16 (Wave 0, W0.d).** Both defects are closed and
D6 is corrected:
- JVMSOUND-001: `Param` now carries `Variadic`/`HasDefault`, set at tabling for
  Java `spread_parameter` and Kotlin `vararg`/default values; the lookup
  forfeits (`AmbiguousMember`) when any same-name callable is elastic.
- JVMSOUND-002: `callableSig` keys each parameter on its RESOLVED type FQN
  (intra-repo) or marked text (primitive/external), so `q.Foo`/`r.Foo` no longer
  collapse while genuine `m(int)` overrides still bind.
- Proof: member-precise unit tests (`TestAnalyzeJavaBodies_VariadicForfeit`,
  `_ResolvedSignature`, Kotlin `_ElasticForfeit`), each demonstrated
  red-without-fix, plus the tabling-level invariant
  `TestBuildTable_RecordsElasticParams` (R4 — the honesty guard now holds at the
  table, where both defects lived).

**REFINEMENT to R3, discovered while fixing this.** Pointing the WP-J9 oracle at
the corpus is necessary but NOT sufficient to catch this class: the oracle keys
calls at method-NAME granularity, so two overloads of the same name in the same
file share a comparison key and a wrong-overload binding is invisible to it. The
oracle needs **signature-aware keys** as well as corpus scale. Until then the
member-precise unit tests are the proof of record for overload mis-binding; a
live varargs case in the oracle would pass with OR without the fix and would be
theater. Booked as part of W0.e.

## 2. The critical path — and it is not JVM-specific

The long pole to Java/Kotlin GA runs through **Go-level** defects, not the
binder. **PARITY-001 and PARITY-002 are CLOSED BY MEASUREMENT** (W0.f-3,
2026-08-16: two dispatches over the pinned clones, identical at COUNT
granularity, fan-out signature gone, grpc-go count-flapping gone — record in
`docs/rc/parity-matrix-real-repo.md`) — **and that same measurement isolated
PARITY-003**, a distinctly-mechanismed defect the two "PARITY-002" gin/grpc-go
rows had been conflating:

```
PARITY-001  ✅ CLOSED BY MEASUREMENT 2026-08-16 (fix M2.1; delete_file FAIL→PASS on real cobra)
PARITY-002  ✅ CLOSED BY MEASUREMENT 2026-08-16 (fix W0.f/ADR 0009; determinism proven via -counts-diff)
PARITY-003  ✅ FIXED AND MEASURED 2026-08-16 (W0.f-4, ADR 0010) — 19/19 PASS, two dispatches count-identical.
      │     The pass-scoped Balanced import aggregation is
      │     REMOVED (it had no legitimate prey in any language, dropped true edges, and merged
      │     other files' evidence). The gate gap that hid it is closed structurally: the
      │     conformance change-class table now runs a PROFILE axis (default + balanced) crossed
      │     with both stores, so a defect in the shipped profile can no longer pass a green table.
      ▼
WP-J7  (real-repo parity over JVM pins)   ← ADR 0008 D8 makes the fixes the ENTRY CRITERION
      ▼
G4     (real-repository parity gate)  ── one of the nine GA gates
      ▼
GA-LANG-java/kotlin-G4 = PASS
```

Everything else is either DONE, BUILDABLE by me now, or an OWNER sign-off.
**If G4 is required for GA-at-declared-capability, the schedule is now WP-J7
itself — no parity defect stands in front of it** (updated four times on
2026-08-16, each supersession kept because that sequence is what a measurement
is FOR: "PARITY-002 is the schedule" → "the measurement run is the schedule" →
the measurement ran and found PARITY-003 → PARITY-003 is fixed and measured,
19/19 PASS, two dispatches count-identical).
Note WP-J7's JVM rows may be less exposed than Go's (Java/Kotlin emit a single
interned file→package edge, not per-file fan-out — the aggregation path is
Go-shaped), but that is an argument, not a measurement.
The one open ADR question that could shorten this path is whether
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

**NEGATIVE RESULT — PARITY-002 does not reproduce hermetically at fixture scale
(measured 2026-08-16, M2.1b).** A throwaway probe built the published correlate
directly: a package directory whose import target is under-determined — two
mutually exclusive `//go:build` variants, an in-package `_test.go`, an external
`package p_test` file — imported by two packages, driven through **seven**
triggers (edit a file in the imported package, edit a build-tag variant, edit
the in-package test file, edit the external test package, edit one importer, add
a file to the imported package, delete a build-tag variant), on **both** stores,
**repeated five times**. All 14 combinations converged on snapshot bytes, every
run — no divergence and no run-to-run variation.

What that does and does not establish. It does **not** refute PARITY-002: the
defect is measured on real pinned clones and that measurement stands. What it
does establish is that **the under-determined-import-target shape is not, by
itself, sufficient** — which the published record already declined to claim
("consistency is not proof and this record does not claim it"), and this is
evidence on that question rather than another restatement of it. So the next
investigation should look for what gin and grpc-go have that a fixture does not:
**scale** (92 and 831 files against 8), and therefore plausibly a **concurrency
or ordering effect in the ingest worker pool** — which is also the only class of
cause that would explain grpc-go's run-to-run variation while the full side stays
byte-stable. The probe was deleted rather than committed: a green test that does
not reproduce the defect it is named for is worse than no test.

**SUPERSEDED 2026-08-16 — PARITY-002 IS NOW REPRODUCED HERMETICALLY, and the
"scale/concurrency" guess above was WRONG.** The negative result stands exactly
as recorded (that probe really did converge, and why is now explained), but its
conclusion pointed the wrong way. The missing variable was not scale — it was
**reachability**. All seven triggers of the first probe landed INSIDE
`dependentsOf`'s closure, so the cascade was accidentally complete and nothing
*could* diverge.

Targeting the closure instead reproduces the defect **deterministically, on both
stores, in a 4-file fixture** (5 consecutive runs identical): add a directory
`y/json` that **nothing imports** but whose package clause collides with the
imported `x/json`, and the importers' fan-out changes while `dependentsOf(y/json)`
stays empty, so neither importer is re-linked:

```
incremental 8 nodes /  8 edges
full        8 nodes / 10 edges
  + impA/main.go --imports--> y/json/b.go   reason "file imports package example.com/m/x/json"
  + impB/main.go --imports--> y/json/b.go   reason "file imports package example.com/m/x/json"
```

Node counts equal, every diverging edge `imports` — the published gin/grpc-go
signature exactly. **Read the reason string:** the importer imports `x/json` and
the edge targets `y/json/b.go`, so *neither side is right* — the full pass emits
a semantically WRONG edge and the incremental pass merely fails to emit it.
`rebuild` is the reference by definition, so `sync` is what "diverges".

Landed as the conformance class `change_colliding_package_dir`, pinned as
`knownDefect: PARITY-002` (published red data, the shape `delete_file` used
before it was fixed), plus a real-repo planner so it also runs on pinned clones.

**Two things the reproduction taught, beyond the repro itself:**
1. **PARITY-002 is FROZEN, not self-healing** — strictly worse than PARITY-001
   was. Re-syncing cannot fix it, because the affected importers are never in
   the re-link set at all; only `graphi rebuild` recovers. The harness assumed
   PARITY-001's "the second apply does what the first should have" for every
   pinned defect; it now requires each row to **declare** its re-application
   shape (`reapplyHeals` / `reapplyFrozen`) and asserts it in both directions.
2. **Step 0 is also done, from data already on disk.** Pairwise-diffing the four
   published `docs/rc/parity-matrix*.json` shows grpc-go inc 69939/69940 against
   full 69772 every run, nodes identical at 14898, and the differing pair shares
   the same `from` while differing in `to` — pointing at the INDEX rather than
   the re-link set. `packageFileNodes` is itself dedup-and-sorted, so the
   remaining run-to-run variance must come from the index's *content*, not its
   iteration. That is the one part still open.

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

### M2 — Clear the critical path — *done 2026-08-16 (PARITY-002 fixed under W0.f, ADR 0009)*
- **M2.1a ✅ PARITY-001 FIXED** (2026-08-16). The incremental path now runs the
  deleted-path purge **and commits it** before `linkFiles` — the order
  `IngestAll` always used. Both published pins flipped in the same commit: the
  hermetic `engine/conformance` row now asserts real parity with a strengthened
  witness, and `internal/parity` (which drives the **built binary**) went from
  `want FAIL` to `want PASS`. The SQLite trap SW-157 recorded is *avoided, not
  disproved* — purging into the link batch is what breaks it; a separate,
  committed batch means `linkFiles` never sees a purged node.
- **M2.1b ✅ PARITY-002 FIXED** (2026-08-16, W0.f / ADR 0009 — after this plan
  was written; the original entry read "still open, see §2 for what it will
  take"). Root cause was a CLOSURE defect: clause-keyed import fan-out vs the
  directory-local incremental cascade. Fixed by module-aware import→directory
  resolution (`engine/link/modulemap.go`), with the colliding-dir class
  promoted to a real parity assertion and `add_nested_gomod` pinning the
  widened cache invalidation. The deliberate non-bundling with M2.1 held.
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

Milestone **M1 is DONE** (2026-08-15, merged as #123). **M2 is DONE**
(2026-08-16): PARITY-001 fixed and `jvm_delete_file` promoted on branch
`claude/parity-001-002-m2`, and PARITY-002 fixed under W0.f (ADR 0009, branch
`claude/parity-002-module-dir-resolution`) — the critical path holds **no open
defect**. What remains before M3 is measurement (`internal/parity` two
dispatches with identical COUNTS on the moved candidate), not fixing; M4 is
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
