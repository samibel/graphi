# G7 perf baselines for Java and Kotlin — first measurement (SW-177, W1.d)

**Measured revision:** `3b8d43f6bc0a264c74424ca209b6fbd2401c9a31` — the W0.f-5
(LINK-001 / ADR 0011) measurement candidate, which is what
`internal/parityreport.CandidateSHA` names at the time of writing.
**Runner class:** `local-sandbox` (**comparison**, not reference) ·
**Harness:** `p0-perf/1` · **Scorer:** `p0-aggregate/1`
**Machine:** Apple M2 Max, 12 logical cores, 64 GB, darwin/arm64, kernel 25.6.0,
APFS, go1.26.6 — captured per run into every `environment.json`.
**Pins:** guava `2214c636…` (java) · okio `8b870e8e…` (kotlin) ·
kotlinx.serialization `3efe324b…` (kotlin)
**Raw data:** [`run-a/`](run-a/), [`run-b/`](run-b/), [`run-c/`](run-c/),
[`platform-control/`](platform-control/), [`freshness-blocked/`](freshness-blocked/)

---

## 0. Read this before quoting any figure on this page

> **This is a COMPARISON-CLASS run. It freezes no budget and satisfies no PRD
> §12.2 gate.** That is not a disclaimer added by the author; it is the
> instrument's own verdict, printed on every invocation and recorded in every
> artifact:
>
> ```
> eval: NOTE - guava on runner class local-sandbox (comparison) is NOT the reference
>       scenario; these numbers are not reference evidence and freeze no budget
> eval:   gate cold_index_p50  UNKNOWN  this run is guava on runner class local-sandbox
>       (comparison), which is not the reference scenario; PRD §12.2 is scoped to the
>       reference scenario only
> ```
>
> `docs/eval/reference-scenario.json` declares exactly one reference class
> (`ubuntu-latest`, x64, 4 vCPU, 16 GB) and says of the comparison class: *"Its
> numbers are never reported as reference values, never satisfy a PRD 12.2 gate,
> and never freeze a budget."* This machine is that comparison class.

**Three revisions are in play and they are not interchangeable.** Every artifact
here records all three, so no reader has to take this paragraph on trust:

| | SHA | what it is | this run's relation to it |
|---|---|---|---|
| measured | `3b8d43f` | the W0.f-5 / ADR 0011 measurement candidate | **this is what was measured** — `environment.json` `measured_sha`, all 27 dispatch legs |
| release candidate | `80d67ed` | v0.7.1, the published/tagged/attested release the evidence index names | **NOT measured** — `candidate_match: false` in every artifact |
| branch HEAD | `df2f43b` | `claude/kotlin-java-canonical-ga-t3b8km` | **NOT measured**, and not publishable — see §11 |

---

## 1. The four perf suites: three measured, one structurally blocked

G7's threshold text is *"the four perf suites include L's corpus with raw run
artifacts checked in under `docs/eval/runs`"*. Measured against the harness:

| # | Suite | Java (guava) | Kotlin (okio, kotlinx.serialization) |
|---|---|---|---|
| 1 | cold index (`-cold-runs 10`) | measured — 30 cold processes | measured — 30 cold processes each |
| 2 | query latency (`-query-executions 1000`) | measured — 3503/dispatch × 3 | measured — 3503/dispatch × 3 |
| 3 | progress stalls | measured × 3 dispatches | measured × 3 dispatches |
| 4 | **freshness / incremental** | **CANNOT RUN** | **CANNOT RUN** |

Suite 4 is not "not done". It **cannot be run on a non-Go pin at all**, and that
is measured rather than reasoned — see §5. **3 of 4 is not 4**, so
`GA-LANG-java-G7` and `GA-LANG-kotlin-G7` stay **UNKNOWN**; rounding it up is the
failure mode this programme has already paid for.

---

## 2. Method, and why it is this method

- **Measured at the candidate, not at HEAD.** A detached worktree was created at
  `3b8d43f`, the harness was built from it, and every invocation ran with that
  worktree as its working directory. This mirrors the `eval-full.yml` jobs,
  which check out the candidate SHA before building the harness, and it is what
  makes `measured_sha` read `3b8d43f` rather than the branch tip.
- **`cmd/eval` is byte-identical between the candidate and branch HEAD** —
  `git diff 3b8d43f..HEAD -- cmd/eval` is empty. The instrument described here
  and the instrument this story's tests pin are the same instrument.
- **The measured tree was pinned at the OBJECT STORE, not at the worktree**
  (SW-175's discipline). `git status` is not proof of a matching tree — a
  smudge filter can make the worktree differ from the blob invisibly to it. Each
  tracked file was re-hashed from its worktree bytes with
  `git hash-object --no-filters` and compared to the id the tree records:
  **candidate `3b8d43f`: checked=1609 mismatched=0; control tree `80d67ed`:
  checked=1407 mismatched=0.**
- **Strictly sequential.** Two harness invocations running at once measure each
  other. Nothing was parallelised.
- **Three dispatches (`run-a`, `run-b`, `run-c`), each a separate process
  tree**, so the published spread is run-to-run and not merely within-run. The
  cold-index suite additionally runs **one process per cold run**, so its n=10 is
  ten processes, not ten iterations inside one.
- **Cold is weaker here than on the reference class, and the artifacts say so.**
  `cache_state: not_dropped` in every `environment.json`: `-drop-caches` is a
  Linux protocol and there is no equivalent imposed here. Cold on this machine
  means "a store that did not exist before", nothing stronger.

---

## 3. Cold index — 30 cold processes per repository

Per-dispatch p50, and the **min…max over all 30 processes** so the spread is
visible rather than summarised away.

| repo | lang | dispatch p50 (ms) | all-30 wall min…max (ms) | wall spread | nodes | edges | DB (MiB) |
|---|---|---|---|---|---|---|---|
| guava | java | 5963 / 5976 / 5977 | **5811 … 6200** | **1.07×** | 46 352 | 73 780 | 42.18 |
| kotlinx.serialization | kotlin | 1174 / 1168 / 1176 | **1159 … 1425** | **1.23×** | 6 369 | 8 906 | 5.43 |
| okio | kotlin | 606 / 609 / 608 | **598 … 631** | **1.05×** | 4 397 | 6 946 | 3.58 |

**Run-to-run agreement is good and the graph is identical.** Dispatch p50s agree
within **0.2 %** (guava), **0.7 %** (kotlinx) and **0.5 %** (okio); node, edge
and DB-byte counts are **bit-identical across all three dispatches** for every
repository, which is the determinism check that makes the wall-clock spread
readable as noise rather than as a changing workload.

**The dispatch medians are much tighter than the sample range, and only the range
is honest about a single measurement.** kotlinx's three medians agree to 0.7 %
while its 30 individual cold indexes span **1.23×** — one process took 1425 ms
against a 1159 ms floor. A single-run baseline for that pin could legitimately
have been anywhere in a 23 % band.

### 3.1 The number that most limits what a developer-machine baseline is worth

This campaign was run **twice**, in full, three hours apart, on the same machine
with the same binaries and the same pins (§10 says why). guava's cold-index
dispatch medians:

| campaign | guava cold p50 (ms), a / b / c |
|---|---|
| first (discarded, §10) | 6719 / 6389 / 6293 |
| second (**published here**) | 5963 / 5976 / 5977 |

**12.7 % between campaigns, against 0.2 % within one.** Nothing about the
product, the pin, the binary or the flags differed — only the ambient state of an
uncontrolled developer machine. This is the strongest single argument in this
document for why a budget may not be frozen from this class (§12), and it is a
measured argument rather than a policy one.

---

## 4. Query latency — and the operation the pool hides

≥1000 timed executions per class per dispatch (FR-8's floor), 3 dispatches.
`sufficient: true` on every class of every dispatch.

| repo | class | p50 (µs), a/b/c | p95 (µs), a/b/c |
|---|---|---|---|
| guava | structural | 59 / 56 / 60 | 111 / 134 / 131 |
| guava | search | 237 / 232 / 239 | 267 / 266 / 263 |
| guava | **agent_tools** | 96 / 94 / 100 | **369 286 / 371 728 / 371 496** |
| kotlinx.serialization | structural | 57 / 55 / 53 | 140 / 135 / 136 |
| kotlinx.serialization | search | 186 / 156 / 185 | 222 / 220 / 213 |
| kotlinx.serialization | **agent_tools** | 93 / 92 / 92 | **40 734 / 40 897 / 40 900** |
| okio | structural | 55 / 55 / 57 | 96 / 98 / 105 |
| okio | search | 504 / 502 / 505 | 537 / 527 / 546 |
| okio | **agent_tools** | 90 / 90 / 91 | **29 615 / 29 486 / 29 614** |

### 4.1 `agent_tools` is bimodal, and the pool percentile is an artifact of pool composition

The class pools four operations at 250 executions each. Three of them are
sub-millisecond; `agent_brief` is not. Decomposed — **measured, not inferred**,
because the harness publishes per-operation latency:

| repo | op | n per dispatch | min (µs), a/b/c | p50 (µs), a/b/c | max (µs) |
|---|---|---|---|---|---|
| guava | **agent_brief** | 250 | **358 301 / 360 370 / 359 273** | 365 562 / 367 630 / 367 465 | 425 518 |
| guava | change_risk | 250 | 54 | 64 | 2 507 |
| guava | explain_symbol | 250 | 91 | 115 | 2 634 |
| guava | related_files | 250 | 57 | 68 | 2 531 |
| kotlinx.serialization | **agent_brief** | 250 | 39 401 / 39 420 / 39 497 | 40 048 / 40 165 / 40 215 | 51 687 |
| okio | **agent_brief** | 250 | 28 674 / 28 697 / 28 705 | 29 174 / 29 070 / 29 138 | 44 012 |

**On guava, 0 of 750 `agent_brief` executions came in under 358 ms**, against
the PRD §12.2 `agent_context_p95` threshold of **500 ms**. The cheapest is
**0.72×** the gate and the most expensive **0.85×** of it. This is a p0-shaped
cost, not a tail — the same shape SW-153 established for `freshness_p95`, and
the same disqualification applies: nothing here would be improved by "shaving
the tail".

**Two things this does NOT say.** (1) It is not a verdict: every §12.2 gate on
this run reads UNKNOWN by runner class, and guava is not the reference scenario
in any case. (2) It is not a claim about the reference class — §9.3 says exactly
what the control does and does not license there.

---

## 5. The freshness suite cannot include a Java or Kotlin pin — measured

> **SUPERSEDED, 2026-08-22 (SW-191). EVALFRESH-001 IS CLOSED.** Everything in
> this section is a correct record of what SW-177 measured on 2026-08-19 and is
> kept unedited for that reason — the transcripts under `freshness-blocked/` are
> what it cites, and rewriting them would leave this page citing something that
> never said what it says. But **it no longer describes the harness.** A run
> today does NOT reproduce the refusals below. See §5.1 for what replaced them.

Running it is the evidence. On each of the three JVM pins:

```
$ eval -manifest corpus/manifest.json -full-run okio -incremental-changes 100 \
       -runner-class local-sandbox -reference-scenario docs/eval/reference-scenario.json

eval: FAIL - full run over okio: incremental: the index contains no modifiable
      Go source files to change
exit status: 1
```

All three pins fail identically, with exit status 1. Transcripts:
[`freshness-blocked/`](freshness-blocked/). **The Go control is beside them** —
the same binary, the same machine, the same flags, on `cobra`:

```
eval: incremental over cobra — 8/8 changes completed, 0 failed
```

Without that control the three refusals would be equally consistent with a
harness that simply does not work off CI. With it, this is language scope.

**Mechanism**, cited rather than asserted, all in `cmd/eval`:

1. `modifiableGoFile` (`incremental.go:468`) is the single filter every change
   class draws its targets through, and its first statement rejects any path not
   ending in `.go`. The three JVM pins contain no `.go` file, so the candidate
   set is empty and the run aborts before timing anything.
2. `goPackageClause` (`incremental.go:485`) supplies the package clause a newly
   ADDED sibling file must declare, and reads Go's `package <ident>` line
   literally. On Java it returns `com.google.common.collect;` — semicolon
   included — and on a Kotlin file that declares no package it returns `""`.
   **So widening the file filter alone would not be sufficient**, which is the
   part a fix story must not discover late.
3. The ADD class writes `graphi_eval_step%04d.go` (`changeseq.go:188`).

The published determinism claim already says it out loud: `changeSequenceMethod`
(`changeseq.go:43`) begins *"A fixed four-step cycle over the indexed **Go**
source files…"*, and that string is emitted verbatim into every freshness
artifact.

**Pinned executably** by `cmd/eval/freshnessjvm_characterization_test.go`, which
fails **with instructions** the moment the filter admits a JVM source file, so
the three places that would then be wrong are named at the point of change.

**Filed as `EVALFRESH-001`** on `projects/graphi/backlog.md`. **Not fixed here:**
a per-language change-class family is new measurement machinery — this story's
own test notes scope it to *"corpus wiring and budget derivation rather than new
measurement machinery"* — and it would mint a new sequence digest, making every
existing freshness artifact incomparable to every new one. It is **not a JVM
defect**: it blocks Python (SW-181), the TypeScript family (SW-182) and every
language in SW-184/185 identically.

### 5.1 What SW-191 changed (dated amendment, 2026-08-22)

`EVALFRESH-001` is closed. The per-language change-class family §5 declined to
build is built: `cmd/eval/sourcefamily.go` states, per language family, the
shape of its package clause (or that it **has** none), whether a directory needs
one before a sibling can be added there, the declaration an append introduces in
that language's syntax, and the name a newly added file carries. All three
mechanisms §5 names moved:

1. the file filter is family-driven, so `.java`, `.kt`, `.py`, `.ts`, `.tsx`,
   `.js` and the rest qualify — and, deliberately, `.json`/`.yaml`/`.md` do
   **not**, because there is no top-level declaration to append to them;
2. the package-clause reader is per family. The JVM reader strips Java's
   terminator and reads Kotlin's unterminated clause; Python and the TypeScript
   family read **nothing**, because they have nothing to read — and the
   directory gate now treats that as admissible instead of dropping the
   directory, which is what made `ky` and `express` abort even after the filter
   was widened;
3. the ADD class writes `GraphiEvalStepNNNN.java`, `graphi_eval_stepNNNN.kt`,
   `…​.py`, `…​.ts`, `…​.js` — the family's own name and extension — and the
   MODIFY class appends that family's own declaration.

`changeSequenceMethod` was rewritten to describe the sequence that now runs, and
**the sequence digest moved with it**, exactly as §5 predicted: freshness
artifacts produced before 2026-08-22 are not comparable to ones produced after.

**Re-measured, `-incremental-changes 100`, same machine class (darwin/arm64,
`local-sandbox`), 2026-08-22 — every pin exits 0:**

| pin | language | completed | classes | note |
|---|---|---|---|---|
| cobra | go | 100/100 | true | the control, unchanged |
| guava | java | 99/100 | false | 1 failure: the Java extractor yields no symbols for `guava-testlib/…/Helpers.java` |
| okio | kotlin | 73/100 | false | 27 failures: the Kotlin extractor stops at the first top-level `suspend fun` |
| kotlinx.serialization | kotlin | 97/100 | false | 3 failures, same Kotlin cutoff |
| flask | python | 100/100 | **true** | |
| ky | typescript | 98/100 | false | 2 failures: the TS extractor yields no symbols for a file with a type-predicate arrow function |
| express | javascript | 100/100 | false | |

`classes=false` on guava, kotlinx.serialization, ky and express is a property of
those **repositories**, not of the harness: none of their highest-degree symbols
has an inbound edge from another directory, so the `cross_package` class is left
visibly uncovered rather than quietly substituted. On okio the class **is**
available and fails, because its only qualifying target is the file the Kotlin
`suspend fun` cutoff blanks.

**The remaining shortfalls are parser-coverage gaps, not harness gaps**, and
they are reproducible in three lines each — for example
`fun a(): Int = 1; internal suspend fun collect(p: Int) { }; fun c(): Int = 3`
yields `a` and not `c`. They are recorded on the
`GA-LANG-{java,kotlin,typescript}-G7` rows in `docs/rc/evidence-index.yaml` and
are out of SW-191's scope.

**Those rows stay `UNKNOWN`.** Closing EVALFRESH-001 and EVALBUDGET-001 removes
two named blockers; it does not supply what G7's PASS still needs — a
reference-class (`ubuntu-latest`) dispatch at the current candidate, on a clean
tree, with a fresh evidence URI and sha per pin.

---

## 6. Progress stalls — the p95 is 0.002 s and the longest stall is 1.72 s

| repo | events | stall p50 (µs) | stall p95 (µs) | **stall max (µs)** |
|---|---|---|---|---|
| guava | 3 408 | 107 / 89 / 83 | 2 470 / 2 366 / 2 476 | **1 679 597 / 1 718 678 / 1 650 388** |
| kotlinx.serialization | 1 007 | 97 / 56 / 98 | 3 234 / 2 920 / 3 339 | 204 077 / 201 675 / 204 545 |
| okio | 422 | 106 / 99 / 153 | 3 323 / 3 671 / 3 228 | 128 115 / 129 311 / 126 644 |

**On guava the stall p95 is 0.0025 s and the longest single stall is 1.72 s —
about 690× the p95, and 0.86× the 2 s `progress_stall_p95` threshold.** The
percentile is not wrong; it is answering a different question from the one a
user asks, which is exactly the property this story was asked to check for. It
reproduces: all three dispatches land between 1.65 s and 1.72 s.

This is **published, not filed**: the pre-existing hidden-stall backlog item is
explicitly outside this story's scope, and a comparison-class figure is evidence
neither for nor against it. What is added is that the shape reproduces on a Java
pin, three times, with the max an order of magnitude closer to the gate than the
gated percentile suggests.

---

## 7. Peak RSS — the largest figure this run produced

`stable_peak_rss_mb` is `getrusage` MAXRSS after the full stable-operation
suite. Over all 30 cold processes per repo:

| repo | rss min…max (MB) | vs the 4 GB program-wide stop rule (PRD §17) |
|---|---|---|
| guava | **2 294 … 2 827** | 0.57 … 0.71× — **not triggered**, and the closest approach yet recorded for a non-stress pin |
| kotlinx.serialization | 1 035 … 1 331 | 0.26 … 0.33× |
| okio | 618 … 781 | 0.15 … 0.20× |

**Stated precisely.** The 2 GB `peak_rss` figure in `reference-scenario.json` is
a **gate scoped to the reference scenario (grpc-go on `ubuntu-latest`)**; guava
is neither, so guava at 2.8 GB **breaches no gate**. The 4 GB stop rule *does*
apply to every measured scenario, and it is **not triggered**. Both statements
are made rather than one, because collapsing them would either invent a breach
or hide a number.

**Worth noticing anyway:** okio indexes 4 397 nodes into a 3.6 MiB store and
still peaks at 618–781 MB — more resident memory than CI's published grpc-go
figure (624/670 MB) for a graph **3.4× smaller**. §9.2 measures how much of that
is the platform, and the answer is: not much.

---

## 8. The Kotlin grammar blob inside the whole-binary budget gate (AC-2)

This is the one half of G7 that is **not** platform-bound: the gate measures a
`linux/amd64 GOAMD64=v1 CGO_ENABLED=0` binary, and cross-compiling that from
darwin/arm64 still produces a linux/amd64 binary. The build contract is taken
from `internal/release.CanonicalBuildArgs` — `-trimpath -buildvcs=true -tags
<the 21 registered subset tags> -ldflags -X …version.Version=dev` — which is
what `internal/bench` actually invokes.

### 8.1 Three measurements, because "accounted for" needs all three

| # | what | measured |
|---|---|---|
| 1 | the `kotlin.bin` blob on disk (`gotreesitter@v0.20.2/grammars/grammar_blobs/`) | **337 236 B** — the `bench/lang-budget.md` figure, **verified** |
| 2 | shipped default binary at the candidate | **34 162 926 B** |
| 3 | same build, `grammar_subset_kotlin` removed | **33 777 508 B** |
| | **Kotlin's marginal cost in the shipped binary (2 − 3)** | **385 418 B** |

**The blob size is not the budget-relevant number, and the difference is 14 %.**
The tag links `kotlin_scanner.go`, the parser-result and registry code as well
as the blob, so Kotlin costs **385 418 B**, not 337 236 B. `bench/lang-budget.md`'s
per-language table lists blob sizes and therefore understates every language's
true cost by a similar margin; that is recorded in a dated amendment to that
file rather than by rewriting the table (D6).

### 8.2 The gate, stated rather than silently widened

`bench/bench-budget.yml` reads `binary_size_bytes: baseline 32 509 872,
budget 34 250 000`.

```
measured 34 162 926   ≤   budget 34 250 000        PASS
headroom     87 074 B  =  0.25 % of budget
Kotlin costs 385 418 B  =  4.4×  the remaining headroom
```

**The budget is met and is not moved by this story.** But the gate has 0.25 %
headroom left, and Kotlin alone costs 4.4× that: the language named in AC-2 is
comfortably *inside* the gate, while the gate itself is *not* comfortable. One
further grammar of Kotlin's size would breach it.

### 8.3 Growth attributed, not guessed

The +5.1 % over the recorded baseline could have been the toolchain. It is not —
measured by rebuilding the **same** older tree with the **newer** toolchain:

| tree | toolchain | bytes |
|---|---|---|
| `80d67ed` | go1.26.5 (its own `go` directive) | 32 741 344 |
| `80d67ed` | **go1.26.6** | 32 750 198 |
| `3b8d43f` | go1.26.6 | 34 162 926 |

- **toolchain 1.26.5 → 1.26.6, same code: +8 854 B (+0.03 %)** — negligible.
- **code `80d67ed` → `3b8d43f`, same toolchain: +1 412 728 B (+4.31 %)**.

So the growth is **code, not toolchain**, and that is a measurement rather than a
plausible story.

> **The one thing not verified here.** These are cross-builds; the gate runs
> natively on `ubuntu-latest`. Every **delta** above is same-method and therefore
> robust to any cross-vs-native offset. The **absolute** headroom figure
> (87 074 B) is only as good as the assumption that a cross-built linux/amd64
> binary is byte-identical to a natively built one, and **that assumption was not
> tested** — it needs one CI dispatch.

---

## 9. The platform control — measured, not asserted

Every published P0 baseline is `Linux-X64` CI; this run is darwin/arm64. Crossing
that boundary is only honest if the size of the boundary is itself measured, so
SW-167's method was used: **rebuild at the older candidate and re-drive locally**,
on the two Go pins where a published CI figure exists at that same revision.

### 9.1 Wall-clock: this machine is 1.5–2.9× faster than the reference class

`local@80d67ed` vs the published `CI@80d67ed` — **same revision and same
toolchain (go1.26.5 both sides, verified from `environment.json`)**, n=10 each.

| metric | repo | CI (run-a / run-b) | local | local ÷ CI |
|---|---|---|---|---|
| cold index p50 (ms) | cobra | 548 / 530 | 190 | 0.35 / 0.36 |
| cold index p50 (ms) | grpc-go | 19 830 / 16 939 | 11 107 | 0.56 / 0.66 |
| `agent_brief` p50 (µs) | cobra | 20 001 / 23 960 | 14 663 | 0.73 / 0.61 |
| `agent_brief` p50 (µs) | grpc-go | 468 243 / 597 418 | 380 878 | 0.81 / 0.64 |

### 9.2 Peak RSS goes the OTHER way: this machine uses 4–29 % MORE

| metric | repo | CI (run-a / run-b) | local | local ÷ CI |
|---|---|---|---|---|
| stable peak RSS p50 (MB) | cobra | 274 / 275 | 353 | **1.29 / 1.28** |
| stable peak RSS p50 (MB) | grpc-go | 624 / 670 | 697 | **1.12 / 1.04** |

**This matters for §7.** The platform inflates RSS here, so guava's 2.3–2.8 GB
is not an artifact of a generous machine — the reference class would, if
anything, read *lower*. It also means the two axes cannot be reasoned about
together: on this machine graphi is faster **and** hungrier.

### 9.3 What the control does NOT license

The measured `agent_brief` local÷CI ratio spans **0.61 … 0.81** over two Go
repositories. Applying that range to guava's local p50 of ~367 ms gives
**453 … 601 ms** on the reference class — a range that **straddles the 500 ms
gate and therefore settles nothing**. That arithmetic is written down so nobody
has to redo it, and it is labelled for what it is: **a projection from two Go
pins, not a measurement of guava.** No verdict is drawn from it. **The only thing
that settles it is a reference-class guava run**, which is §12's next action.

### 9.4 LINK-001's effect, with the toolchain isolated

The candidate legs use go1.26.6 and the `80d67ed` legs use go1.26.5 (each tree's
own `go` directive), so their difference would have been code **+** toolchain. A
third leg rebuilds the same `80d67ed` product code with go1.26.6, leaving code as
the only difference — grpc-go, all local, n=10 / n=250:

| tree | toolchain | cold p50 (ms) | RSS p50 (MB) | `agent_brief` p50 (µs) | edges |
|---|---|---|---|---|---|
| `80d67ed` | go1.26.5 | 11 107 | 697 | 380 878 | 99 736 |
| `80d67ed` | **go1.26.6** | 11 143 | 701 | 379 713 | 99 736 |
| `3b8d43f` | go1.26.6 | 11 017 | 675 | 319 771 | **82 942** |

- **toolchain alone** (rows 1→2, identical source): cold +0.3 %, RSS +0.6 %,
  `agent_brief` **−0.3 %** — nil, at this campaign's noise floor.
- **code alone** (rows 2→3, identical toolchain): edges **−16.8 %**,
  `agent_brief` **−15.8 %**, cold index −1.1 %, RSS −3.7 %.

**One attribution is claimed and one is explicitly refused.**

- **Claimed:** LINK-001's **−16.8 % edges** on grpc-go come with a **−15.8 %
  `agent_brief`** improvement, with the toolchain isolated and contributing
  −0.3 %. 16 % is far outside anything this campaign measured as noise.
- **Refused:** no cold-index attribution. The code effect there is −1.1 %, which
  is well inside the 12.7 % campaign-to-campaign band §3.1 measured on this
  machine. It is reported and **not** explained.

This also confirms the story's premise: a baseline taken before LINK-001 landed
would have been describing a tree that no longer ships.

---

## 10. What was measured, what was inferred, and why this campaign was run twice

**Measured:** every figure in §3, §4, §6, §7, §8 and §9; the freshness block and
its Go control (§5); the tree pinning (§2); the toolchain/code splits (§8.3, §9.4).

**Inferred, and labelled at the point of use:** exactly one thing — the
453…601 ms projection in §9.3, which is arithmetic over two Go pins and is not a
statement about guava.

**Not claimed anywhere:** any "reproduced across CPU families" statement. Every
figure on this page is one CPU model (`Apple M2 Max`), recorded in every
`environment.json`. AC-6 exists because the P0 line shipped that claim wrongly
four times; here there is nothing to check, and that is the honest form.

**No profiles were produced**, because no budget was missed: every §12.2 gate on
this run reads UNKNOWN by runner class, and the fail-closed budget path
(`-budgets`) was never engaged because it refuses this class outright (§12).
AC-5's antecedent did not fire. The two figures that *look* like misses — guava
`agent_brief` and guava peak RSS — are published in full in §4.1 and §7 rather
than quietly dropped.

> **Why this campaign was run twice, and why the first run was discarded rather
> than repaired.** The first campaign recorded its `-workdir` into every artifact
> (`store_path`, `meta_path`, `report_path`, `filesystem_path`), and that path
> embedded the maintainer's username. graphi's own pre-commit privacy guard
> blocked the commit — correctly; this is a public repository. The artifacts were
> **re-measured with a neutral working directory**, not rewritten: editing a
> checked-in raw sample so that a guard goes quiet is exactly the kind of silent
> correction the evidence rules exist to prevent, and it would also have left
> `run.json`'s digests describing files that no longer existed.
>
> The re-run is not free of information — it produced §3.1, the single most
> limiting number in this document. **Both campaigns' guava figures are stated
> there**, and the discarded campaign's are not hidden merely because its files
> were not checked in. The first campaign additionally had one `-aggregate`
> invocation overlapping a measured leg; the re-run has no such lapse.

---

## 11. What this run does and does not license

**On the candidate.** These are measurements of `3b8d43f`. They are **not**
measurements of the published release candidate `80d67ed` — `candidate_match`
reads `false` in every artifact and the harness prints the mismatch on every
invocation. No figure here should be attached to v0.7.1.

**On the branch.** `./cmd/graphi` built with `-trimpath -buildvcs=false` from
the candidate is `036be635…`; from branch HEAD it is `4f0e1a20…`, so
`ProductDiffEmpty` is `false` at HEAD and **nothing dispatched from this branch
is publishable**. That is an inherited, escalated owner decision which this story
neither caused nor is permitted to fix. Its consequence *here* is narrow and
worth stating exactly: the measurement was taken **at the candidate, in a clean
worktree**, so these numbers are honest about `3b8d43f` — but they cannot be
promoted into a published, publishable run until the candidate moves, which is
one of the reasons the G7 rows stay UNKNOWN.

**On the machine.** Comparison class. Freezes no budget, satisfies no gate. And
per §3.1, this class moves 12.7 % campaign to campaign with nothing changed.

---

## 12. Budgets: none was derived, and that is an escalation (AC-1)

AC-1 requires each budget to be **derived from a measurement, never estimated**.
On this machine that is not possible, and the refusal is the product's own:

- `docs/eval/hero-budgets.json` declares `runner_class: ubuntu-latest`.
- `evaluateFullRunBudgets` (`cmd/eval/budgets.go:101-103`) returns
  `runner class %q does not match budget runner %q` for any other class — the
  gate is fail-closed by design.
- `docs/eval/reference-scenario.json`: *"A measurement taken on the comparison
  class is never a reference value and never freezes a budget."*

Writing this run's numbers into that file would have stated a **reference-class
ceiling from comparison-class data** — a fabricated budget wearing a measured
budget's label, and §3.1 shows the data would have been 12.7 % arbitrary anyway.
**It was escalated, not complied with, and no number was invented.** Filed as
`EVALBUDGET-001`.

**Where the two languages actually stand.** `guava` **is** already in
`hero-budgets.json`'s `real_repos.selection`, under explicitly **historical,
non-ratcheting** ceilings from a retired harness, and the weekly `eval-full` job
already enforces them for it. **Kotlin has no entry at all** — neither `okio` nor
`kotlinx.serialization` appears — which is SW-180 divergence D-8, still open.

**What remains, precisely:** one `eval-full` dispatch on `ubuntu-latest` at the
current candidate over guava, okio and kotlinx.serialization, and then — and only
then — a budget edit. This document is the measurement that dispatch has to
reproduce.

---

## 13. Reproducing this run

```bash
# 1. the candidate, pinned at the OBJECT STORE rather than at the worktree
git worktree add --detach /tmp/cand 3b8d43f6bc0a264c74424ca209b6fbd2401c9a31
#    then re-hash every tracked file with `git hash-object --no-filters` and
#    compare against `git ls-files -s` (SW-177: 1609 checked, 0 mismatched)

# 2. the harness, built FROM the candidate
cd /tmp/cand && CGO_ENABLED=0 go build -o /tmp/eval ./cmd/eval

# 3. one leg — repeat per repo x per dispatch; NOTHING runs in parallel.
#    Keep -workdir and -out on a path that carries no private string: they are
#    recorded verbatim into every artifact.
/tmp/eval -manifest corpus/manifest.json -full-run guava -cold-runs 10 \
  -runner-class local-sandbox \
  -reference-scenario docs/eval/reference-scenario.json \
  -candidate docs/rc/evidence-index.yaml \
  -workdir /tmp/sw177-wd -export-raw <run-dir> -out /tmp/sw177-wd/out.json
#    query latency:   -query-executions 1000
#    progress stalls: neither flag (stalls are measured on every run)
#    freshness:       -incremental-changes 100   -> exits 1 on every JVM pin (§5)

# 4. the reproduction gate — exits 0 for all 37 leaf run directories here
/tmp/eval -aggregate <run-dir>

# 5. the binary-size half (§8), platform-independent by construction
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
  go build -trimpath -buildvcs=true -tags "$(the 21 subset tags)" \
  -ldflags "-X github.com/samibel/graphi/internal/version.Version=dev" \
  -o /tmp/graphi ./cmd/graphi
```
