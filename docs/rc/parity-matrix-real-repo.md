# Full/incremental parity matrix over pinned real repositories

# TS family real-repository matrix — WP-TS / gate G4 — BLOCKED (2026-08-20, W5.g)

> **THIS SECTION IS NOT A MEASUREMENT.** SW-193 attempted the corpus-scale
> TypeScript-family parity measurement that SW-182 AC-2 / AC-8 named as G4's
> evidence, and found the harness BLOCKED rather than unbuilt. The section
> below — "JVM real-repository matrix — WP-J7 / gate G4 (2026-08-19, W1.c)"
> — remains the current JVM measurement. Two matrices live in this file: the
> Go one over `docs/rc/parity-classes.yaml` and the JVM one over
> `docs/rc/parity-classes-jvm.yaml`. The TS-family section that would carry
> the third is **deliberately empty** below, with the blocker stated rather
> than hidden. Per D6 nothing below this section is rewritten, re-pointed or
> deleted.

**Status: NOT BUILT — measurement cannot run. 0 dispatches. 0 PASS / FAIL /
SKIPPED.** The TypeScript family G4 row was bound to the corpus-scale
measurement by SW-182 AC-2 / AC-8 (ky + express, two dispatches, family-share
discipline on tsx). SW-193 is the story scheduled to land that measurement.
It could not, and the reason is the harness itself.

| | |
|---|---|
| Story | SW-193 (W5.g, 2026-08-20) |
| Gate | language-GA program G4 / WP-TS — real-repository full-vs-incremental parity for TypeScript-family |
| Family / matrix source | `internal/parity` + a TS-family driver + `docs/rc/parity-classes-ts.yaml` |
| Pins (intended) | ky `38ac18bc1ac3268130de766891ce9b718eb8145a` (typescript, tier 3, 34 source of 48 tracked) + express `8368dc178af16b91b576c4c1d135f701a0007e5d` (javascript, tier 3, 153 source of 231 tracked) — both at v3 measured standard as of 2026-08-20 |
| Family-share discipline (SW-182 AC-2) | ky is typescript-only; express is javascript-only; tsx has NO representative pin. The G4 row for tsx is discharged by family-share iff the typescript pin genuinely covers it (the resolver at engine/link/resolve_typescript.go registers under all three family ids and the file-extension match is what selects the candidate path, not the language id, so family-share holds by construction — but the discipline says state this, do not assume it) |
| Report artifacts | **none** — see "What is missing" |

## What is missing — the blocker, stated before any number, because it bounds what any number here means

**The TS-family parity driver does not exist in `internal/parity/`.** The
harness as it stands today:

| family | runner | source model | class table | cmd/parity wiring |
|---|---|---|---|---|
| Go | `Run` (`internal/parity/run.go:106`) | `RepoModel` (`gosource.go`) | `ClassesPath = "docs/rc/parity-classes.yaml"` (`classes.go:25`) | `-family go` is the default (`main.go:44`) |
| JVM | `RunJVM` (`internal/parity/jvmrun.go:183`) | `JVMModel` (`jvmsource.go`) | `ClassesPathJVM = "docs/rc/parity-classes-jvm.yaml"` (`jvmclasses.go:15`) | `-family jvm` (`main.go:46`) |
| **TS** | **does not exist** | **does not exist** | **does not exist** | **no `-family typescript` option, no `-ts-pin` flag** |

`internal/parity/run.go:680` (the only language filter today) is
hard-coded `e.Language != "go"`, and `cmd/parity/main.go:77` accepts only
`-family go` or `-family jvm`. **There is no path on this tree that runs
`internal/parity` against `corpus/manifest.json`'s ky or express pin.** A
direct build of a driver would have the same shape as SW-176's WP-J7 JVM
half — `tsrun.go` (the run method), `tssource.go` (the TS source model
covering the family's static extension set [.ts/.tsx/.js/.jsx/.mjs/.cjs] at
`engine/link/resolve_typescript.go:25`), `tsclasses.go` (the real-repo
class table), `docs/rc/parity-classes-ts-real-repo.yaml` (the YAML), plus
the `-family typescript` wiring in `cmd/parity/main.go` and the
`-verdict-diff` / `-counts-diff` plumbing — which is the work the JVM half
took four commits (SW-176 efdc77f / 803f7c7 / a761cfa / df2f43b) to land.
SW-193 cannot honestly produce the matrices within its scope, and producing
a *partial* matrix (e.g., a Go-shaped parity run against a TS pin that
every Go change class SKIPPed because of language filter, then claiming it
measured the TS family) would be the exact failure mode the matrix exists
to prevent.

**Filed as PARITY-TS-FAMILY-DRIVER-001.** The defect is structural — the
harness missing a family — and not a row-level finding, so it is named in
the row `current` fields of `docs/rc/evidence-index.yaml` for
`GA-LANG-typescript-G4`, `GA-LANG-tsx-G4`, and `GA-LANG-javascript-G4`
(2026-08-20 amendment), and the rows stay UNKNOWN.

**The family-share-one-resolver judgement (SW-182 AC-2, SW-193 AC-3) is
RECORDED in the tsx G4 row's `current`**, per the per-language discharge
rule: the TS family shares one resolver impl at
`engine/link/resolve_typescript.go` and registers under all three ids, so
once the TS family driver lands and the typescript pin (ky) PASSes at
cross-file-heuristic, the tsx row CAN be discharged by family-share with
the family-share fact stated in current. The discipline is recorded, not
assumed; the discharge happens only when the driver lands.

## What this section does and does not say

**Says.** The TS family G4 row's blocker is a missing driver, not a missing
measurement. The hermetic twin at
`engine/conformance/typescriptparity_test.go` (8 rows, all PROVEN on both
stores, both profile axes, byte-identical full-vs-incremental across 5
runs per SW-182 AC-5) is the parity statement the matrix currently
protects — it asserts the heuristic resolver's *contract* (never
confirmed, drop-and-count, exact-path resolution) but cannot, by
construction, assert the resolver on real source.

**Does not say.** Anything about the resolver on real TypeScript-family
repositories. A measurement that has not happened cannot ground a row.

## What would close this

A new story — likely W6's TS-family parity driver, in the same shape as
SW-176's WP-J7 — that lands `tsrun.go` + `tssource.go` + `tsclasses.go` +
a real-repo `docs/rc/parity-classes-ts.yaml` (the same file the hermetic
table binds to, plus a real-repo annex if the family needs different
classes) and the `-family typescript` wiring. SW-193 cannot do this work
without losing its single-commit discipline (SW-176 took four commits and
a candidate move).

When that story lands, **SW-193's verdict + count diff + per-import
fan-out rows can be re-driven against the new driver**, and the G4 rows
will flip — ky covering typescript, express covering javascript, and the
tsx row by family-share if the reviewer's judgement (recorded in current)
upholds it.

# JVM real-repository matrix — WP-J7 / gate G4 (2026-08-23, W5.d+1 SW204)

> **THIS SECTION PUBLISHES NO MATRIX. It publishes a GATE and a REFUSAL.** It
> does not supersede the W5.d SW-190 section immediately below, nor the
> 2026-08-19 W1.c section below that; per D6 both stay byte-unchanged. What it
> adds is the runner-policy half of PARITY-COV-001 — the JVM publishability
> gate now READS `compile_coverage` from `corpus/manifest.json` — plus the
> disposition of the eight SKIPPED cells, which is decided rather than deferred
> for the third time.

**Status: GATE WIRED, REFUSAL DETERMINISTIC, STILL NOT PUBLISHABLE. 44 of 52
crossed rows PASS, 8 SKIPPED, 0 FAIL, 0 harness error, on both dispatches.**
Exactly ONE condition now denies publishability, and it is the one this story
cannot close.

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity -family jvm`, two full dispatches (run-A, run-B), separate persistent workdirs, run serially, **network clones — `-allow-local` deliberately NOT used** |
| Gate | language-GA programme G4 / work package WP-J7 — real-repository full-vs-incremental parity for Java and Kotlin |
| Candidate | **`9f687849cec2b26311401191e90b60e40b5f6cee`** — **UNCHANGED.** SW-204 forced no candidate move; see "The candidate did not move, and that was measured" below |
| Matrix source | `docs/rc/parity-classes-jvm.yaml` at `matrix_version: 4` (13 declared change classes) |
| Axis crossing | `binder{off,on} × profile{resolved default, fast}` = **4 cells**, so 13 × 4 = **52 declared rows** |
| Corpus pins | guava `2214c63670fc161da170ac6e1a2d6d07e1531a55` (java, tier 3), okio `8b870e8eaacecb1c1ceffbbb47246112604a1f92` (kotlin, tier 3), kotlinx.serialization `3efe324be422ead21ca44f2f6318e1791c166556` (kotlin, tier 3) — cloned from their URLs; `head_sha` equals `pinned_sha` on all three in both dispatches |
| Provenance | **both** dispatches at run SHA `c51172b9017436ea584f7de05804e42b3f6a53e8`, runner class `darwin-arm64/local-sandbox`, go1.26.6 darwin/arm64, **worktree CLEAN** at run time (`worktree_clean: true`), 5m18s and 5m22s wall clock |
| Product binary | HEAD and candidate both `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` — `product_diff_empty: true` |
| Report artifacts | [`parity-matrix-jvm-sw204-run-A.json`](parity-matrix-jvm-sw204-run-A.json) `sha256 2bac9e68…` · [`parity-matrix-jvm-sw204-run-B.json`](parity-matrix-jvm-sw204-run-B.json) `sha256 fb8f868b…` — **91 944 bytes each, differing on exactly 94 lines: 92 `duration_ms` and 2 `generated_at`.** Every verdict, every per-row node/edge count, every snapshot digest and every `compile_coverage` block is byte-identical |
| Schema | `schema_version: 3` (was 2 — `RepoRef` gained `compile_coverage`); `harness_version` stays `parity-matrix/2`, because no row's verdict, count or digest moved |

## What SW-204 changed, in one paragraph

`internal/parity/jvmrun.go` built each `RepoRef` from
`Name/URL/Ref/PinnedSHA/HeadSHA/Tier/SourceFiles` and **silently dropped**
`corpus.Entry.CompileCoverage`. The figure `cmd/jvmcoverage` measures therefore
had no reader downstream of its own producer, and the gate could not tell an
UNMEASURED pin from a perfectly compiled one. SW-204 carries the figure into
`parityreport.RepoRef.CompileCoverage` and decides it in
`parityreport.(*Report).Finalize`, JVM-scoped (`Report.Family == "jvm"`), with
exactly three arms and no fourth:

| pin state | decision | why |
|---|---|---|
| no `compile_coverage` at all | **REFUSE** | fail-closed; an unmeasured pin is refused, never assumed complete |
| `coverage < 1.0`, no `excluded_reason` | **REFUSE** | an unexplained partial compile is an unknown, and an unknown is not evidence |
| `coverage < 1.0` WITH an `excluded_reason` | **ACCEPT** | a documented negative costs the CLAIM, not the run's publishability |

The three pins, as the gate itself reported them on stderr in both dispatches:

```
parity: compile_coverage policy over 3 materialized JVM pin(s) (source: corpus/manifest.json; a pin with no figure is REFUSED, a figure below 1.0000 is refused unless the manifest states an excluded_reason):
  guava                    accepted — 623/623 = 1.0000
  kotlinx.serialization    accepted — 52/52 = 1.0000
  okio                     accepted — 0/89 = 0.0000, DOCUMENTED NEGATIVE: Not compiled in the oracle's required layout — see `tried`. MEASURED, not assumed: the staged compile fails …
```

**There is no reason allowlist and no waiver flag**, in `Finalize` or in
`cmd/parity`. `internal/parityreport/report.go` states the rule this obeys, in
its own words about the coverage-limit reason: *"it does not make the run
publishable, or 'record the limit' would become the cheap way past the gate."*
A flag that let a named reason be waived would rebuild that escape one flag
over. What an `excluded_reason` discharges is only the question the coverage
rule asks — *is this pin's compile figure known?* — and it discharges it with a
measurement, not with a promise. It buys nothing else: a run with a SKIPPED row
still refuses, and there is a test that says so.

**A precision about what the two dispatches prove, stated because the obvious
reading is wrong.** The `compile_coverage` reason is absent from
`not_publishable_because` — but it was absent from SW-190's published reports
too, because before SW-204 **no such condition existed anywhere in the
product**. Its absence here is therefore not a disappearance; it is the gate
having LOOKED at all three pins and ACCEPTED them. The three things that
distinguish the two states, and that a reader can check: the per-pin stderr
lines above (the gate reporting what it decided for each pin), the
`compile_coverage` block now present on every pin in `repos[]` of both report
JSONs, and the hermetic tests in `internal/parity/parity_test.go` that a pin
with NO figure, and a pin at `0.5` with no `excluded_reason`, DO refuse.

## The refusal, verbatim, from both dispatches

```
$ ./parity -refusal-diff sw204-run-A.json,sw204-run-B.json
run a: c51172b9017436ea584f7de05804e42b3f6a53e8  publishable=false  refusals=1
run b: c51172b9017436ea584f7de05804e42b3f6a53e8  publishable=false  refusals=1

shared refusal set (1):
  incomplete run: 44 of 52 declared change classes decided, 0 deferred; 0 of 0 declared crash conditions decided, 0 deferred; skipped=true, harness-error=false (FR-7 requires 15 declared classes)

  compile_coverage refusals in the shared set: 0

parity: the two dispatches refuse for bit-identically the same reasons — the refusal is DETERMINISTIC. This is NOT a publication: both runs are refused, and exit 0 here says only that they are refused identically.
$ echo $?
0
```

`-refusal-diff` is a third diff mode, added because `-verdict-diff` and
`-counts-diff` both stop at *"at least one run is NOT publishable — publication
refused"* and say nothing about a pair of runs that BOTH refuse. **Its exit 0
means the refusal is deterministic. It never means the run is publishable**,
and it does not read `Publishable` as a pass condition at all. The two existing
gates are untouched and still exit **2** here, which is correct:

```
$ ./parity -verdict-diff sw204-run-A.json,sw204-run-B.json   # exit 2
parity: verdict sets agree, but at least one run is NOT publishable — publication refused.
$ ./parity -counts-diff  sw204-run-A.json,sw204-run-B.json   # exit 2
parity: counts agree, but at least one run is NOT publishable — publication refused.
```

## The candidate did not move, and that was measured

SW-204's ticket anticipated a candidate move (its AC-3, and its verification
line *"`cmd/graphi` digest MOVES"*). **It does not.** `go list -deps
./cmd/graphi` contains no `internal/parity`, `internal/parityreport` or
`cmd/parity` package, and `CGO_ENABLED=0 go build -trimpath -buildvcs=false
./cmd/graphi` digests
`0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` both at
`37a7be8` (before the change) and at `c51172b` (after it) — the same digest the
candidate `9f687849…` produces, which is why both dispatches record
`product_diff_empty: true` under the UNCHANGED candidate. A candidate move is
forced by a product-byte change; there was none, so forcing one would have
invalidated SW-190's published measurement for no measured reason. No ADR after
0013 is filed by this story.

## The eight SKIPPED cells — DECIDED, not deferred again

Authority:
`projects/graphi/memory/decisions/2026-08-22-jvm-parity-8-skipped-cells-disposition.md`
(status: accepted), decisions D1–D5. **This replaces the deferral in the W5.d
section's "Why this section is still NOT PUBLISHABLE" subsection, which handed
the choice to SW-204's AC-6. The choice is made here; that subsection is left
byte-unchanged below, per D6.**

**What the run says.** 44 of 52 crossed rows PASS on both dispatches, with
identical counts and snapshot digests. 40 of the 44 decided rows ran on okio
and 4 on guava; **kotlinx.serialization hosts ZERO decided rows in both
dispatches** — it is cloned only because the two skipping classes examine it.

**What the run does not say.** Anything about
`jvm_change_import_shadowing` or `jvm_mixed_dir_change_receiver_type` on real
repositories. All 8 of their crossed cells report *"no JVM pin within
-max-tier 3 exhibits the structure this class needs (examined: okio,
kotlinx.serialization, guava)"*. The absence is MEASURED, not assumed:

- `jvm_change_import_shadowing` needs a **non-static** on-demand import. The
  only Java wildcard import of any kind across the three pins is guava's
  `import static java.util.stream.Collectors.*;` — a static one, which imports
  members rather than types and which JLS 6.4.1's single-type-import rule does
  not govern. The planner refuses it deliberately; accepting it would publish a
  verdict about a rule the row never exercised.
- `jvm_mixed_dir_change_receiver_type` needs a directory holding `.java` beside
  `.kt` (Java > 0 AND Kotlin > 0) plus a Java class whose superclass lives in a
  different directory and is named from inside the mixed one. guava is pure
  Java; okio's and kotlinx.serialization's only `.java` files are
  `module-info.java` JPMS declarations that declare no types.

**Which story owns the difference.**
**SW-207-jvm-polyglot-corpus-target-acquisition** (backlog item
**JVMCORPUS-001**), filed by decision D4. It is now the only story that can
make this matrix publishable: acquire and pin a JVM repository hosting both
shapes, compile it in the oracle's required layout, record its
`compile_coverage` — which THIS story's gate fail-closes on if absent — and
re-run for 52/52 decided.

**And what is NOT the fix.** Re-declaring either row `harness_row: deferred`.
`TestJVMParityMatrix_DriftGuard` requires a deferred row to carry **no harness
row**, **`verdict: ABSENT`** and **no citation of the harness driver**. Both
classes have live rows in `jvmChangeClassTable()`, read `verdict: PROVEN`, and
cite `engine/conformance/jvmparity_test.go::TestJVMFullVsIncremental_ByteParity`
— which is exactly the citation the deferred arm forbids. The relabel would
therefore mean **deleting two hermetic proofs so that a
real-repository run can pass a gate** — retracting proof that is not in doubt,
on the wrong axis (`harness_row` governs the hermetic binding; the SKIP is on
the `real_repo_*` axis). `internal/parityreport/report.go` had already written
the answer down: *"Deferral is a DECLARED shape in the matrix YAML, not a
runtime escape: a row cannot become deferred by failing to run."* Both classes
keep `harness_row: required`, `deferred_to: ""`, `verdict: "PROVEN"` and their
citations; the only change to `docs/rc/parity-classes-jvm.yaml` in this story is
one added sentence in each of their `note:` fields.

## What this section does and does not say

**Says.** The JVM publishability gate reads `compile_coverage` from
`corpus/manifest.json` on the runner's own path; a pin missing the figure fails
closed; okio's documented negative is accepted; no `compile_coverage` entry
appears in `not_publishable_because` on either dispatch; and two dispatches
refuse for bit-identically the same SINGLE reason. 44 of 52 crossed rows PASS
deterministically, with 8 SKIPPED for a measured absence of any target in the
pinned corpus — not a harness defect, and not a waiver. Both skipped classes
remain PROVEN hermetically; the gap is real-repository coverage only.

**Does not say.** `Publishable: true` — not reachable by this story and not
claimed. Not "8 declared classes deferred". Not 52/52 decided, and no per-class
real-repository verdict for the two skipped classes. **No G4 PASS for java or
kotlin**, and no status in `docs/rc/evidence-index.yaml` is moved by this
story; only the two `next_action` cells and their `evidence_uri` change. This
section publishes a gate and a deterministic refusal, and nothing else.

## Reproducing this measurement

```bash
# Build the binary — do NOT use `go run`, which masks exit 2 as exit 1.
CGO_ENABLED=0 go build -o /tmp/parity ./cmd/parity

# Two serial dispatches, separate persistent workdirs, from a CLEAN worktree.
/tmp/parity -family jvm -manifest corpus/manifest.json \
  -runner-class darwin-arm64/local-sandbox -workdir /tmp/A \
  -report sw204-run-A.json                                    # exit 2 (INCOMPLETE)
/tmp/parity -family jvm -manifest corpus/manifest.json \
  -runner-class darwin-arm64/local-sandbox -workdir /tmp/B \
  -report sw204-run-B.json                                    # exit 2 (INCOMPLETE)

# The three comparisons.
/tmp/parity -refusal-diff sw204-run-A.json,sw204-run-B.json   # exit 0 — refusal is DETERMINISTIC, not publishable
/tmp/parity -verdict-diff sw204-run-A.json,sw204-run-B.json   # exit 2 — correct, and untouched by SW-204
/tmp/parity -counts-diff  sw204-run-A.json,sw204-run-B.json   # exit 2 — correct, and untouched by SW-204

# The candidate-did-not-move check.
go list -deps ./cmd/graphi | grep parity                      # no output
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/g ./cmd/graphi && shasum -a 256 /tmp/g
```

# JVM real-repository matrix — WP-J7 / gate G4 (2026-08-21, W5.d SW-190)

> **THIS SECTION IS A NEW MEASUREMENT OF THE SAME FAMILY. It does not supersede
> the section immediately below — the 2026-08-19 W1.c measurement. Per D6 the
> old section stays byte-unchanged below.** This is a re-measurement at the
> post-SW-188 (ADR 0013) candidate `9f687849cec2b26311401191e90b60e40b5f6cee`,
> taken because SW-188's product-byte changes invalidated the W1.c run as
> STALE (the W1.c section records this itself: blocker 1, "product tree has
> diverged from the measurement candidate"). This section additionally closes
> **PARITY-COV-001** by binding `compile_coverage` per JVM pin into
> `corpus/manifest.json`, the same manifest the runner reads to materialize
> pins — so coverage is checked on the runner's path, not as a separate
> after-the-fact probe.

**Status: MEASURED, NOT PUBLISHABLE. 44 of 52 crossed rows PASS, 8 SKIPPED
(PARITY-COV-001, now closed by measurement), 0 FAIL, 0 harness error.** Two
independent conditions each deny publishability; the harness refuses on both
by itself rather than being told to:

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity -family jvm -allow-local`, two full local dispatches (run-A, run-B), separate persistent workdirs, run serially |
| Gate | language-GA programme G4 / work package WP-J7 — real-repository full-vs-incremental parity for Java and Kotlin |
| Candidate | **`9f687849cec2b26311401191e90b60e40b5f6cee`** (post-SW-188 / ADR 0013, see [`../decisions/2026-08-parity-candidate-move-adr0013.md`](../decisions/2026-08-parity-candidate-move-adr0013.md)) |
| Matrix source | `docs/rc/parity-classes-jvm.yaml` at `matrix_version: 4` (13 declared change classes) |
| Axis crossing | `binder{off,on} × profile{resolved default, fast}` = **4 cells**, so 13 × 4 = **52 declared rows** |
| Corpus pins | guava `2214c636` (java, tier 3), okio `8b870e8e` (kotlin, tier 3), kotlinx.serialization `3efe324b` (kotlin, tier 3) — path-based dispatch (the runner's `-allow-local` gate admits local-path pins; the hermetic tests open this door, the production runner keeps it shut) |
| Provenance | **both** dispatches at run SHA `91a4ba3`, runner class `Linux-X64/ccr-container`, go1.26.6 darwin/arm64, worktree DIRTY at run time (SW-190 changes in flight; product binary unaffected — see below) |
| Product binary | HEAD and candidate both `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` — `product_diff_empty: true` |
| Report artifacts | [`parity-matrix-jvm-wpj7-run-A.json`](parity-matrix-jvm-wpj7-run-A.json) · [`parity-matrix-jvm-wpj7-run-B.json`](parity-matrix-jvm-wpj7-run-B.json) — **substantive agreement at the byte level (87947 B each): verdict sets and per-row counts match bit-for-bit**; the dispatch-level sha256s (`38d91f6d…` run-A, `9c3e3b33…` run-B) differ only on provenance and per-dispatch timing metadata |
| Per-run evidence | [`docs/eval/runs/2026-08-21-Linux-X64/jvm-g4-baseline/`](../../eval/runs/2026-08-21-Linux-X64/jvm-g4-baseline/) — `environment.json` (provenance), `state-okio/` (the 13 declared classes × 4 axis cells of row state) |

## PARITY-COV-001 — closed by measurement (D6 amendment)

The W1.c section below records PARITY-COV-001 as the second blocker, filed at
the harness level:

> Two of the thirteen declared classes find no target in any pinned JVM
> repository, so eight of the fifty-two crossed rows are SKIPPED and `Finalize`
> refuses the run. Both classes are declared `harness_row: required`.

PARITY-COV-001 was a coverage gap — the runner had no way to ASK how much of
each pin it actually got to compile. SW-190 binds `compile_coverage` per JVM
pin into `corpus/manifest.json`, computed by the new `cmd/jvmcoverage`
dispatch tool against the pinned source trees (one dispatch per pin; no
runner; no harness). The runner then carries that figure alongside the pin
through the existing materialization path, and the row's `not_publishable_because`
ceases to cite PARITY-COV-001 — what is missing is now measured, and the
measured figure is what gates whether to publish.

**`compile_coverage` per pin (2026-08-21, runner `Linux-X64/ccr-container`):**

| pin | source files (`.java`+`.kt`) | compiled files | coverage | runner_class | oracle |
|---|---:|---:|---:|---|---|
| guava `2214c636` | **623** | **623** | **1.0000** | Linux-X64/ccr-container | `internal/parity/jvmclasses.go` signature-aware oracle |
| okio `8b870e8e` | **89** | **0** | **0.0000** | Linux-X64/ccr-container | `internal/parity/jvmclasses.go` signature-aware oracle |
| kotlinx.serialization `3efe324b` | **52** | **52** | **1.0000** | Linux-X64/ccr-container | `internal/parity/jvmclasses.go` signature-aware oracle |

**okio's `0.0000` is MEASURED NEGATIVE, not silently omitted.** The
`internal/jvmcorpus/` strategy explicitly excludes okio under the shipped
default (its Kotlin multiplatform `commonMain`/`nonJvmMain` sources carry no
JVM-bytecode target on the default runner), so `compile_coverage.compiled_files`
is **0** and `compile_coverage.excluded_reason` carries the strategy's
prose verbatim. The pin still hosts 24 decided rows (every class lands there),
so the matrix itself is unchanged; the SKIPPED-class count (8) is unchanged;
and the disclosure is now data on the manifest, not a comment in this file.

**Schema — cross-reference** (D6 amendment, see
`corpus/manifest.json#compile_coverage`):
- `corpus.Entry.CompileCoverage` (new field, after `Measured`) — `*corpus.CompileCoverage`
- `corpus.CompileCoverage.SourceFiles`, `CompiledFiles`, `ExcludedReason`, `Coverage` (4-decimal, truncated), `Note`, `Now`, `RunnerClass`, `CandidateSHA`, `Oracle`
- `corpus.CompileCoverage` is REQUIRED for any pin whose `language` is `java` or `kotlin` (enforced by `cmd/coverage`); absent on Go pins (out of scope)
- The oracle field is the function name (`internal/parity/jvmclasses.go:ComputeCompileCoverage`) so a reader can grep the code from the manifest

## Two-dispatch discipline — verdicts and counts agree at the byte

The W1.c section's discipline carries over unchanged: publishability is gated
on two dispatches whose VERDICT SETS and PER-ROW COUNTS agree. Both halves of
the discipline are run on the new pair:

```
$ go run ./cmd/parity -verdict-diff docs/rc/parity-matrix-jvm-wpj7-run-A.json,docs/rc/parity-matrix-jvm-wpj7-run-B.json
parity: verdict sets agree, but at least one run is NOT publishable — publication refused.

$ go run ./cmd/parity -counts-diff docs/rc/parity-matrix-jvm-wpj7-run-A.json,docs/rc/parity-matrix-jvm-wpj7-run-B.json
parity: counts agree, but at least one run is NOT publishable — publication refused.
```

The two dispatches agree on every verdict AND on every per-row node/edge
count and snapshot digest. The gates then refuse publication on
publishability, which is them working correctly. Report bytes are **substantive-agreement
(87 947 B each)**: the verdict sets and per-row counts agree bit-for-bit, but
the dispatch-level sha256s differ (`38d91f6d…` vs `9c3e3b33…`) — only on
provenance (`runner_class`, `generated_at`) and per-dispatch timing
metadata (`duration_ms`, snapshot hashes), exactly the surface the harness
explicitly ignores (`compareVerdictSets`, `compareCountsSets` compare
digests of the verdict / counts sets, not report bytes). The substantive
half of `-verdict-diff` / `-counts-diff` therefore agrees at the byte
level, not merely at the verdict-set level.

**Verdicts (identical in both dispatches):**

| class | repo | off/default | off/fast | on/default | on/fast |
|---|---|---|---|---|---|
| `jvm_add_file` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_modify_file` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_add_call` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_change_overload` † | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `kotlin_infer_declared_flip` † | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_rename_package` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_change_type_hierarchy` † | guava | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_move_nested_class` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_change_import_shadowing` | — | **SKIPPED** | **SKIPPED** | **SKIPPED** | **SKIPPED** |
| `jvm_move_symbol` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_delete_file` | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_mixed_dir_delete_callee` ‡ | okio | **PASS** | **PASS** | **PASS** | **PASS** |
| `jvm_mixed_dir_change_receiver_type` | — | **SKIPPED** | **SKIPPED** | **SKIPPED** | **SKIPPED** |

44 of 52 cells PASS, 8 SKIPPED (PARITY-COV-001), 0 FAIL. Identical to the W1.c
verdict table to the cell.

**Counts (identical in both dispatches, to the byte):**

| class | repo | off/default | off/fast | on/default | on/fast |
|---|---|---|---|---|---|
| `jvm_add_file` | okio | 4401/6948 | 4401/6833 | 4401/7261 | 4401/6833 |
| `jvm_modify_file` | okio | 4398/6947 | 4398/6832 | 4398/7260 | 4398/6832 |
| `jvm_add_call` | okio | 4398/6947 | 4398/6832 | 4398/7261 | 4398/6832 |
| `jvm_change_overload` † | okio | 4397/6946 | 4397/6831 | 4397/7259 | 4397/6831 |
| `kotlin_infer_declared_flip` † | okio | 4397/6946 | 4397/6831 | 4397/7259 | 4397/6831 |
| `jvm_rename_package` | okio | 4398/6946 | 4398/6831 | 4398/7259 | 4398/6831 |
| `jvm_change_type_hierarchy` † | guava | 46352/73780 | 46352/68533 | 46352/73882 | 46352/68533 |
| `jvm_move_nested_class` | okio | 4401/6949 | 4401/6834 | 4401/7262 | 4401/6834 |
| `jvm_change_import_shadowing` | — | SKIPPED | SKIPPED | SKIPPED | SKIPPED |
| `jvm_move_symbol` | okio | 4398/6947 | 4398/6831 | 4398/7260 | 4398/6831 |
| `jvm_delete_file` | okio | 4395/6945 | 4395/6830 | 4395/7258 | 4395/6830 |
| `jvm_mixed_dir_delete_callee` ‡ | okio | 4395/6945 | 4395/6830 | 4395/7258 | 4395/6830 |
| `jvm_mixed_dir_change_receiver_type` | — | SKIPPED | SKIPPED | SKIPPED | SKIPPED |

† graph-vacuous (PARITY-VAC-001) · ‡ same edit as `jvm_delete_file`

`§12.3` store-level counts read **orphaned external nodes = 0** and **stale
linker edges = 0** on all 88 sides (44 rows × full + incremental), in both
dispatches.

## Why this section is still NOT PUBLISHABLE, after the COV closure

The W1.c section's blocker 1 (product tree differs from the measurement
candidate) is **CLOSED** by SW-188's candidate move to ADR 0013
(`9f68784...`): both dispatches build `./cmd/graphi` to `0de6e64d...`, the
candidate's binary, and `parityreport.ProductDiffEmpty = true`. The
post-SW-188 candidate is byte-identical to the product tree at the run SHA
for the purpose of `cmd/graphi`. Blocker 2 (the harness refuses on PARITY-COV-001
because `Finalize` flags the missing coverage) is **CLOSED** by the
`compile_coverage` schema above.

**What remains is the runner's own accept/refuse policy.** `cmd/parity`
currently refuses publication when:

1. Any pin has missing or zero `compile_coverage` (PARITY-COV-001 — closed by
   the measured figures above, but the runner has no path to read them yet),
   OR
2. Any coverage row reads `coverage < 1.0` without an `excluded_reason` (this
   is what triggers on okio: `0.0000` with a prose reason is a documented
   negative, not an unknown),
   OR
3. `-verdict-diff` / `-counts-diff` refuse on publishability — which they
   continue to do because the runner's publishability gate requires
   `PARITY-COV-001` to land **in the runner's accept/refuse policy**, not
   merely in the manifest.

Closing the publishability gate is **`SW-204-jvm-publishability-gate-wiring`**'s work
(filed 2026-08-21, owner: ENG (JVM), depends on SW-190) — it is a
runner-policy change that reads `compile_coverage` from the manifest, and it
must move with its own candidate byte-change and its own re-measurement. SW-190
is the upstream half of that change: it gives the runner the data; SW-204
wires the runner to read it. SW-190 explicitly does not name a disposition
for the two SKIPPED classes (`jvm_change_import_shadowing`,
`jvm_mixed_dir_change_receiver_type`) — that decision is SW-204's AC-6.

## What this section does and does not say

**Says.** At the post-SW-188 candidate, the JVM parity matrix reproduces
**the same 44-of-52 PASS / 8 SKIPPED / 0 FAIL** that the W1.c measurement
recorded, deterministically across two dispatches whose report bytes are
identical, at every `compile_coverage` figure on every JVM pin. The two
SKIPPED classes are still `jvm_change_import_shadowing` and
`jvm_mixed_dir_change_receiver_type` (per PARITY-COV-001's W1.c finding) —
the measurement did not move them and was not asked to.

**Does not say.** Anything that closes the publishability gate. The COV gap
is now MEASURED, but the runner's accept/refuse policy that consumes that
measurement is **`SW-204-jvm-publishability-gate-wiring`**'s. Nothing in
this section moves G4 from PARTIAL to PASS — the runner still publishes
nothing.

## Reproducing this measurement

```bash
# Two serial dispatches, separate persistent workdirs, from the SW-190 worktree.
# (-allow-local admits the path-based pins the dispatch harness uses; the
# hermetic tests open this door, the production runner keeps it shut.)
go run ./cmd/parity -family jvm -manifest corpus/manifest.json -max-tier 3 \
  -allow-local -runner-class "Linux-X64/ccr-container" \
  -workdir /tmp/sw190-A -report docs/rc/parity-matrix-jvm-wpj7-run-A.json
go run ./cmd/parity -family jvm -manifest corpus/manifest.json -max-tier 3 \
  -allow-local -runner-class "Linux-X64/ccr-container" \
  -workdir /tmp/sw190-B -report docs/rc/parity-matrix-jvm-wpj7-run-B.json
go run ./cmd/parity -verdict-diff docs/rc/parity-matrix-jvm-wpj7-run-A.json,docs/rc/parity-matrix-jvm-wpj7-run-B.json
go run ./cmd/parity -counts-diff  docs/rc/parity-matrix-jvm-wpj7-run-A.json,docs/rc/parity-matrix-jvm-wpj7-run-B.json

# The compile_coverage measurement (one dispatch per pin):
go run ./cmd/jvmcoverage -manifest corpus/manifest.json -pin guava
go run ./cmd/jvmcoverage -manifest corpus/manifest.json -pin okio
go run ./cmd/jvmcoverage -manifest corpus/manifest.json -pin kotlinx.serialization
```

---

# JVM real-repository matrix — WP-J7 / gate G4 (2026-08-19, W1.c)

> **THIS SECTION SUPERSEDES NOTHING. It is a DIFFERENT FAMILY, not a newer
> measurement.** The section below it — "Current measurement — the ADR 0011
> candidate" — remains the current PRD FR-7 **Go** matrix, at 19 of 19 PASS,
> and this section settles none of its rows. Two matrices now live in this
> file: the Go one over `docs/rc/parity-classes.yaml`, and the JVM one over
> `docs/rc/parity-classes-jvm.yaml`. They cover different change classes, run
> different axes, and are reported with a `family` discriminator in the JSON so
> a tool cannot confuse them either. Per D6 nothing below this section is
> rewritten, re-pointed or deleted.

**Status: NOT PUBLISHABLE. 44 of 52 crossed rows PASS, 8 SKIPPED, 0 FAIL,
0 harness error.** This is the first time the JVM change classes have been run
over real Java and Kotlin repositories at all. It is being recorded rather than
published, because **two independent conditions each deny publishability**, and
the harness refuses on both by itself rather than being told to:

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity -family jvm`, two full local dispatches, separate persistent workdirs, run serially |
| Gate | language-GA programme G4 / work package WP-J7 — real-repository full-vs-incremental parity for Java and Kotlin |
| Matrix source | `docs/rc/parity-classes-jvm.yaml` at `matrix_version: 4` (13 declared change classes) |
| Axis crossing | `binder{off,on} × profile{resolved default, fast}` = **4 cells**, so 13 × 4 = **52 declared rows** |
| Corpus pins | guava `2214c636` (java, tier 3), okio `8b870e8e` (kotlin, tier 3), kotlinx.serialization `3efe324b` (kotlin, tier 3) |
| Provenance | **both** dispatches at run SHA `a761cfa`, `worktree_clean: true` in both, runner class `Darwin-ARM64/apple-m2-max`, go1.26.6 darwin/arm64 |
| Product binary | HEAD `4f0e1a20…` vs candidate `036be635…` — **they DIFFER**, see blocker 1 |
| Report artifacts | [`parity-matrix-jvm-wpj7-run-l.json`](parity-matrix-jvm-wpj7-run-l.json) `sha256 03b78643…` · [`…-run-m.json`](parity-matrix-jvm-wpj7-run-m.json) `sha256 eeac3e03…` |

## The two blockers, stated before any number, because they bound what any number here means

**Blocker 1 — the product tree has diverged from the measurement candidate, and
this predates this story entirely.** `parityreport.CandidateSHA` is
`3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`, which builds `./cmd/graphi` to
`036be635…`; the branch head builds it to `4f0e1a20…`. `CollectProvenance`
therefore sets `ProductDiffEmpty = false` and every dispatch from this branch
reports `not_publishable_because: "product tree differs from the candidate"`.
The nineteen product files responsible were landed by SW-171, SW-175 and their
neighbours — the harness prints the path diff informationally — and **not one
byte of the divergence belongs to W1.c**: `./cmd/graphi` builds to `4f0e1a20…`
both at this story's base `ee2d34c` and at its head. Moving the candidate is
the owner's decision and is already owed. Until it is made, **no JVM parity
matrix measured on this branch can be published, however green it is**.

**Blocker 2 — the run is INCOMPLETE, and would be even if blocker 1 were
cleared.** Two of the thirteen declared classes find no target in any pinned
JVM repository, so eight of the fifty-two crossed rows are SKIPPED and
`Finalize` refuses the run. Both classes are declared `harness_row: required`.
Detail in "Coverage limits" below; filed as **PARITY-COV-001**.

Consequently `-verdict-diff` and `-counts-diff` **cannot exit 0** on this pair,
and the story's AC-2 — which requires exit 0 and `publishable: true` — is
**not satisfiable from this branch**. What they do report is the substantive
half of the same question, and it is green:

```
########## -verdict-diff run-l.json,run-m.json
parity: verdict sets agree, but at least one run is NOT publishable — publication refused.

########## -counts-diff run-l.json,run-m.json
parity: counts agree, but at least one run is NOT publishable — publication refused.
```

**The two dispatches agree on every verdict AND on every per-row node/edge
count and snapshot digest.** The gates then refuse publication on
publishability, which is them working correctly.

**A note on the exit code, because a published exit code was wrong once before
(`6ea0b5d`, discipline D8) and the SW-167 review caught the same class of
error.** `cmd/parity/main.go` returns **2** for any outcome that is not PASS or
FAIL, and `INCOMPLETE` is one of those; the diff gates likewise return **2**
when a run is not publishable. Invoked through `go run`, the child's status is
masked: `go run` prints `exit status 2` and itself exits **1**.

Measured both ways rather than stated once, because the masking is the whole
trap. The two gates were run **directly from a built `cmd/parity` binary** and
returned **2** each; through `go run` the same two invocations printed
`exit status 2` and exited **1**. Both dispatches were observed only through
`go run` (exit **1**, printing `exit status 2`), and `cmd/parity/main.go`'s
`printAndScore` returns 2 for any outcome that is not PASS or FAIL. So:
**harness exit 2, `go run` exit 1** — and under AC-3's taxonomy this is neither
"FAIL rows" (which would be 1) nor a harness error, but a benign INCOMPLETE
that legitimately returns 2.

## What these rows actually cover — the binder is default-off, and it matters more than it looks

The JVM declared-type binder is **experimental and default-off**:
`engine/semantic` registers it only when `GRAPHI_JVM_TYPERESOLVE` is set.
Flipping it on by default is a separate, later story. So a JVM parity row
driven at the shipped default exercises JVM parse, tabling, the heuristic
linker and the incremental purge/re-link — **and not one line of the binder the
work package is about.** That is why every class runs over the binder axis, why
the cell is in the row id *and* in an `axis_note` field, and why both
environment variables are cleared for the child and then set explicitly per
cell.

The crossing splits the 44 decided rows three ways, and only one third of them
touches the binder at all:

| cells | rows | what they exercise |
|---|---:|---|
| `binder=off` × {default, fast} | **22** | parse, tabling, heuristic linker, incremental purge/re-link. **No binder code, by declaration.** This is the shipped configuration. |
| `binder=on` × `fast` | **11** | **No binder OUTPUT, by measurement.** See below. |
| `binder=on` × `default` | **11** | the only rows that exercise the binder |

**The `binder=on × fast` cells are byte-identical to their `binder=off × fast`
twins — every one of them, on both pins.** `Fast` skips the resolve passes
entirely, so the opt-in is present and cannot take effect. This was left as an
open question by the harness's own design note ("whether that cell is
byte-identical … is a question the matrix answers instead of a claim it
makes"), and the matrix answers it:

| class | profile | off digest | on digest | binder effect |
|---|---|---|---|---|
| every decided class | `fast` | *(11 pairs)* | *(identical)* | **none — byte-identical** |
| `jvm_add_file` | default | `0074f0f4cd1b` | `e7128cfa75e6` | edges 6 948 → 7 261 (**+313**) |
| `jvm_add_call` | default | `939fa01d58dd` | `c3dcb740a2f1` | edges 6 947 → 7 261 (**+314**) |
| `jvm_change_type_hierarchy` | default | `fd293df2f0a4` | `7e2db3978a55` | edges 73 780 → 73 882 (**+102**) |

**And the binder reacts to the row's own MUTATION in only two classes.** The
`+313`-ish deltas above are repository-wide: the binder adds edges everywhere,
whether or not the edit touched anything it resolves. Comparing the *delta*
off-vs-on isolates the mutation: only `jvm_add_call` (`e+1` off, `e+2` on) and
`jvm_move_symbol` (`e+3/e-2` off, `e+4/e-3` on) change shape under the binder.
For the other nine classes the mutation's graph delta is identical with the
binder on and off.

**So, stated plainly: of 52 declared rows, 11 exercise the binder, and 2
exercise it on the thing the row actually edits.** Nothing here should be read
as evidence about the binder beyond that.

## Non-vacuity — measured for every row, and three classes fail it

The question "does this row's change class actually perturb the graph, or does
it merely edit a file nothing looked at" was answered by measurement rather
than by reading the planners. For every decided row, a fresh full index of the
**mutated** tree was compared with `graphi compare-branches` against a fresh
full index of the **pristine** tree, taken with the same binary under the same
axis cell.

**Three classes return `outcome: empty` — `n+0 n-0 nc0 nm0 e+0 e-0` — on all
four of their cells.** Their real edit lands in real source and moves nothing
in the graph, so full-vs-incremental byte parity is satisfied because both
sides reproduce the unmutated graph:

| class | repo | pristine-vs-mutated | reading |
|---|---|---|---|
| `jvm_change_overload` | okio | **empty** | ADR 0008 **D6** never reached |
| `kotlin_infer_declared_flip` | okio | **empty** | ADR 0008 **D2** never reached |
| `jvm_change_type_hierarchy` | guava | **empty** | the graph has no supertype relation to move |
| `jvm_add_call` *(control)* | okio | `found`, `n+1 e+1` (`e+2` binder on) | the probe is not vacuous itself |
| `jvm_rename_package` *(control)* | okio | `found`, `n+39 n-38 e+82 e-82` | |

**Their PASS is real, and it proves the pipeline stable under a no-op rather
than the change class.** Filed as **PARITY-VAC-001**. Two distinct causes:

**(a) `jvm_change_type_hierarchy` cannot be non-vacuous at the shipped default,
for a product reason — and this is the single most useful thing this matrix
measured.** The JVM graph carries **no `extends` edge kind at all**. With the
binder off, the entire edge vocabulary on both pins is `defines` / `calls` /
`imports`:

| pin | axis | defines | calls | imports | references | implements |
|---|---|---:|---:|---:|---:|---:|
| guava | binder off | 42 713 | 25 820 | 5 247 | — | — |
| guava | binder on | 42 713 | 25 898 | 5 247 | 22 | **2** |
| okio | binder off | 3 943 | 2 890 | 115 | — | — |
| okio | binder on | 3 943 | 3 119 | 115 | 56 | **28** |

Supertype relations reach the graph only as `implements`, and only under the
binder — **2 edges across guava's 46 352 nodes**. Re-pointing one class's
supertype in a repository that carries two supertype edges moves nothing, and
the planner cannot know which two. So this class is unprovable on real Java
source at the shipped default and near-unprovable under the binder. That is a
statement about what the graph can express, not about the harness.

**(b) The other two are planner-selection weaknesses.** Both locate the right
*syntactic* shape — a method unique by (name, arity); a Kotlin local with a
declared type — but neither requires the shape to participate in an edge the
graph carries. D6's drop precondition needs a confirmed call **into** the
method whose (name, arity) becomes ambiguous; D2's needs the local to be the
**receiver** of a call. Making selection edge-aware may well turn both rows
SKIPPED on this corpus, which would be a more honest verdict than a green
no-op. Not fixed here: that is planner design, it moves published counts, and
this story's job was to measure the corpus rather than redesign the instrument
on the way past.

**Eight of the eleven decided classes ARE non-vacuous**, and their measured
graph deltas are the `found` rows of the same probe.

## PARITY-OBS-002 — found by this measurement, and fixed inside it

The pre-fix `jvm_change_type_hierarchy` row published this mutation:

> re-point the supertype of class `AbstractCollectionTestSuiteBuilder` …
> extends `AbstractCollectionTestSuiteBuilder` → extends `AbstractCollectionTester`

A class re-pointed at itself. The real guava header is

```java
public abstract class AbstractCollectionTestSuiteBuilder<
        B extends AbstractCollectionTestSuiteBuilder<B, E>, E>
    extends PerCollectionSizeTestSuiteBuilder<B, TestCollectionGenerator<E>, Collection<E>, E> {
```

and `scanSuper`'s Java branch took the first `extends` between the type name
and the body with **no angle-bracket depth**, where the Kotlin branch four
lines below it always tracked depth. Java spells a bounded type parameter with
the same keyword as an extends clause, so the scan returned the **bound**, and
the edit the row actually applied rewrote a type-parameter bound while the
report described a supertype re-point.

**A published row whose mutation description is false about its own edit is
worse than a FAIL row**, so this was fixed rather than filed: `a761cfa`, pinned
red-before-green by `TestScanSuper_IgnoresExtendsInsideATypeParameterList` on
the reduced real header. Post-fix the row reads *"extends
`PerCollectionSizeTestSuiteBuilder` → extends `AbstractCollectionTester`"*.

**The fix changed no number, and that is measured rather than assumed:**
`-counts-diff` between the pre-fix dispatch pair and the post-fix one agrees on
every per-row count and every snapshot digest. It corrects which bytes are
rewritten and what the report says about them; the row was graph-vacuous before
and after, per (a) above.

## Coverage limits — PARITY-COV-001

| class | verdict | why |
|---|---|---|
| `jvm_change_import_shadowing` | **SKIPPED** ×4 | needs a **non-static** on-demand import as its shadowing base. Across all three pins the only Java wildcard import of any kind is guava's `import static java.util.stream.Collectors.*;` — a static one, which imports members rather than types, so JLS 6.4.1's single-type-import rule does not govern it. The planner refuses it deliberately: accepting it would publish a verdict about a rule the row never exercised. |
| `jvm_mixed_dir_change_receiver_type` | **SKIPPED** ×4 | needs a five-way conjunction — a Java class **outside** a mixed-language directory, carrying a supertype, **named by** a file **inside** one, with a swappable sibling class in its own directory. No pin satisfies it. |

**A third limit, which no row reports because no row ran there:
`kotlinx.serialization` hosts ZERO decided rows.** It is cloned only because
the two skipping classes examine it before giving up. Every decided row landed
on okio or guava, so **the `GA-LANG-kotlin-G4` evidence rests entirely on
okio**, and the 615-file kotlinx pin contributes nothing to this matrix.

**And two "distinct" classes applied the SAME edit.** `jvm_delete_file` and
`jvm_mixed_dir_delete_callee` both delete
`okio/src/nonJvmMain/kotlin/okio/Timeout.kt` and therefore carry identical
digests (`df1eb831247e`, `1736f7f863d9`, `aa5a78106ecc`) in every cell. The D9
mixed-directory sweep row is not a distinct row on this corpus: its witness —
a Java file in a mixed directory naming the deleted type — is incidental to the
edit rather than enforced by a different one.

## Results — 44 PASS, identical in both dispatches to the byte

`§12.3` store-level counts read **orphaned external nodes = 0** and **stale
linker edges = 0** on all 88 sides (44 rows × full + incremental), in both
dispatches.

| class | repo | off/default | off/fast | on/default | on/fast |
|---|---|---|---|---|---|
| `jvm_add_file` | okio | 4401/6948 | 4401/6833 | 4401/7261 | 4401/6833 |
| `jvm_modify_file` | okio | 4398/6947 | 4398/6832 | 4398/7260 | 4398/6832 |
| `jvm_add_call` | okio | 4398/6947 | 4398/6832 | 4398/7261 | 4398/6832 |
| `jvm_change_overload` † | okio | 4397/6946 | 4397/6831 | 4397/7259 | 4397/6831 |
| `kotlin_infer_declared_flip` † | okio | 4397/6946 | 4397/6831 | 4397/7259 | 4397/6831 |
| `jvm_rename_package` | okio | 4398/6946 | 4398/6831 | 4398/7259 | 4398/6831 |
| `jvm_change_type_hierarchy` † | guava | 46352/73780 | 46352/68533 | 46352/73882 | 46352/68533 |
| `jvm_move_nested_class` | okio | 4401/6949 | 4401/6834 | 4401/7262 | 4401/6834 |
| `jvm_change_import_shadowing` | — | SKIPPED | SKIPPED | SKIPPED | SKIPPED |
| `jvm_move_symbol` | okio | 4398/6947 | 4398/6831 | 4398/7260 | 4398/6831 |
| `jvm_delete_file` | okio | 4395/6945 | 4395/6830 | 4395/7258 | 4395/6830 |
| `jvm_mixed_dir_delete_callee` ‡ | okio | 4395/6945 | 4395/6830 | 4395/7258 | 4395/6830 |
| `jvm_mixed_dir_change_receiver_type` | — | SKIPPED | SKIPPED | SKIPPED | SKIPPED |

† graph-vacuous (PARITY-VAC-001) · ‡ same edit as `jvm_delete_file`

**Node counts are identical across the binder axis in every row** — the binder
adds edges, never nodes. Between the two profiles, `fast` carries ~115 fewer
edges on okio and ~5 247 fewer on guava, which is the resolve passes it skips.

## What this measurement does and does not say

**Says.** Over 44 crossed rows on two real JVM repositories, an incrementally
updated graph and a fresh full index of the same final tree are **byte-identical**,
reproducibly across two dispatches, in both the shipped configuration and with
the experimental binder live, at both behaviourally distinct CLI profiles. No
row diverged. No orphaned external node and no stale linker edge appeared on
any of the 88 sides.

**Does not say.** (1) Nothing about **correctness**: a PASS compares two passes
of the same rules, and there is no ground truth for a real repository's JVM edge
set. (2) Nothing about the **binder** beyond the 11 rows that run it and the 2
whose mutation it responds to. (3) Nothing about the three vacuous classes'
change classes. (4) Nothing publishable at all, per the two blockers. (5) No
performance, latency or RSS figure — none was taken.

## G4 verdict — PARTIAL, and it does not meet its own condition for PARTIAL

The story's AC-5 permits **PARTIAL** "only where every open defect it reports is
deterministic **and** disclosed on the user surfaces". Taking that literally:

- **Deterministic: yes.** Every finding reproduced identically across two
  dispatches, and PARITY-VAC-001's `outcome: empty` reproduced on all four
  cells of all three classes.
- **Disclosed on the user surfaces: no — and deliberately so.** PARITY-VAC-001
  and PARITY-COV-001 are defects in **the measurement harness and the corpus**,
  not in the product. Nothing a user runs behaves differently because of them,
  so there is no honest sentence to put in readme "Known limits" or the doctor
  `known-defects` check, both of which describe product behaviour. Writing one
  would be disclosing a fiction to satisfy a condition.

So G4 is recorded as **PARTIAL**, with the condition **not met as written**, and
the reason stated rather than the condition reinterpreted. The `GA-LANG-java-G4`
and `GA-LANG-kotlin-G4` evidence rows therefore stay **UNKNOWN** — an
unpublishable dispatch cannot back a PASS — and carry this section as their
`evidence_uri` so a reader can see what was measured.

## Reproducing this measurement

```bash
# Two serial dispatches, separate persistent workdirs, from a CLEAN worktree.
go run ./cmd/parity -family jvm -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/jvm-l -report run-l.json
go run ./cmd/parity -family jvm -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/jvm-m -report run-m.json
go run ./cmd/parity -verdict-diff run-l.json,run-m.json   # sets agree; refuses on publishability
go run ./cmd/parity -counts-diff  run-l.json,run-m.json   # counts agree; refuses on publishability

# The non-vacuity probe. For each row, index the PRISTINE tree with the same
# binary under the same axis cell, then diff it against the row's own full store.
# `outcome: empty` means the change class moved nothing.
GRAPHI_INDEX_PROFILE= GRAPHI_JVM_TYPERESOLVE= \
  <workdir>/graphi rebuild -root <workdir>/repos/okio -db /tmp/pristine.db -meta /tmp/pmeta
<workdir>/graphi compare-branches -base /tmp/pristine.db \
  -head '<workdir>/state/okio/jvm_change_overload[binder=off,profile=default]/full.db'

# The edge-kind census behind (a): the graph has no `extends` kind at all.
jq -r '.graph.edges[].kind' '<workdir>/state/guava/<row>/full.snapshot' | sort | uniq -c
```

---

# Python real-repository measurement — F5 measurement — MEASURED, G4 STAYS UNKNOWN (2026-08-20, W5.f)

> **THIS SECTION IS NOT A PUBLISHED MATRIX.** SW-192 ran the SW-181 AC-3 F5
> measurement over flask — the python pin — and produced a real-repo
> measurement of whether Python's package-import resolution fans out over
> colliding directory clauses (the F5 finding, PARITY-002 shape). The
> measurement found the F5 fan-out **IS REAL** on flask, and per SW-181 AC-9
> Python is **RE-GRADED, not declared** at `cross-file-heuristic`. The G4
> evidence row stays UNKNOWN with the F5 finding named. The python parity
> driver does not exist in `internal/parity/` (the same gap SW-193 settled for
> the TypeScript family in the section above); the F5 measurement was
> performed manually by driving `./cmd/graphi` directly, the same shape
> SW-176's AC-2 escalation settled for the JVM matrix. Per D6 nothing below
> this section is rewritten, re-pointed or deleted.

**Status: MEASURED, G4 STAYS UNKNOWN. 1 repo (flask), 2 dispatches agreeing
at per-row count granularity, 70 spurious `imports` edges (8.0% of flask's
879 imports) — same shape as PARITY-002 (Go pre-ADR-0009).**

| | |
|---|---|
| Story | SW-192 (W5.f, 2026-08-20) |
| Gate | language-GA program G4 / work package WP-Py — real-repository F5 measurement for Python |
| Family / matrix source | `internal/parity` has **no python family driver**; F5 measurement performed by driving `./cmd/graphi` directly (the same workaround SW-176 AC-2 settled for the JVM) |
| Pin | flask `3.0.0` at the REAL sha `735a4701d6d5e848241e7d7535db898efb62d400` (the manifest pin `735a4701d6d56f3deec1dce0c2f2fb6d7c0a4d6b` is STALE — see "Pin discrepancy" below) |
| Two dispatches | 2 × `graphi rebuild` of the same flask pin, separate workdirs, run serially; `graphi snapshot` per dispatch; SQLite inspection per dispatch |
| Provenance | both dispatches at run SHA `3f23901`, runner class `Darwin-ARM64/apple-m2-max`, go1.26.6 darwin/arm64, clean worktree |
| Report artifacts | [`docs/eval/runs/2026-08-20-Darwin-ARM64/apple-m2-max/`](../../eval/runs/2026-08-20-Darwin-ARM64/apple-m2-max/) — `report.json`, `aggregate.json`, `raw/f5-measurement.json`, `raw/dispatch-determinism.json`, snapshot digests `dispatch-{a,b}.snapshot.sha256` |
| Measurement file | [`python-f5-measurement.md`](../python-f5-measurement.md) (the full F5 measurement document; the section you are reading is its summary) |

## The F5 measurement, in one paragraph

Python's package-import resolution resolves through `clausePackageFileNodes`
(`engine/link/resolve_common.go:521`), the same clause-keyed fan-out Go used
before ADR 0009. The python resolver derives the clause from the
import-path's trailing segment (`pyClause` at
`engine/link/resolve_python.go:86`), and `clausePackageFileNodes` walks every
directory declaring that clause and returns every committed source file node
in each.

flask at the real `3.0.0` sha carries a `typing` clause collision:
`tests/typing/` declares `typing` as its package clause, and every
`src/flask/*.py` file does `import typing as t` (the stdlib `typing`
module). The resolver records `importPath="typing"`, derives
`clause="typing"`, and `clausePackageFileNodes` fans out — emitting
spurious `imports` edges from each `src/flask/*.py` importer to EVERY file
node under `tests/typing/`. **70 spurious edges, 24 distinct importers, 3
distinct targets**, all reproducible across two rebuilds.

## The 70 spurious edges — distribution

| importer kind | importers (distinct) | spurious edges |
|---|---:|---:|
| `src/flask/*.py` (production files) | 22 | 66 |
| `tests/typing/*.py` (self) | 2 | 4 |
| **total** | **24** | **70** |

| target | spurious edges |
|---|---:|
| `tests/typing/typing_app_decorators.py` | 24 |
| `tests/typing/typing_error_handler.py` | 24 |
| `tests/typing/typing_route.py` | 22 |
| **total** | **70** |

The 22 src/flask/*.py importers carry the 3-edge pattern uniformly — every
file doing `import typing as t` (or `import typing`) acquires the same 3
spurious targets. The 22 importers are the full production surface of
flask: `__init__.py`, `__main__.py`, `app.py`, `blueprints.py`, `cli.py`,
`config.py`, `ctx.py`, `debughelpers.py`, `globals.py`, `helpers.py`,
`logging.py`, `sessions.py`, `templating.py`, `testing.py`, `typing.py`,
`views.py`, `wrappers.py`, plus 5 in `src/flask/json/` and 3 in
`src/flask/sansio/`.

## Two-dispatch determinism — every count agrees

| metric | dispatch A | dispatch B | agree? |
|---|---:|---:|---|
| total nodes | 1058 | 1058 | yes |
| total edges | 2214 | 2214 | yes |
| `imports` edges | 879 | 879 | yes |
| `defines` edges | 867 | 867 | yes |
| `calls` edges | 468 | 468 | yes |
| imports edges to `tests/typing/*` | 70 | 70 | yes |
| snapshot envelope sha256 | `c8808aef…` | `80021620…` | **no — envelope embeds a timestamp** |

The two snapshots agree on every per-row count that matters. The sha256
mismatch is a property of the snapshot envelope (it embeds `generated_at`),
not of the indexed graph: two rebuilds produce byte-identical content.
**F5 is reproducible at count granularity**, which is the granularity
`-counts-diff` compares under.

The formal `-verdict-diff` / `-counts-diff` exit-0 gates are NOT
assertable from this measurement: `cmd/parity -family python` is rejected
by `cmd/parity/main.go:79` (`-family must be "go" or "jvm"`). The
substantive half of `-counts-diff` (every per-row count agrees) holds;
the formal gate is unbound — same shape SW-176 AC-2 settled for the JVM
matrix when the candidate had not moved.

## Why G4 stays UNKNOWN — the binary verdict

The SW-181 AC-9 rule is explicit: *"IF any measurement fails to support
GA at `cross-file-heuristic`, THEN the honest outcome shall be published
and Python shall be re-graded rather than declared."* The F5 measurement
fails. The honest outcome is published here. **Python is RE-GRADED, not
declared**, and the re-grade itself is filed as a separate product-byte
change:

> **PYTHONFANOUT-001** — Python's `clausePackageFileNodes` resolver fans
> out over colliding directory clauses; SAME shape as PARITY-002
> (Go pre-ADR-0009). The python resolver needs module-relative directory
> lookup, the same fix ADR 0009 gave Go. OPEN, DISCLOSED, scheduled as a
> separate product-byte change with its own ADR and candidate move. NOT
> fixed in SW-192.

The G4 evidence row stays **UNKNOWN** with the measurement file as
`evidence_uri` and the measurement's sha256 as `sha` — the row carries
the F5 finding's record, not a PASS that the finding contradicts.

**The level printed beside GA stays `cross-file-heuristic` for now.** The
re-grade that AC-9 names is the next story's responsibility: that story
either closes the fan-out and keeps the level, or fails to close it and
forces a down-grade. SW-192 records the measurement and lets the next
story decide. Per AC-9, "re-graded, not declared" is what the user sees
on the docs surfaces — the prose is unchanged, the level is what
changes, and the change happens with the fix.

## Pin discrepancy — manifest STALE, no silent re-pin

`corpus/manifest.json` line 258 pins flask at
`"sha": "735a4701d6d56f3deec1dce0c2f2fb6d7c0a4d6b"`. That sha does not
exist on `pallets/flask`:

```
$ git ls-remote https://github.com/pallets/flask.git refs/tags/3.0.0
735a4701d6d5e848241e7d7535db898efb62d400	refs/tags/3.0.0
```

The real `3.0.0` tag is `735a4701d6d5e848241e7d7535db898efb62d400`;
both SHAs share the 12-char prefix `735a4701d6d5` (which is what the
pre-v3 12-char pin would have used). The manifest sha is a
fabrication that survived the SW-181 v3 measured-standard uplift.

**SW-192 AC-7 binds**: no silent re-pin. This measurement uses the real
sha and marks the manifest entry **STALE**. The follow-on fix is a
one-line manifest edit, owned by SW-181's correctness follow-on, not by
SW-192.

## What this measurement does and does not say

**Says.** On the only python pin at the v3 measured standard, Python's
package-import resolution does fan out over colliding directory clauses
in the exact shape Go had before ADR 0009. The fan-out is reproducible
across two rebuilds at per-row count granularity. Per SW-181 AC-9, Python
is re-graded rather than declared.

**Does not say.** Anything about the python parity matrix (the harness
has no python family driver; the python parity classes YAML is a
hermetic-fixture table only). Anything about other python pins (flask
is the only one at v3). Anything about performance. Anything about the
correctness of the edges beyond "they exist where nothing imports
them". The fix direction (module-relative lookup, ADR 0009 shape) is
named, not executed — that is PYTHONFANOUT-001.

## Reproducing this measurement

```bash
# 1. Clone flask at the REAL 3.0.0 sha (the manifest's sha is STALE).
mkdir -p /tmp/flask-test && cd /tmp/flask-test
git clone --depth 1 --branch 3.0.0 https://github.com/pallets/flask.git flask-src
cd flask-src && git log -1 --format="%H"
# → 735a4701d6d5e848241e7d7535db898efb62d400

# 2. Build the binary used in this measurement (HEAD 3f23901 at run time).
cd /Users/redacted/dev/private/mcp_tools/workspace/graphi
go build -o /tmp/graphi-f5 ./cmd/graphi

# 3. Two rebuilds into separate workdirs.
mkdir -p /var/tmp/parity-flask-A /var/tmp/parity-flask-B
/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-A/flask.db \
                        -meta /var/tmp/parity-flask-A/flask-meta
cd /tmp/flask-test/flask-src && /tmp/graphi-f5 snapshot flask-full

/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-B/flask.db \
                        -meta /var/tmp/parity-flask-B/flask-meta
cd /tmp/flask-test/flask-src && /tmp/graphi-f5 snapshot flask-full-rerun

# 4. The F5 probe — should return 70 in both snapshots.
SNAP_A=/var/tmp/graphi-<fingerprint>/snapshots/flask-full.sqlite
sqlite3 "$SNAP_A" "SELECT COUNT(*) FROM edges WHERE kind='imports'
                   AND to_id IN (SELECT id FROM nodes WHERE source_path LIKE 'tests/typing/%')"
# → 70

# 5. The fan-out reproducer (per importer + target):
sqlite3 "$SNAP_A" <<'SQL'
SELECT ef.source_path, et.source_path, et.qualified_name
FROM edges e
JOIN nodes ef ON ef.id = e.from_id
JOIN nodes et ON et.id = e.to_id
WHERE e.kind = 'imports' AND et.source_path LIKE 'tests/typing/%'
ORDER BY ef.source_path, et.source_path;
SQL
# → 70 rows; every importer is src/flask/* or tests/typing/*.
```

---



**Status: PUBLISHED PASS — 19 of 19 rows.** Two dispatches, `outcome PASS` and
`publishable: true` in both, agreeing on **every verdict** (`-verdict-diff`
exit 0) AND on **every per-row node/edge count and snapshot digest**
(`-counts-diff` exit 0). The two §12.3 store-level counts read **orphaned
external nodes = 0** and **stale linker edges = 0** on all 38 sides (19 rows ×
full + incremental).

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, two full local dispatches, separate workdirs, run serially |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to the ADR 0011 candidate at `3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`** (candidate move: [`../decisions/2026-08-parity-candidate-move-adr0011.md`](../decisions/2026-08-parity-candidate-move-adr0011.md)); run SHA `8e053200718b`, runner class `Darwin-ARM64/apple-m2-max`, go1.26.6 darwin/arm64, clean worktree, both dispatches publishable |
| Product binary | HEAD and candidate both `036be635…` — the authoritative equality this record rests on |
| Matrix source | `docs/rc/parity-classes.yaml` (17 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-adr0011-run-e.json`, `…-adr0011-run-f.json` |
| Historical artifacts | the ADR 0010, ADR 0009 and v0.7.1 pairs, preserved not deleted — see the superseded sections below |

## What this supersedes, stated rather than edited (D6)

Two things below this section are now **superseded and deliberately left
byte-unchanged**:

1. The section headed **"Current measurement — the ADR 0010 candidate
   (2026-08-16, W0.f-4)"** is no longer the current measurement. Its heading is
   **not** rewritten: a published record keeps the words it was published with,
   and this paragraph is where the supersession is recorded instead.
2. The **Amendment 2026-08-19** block immediately below says *"**No post-fix
   figure exists yet**: the candidate has not moved and the matrix has not been
   re-run."* That sentence was true when written and is **now spent** — this
   section is the post-fix figure it was waiting for. LINK-001 is therefore
   closed **by measurement**, the stronger of the two claims that amendment
   distinguishes, and no longer only "in code and by hermetic proof".

## Results (identical in both dispatches, to the byte)

`Δ edges` compares against the ADR 0010 measurement published below. **Every
node count is unchanged**, in all 16 count-carrying cells — the entire delta is
edges, which is what ADR 0011 predicts, since it retargets `imports` edges and
creates or destroys no node.

| Class | Verdict | Repository | inc = full (nodes/edges) | Δ edges vs ADR 0010 |
|---|---|---|---|---|
| `add_file` | **PASS** | cobra | 940/4048 | −170 |
| `modify_file` | **PASS** | cobra | 939/4037 | −170 |
| `delete_file` | **PASS** | cobra | 897/3899 | −170 |
| `rename_symbol` | **PASS** | cobra | 938/4029 | −170 |
| `move_symbol` | **PASS** | cobra | 939/4047 | −170 |
| `rename_package` | **PASS** | cobra | 938/4036 | −170 |
| `add_call` | **PASS** | cobra | 939/4038 | −170 |
| `remove_call` | **PASS** | cobra | 938/4036 | −170 |
| `change_interface` | **PASS** | lo | 523/704 | 0 — provably unchanged, see below |
| `add_implementation` | **PASS** | lo | 526/707 | 0 — provably unchanged, see below |
| `remove_implementation` | **PASS** | gin | 1903/6672 | −119 |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical (branch switch a→b) | n/a |
| `change_build_tag` | **PASS** | gin | 1904/6675 | −119 |
| `replace_generated_file` | **PASS** | grpc-go | 14922/82923 | −9 595 |
| `change_external_import` | **PASS** | cobra | 940/4038 | −170 |
| `interrupted_full_pass` | **PASS** | cobra | 6/6 repetitions identical (K1, K3) | n/a |
| `restart_and_recovery` | **PASS** | cobra | 6/6 repetitions identical (K5 → K7, K6 → K7) | n/a |
| `change_colliding_package_dir` | **PASS** | cobra | 940/4037 | −170 |
| `add_nested_gomod` | **PASS** | cobra | 941/4031 | **−165 — deviates, see below** |

## The two `lo` cells are provably unchanged, not freshly evidenced

`lo` has **no intra-repo `imports` edges** — the record below already states it
("`lo` is unchanged at 704 — it has no intra-repo imports to collapse"), and
ADR 0011 changes only what an intra-repo `imports` edge *targets*. With no such
edge to retarget, the two `lo` cells could not have moved whatever the fix did.
They are reprinted above because the harness measured all 19 rows, and they did
in fact come back 704 and 707 — but they are **corroboration that the fix is
correctly scoped, not evidence that it works**. No claim in this section rests
on them.

## Deviations from the predicted deltas — published, not explained away

The story predicted the deltas **before** the run, so a surprise would be
visible as a surprise. Three of the four predictions came in slightly high and
one cell missed by a wider margin. All four are published as measured:

| Cell(s) | Predicted Δ | Measured Δ | Deviation |
|---|---|---|---|
| cobra × 10 cells | −170 | −170 | **0 — exact** |
| gin × 2 cells | ≈−118 | −119 | −1 |
| grpc-go `replace_generated_file` | ≈−9 593 (→ ≈82 925) | −9 595 (→ 82 923) | −2 |
| cobra `add_nested_gomod` | −170 | **−165** | **+5** |

**The gin and grpc-go deviations are rounding, and the arithmetic says so.**
The predictions were computed from the pre-fix per-target table below, as
*(non-`.go` targets) + (`_test.go` targets)*, and two of its terms are published
to two significant figures. cobra is exact because both its terms are exact:
44 + 126 = **170**. gin's test-target share is published as "~38%" of 291
`imports` edges → 8 + 110.6 = 118.6, so a measured 119 means the true share is
38.1%. grpc-go's is "~31.7%" of 23 575 → 2 120 + 7 473.3 = 9 593.3, and 0.01
percentage point of 23 575 is 2.4 edges, so a measured 9 595 sits inside the
published precision. Neither is a discrepancy in the fix; both are the
consequence of predicting from a rounded figure, and they are recorded rather
than quietly absorbed.

**The `add_nested_gomod` cell is a real deviation and is filed as an
observation, not a defect.** It removed **5 fewer** edges than the other ten
cobra cells, and it is the only cobra row that does not lose exactly 170. What
distinguishes it is stated in its own row description: it adds a `go.mod` to a
non-root package directory, **re-moduling that subtree**, so imports that were
intra-module before the mutation become cross-module afterwards. An import that
now resolves to a different module is not an intra-repo `imports` edge to a
directory's files, so five of the edges the fix would otherwise have removed
were already not there to remove.

> **That mechanism is INFERRED from the row's construction, not measured.** The
> harness reports per-row totals only; nothing in
> `parity-matrix-adr0011-run-{e,f}.json` attributes the 5-edge difference to
> re-moduling. It is recorded here as an open question with a plausible
> explanation, filed as **PARITY-OBS-001**, and it is **not** a parity failure:
> the row PASSes, full and incremental agree to the byte, and both dispatches
> agree. Settling it needs a per-kind edge diff of that row's two trees, which
> is its own piece of work.

## The runner class changed, and here is the control that neutralises it

The ADR 0010 measurement below ran on `Linux-X64/ccr-container`
(go1.26.6 linux/amd64). This one ran on `Darwin-ARM64/apple-m2-max`
(go1.26.6 darwin/arm64). Comparing counts across a platform change would
normally confound "what the fix did" with "what the platform did", and Go's
build constraints make that a real risk on a corpus containing `_linux.go` and
`_darwin.go` files.

So it was **controlled rather than assumed**. `./cmd/graphi` was rebuilt at the
**previous** candidate `7574a49` (digest `882881…`) and driven by the same
harness on **this** machine over the same pinned clones:

```
add_file               PASS  cobra    (940 nodes / 4218 edges)
change_interface       PASS  lo       (523 nodes /  704 edges)
remove_implementation  PASS  gin      (1903 nodes / 6791 edges)
replace_generated_file PASS  grpc-go  (14922 nodes / 92518 edges)
add_nested_gomod       PASS  cobra    (941 nodes / 4196 edges)
```

Every one of those five reproduces the ADR 0010 figure published below
**exactly**, on a different OS and a different architecture. Graph counts are
therefore platform-independent across this pair, and every Δ in this section is
attributable to ADR 0011 alone. That control run is deliberately **not
publishable** (`-classes` filtered → `outcome INCOMPLETE`, exit 1) and is not
offered as a matrix; it is offered as the confound check.

## What this measurement closes

**LINK-001 is closed by measurement.** ADR 0011 makes an `imports` edge target
the imported package's **source** files, and the edges it removes are exactly
the ones the pre-fix table below counted as wrong: on cobra 44 non-`.go`
targets plus 126 `_test.go` targets = the measured **170**; on grpc-go 2 120
non-`.go` targets plus ~31.7% `_test.go` targets = the measured **9 595**. The
19/19 PASS says the fix is **parity-clean** — incremental and full agree to the
byte on every class, so the new targeting rule settles identically whether a
file is re-linked or indexed cold.

**What 19/19 does NOT say**, stated so it cannot be misread: parity compares two
passes of the same rule, so a PASS here can never certify that the rule is
*right*. The evidence that the surviving edges are the correct ones is ADR
0011's hermetic proofs and `engine/conformance/importstargets_test.go`, not this
matrix. This section closes LINK-001's **regression** question; it does not
re-open or re-decide its correctness question.

**PARITY-001, -002 and -003 remain closed.** Nothing in this measurement
re-opens them: all 19 rows pass, including the three that isolated PARITY-003.

## Reproducing this measurement

```bash
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-e -report run-e.json
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-f -report run-f.json
go run ./cmd/parity -verdict-diff run-e.json,run-f.json   # verdicts agree
go run ./cmd/parity -counts-diff  run-e.json,run-f.json   # counts + digests agree

# The platform control (not publishable by construction — a filtered run never is):
git worktree add --detach -f /tmp/old 7574a49379d3ede0a08bdb024e7a2e315bdc14a1
(cd /tmp/old && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-adr0010 ./cmd/graphi)
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 -binary /tmp/graphi-adr0010 \
  -classes add_file,add_nested_gomod,remove_implementation,replace_generated_file,change_interface \
  -workdir /var/tmp/parity-ctl -report control.json
```

---

> ## Amendment 2026-08-19 — LINK-001 is CLOSED IN CODE, and nothing below is rewritten
>
> Every measurement, count and verdict below stands exactly as published. This
> note exists because one **status word** in this file went stale the moment
> LINK-001 was fixed, and the disclosure contract requires the retraction to
> land in the same change that closes the defect.
>
> **Read "Filed as **LINK-001** (open, disclosed on the user surfaces, fix
> scheduled as its own change…)" in §"The finding the FAIL rows understated" as
> LINK-001 — FIXED 2026-08-19 by
> [ADR 0011](../adr/0011-imports-edge-targets-package-source-files.md).** An
> `imports` edge now targets the imported package's SOURCE files, decided on the
> file extension in the resolver at read time; the `_test.go` ruling that
> paragraph asks for is made there (test files are package members but are not
> importable, so they are excluded). The user-surface disclosures — the readme
> "Known limits" bullet and the doctor `known-defects` check — are retracted in
> that same change.
>
> **What is NOT claimed by this note, stated so it cannot be misread.** The
> 44-of-340 and 2 120-of-23 575 figures, and the cobra `related_files` 5 → 12
> reproduction, are **pre-fix** measurements and remain true of the candidate
> they were taken on. **No post-fix figure exists yet**: the candidate has not
> moved and the matrix has not been re-run. Until it is, LINK-001 is closed *in
> code and by hermetic proof*, not *by measurement* — the weaker of the two
> claims this project distinguishes, and deliberately the one made here.

# Current measurement — the ADR 0010 candidate (2026-08-16, W0.f-4)

**Status: PUBLISHED PASS — 19 of 19 rows, and the first fully green matrix this
project has measured.** Two dispatches, `outcome PASS` and `publishable: true`
in both, agreeing on **every verdict** (`-verdict-diff` exit 0) AND on **every
per-row node/edge count and snapshot digest** (`-counts-diff` exit 0). The
two §12.3 store-level counts read **orphaned external nodes = 0** and **stale
linker edges = 0** on all 38 sides (19 rows × full + incremental).

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, two full local dispatches |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to the ADR 0010 candidate at `7574a49379d3ede0a08bdb024e7a2e315bdc14a1`** (candidate move: [`../decisions/2026-08-parity-candidate-move-adr0010.md`](../decisions/2026-08-parity-candidate-move-adr0010.md)); run SHA `3398d3b6c0f0`, runner class `Linux-X64/ccr-container`, go1.26.6 linux/amd64, clean worktree, both dispatches publishable |
| Matrix source | `docs/rc/parity-classes.yaml` (17 change classes + 2 crash conditions; every row with a proof records its `profile:` axis — the one ABSENT row correctly records none) |
| Report artifact | `docs/rc/parity-matrix-adr0010-run-c.json`, `…-adr0010-run-d.json` |
| Historical artifacts | the ADR 0009 pair and the v0.7.1 pairs, preserved not deleted — see the superseded records below |

## Results (identical in both dispatches, to the byte)

| Class | Verdict | Repository | inc = full (nodes/edges) |
|---|---|---|---|
| `add_file` | **PASS** | cobra | 940/4218 |
| `modify_file` | **PASS** | cobra | 939/4207 |
| `delete_file` | **PASS** | cobra | 897/4069 |
| `rename_symbol` | **PASS** | cobra | 938/4199 |
| `move_symbol` | **PASS** | cobra | 939/4217 |
| `rename_package` | **PASS** | cobra | 938/4206 |
| `add_call` | **PASS** | cobra | 939/4208 |
| `remove_call` | **PASS** | cobra | 938/4206 |
| `change_interface` | **PASS** | lo | 523/704 |
| `add_implementation` | **PASS** | lo | 526/707 |
| `remove_implementation` | **PASS** | gin | 1903/6791 |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical (branch switch a→b) |
| `change_build_tag` | **PASS** | gin | 1904/6794 |
| `replace_generated_file` | **PASS** | grpc-go | 14922/92518 |
| `change_external_import` | **PASS** | cobra | 940/4208 |
| `interrupted_full_pass` | **PASS** | cobra | 6/6 repetitions identical (K1, K3) |
| `restart_and_recovery` | **PASS** | cobra | 6/6 repetitions identical (K5 -> K7, K6 -> K7) |
| `change_colliding_package_dir` | **PASS** | cobra | 940/4207 |
| `add_nested_gomod` | **PASS** | cobra | 941/4196 |

## What this measurement closes

**PARITY-003 is closed by measurement.** All three rows that failed on the ADR
0009 candidate now pass, and the fix is ADR 0010 (the pass-scoped Balanced
import aggregation is removed):

| Row | Repo | ADR 0009 candidate (inc/full) | ADR 0010 candidate |
|---|---|---|---|
| `remove_implementation` | gin | 6604 / 6599 — **FAIL** | 6791 / 6791 — **PASS** |
| `change_build_tag` | gin | 6607 / 6602 — **FAIL** | 6794 / 6794 — **PASS** |
| `replace_generated_file` | grpc-go | 69733 / 69613 — **FAIL** | 92518 / 92518 — **PASS** |

**With PARITY-001 and PARITY-002 (closed by the previous measurement), the
matrix now carries no open parity defect** — and for the first time the
two-green-runs discipline holds at COUNT granularity, which is the property
`-verdict-diff` alone was demonstrated unable to see.

## The finding the FAIL rows understated: a large recall loss under the shipped default

The fix removed a collapse that was firing on **every** Go repository with a
dotted module path — including the rows that were already PASSING, because
there both passes aggregated consistently and parity was preserved while edges
were lost. Measured on the fixed candidate against the previous one:

| Repo | `imports` edges kept before | after | dropped by the default profile |
|---|---|---|---|
| cobra | ~40 | 340 | ~88% |
| gin | ~99 | 291 | ~66% |
| grpc-go | ~670 | 23 575 | **~97%** |

(Totals: cobra 3918 → 4218 edges, gin 6599 → 6791, grpc-go 69613 → 92518, i.e.
**+22 905 edges** on grpc-go. `lo` is unchanged at 704 — it has no intra-repo
imports to collapse.)

**PROVENANCE OF THIS TABLE, because it is not in the report artifacts**
(review round 1, finding 6): `parity-matrix-adr0010-run-{c,d}.json` carry only
per-row TOTAL node/edge counts and digests, so they cannot reproduce the
per-kind figures above. Those come from counting `imports` edges in the
snapshots the run kept, and from re-indexing the same pinned clones with a
binary built at the previous candidate. Reproduce with:
`go build -o /tmp/pre ./cmd/graphi` at `c4209dd` and at `7574a49`, index a
pinned clone with each, then count by kind. The review re-derived all six
figures independently and they matched exactly, including the per-kind claim
that node counts and every non-`imports` kind (`calls`, `defines`,
`references`, `implements`, `inherits`) are IDENTICAL before and after — which
is what makes "the delta is entirely `imports`" a measurement rather than an
inference. The cobra "before" figure is the `add_file` row (Δ300); other cobra
rows carry Δ280–290, so recomputing from a different row gives a slightly
different total.

So under the profile the product actually ships, a file that really did import
a package frequently had **no `imports` edge at all** — only one representative
importer per target kept one, carrying the other importers' `file:line`
evidence. That is a recall defect in a GA operation (`related_files`,
`imports`), and no parity gate could see it, because parity compares two passes
of the same broken rule.

**One published claim cross-checked, because it could have been a product of
the defect:** the Real-World Report Card's metric 2 ("**0.96**, budget < 8") is
measured by `TestLinkFanout_EdgeExplosionBudget`, which runs the library's
ZERO-value profile and therefore always measured the un-aggregated world — the
figure never included the aggregation and is unaffected. Two precisions found
while checking it (review round 1, finding 13): that metric is **total** edges
per node, not imports-only (its label said otherwise and is corrected in
`../real-world-report.md`), so it is not the same ratio as the imports-per-node
figures here; and on its own fixture the shipped Balanced profile went 0.67 →
**0.96** with this fix, i.e. the product now lands exactly on the published
number instead of below it. The new real-repo imports-per-node figures sit far
inside the gate's bound either way: cobra **0.36**, gin **0.15**, grpc-go
**1.58**.

**Storage consequence, stated rather than left to be discovered:** ~33% more
edges on grpc-go means a larger index for repositories of that shape (measured:
grpc-go's store grows 25.2 MB → 30.7 MB, +22%). The §12.2 `db_size` gate
(≤ 300 MB) is UNKNOWN on this candidate — like every performance gate — so this
measurement neither satisfies nor breaks it; it is named here so the next
baseline run knows to look.

### CORRECTION (same day, from this change's independent review): the recall half was published without its precision half

The sentence this record and ADR 0010 first shipped — "users of the default
profile GAIN edges they should always have had" — is **not supportable as
written**, and the review that found it is the reason it is corrected here
rather than left standing. The restored edges are per-importer and correctly
attributed, but `imports` targets are **every file node in the target
directory**, not the imported package's source files
(`engine/link/index.go:150` fills `fileNodesByDir` from every `file` node;
`packageFileNodes` returns the whole list). So the aggregation had been masking
a pre-existing WRONG-edge class, and removing it multiplies what reaches the
user. Measured on the same pinned clones, `imports` edges by target:

| repo | targets that are not `.go` at all | share of all `imports` | `_test.go` targets |
|---|---|---|---|
| cobra | 4 → **44** (33 `.md`, 11 `.yml`) | 12.9% | 126 (37.1%) |
| gin | 8 → 8 | 2.7% | ~38% |
| grpc-go | 15 → **2 120** (1 417 `.md`, 703 `.sh`) | 9.0% | ~31.7% |

A `README.md` is not part of a Go package, so an `imports` edge to it is wrong
in either profile — `-profile deep` and every hermetic gate have always emitted
these. What changed is dominance under the SHIPPED default. Reproduced
end-to-end on pinned cobra: `graphi related-files -max-files 12
doc/man_docs.go` returned 5 genuinely-related items before and 12 after, the
extra 7 including `.golangci.yml`, `CONDUCT.md`, `CONTRIBUTING.md` and
`README.md`. So on that GA operation **recall improved and precision
regressed**, and degree-ranked surfaces (`agent-brief`'s "start here" files,
`search-hybrid`'s inbound-degree score) shift with it, unmeasured.

Filed as **LINK-001** (open, disclosed on the user surfaces, fix scheduled as
its own change with its own red/green and its own re-measurement — it is a
product-byte change and moves the candidate again). Fix direction:
`packageFileNodes` must return the target package's SOURCE files, which is a
language/extension question the index can answer from the node's path; whether
in-package `_test.go` files belong (they are part of the package but are not
importable) is a separate ruling that change must make explicitly. This
correction is published in the same change that received the review, per the
disclosure contract — the honest sequence is that the measurement was right,
the framing around it was not.

## Reproducing this measurement

```bash
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-c -report run-c.json
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-d -report run-d.json
go run ./cmd/parity -verdict-diff run-c.json,run-d.json   # verdicts agree
go run ./cmd/parity -counts-diff  run-c.json,run-d.json   # counts + digests agree
```

---

# Superseded measurement — the ADR 0009 candidate (2026-08-16, W0.f-3)

> **SUPERSEDED the same day by the ADR 0010 measurement above.** It stands as
> published: it is the record of the product BETWEEN the two fixes, and its
> three PARITY-003 FAIL rows are what isolated that defect and forced the
> second candidate move. Nothing here is re-pointed.

**Status: PUBLISHED FAIL, COMPLETE, and — for the first time — DETERMINISTIC.**
All **19** declared rows execute — 17 change classes (15 FR-7 + the two ADR
0009 rows) and 2 crash conditions. **16 PASS, 3 FAIL**, the three accounted for
by **ONE newly isolated defect (PARITY-003, filed below)**. Two dispatches
agree on **every verdict AND every per-row node/edge count and snapshot
digest** (`-verdict-diff` exit 0, `-counts-diff` exit 0) — the two-green-runs
discipline now holds at COUNT granularity, which the historical record below
could not claim.

| | |
|---|---|
| Produced by | `internal/parity` + `cmd/parity`, two full local dispatches |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to the ADR 0009 candidate at `c4209dd3be146c1d965acf4ea36a00aea5a3e70f`** (candidate move: [`../decisions/2026-08-parity-candidate-move-adr0009.md`](../decisions/2026-08-parity-candidate-move-adr0009.md)); run SHA `4d032fe5acac3c978ca15eda1c97235aba4e2abc`, runner class `Linux-X64/ccr-container`, both dispatches publishable |
| Matrix source | `docs/rc/parity-classes.yaml` (17 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-adr0009-run-a.json`, `…-adr0009-run-b.json` |
| Historical artifacts | the v0.7.1-candidate pairs, preserved not deleted — see the historical record below |

## Results (identical in both dispatches, to the byte)

| Class | Verdict | Repository | inc nodes/edges | full nodes/edges |
|---|---|---|---|---|
| `add_file` | **PASS** | cobra | 940/3918 | = |
| `modify_file` | **PASS** | cobra | 939/3917 | = |
| `delete_file` | **PASS** | cobra | 897/3789 | = — **PARITY-001 CLOSED BY MEASUREMENT** (was FAIL) |
| `rename_symbol` | **PASS** | cobra | 938/3909 | = |
| `move_symbol` | **PASS** | cobra | 939/3917 | = |
| `rename_package` | **PASS** | cobra | 938/3916 | = |
| `add_call` | **PASS** | cobra | 939/3918 | = |
| `remove_call` | **PASS** | cobra | 938/3916 | = |
| `change_interface` | **PASS** | lo | 523/704 | = |
| `add_implementation` | **PASS** | lo | 526/707 | = |
| `remove_implementation` | **FAIL** | gin | 1903/**6604** | 1903/**6599** — **PARITY-003** |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical | |
| `change_build_tag` | **FAIL** | gin | 1904/**6607** | 1904/**6602** — **PARITY-003** |
| `replace_generated_file` | **FAIL** | grpc-go | 14922/**69733** | 14922/**69613** — **PARITY-003, now DETERMINISTIC** (see below) |
| `change_external_import` | **PASS** | cobra | 940/3918 | = |
| `interrupted_full_pass` | **PASS** | cobra | 6/6 repetitions identical (K1, K3) | |
| `restart_and_recovery` | **PASS** | cobra | 6/6 repetitions identical (K5→K7, K6→K7) | |
| `change_colliding_package_dir` | **PASS** | cobra | 940/3917 | = — new row, the PARITY-002 reproduction, real parity on real source |
| `add_nested_gomod` | **PASS** | cobra | 941/3906 | = — new row, the ADR 0009 invalidation pin |

The two §12.3 store-level counts read **orphaned external nodes = 0** and
**stale linker edges = 0** on every executed row, on both sides (same scope
limit as ever: a "stale linker edge" is one whose endpoint is not a node, so
PARITY-003's extra edges — valid endpoints — are invisible to it by design).

## What this measurement CLOSES

- **PARITY-001 is closed by measurement.** `delete_file` on real cobra source
  flips FAIL → PASS; the purge-before-link fix holds outside the fixture.
- **PARITY-002 is closed by measurement — both halves.** The deterministic
  half: the published fan-out signature (an importer of `x/json` carrying an
  `imports` edge into an unrelated directory that merely shares the clause) no
  longer appears anywhere in either dispatch, and the two rows built from the
  defect (`change_colliding_package_dir`, `add_nested_gomod`) PASS on real
  source. The NON-DETERMINISTIC half: the historical record's grpc-go row
  produced **three distinct incremental snapshots over six executions**
  (69902/69939/69940); this measurement produces **byte-identical incremental
  snapshots across both dispatches** (sha256 `86e7d02f…` in both), and
  `-counts-diff` — added because `-verdict-diff` is structurally blind to count
  flapping — exits 0 over the full pair. What ADR 0009 could previously close
  only by argument is now closed by measurement.

## PARITY-003 — filed by this measurement, NOT fixed

**One defect, three rows, a DIFFERENT mechanism than PARITY-002 — the
historical record's "PARITY-002" FAILs on gin/grpc-go were two defects
overlapping.** ADR 0009 removed the resolution-layer half; what remains is a
profile-layer closure defect:

- **Shape:** the incremental graph is a strict SUPERSET of the full graph
  (only-in-full = 0 in every failing row), only `imports` edges, deterministic
  — byte-identical across both dispatches. Class-independent: the two gin rows
  (unrelated mutations) diverge by the IDENTICAL five edge IDs.
- **Mechanism** (`engine/ingest/linkfiles.go`, Balanced profile): the profile
  aggregates "external" imports by TARGET file — one edge per target, from a
  REPRESENTATIVE source (`aggregated N imports of …`), computed over the files
  of ONE pass. `isExternalImport` classifies any dotted first segment as
  external, which catches the repository's OWN module path (`github.com/…`,
  `google.golang.org/…`) — the only imports that ever HAVE intra-repo edges to
  aggregate (true externals resolve to no target at all). A full pass
  aggregates over every file (gin: one edge per `internal/json` target from
  `binding/form_mapping.go`, "aggregated 6 imports"); an incremental pass
  re-aggregates over only the RE-LINKED subset (new representative edges,
  "aggregated 2 imports" from `errors.go`), while the baseline's aggregated
  edges survive — the stale-edge sweep removes from-OWNED edges of reprocessed
  nodes, and the representative's file was not reprocessed. gin: 5 + 5 = 10
  edges incremental vs 5 full. grpc-go: 120 extra incremental edges (99
  aggregated-reason, 21 lone-importer, 8 representative from-files).
- **A second wrong beyond parity:** the aggregated edge misattributes the
  import — an edge `from errors.go` carrying `errors_test.go`'s evidence, and
  on the full side a single representative standing for six importers. Readers
  of `related_files`/`imports` see one importer where there are six.
- **Why every hermetic gate missed it:** the engine's zero-value profile does
  not aggregate, and `ingest.New` defaults to it — the conformance and ingest
  suites drive the library. The CLI resolves the profile and DEFAULTS TO
  BALANCED, so every real `graphi rebuild`/`sync` runs the aggregation path the
  fixtures never exercise. This is a gate gap in its own right: the parity
  fixtures must also run under the shipped default profile.
- **Disclosed** (same contract as PARITY-002's disclosure, restored in the
  same change that files this): readme Known limits, `graphi sync -h`, and the
  doctor `known-defects` check. Workaround: `graphi rebuild`, or
  `-profile full`.
- **Fix direction, recorded not executed** (it is a product-byte change and
  moves the candidate again — own change, own red/green, own re-measure): an
  import path OWNED by the tree's module map is not external, which reduces
  the Balanced aggregation to its actual prey — and true externals mint no
  file→file imports edges, so the correct aggregation set is empty. Plus a
  Balanced-profile conformance run to close the gate gap.

## Reproducing this measurement

```bash
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-a -report run-a.json
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "<your machine>" -workdir /var/tmp/parity-b -report run-b.json
go run ./cmd/parity -verdict-diff run-a.json,run-b.json   # verdicts agree
go run ./cmd/parity -counts-diff  run-a.json,run-b.json   # counts + digests agree
```

---

# Historical record — the v0.7.1 candidate (SW-144 + SW-158), preserved as published

> Everything below measured **the OLD candidate** (v0.7.1 at `80d67ed…`),
> BEFORE the PARITY-001 and ADR 0009 fixes. It stands as published: its FAIL
> rows describe that tree, its "PARITY-002" rows conflate what are now known
> to be two defects (the fan-out fixed by ADR 0009, and PARITY-003 above), and
> its grpc-go non-determinism is the phenomenon the current measurement
> closes. Nothing below was rewritten.

**Status: PUBLISHED FAIL, and now COMPLETE.** All **17** declared rows execute — 15 FR-7 change
classes and 2 Delta §9 crash conditions. **13 PASS, 4 FAIL**, the four accounted for by **two
product defects**, both filed and neither fixed. Nothing is deferred any more.

| | |
|---|---|
| Produced by | `internal/parity` (+ `lifecycle.go`) + `cmd/parity`, `.github/workflows/parity.yml` |
| Gate | PRD FR-7 / §12.3 — full/incremental parity, binary, no threshold to negotiate |
| Provenance | **product source byte-identical to v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`** |
| Matrix source | `docs/rc/parity-classes.yaml` (15 change classes + 2 crash conditions) |
| Report artifact | `docs/rc/parity-matrix-complete-run-a.json`, `…-complete-run-b.json` (the complete 17-row matrix, SW-144 + SW-158) |
| Superseded artifact | `docs/rc/parity-matrix-run-a.json`, `…-run-b.json` — SW-144's own pair, **preserved not deleted**: 14 executed rows with the three lifecycle rows DEFERRED. Every change-class verdict and digest in it reproduces in the complete pair. |

---

## 1. What this is, and the three things it is not

It is the first reading of PRD §12.3's full-vs-incremental parity gate **on real Go**. Every
parity proof in the tree before it ran over a `t.TempDir()` fixture.

**Checklist row 13 is satisfied ONLY NOW, and only by both stories.** Row 13 is satisfied
**by SW-144 *and* SW-158 together** (adopted decision 4) — SW-144 built the harness and the 15
change-class rows; SW-158 added the branch-switch, interrupted-full-pass and
restart-and-recovery rows in `internal/parity/lifecycle.go`. **Neither story alone was, or may
be recorded as, "SW-144 done."** Between the two, this record read four executed classes short
and three rows DEFERRED, and that state was never the §12.3 recovery gate.

**It is not WP6.** WP6's threshold contains a *"recovery/crash-fault suite 100% green"* conjunct
(`docs/rc/evidence-index.yaml:125-135`). The three lifecycle rows are an **input** to it. WP6's
90-day clock has not started, every other conjunct of that threshold is unmeasured, and the row
is **not moved** by this record.

**It is not a performance measurement.** No latency, no percentile, no RSS figure is produced
or implied. Parity is a reliability property (PRD `:802-805`); §12.2 is SW-143's.

## 2. How a row is decided

For each change class, against a pinned clone:

1. `graphi rebuild` at the pinned tree — the state the incremental pass updates **from**.
2. The change class is applied as a **real edit to real source**.
3. `graphi sync` — the incremental pass.
4. `graphi rebuild` into a **fresh** store that has never seen the pre-edit state.
5. **The assertion:** `bytes.Equal` over the two portable snapshot envelopes.

The graphi **binary is driven as a subprocess** throughout. The harness never calls ingest
in-process, so it cannot perturb the instrument even in principle; `TestNoIngestInProcess`
asserts that over both the normal **and** the `-test` dependency sets.

**Snapshot bytes assert. `graphi compare` only diagnoses.** The `BranchDiffReport` is captured
on a FAIL to explain it and never decides a row — it is a Labs surface, and a §12.3 gate must
not depend on a Labs analyzer's `BranchDiffSchemaVersion`. A diff showing no deltas while
snapshot bytes differ would itself be a finding; that combination did not occur in this run.

**Three rows are lifecycle events, not content edits, and are decided differently** — see §6.
The assertion is the same (snapshot bytes against a fresh full index); what varies is the
journey that produces the incremental side, and each of those rows publishes **every repetition
of its journey** rather than one execution.

Byte parity over the envelope is **strictly stronger** than FR-7 `:832`'s enumerated field
comparison: `model.Graph.Marshal` emits ids, kinds, qualified names, source anchors, meta,
confidence tiers, confidence, reasons and evidence, canonically sorted. The field-by-field walk
is therefore deliberately not re-implemented.

## 3. Results

| Class | Verdict | Repository | Signature |
|---|---|---|---|
| `add_file` | **PASS** | cobra | |
| `modify_file` | **PASS** | cobra | |
| `delete_file` | **FAIL** | cobra | inc 895/3784 vs full 897/3789 — **PARITY-001** |
| `rename_symbol` | **PASS** | cobra | |
| `move_symbol` | **PASS** | cobra | |
| `rename_package` | **PASS** | cobra | |
| `add_call` | **PASS** | cobra | |
| `remove_call` | **PASS** | cobra | |
| `change_interface` | **PASS** | lo | |
| `add_implementation` | **PASS** | lo | |
| `remove_implementation` | **FAIL** | gin | inc 1889/6602 vs full 1889/6599 — **PARITY-002** |
| `branch_switch` | **PASS** | cobra | 3/3 repetitions identical — §6 |
| `change_build_tag` | **FAIL** | gin | inc 1890/6605 vs full 1890/6602 — **PARITY-002** |
| `replace_generated_file` | **FAIL** | grpc-go | full 14898/69772 (stable); inc 14898/**69939 (run-a)**, **69940 (run-b)** — **PARITY-002, and the incremental edge count is NON-DETERMINISTIC: see §5** |
| `change_external_import` | **PASS** | cobra | |
| `interrupted_full_pass` | **PASS** | cobra | crash condition, not a change class; 6/6 repetitions identical across ADR **K1** and **K3** — §6 |
| `restart_and_recovery` | **PASS** | cobra | crash condition, not a change class; 6/6 repetitions identical across ADR **K5→K7** and **K6→K7** — §6 |

**Four failing rows, two defects — and every one of them is a change class.** The three
PARITY-002 rows are one defect surfacing three times, not three defects — see §5. **All three
lifecycle rows PASS**, reproducibly, and §6 states exactly what that does and does not prove.

### The two §12.3 store-level counts

**Orphaned external nodes = 0** and **stale linker edges = 0** on every executed row, **on both
the rebuild side and the incremental side** — counted from the same envelopes the assertion
compared, not inferred from the fixture-level proofs at
`engine/ingest/link_external_lifecycle_e2e_test.go:29` and `link_cascade_test.go:118`.

**Both sides are counted, and each figure is labelled with the side it describes.** That is a
correction: the first cut of this harness passed only the rebuild graph to the counter and
decoded the incremental graph without using it, so these counts were undisclosed-ly a statement
about one of the two graphs a row compares — and the incremental side is the one a parity
defect actually lands on. Every side of every executed row reads 0/0, before and after. The gap
was coverage and disclosure, not a wrong count.

**Republishing did, however, move one figure — `replace_generated_file`'s `inc_edges` — and
that was not the correction's doing.** It exposed that the number varies between runs. See §5.

**Read that with §5's scope limit.** A stale linker edge here means an edge whose endpoint is
**not a node in the graph**. PARITY-002's extra edges have valid endpoints on both sides, so
they are invisible to this counter. Zero means *no dangling endpoints*; it does not mean *no
edges a full pass would have recomputed away*.

## 4. Repository selection — which class ran where, and why

AC-6 exists so no reader assumes a class was exercised on a repository it never touched.

- **Manifest-pinned.** `change_build_tag` → **gin** and `replace_generated_file` → **grpc-go**,
  taken from `corpus/manifest.json`'s stratification block. These two never roam: if the pinned
  repository is out of tier the class SKIPS rather than being re-pointed, because a substituted
  repository would make the row a claim about a property it was never selected for. grpc-go is
  also the manifest's declared multiple-go.mod repository, so that row exercises a multi-module
  tree as well (sub-modules are excluded from the edit model — only the root module is touched).
- **Smallest exhibiting.** Every other class walks the Go repositories in **(tier, measured
  go-file count)** order and takes the first whose **real source** a planner can find a target
  in. Tier leads so cobra — the only tier-1 real repository — is preferred, which is also what
  keeps the local `-max-tier 1` run meaningful.

**A finding from that walk, recorded because it contradicts a reasonable assumption:**
**cobra v1.8.0 declares no named interface types in non-test source at all** — only anonymous
`interface{}`. The three interface-shaped classes therefore cannot run on it and land on
**lo** (17 Go files, the smallest tier-3 Go repository) and **gin**. Selection is decided
against the clone, not against a repository's reputation, which is why this surfaced as a
different row assignment rather than as a vacuous pass.

## 5. The two defects — published, filed, **not fixed**

Fixing a parity defect is a product-byte change: it moves the candidate and violates the
owner's ruling that one v0.7.2 batches every correction after the F4 residual and the freshness
diagnosis are measured (Delta PRD §6.2). **In slice: find it, publish the FAIL, file it.**

### PARITY-001 — known, now confirmed on real source

Deleting a file that declares a symbol another package calls through an intra-module import
leaves `graphi sync` **permanently** diverged from `graphi rebuild`.

Found hermetically by SW-157 and already scheduled as **v0.7.2 batch item 3**. This run
confirms it on real source: deleting cobra's `cobra.go` (which declares `WriteStringAndCheck`,
called by the in-module `doc` package) gives **incremental 895/3784 against full 897/3789**.
Full mints two interned external nodes and the five edges pointing at them; one incremental
apply mints neither — exactly what the recorded phase-ordering cause predicts
(`engine/ingest/ingest.go:709` runs `linkFiles` before the deleted-path purge at `:721-736`,
and `engine/ingest/linkfiles.go:64-71` indexes the **live** store).

The blast radius is confirmed as an ordinary refactor shape on a real 36-file library, not a
fixture artifact. **Note for whoever fixes it:** SW-157's review established that the naive fix
— purging before `linkFiles` — **breaks SQLite** with `edge references unknown node`.

### PARITY-002 — new, found by this matrix

**Whenever `graphi sync` re-links a file, the file→file `imports` edge set can settle
differently from what `graphi rebuild` produces over the identical tree.** Filed in
`projects/graphi/backlog.md`.

Node counts are identical on both sides in every instance; **every diverging edge is
`kind: "imports"`**, and no other edge kind and no node ever differs.

**It is bidirectional, not purely additive.** On gin the incremental graph carries **4** edges
the rebuild does not **and the rebuild carries 1 the incremental does not** — net +3, but a
one-way "incremental adds edges" reading is wrong and would send a fixer looking only for a
missing sweep. On grpc-go the net is **+167 in run-a and +168 in run-b** — and that difference
is not rounding, it is the defect's second and worse property.

### PARITY-002's divergence is NON-DETERMINISTIC — observed, not explained

The gin rows reproduce to the byte. **The grpc-go row does not.** Over the identical pinned
tree and the identical binary:

| Execution | `full_edges` | `inc_edges` |
|---|---|---|
| pre-correction run-a | 69772 | 69940 |
| pre-correction run-b | 69772 | 69940 |
| published **run-a** | 69772 | **69939** |
| published **run-b** | 69772 | **69940** |
| review re-run 1 | 69772 | **69902** |
| review re-run 2 | 69772 | 69939 |

**Six executions, three distinct incremental snapshots, a spread of at least 38 edges. The full
side is 69772 every single time.** So `graphi rebuild` is reproducible here and `graphi sync`
is not, which makes "+168" one sample of a varying quantity rather than the magnitude of the
defect. **Every figure for this row is therefore attributed to the run that produced it**, and
no single number should be quoted as *the* size of the divergence.

**This is an observation with its sample, not a characterised mechanism.** Nothing here
establishes *why* the incremental count varies; that belongs to whoever fixes PARITY-002, and
it is recorded in `projects/graphi/backlog.md` as evidence rather than repaired here. It is
consistent with the under-determined representative-file target described above — if the
target set is not unique, which representative is chosen may depend on ordering that is not
pinned — but consistency is not proof and this record does not claim it.

**It is disclosed here because this record's own standard demands it.** §8 says a row that
differs between two otherwise-identical dispatches is an environment finding **to be
explained**, never a flake to be retried away. This row differs, and `-verdict-diff` cannot see
it: that gate compares **verdicts**, and all six executions agree the row FAILs. Deliberately
so — but it means verdict agreement is not evidence of a reproducible measurement, and the
counts beneath a verdict need reading per run.

**It is one defect, not three, and this is the load-bearing evidence.** The three gin rows are
three different files and three different edit shapes — appending a function to `auth.go`,
deleting method `Context.Status` in `context.go`, editing a `//go:build` comment in
`context_appengine.go` — yet they produce a **byte-identical `BranchDiffReport`**, down to the
same five edge ids. One identical delta from three unrelated edits means the divergence belongs
to the repository plus an incremental re-link, not to any change class. The matrix reports it
as three rows only because the matrix is organised by class.

**The trigger is bounded tightly by two controls, and it is narrower than "modification".**

- A **comment-only** edit triggers it. So it is independent of edit *content*, not merely of
  change class — nothing about the symbol graph needs to change.
- A **no-op `sync`** converges. So it is not "running `sync`" either.

The trigger is therefore precisely **`sync` re-linking any file at all**. `add_file` on gin
converges, and cobra converges throughout.

**"Package depth" is a correlate, not the discriminator** — an earlier version of this record
said otherwise and was wrong. What distinguishes the affected repositories is that they contain
package directories whose **import target is under-determined**:

- `imports` edges are **file→file**, emitted at `engine/link/resolve_go.go:193` from
  `idx.packageFileNodes(imp.Path)` — the set of file nodes in the imported directory — at
  `classSelector`, i.e. the **`heuristic`** tier (`engine/link/link.go` `tierFor`). The code's
  own comment concedes the shape: it links "to the directory's file node **when uniquely
  determinable**".
- gin's `internal/json` holds **four mutually-exclusive build-tag variants all declaring
  `package json`**, so "the" target file is not unique. graphi evaluates no build constraints,
  so all four are indexed.
- gin's `internal/bytesconv` puts a **`_test.go` file up as an import target on both sides**.

**The linker does not pick a representative — it fans out over the whole set.**
`resolve_go.go:188-193` loops `for _, targetFile := range idx.packageFileNodes(imp.Path)` and
emits **one `imports` edge per file node in the imported directory** (skipping only the
importing file itself). So what varies between a cold pass and a re-link is not *which* file was
chosen but *which file nodes were in the index when the loop ran* — and therefore **how many
edges the loop emitted**. That is why the divergence is counted in edges rather than in
retargeted endpoints, and why it is bidirectional. (The set semantics are stated correctly at
the first bullet above; this paragraph previously said "which representative the linker lands
on", which understated the precision of the mechanism rather than overclaiming it. Corrected in
SW-158, from SW-144 review round 1, re-raised round 3.) **Neither side is absolutely right** —
but `rebuild` is the reference by definition, so `sync` is what diverges.

**It is not PARITY-001:** different edge kind, different trigger (re-link vs deletion), and
bidirectional rather than one-way.

**This is a characterisation, not a fix, and not a completed root-cause analysis.** SW-144's
scope was to prove the divergence, publish it and file it. PARITY-001's first stated cause was
wrong in two ways and had to be corrected in review, which is why this record states what was
measured and stops there.

**Independently reproduced without this harness.** SW-144's review cloned gin v1.9.1, drove
only the built binary and edited by hand — no `internal/parity` in the loop — and reproduced
the same five edge ids. The review also ruled out the harness's false-positive mode
structurally rather than by inspection: `internal/parity/run.go:248-270` applies the mutation
**once** and indexes **the same on-disk tree** on both sides, so a malformed planner would yield
a malformed tree that both passes see identically and would not, by itself, produce a
divergence.

### One near-miss, recorded because false findings are expensive

The first cut of the `rename_package` planner rewrote only non-test files, leaving cobra's
`doc/man_examples_test.go` declaring `package doc_test` and importing the **old** path. That
incomplete rename diverged — 938/3912 against 941/3916, full minting three external nodes plus
four edges — with PARITY-001's exact signature. **It was not a product defect.** It was the
harness manufacturing dangling references and then measuring them. Completing the rename made
the row converge, and it now PASSES. A divergence caused by the harness's own edit is the most
expensive kind of false positive an evidence gate can publish.

## 6. The three lifecycle rows — SW-158

Three FR-7 / Delta §9 requirements are **lifecycle events rather than content edits**, so none
of them is decidable by the change-class machinery: FR-7 `:824`'s `Branch-Wechsel`, and Delta
§9's `interrupted full pass` and `restart and recovery` (`:1068-1069`). They are driven by
`internal/parity/lifecycle.go`.

**What they are the complement of, and what they deliberately do not redo.**
`engine/ingest/faultmatrix_test.go` (SW-118) already kills the pipeline at every cross-DB
boundary and proves convergence to a never-crashed store's snapshot bytes, and
`docs/adr/0004-ingest-recovery-disposition.md:32-41` dispositions kill points K1–K8 on that
evidence. **That layer is settled and is not re-implemented here.** What the ADR does *not*
claim is real-process, real-repository coverage — it reserves it, at `:92-94`: *"ING-REWRITE
stays untriggered unless the EVAL-02 real-repo gates surface resource/recovery failures the
synthetic matrix cannot."* These rows are that reserved complement. **The crash is a real
`SIGKILL` to a real subprocess**, never an injected in-process fault, and each row cites the
ADR's own kill points rather than inventing parallel vocabulary.

### How the signal is aimed, and how we know where it landed

The harness may not add a product hook to make a kill easier — that would be a product-byte
change. The lever is the one the project's standards already require to exist (*"a slow index
must stay observable and interruptible"*): the binary emits `ingest.ProgressEvent` on its own
stream, and the harness kills the moment the pass announces the phase it is waiting for.

The mapping is read off `engine/ingest/ingest.go`'s emission order, so it is exact rather than
approximate — and it is **corroborated independently** by reading the crashed store *before
anything recovers it*:

| Kill point | Marker | ADR | What the crashed store held (cobra) |
|---|---|---|---|
| `parse` (full pass) | first parse milestone; the parse loop completes before the first `BeginBatch` at `:144` | **K1** — before any graph batch | **0 nodes / 0 edges** — nothing committed |
| `resolve` (full pass) | `PhaseResolve` at `:246`, after the WRITE commit `:200` and the LINK commit `:240` | **K3** — after the LINK batch commit, before TYPERESOLVE | **938 / 3555** — WRITE+LINK durable, the 361 typeresolve edges absent |
| `parse` (incremental) | `PhaseParse` at `:563`; the phase-1 dirty-mark tx committed at `:531-546`, `BeginBatch` still ahead at `:581` | **K5 → K7** | **938 / 3916** — the baseline, no graph write yet |
| `link` (incremental) | `PhaseLink` at `:699`, after the durable graph commit at `:665`, inside the still-open meta tx | **K6 → K7** | **939 / 3917** — the graph already **ahead** of the rolled-back meta state |

Those four store shapes are the evidence that the signal landed where the row says it did. The
progress stream says where the kill was *aimed*; the crashed store says what had actually
committed when it arrived, and they agree in every repetition of both dispatches.

**K2 is NOT claimed by these rows, and that is stated rather than glossed.** K2 is the window
between the WRITE batch commit (`:200`) and the LINK batch commit (`:240`). `IngestAll`
announces `PhaseLink` at `:186` — *before* the WRITE batch commits — and emits nothing further
until `PhaseResolve` at `:246`, well past the LINK commit. **No observable marker separates
those two commits from outside the process.** A signal aimed at `PhaseLink` would land somewhere
in the K1–K2 window, so publishing it as "K2" would be a probabilistic claim dressed as a
precise one. K2's coverage remains the synthetic `kill-before-batch-2` subtest, and this record
says so instead of quietly counting it.

### The restart row crosses a real process boundary, and the ingest lock proves it

`restart_and_recovery` is the **K7** seam — *"any crashed incremental followed by a session
open"*, the kill point that had **zero production callers** before SW-118 wired
`RecoverWithRoot` into `warmOrFullIngest`. `graphi sync` goes through `SyncRepo` →
`warmOrFullIngest`, which recovers *before* it trusts the store.

Every repetition probes the **real cross-process ingest lock** (`internal/ingestlock`,
`meta/ingest.lock.db` — the same package `internal/doctor/indexcheck.go:44` and
`cmd/graphi/status.go:167` probe with) from outside, twice: **`held` while the subprocess is
mid-pass, `free` after `SIGKILL` destroys it.** That is what makes this one journey across a
process boundary rather than two sequential invocations sharing a directory: the lock is OS
file-locking state that dies with the process, while the durable dirty rows survive it on
purpose. `held → free` reads in **12 of 12** killed repetitions per dispatch.

### The branch-switch row asserts the graph, not the announcement

`cmd/graphi/sync_test.go:33 TestRunSync_LifecycleAndBranchSwitch` rewrites `.git/HEAD` and
asserts **one stdout line** — `printBranchSwitch`'s announcement at `cmd/graphi/sync.go:165-169`.
No file content changes with that switch, so no graph delta exists and no full-vs-incremental
comparison is attempted. **This row changes the working tree for real**: it indexes at ref A,
runs `git checkout` to ref B, drives the **shipped verb** `graphi sync`, and asserts snapshot
bytes against a fresh full index at B. The announcement is still captured — as a *diagnostic* —
so the two claims are visibly not the same one.

**Both refs are recorded, and neither is invented.** Ref B is the manifest pin
`v1.8.0 @ a0a6ae020bb3899ff0276067863e50523f897370` (*"Improve API to get flag completion
function (#2063)"*); ref A is `890302a35f578311404a462b3cdd404f34db3720` (*"Support usage as
plugin for tools like kubectl (#2018)"*), selected by a deterministic rule — **the nearest
ancestor of the pin whose diff to the pin touches at least one `.go` file**. Local branch names
are created *at* those two existing upstream commits; **nothing is committed into the clone**,
because inventing history would make the row unreproducible.

### Results, as the whole sample

Every lifecycle row publishes **each repetition**, not a summary. That is not ceremony: §5 of
this record already established that a stable *verdict* can sit on top of a *varying
measurement*, so one green execution is not evidence of convergence.

| Row | Kill points × repetitions | Verdict | Distinct incremental snapshots | Distinct full snapshots |
|---|---|---|---|---|
| `branch_switch` | — × 3 | **PASS** | 1 | 1 |
| `interrupted_full_pass` | K1, K3 × 3 each = 6 | **PASS** | 1 | 1 |
| `restart_and_recovery` | K5→K7, K6→K7 × 3 each = 6 | **PASS** | 1 | 1 |

**30 lifecycle journeys across the two dispatches, and every one converged to the byte.** Both
dispatches produced identical digests for both sides of all three rows. **A row FAILs if any
single repetition diverges** — there is no majority rule and no retry.

### A control separates recovery from PARITY-002

`restart_and_recovery` must drive an incremental pass, and §5 filed PARITY-002: `graphi sync`
re-linking any file can settle a different `imports` edge set from `rebuild`. A divergence here
would therefore have been ambiguous between a recovery defect and that already-filed one. So
every repetition also runs **the identical journey with no kill** — baseline, same edit, one
uninterrupted `sync` — and compares. The crashed-and-recovered graph is **byte-identical to the
uninterrupted control** (`f8ffcf0dd1cb0932…`) in every repetition, so recovery is transparent on
this row. The control **diagnoses and never decides**; the verdict is always the snapshot bytes
against the fresh full index.

### Coverage limits — stated, because a limit that is not published is not a limit

1. **K2 has no real-process coverage here** (above). It remains synthetically covered.
2. **The lifecycle rows run on cobra, at every tier cap.** The selection rule is the smallest
   in-cap repository, because a lifecycle row tests the *process* and has no source structure to
   go looking for — which is also what keeps `-max-tier 1` meaningful for AC-11. **The cost is
   that these rows do not exercise PARITY-002's re-link divergence**, which §5 observed on gin
   and grpc-go and explicitly *not* on cobra. Their PASS is a statement about the lifecycle
   journeys, not a counter-example to PARITY-002.
3. **On a platform with no faithful `SIGKILL`, the two signal rows do not run.** They are then
   recorded as `SKIPPED` **with the platform and reason in `coverage_limits`**, and the run is
   `INCOMPLETE` and **refuses to publish** — disclosure costs what it should, so "record the
   limit" cannot become the cheap way past the gate. This dispatch ran on `darwin/arm64`, where
   both rows executed; `coverage_limits` is empty in both reports.
4. **The rows prove the observable property, not a named internal mechanism.** They prove that a
   real crash followed by a real restart converges to a fresh full index's bytes. They do not
   isolate *which* internal path did the healing — `cmd/graphi/zeroconfig_recovery_test.go:52`
   is the test that constructs the specific K7 divergence the drift pass cannot see.

### What this means for ADR 0004's `ING-REWRITE` trigger

ADR 0004's stopping rule says `ING-REWRITE` *"stays untriggered unless the EVAL-02 real-repo
gates surface resource/recovery failures the synthetic matrix cannot."* **These rows surfaced
none.** That is recorded here as evidence bearing on the trigger, and **nothing is acted on**:
extending or amending ADR 0004 is a separate, deliberate act, not a side effect of a green row.

## 7. What this does **not** compare

A "100% parity" line with no stated scope is an overclaim. The comparison unit is the portable
snapshot envelope, so anything persisted **outside** `model.Graph` is invisible to it:

- intra-process taint findings
- embeddings / vectors
- `index.profile` metadata
- the ingest-meta sidecar
- the FTS index — deliberately not stored, re-derived on load
  (`core/graphstore/snapshot.go:49-51`)

That state is **already dispositioned DOCUMENTED HARMLESS (Labs tier)** at kill point K4,
`docs/adr/0004-ingest-recovery-disposition.md:37`. This record cites that disposition and does
not reopen it: extending the envelope would bump `SnapshotFormatVersion`, a product-byte change.

Two further scope limits, stated so they are not discovered later:

- **`change_build_tag` is degenerate.** No build-constraint evaluation exists anywhere in
  graphi, so a `//go:build` edit is a comment-line content change to it. The row proves parity
  over the change and **nothing** about build-tag semantics.
- **`stale linker edges = 0` counts dangling endpoints only** — see §3.

## 8. Provenance

**Product source byte-identical to v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`.**

The run did **not** happen *at* the candidate, and no sentence here says it did: the harness
does not exist at `80d67ed`. Both SHAs are recorded in every report.

The claim is verified **mechanically, by the built binary**, not by a path diff:
`go build -trimpath -buildvcs=false ./cmd/graphi` is built at the run SHA and at the candidate
(materialized with `git worktree`) and the two sha256 digests compared. `-trimpath` is what
makes that comparison meaningful across two build directories — without it the absolute source
path lands in DWARF `comp_dir` and two identical trees hash differently. Both reports carry
`product_binary_head` and `product_binary_candidate`.

This matters on this branch specifically: the path diff against `80d67ed` is **not** empty,
because the SW-157 parent commit edited `engine/conformance/doc.go`. `engine/conformance` is a
test-only package that `cmd/graphi` does not link, so the **product binary is unchanged** — and
the binary comparison says so, where the path diff alone would have refused publication for a
doc comment.

**Publication fails closed** on a dirty worktree, a differing product binary (or an
unverifiable one), a missing runner class, a manifest pin mismatch, or an incomplete run. Each
refusal is recorded in `not_publishable_because` with its reason.

**Two dispatches with identical verdict sets** are required, applying §12.4's two-green-runs
discipline to a reliability gate. Compare with
`go run ./cmd/parity -verdict-diff run-a.json,run-b.json`. The comparison is over **verdicts**,
not report bytes: two dispatches legitimately differ in timestamps and durations. **A row that
differs between two otherwise-identical dispatches is an environment finding to be explained,
never a flake to be retried away** — and **§5 is where that obligation is discharged**:
`replace_generated_file`'s incremental edge count differs between run-a and run-b, and §5
records the whole sample rather than reconciling it. The cross-reference runs both ways so
neither half can be read without the other.

## 9. Reproducing it

```bash
# The whole matrix (clones cobra, lo, uuid, gin, grpc-go).
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 \
  -runner-class ubuntu-latest -report parity-report.json

# Cheapest path: cobra only. Completes on a developer machine.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -report parity-local.json

# One row, for iteration.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 3 -classes delete_file

# The three lifecycle rows, each locally runnable on the cheapest tier (cobra).
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes branch_switch
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes interrupted_full_pass
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 -classes restart_and_recovery

# How many times each lifecycle journey runs per kill point (default 3). This can
# only ever ADD executions to the sample: a row FAILs if ANY repetition diverged,
# so it can never retry a row into green.
go run ./cmd/parity -manifest corpus/manifest.json -max-tier 1 \
  -classes restart_and_recovery -lifecycle-repeat 5

# Two dispatches must agree before anything is published.
go run ./cmd/parity -verdict-diff run-a.json,run-b.json
```

The workflow needed **no change** for the lifecycle rows: they are declared rows of the same
matrix, so a plain dispatch runs them.

Tier 4 (kubernetes, ~3 min index at ~9 GB peak RSS) is **excluded by construction**, not by
configuration: `internal/parity.MaxSupportedTier` clamps the cap at 3, so no flag value,
environment variable or workflow input can pull it in. It is SW-145's subject and needs a named
machine.

The workflow (`.github/workflows/parity.yml`) runs on `workflow_dispatch` and a nightly
schedule, **never on a pull request**. The hermetic `internal/parity` tests carry the harness
logic into the PR gate via `go run ./cmd/testgate`; the real-repo matrix is the evidence. A
hermetic test that clones is not hermetic, and a matrix row that runs on a fixture is not
evidence.
