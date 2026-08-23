# Decision: the P0 candidate must move — **Outcome B**, one correction candidate

**Status:** accepted · **Date:** 2026-07-29 · **Story:** SW-135 ·
**Spec:** P0 Diagnosis + Candidate Decision · **Risk:** high ·
**Sign-off:** PENDING (SW-135 is a build; the human ship gate signs off)

## CORRECTION — 2026-08-23 (SW-205): the Output path note cites a file that was never created

Per the never-rewrite discipline this block is **added**; nothing below it is
rewritten, re-pointed or deleted, and the decision this record makes is unchanged
and still stands.

**C-1 — `docs/decisions/2026-07-p0-candidate-correction-decision.md` does not
exist and never did.** The Output path note below quotes it as the name Delta PRD
§8 gave this artifact, and says in the same breath that this file carries the
ticket's path instead. That is accurate as prose, but the quoted name is written
as a path citation and reads, to anything mechanical, as a claim that the file is
there. It is not: `git log --all --diff-filter=A -- 'docs/decisions/2026-07-p0-candidate-correction-decision.md'`
returns nothing. **The artifact is this record**; the PRD's name for it was never
used and no such file should be looked for.

**Why this block, and what it costs.** SW-205's citation verifier resolves every
path a governed record cites, and it cannot tell a quoted NAME from a claim that a
file exists — a declared limit of its grammar, recorded in
`internal/evidence/citation.go`. This banner is the project's sanctioned response
to a false citation in a published record: state the truth on top, leave the body
alone. It also marks this P0-era record — superseded five candidate moves over —
as published-and-not-to-be-rewritten, so the verifier stops requiring its
citations to resolve and reports them in its exempted count instead.

**Decision.** The frozen P0 candidate **v0.7.0 at `5815db5`** cannot produce the evidence
PRD §12.2 gate 9 requires, and cannot be made to. **Outcome B is selected:** one bounded
correction candidate is authorised, per Delta PRD §6.2 and story SW-136.

**This record does not move the candidate.** It decides that the candidate *must* move.
`5815db5` remains the candidate of record until SW-136 publishes a tagged, attested
successor **and** writes its successor freeze record. Until that happens, nothing in
`docs/rc/evidence-index.yaml` changes — see [§7](#7-the-cost-of-this-decision-stated-before-it-is-paid).

> Output path note: Delta PRD §8 names this artifact
> `docs/decisions/2026-07-p0-candidate-correction-decision.md`. SW-135's AC-1 names
> `docs/decisions/2026-07-p0-candidate-decision.md`. The ticket is the binding
> instruction, so this file carries the ticket's path; the PRD's path is a naming
> difference and nothing else.

---

## 1. What this decision is made from

Exactly two inputs, both in-repo and both citable:

| Input | Where |
|---|---|
| The proven, classified findings F1–F5 | [`docs/eval/p0/partial-outcome-diagnosis.md`](../eval/p0/partial-outcome-diagnosis.md) (SW-134) |
| The move bar | [Delta PRD §6.2](../plan/2026-07-graphi-p0-completion-delta-prd.md) and its Story SW-135 outcome conditions |

The published baseline (`docs/eval/runs/2026-07-28-ubuntu-latest/`) is the evidence both
of those rest on, and every number quoted below was re-read from it for this record
rather than copied from the diagnosis — see [§10](#10-verification).

### 1.1 The bar, verbatim

Delta PRD §6.2, *Principle: one bounded candidate correction cycle*:

> Do not repeatedly move the candidate after every small defect.
>
> A second candidate move is allowed only for:
>
> - a proven P0 blocking product defect,
> - a proven measurement-integrity defect,
> - a High/Critical security issue,
> - a failure that makes required evidence impossible to produce.
>
> “Main moved on” is not a reason.

The same section fixes the version and, decisively for this decision, the *scope* of what
counts as a candidate-changing correction:

> Recommended version if production **or measurement** code changes: `v0.7.1`

The criteria are referred to below as **C1** (product defect), **C2**
(measurement-integrity), **C3** (security), **C4** (required evidence impossible).

### 1.2 The Delta PRD's own conditions for each outcome

Story SW-135 of the Delta PRD states them as lists, and they are not symmetrical:
Outcome A is *allowed only when* all of its conditions hold; Outcome B is *required when*
any of its conditions holds.

| Outcome A — retain v0.7.0, allowed only when… | Holds? |
|---|---|
| the product behavior is correct | **YES** — F5; `partial` under truncation is designed, documented, GA-frozen (diagnosis §7) |
| no measurement-integrity code must change | **NO** — F1–F3 require `cmd/eval/querylatency.go:430-435` to change; F4 requires `engine/scenario/fixture.go:292,295,297` |
| the scenario can be corrected without invalidating the measurement contract | **NO** — F4's correction changes the item cap the agent-tools latencies were taken at, so pre- and post-correction figures are not comparable |
| all changes are documentation-only and do not alter the measured instrument | **NO** — no documentation change recovers a single one of the 25 rejected executions |

One failed condition would be enough. Three fail. **Outcome A is not available.**

| Outcome B — cut one correction candidate, required when… | Holds? |
|---|---|
| a Stable product defect must be fixed | NO |
| a measurement-integrity defect must be fixed | **YES** — [§2](#2-finding-by-finding-against-the-four-criteria-ac-2) |
| a required regression test changes the candidate harness | **YES** — SW-134 already added `cmd/eval/partialoutcome_characterization_test.go`, and the correction requires more |
| the existing candidate cannot produce valid P0 evidence | **YES** — [§3](#3-the-single-defect-that-forces-the-move-ac-3) |

Note the phrase *“a required regression test changes **the candidate harness**”*. The
Delta PRD speaks of the harness as belonging to the candidate. That is not a reading
imposed here; it is the document's own vocabulary, and it is consistent with §6.2 setting
`v0.7.1` for a change to **measurement** code.

---

## 2. Finding by finding, against the four criteria (AC-2)

Every row cites the diagnosis section that proves it. “Clears the bar” means: this
finding, **on its own**, satisfies at least one of C1–C4.

| # | Finding (diagnosis §1) | Class | C1 product | C2 measurement-integrity | C3 security | C4 evidence impossible | Clears the bar? |
|---|---|---|---|---|---|---|---|
| **F1** | `explain_symbol` returns `partial`; the harness refuses to count it — **16 of 250** executions lost (§2.3, §4.1) | P0-MEASUREMENT | no (§7) | **YES** | no | **YES** | **YES** |
| **F2** | `change_risk`, same rejection rule — **4 of 250** (§2.3, §3, §4.1) | P0-MEASUREMENT | no (§7) | **YES** | no | **YES** | **YES** |
| **F3** | `related_files`, same rejection rule on a wider allowed set — **5 of 250** (§2.3, §3, §4.1) | P0-MEASUREMENT | no (§7) | **YES** | no | **YES** | **YES** |
| **F4** | The harness measures at item cap **10**; every shipped surface defaults to **20** (§6) | P0-SCENARIO | no (§7) | **YES**, qualified | no | no (§10.2 — unproven) | **YES**, but see [§3.2](#32-why-f4-does-not-independently-force-this-move) |
| **F5** | No product defect is established (§7) | not a defect | no | no | no | no | **NO** |

### 2.1 Why F1–F3 are C2 — and not merely an undercount

“Fewer samples than asked for” would be a sampling shortfall. Three properties in the
diagnosis make this a defect in the *integrity* of the measurement:

1. **One pool, two incompatible countability rules.** `agent_brief` sits in the same
   FR-8 pool (`docs/eval/reference-scenario.json`, gate `agent_context_p95`, operations
   `agent_brief`/`explain_symbol`/`change_risk`/`related_files`) and declares
   `allowed: ["found","partial"]`; the other three declare `["found"]` (and
   `["found","empty"]` for `related_files`). One percentile is being reported over four
   operations whose observations were admitted under different rules
   (diagnosis §2.3, verified at `cmd/eval/querylatency.go:430-455`).
2. **The exclusion is systematic and biased toward the slow tail.** The rejected
   executions are precisely those whose answers were large enough to truncate — the ones
   with the most items to resolve, rank and assemble. The retained distribution is
   therefore missing its heaviest members by construction, not at random
   (diagnosis §11).
3. **The rejection is wrong on the harness's own terms.** The requirement string is
   *“resolved target with a valid found envelope”*, and the justification is *“a fast
   wrong answer is not a fast answer”* (`cmd/eval/querylatency.go:102-105`). The target
   *was* resolved and the envelope *was* valid; a `partial` is a correct answer that
   truthfully declares its own truncation (diagnosis §7). The instrument is discarding
   correct observations.

An instrument that admits correct observations under mutually inconsistent rules and
drops its slow tail non-randomly is not measuring what it reports. That is C2, and it is
proven, not argued: it is reproduced in both published runs, on two CPU families, and
pinned by `cmd/eval/partialoutcome_characterization_test.go`.

> **CORRECTION 2026-08-20 (SW-150, CPU attribution sweep) — added, nothing above is
> rewritten.** "Two CPU families" is a run-summary shorthand; the published
> `environment.json` files show FOUR CPU models across the 40 per-job records
> (`AMD EPYC 7763`, `AMD EPYC 9V74`, `Intel Xeon Platinum 8573C`,
> `Intel Xeon Platinum 8370C`; run-a spans 3 models, run-b spans 4). The per-job
> truth for the gate-bearing grpc-go jobs is at
> `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md:272-304`. The
> determinism claim above is therefore *strengthened* (reproduced across a 3-model
> and a 4-model run, not a 2-model one). The cross-silicon reproduction claim is
> *weakened*: the C2/C4 shortfall's host was the `progress-stalls/grpc-go` job,
> which ran `AMD EPYC 7763 → AMD EPYC 7763` — one model in both runs — so this
> sentence's "reproduced across CPU families" qualifier has no silicon backing for
> that gate. The artifact-property half of the claim stands on the per-job
> reproducibility inside each run (the symbol sample digest and the
> degree-ordered count), not on cross-run silicon diversity. Sweep record:
> `docs/eval/p0/sw-150-cpu-attribution-sweep.md`.

### 2.2 Why F1–F3 are also C4

At `5815db5` the agent-tools pool reaches **975** of FR-8's floor of **1000**
(`internal/evalreport/querylatency.go:40`, `QueryExecutionMinimum`), in both published
runs, on an Intel Xeon Platinum 8573C and an AMD EPYC 9V74. The shortfall is not noise:
the reject count is `|{s ∈ sample : items(op, s) > 10}|`, and every term of it — a
degree-ordered symbol sample published with digest `332036d65a7ec805`, a compile-time
cap of 10, and pure graph properties — is machine-independent (diagnosis §4.2). It
reproduces on 4 of the 5 pinned repos (`lo` 0, `uuid` 14, `gin` 23, `grpc-go` 25,
`cobra` 39; diagnosis §4.4).

> **CORRECTION 2026-08-20 (SW-150, CPU attribution sweep) — added, nothing above is
> rewritten.** "Intel Xeon Platinum 8573C and an AMD EPYC 9V74" is the run-summary
> shorthand, not the per-job truth. The published `environment.json` files show
> FOUR CPU models across the 40 per-job records (`AMD EPYC 7763`,
> `AMD EPYC 9V74`, `Intel Xeon Platinum 8573C`, `Intel Xeon Platinum 8370C`;
> run-a spans 3 models, run-b spans 4). The per-job truth for the
> `query-latency/grpc-go` job that produced the 25-execution shortfall is at
> `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md:284-287`:
> **AMD EPYC 9V74 in run-a, AMD EPYC 7763 in run-b — both AMD EPYC**. So the
> shortfall was reproduced across AMD generations, but **not** across Intel and
> AMD, and §2.2's "two CPU families" framing of the cross-silicon reading is
> weakened rather than supported by the per-job data. The per-run reproducibility
> and the machine-independent terms of the reject count (the symbol sample digest
> and the degree-ordered count) are untouched: the determinism claim is *stronger*
> (a 3-model run and a 4-model run, not a 2-model one) even as the cross-silicon
> framing is weaker. Sweep record:
> `docs/eval/p0/sw-150-cpu-attribution-sweep.md`.

Therefore: **re-running the baseline at `5815db5` any number of times produces 975 again.**
Gate 9 has no path to a verdict on this candidate. That is exactly *“a failure that makes
required evidence impossible to produce”*.

### 2.3 Why F4 is C2 but not C4

Every shipped surface resolves “caller passed no cap” to `shape.DefaultMaxItems = 20`
(`engine/agenttools/shape/shape.go:21`); the harness resolves it to **10**
(`engine/scenario/fixture.go:292,295,297`). The harness is therefore timing a
configuration **no default-configured user of the product ever runs**, and every answer
whose natural size lands in 11–20 items is reported `partial` to the instrument and
`found` to a user (diagnosis §6). A latency measured under a strictly smaller item cap is
systematically optimistic relative to the shipped default. That is a measurement-integrity
consequence, whatever the classification of its site — SW-134 classifies it P0-SCENARIO
because the *defect* is an invalid reference expectation; the class says where it lives,
C2 asks what it does.

It is **not** C4: the diagnosis explicitly declines to claim that raising the cap would
recover the 25 executions, because how many of the 250 sampled grpc-go symbols have 11–20
items versus more than 20 is not derivable from the published artifacts (§6, §10.2). F4
makes the eventual number *wrong*; F1–F3 make it *unobtainable*.

---

## 3. The single defect that forces the move (AC-3)

### 3.1 It is the countability rule, not one operation

> **D1 — the agent-tools countability rule at `cmd/eval/querylatency.go:430-435` omits
> `partial` from the `allowed` set of `explain_symbol`, `change_risk` and
> `related_files`, while the fourth member of the same FR-8 pool declares it countable.**

F1, F2 and F3 are three *manifestations* of D1, not three defects. The diagnosis proves
they share one producer (`engine/agenttools/shape/shape.go:171`, the only site that can
set `partial` for these operations, §2.1) and one rejection site (§2.3); they differ only
in what fills the item list (§3). And they are not separable: the 25 rejections split
**16 + 5 + 4**, so correcting any proper subset leaves the pool at 984, 995 or 996 of
1000 — still undersampled, still UNKNOWN (diagnosis §8: *“Whatever SW-135 decides has to
cover all three or none.”*).

D1 forces the move under **C4** (proven, deterministic, both runs, two CPU families) and
independently under **C2** ([§2.1](#21-why-f1f3-are-c2--and-not-merely-an-undercount)).
No other finding is needed to reach Outcome B, and the decision would be the same if
F4 and F5 did not exist.

> **CORRECTION 2026-08-20 (SW-150, CPU attribution sweep) — added, nothing above is
> rewritten.** "Both runs, two CPU families" is the run-summary shorthand; the
> per-job truth is four CPU models across the 40 jobs (see §2.2 and
> `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md:272-304`). The C4
> argument is *strengthened* (reproduced across a 3-model and a 4-model run), but
> the cross-silicon reading is *weakened* for the C2/C4 host: the
> `query-latency/grpc-go` job that produced the 25-execution shortfall ran
> `AMD EPYC 9V74 → AMD EPYC 7763` (both AMD EPYC), so the shortfall was never
> reproduced across Intel and AMD, only across AMD generations; and the
> `progress-stalls/grpc-go` job ran `AMD EPYC 7763 → AMD EPYC 7763` (one model in
> both runs), so this section's "reproduced across CPU families" qualifier has no
> silicon backing at all for the 15.5 s stall tail. The decision is unchanged:
> the per-job reproducibility inside each run, the determinism of the reject
> count, and the machine-independent terms of the C2/C4 argument are not touched
> by silicon diversity. §3.1 must not be read alone without this. Sweep record:
> `docs/eval/p0/sw-150-cpu-attribution-sweep.md`.

### 3.2 Why F4 does not independently force this move

F4 clears the bar under C2, and it will be corrected in the same cycle. It does not
*force this move*, for two reasons:

1. **It corrupts no published verdict.** The three affected operations feed exactly one
   PRD §12.2 gate — `agent_context_p95` — and that gate has **no verdict**: it reads
   UNKNOWN in both runs. `caller_callee_impact_p95` is a different pool
   (`callers`/`callees`/`impact`), and no other gate reads the agent-tools class
   (`docs/eval/reference-scenario.json`). There is no number in the published baseline
   that F4 makes wrong, because the only number it could have made wrong was never
   published as a verdict.
2. **A scenario correction is not by itself a candidate-moving event.** The Delta PRD's
   Outcome A explicitly contemplates *“the scenario can be corrected without invalidating
   the measurement contract”* — i.e. it treats scenario fixes as, in principle,
   retainable. Here that condition happens to fail, but it fails *because the correction
   is entangled with D1's*, not because a cap value is a candidate property.

**Stated honestly, because it cuts against the tidiness of the answer:** in a
counterfactual world where D1 did not exist, F4 would still have had to be corrected
before gate 9's number could be believed, and that correction is measurement code, so it
might well have forced a move on its own. That counterfactual is not this decision. In
*this* world D1 is proven at the higher standard (C4) and F4 is batched behind it under
§6.2's *“batch the minimal required corrections”* — deferring F4 to a later cycle would
mean re-running the whole baseline twice, which is the outcome §6.2 exists to prevent.

**F5 requires nothing.** It is a non-finding: it establishes that no product defect
explains the observed `partial` outcomes. It carries no correction and touches no code.

---

## 4. Why Outcome A was tested seriously, and why it fails

The bar is deliberately high, SW-131 already moved this candidate once, and the diagnosis
itself ends with *“no finding in this diagnosis requires a change to code that ships in
the candidate binary.”* The case for retention is real and was tested:

**The case for A.** No shipped byte changes. `engine/scenario` is imported by `cmd/eval`
and by nothing else — re-verified for this record across all seventeen `cmd/*` binaries
([§10](#10-verification)) — so editing the cap changes no byte of the shipped `graphi`
binary despite living under `engine/`. The product behaviour is correct (F5). A move is
expensive. Therefore: fix the harness, re-run, keep v0.7.0.

**Why it fails — mechanically, not rhetorically.** The harness cannot be corrected and
then used to measure `5815db5`, because the harness *is* the code under measurement's
own checkout:

```go
// cmd/eval/coldseries.go:159-163
series.CandidateMatch = !series.WorktreeDirty && strings.EqualFold(strings.TrimSuffix(measured, "+dirty"), candidateSHA)
if !series.CandidateMatch {
	series.Warnings = append(series.Warnings, fmt.Sprintf(
		"measured revision %s is not the frozen candidate %s (cited from %s): these numbers are not evidence about the candidate artifact",
```

Every corrected run is taken at a revision that is not `5815db5`, so `candidate_match` is
false and the harness itself stamps the output *“these numbers are not evidence about the
candidate artifact.”* The published baseline's central provenance claim is that
`candidate_match` is **true** in every `environment.json` of both runs. Retaining v0.7.0
while correcting the harness would produce evidence that the instrument declares is not
about v0.7.0. That is not a candidate retention; it is a candidate move with the
paperwork left undone.

This is the mirror image of the blocker that moved the candidate the first time
(`2026-07-p0-candidate-freeze-v070.md` §1: *“the frozen artifact is unmeasurable by
construction”*). Then the harness was absent at the candidate SHA; now the harness is
present but cannot produce a valid reading. Same class of blocker, same resolution.

**“It would be tidier” and “main moved on” played no part.** The reason is a proven,
deterministic inability to produce a required §12.2 measurement.

---

## 5. Gate 9 (`agent_context_p95`) under this decision (AC-5)

**On v0.7.0, gate 9's UNKNOWN is now final.** It is not a pending reading; it is a closed
fact about a candidate that cannot reach FR-8's floor. Nothing SW-136 does can make
`5815db5` resolve it.

**On the successor, gate 9 starts with no reading at all.** Before it can read anything
other than UNKNOWN, all of the following must be true:

1. **D1 is corrected** — the agent-tools pool measures all four operations under one
   countability rule — with the regression test written before the correction
   (Delta PRD §6.2 step 2, SW-136 requirements), and F4's cap corrected in the same
   change so the measurement is taken at the shipped default.
2. **A correction candidate is cut** — published, tagged, attested, with a complete
   successor freeze record, digests, SBOM and rebuild evidence (SW-136). Recommended
   version `v0.7.1`.
3. **A fresh baseline is executed on that candidate** — two complete runs on the
   reference runner class, with `candidate_match` true in every `environment.json`.
4. **The pool reaches at least 1000 timed executions in both runs**
   (`internal/evalreport/querylatency.go:40`), i.e. `sufficient: true` in
   `.repo.query_latency.pools[]` for `agent_context_p95`.
5. **Only then** is a p95 compared with the 500 ms threshold.

Two things that must not be assumed at step 5:

- **The verdict may be FAIL.** Correcting an instrument is not a promise about what it
  will read. Nothing in this decision, in SW-134's diagnosis or in the published baseline
  is evidence that gate 9 passes.
- **The corrected pool will contain the executions the old one excluded** — the largest
  answers, at the shipped cap of 20 rather than 10. Both changes push the measured
  distribution **upward**, not downward. Anyone expecting the corrected number to sit
  near 471.250 ms is expecting the wrong thing, for the reason in [§6](#6-the-471250-ms-played-no-part-ac-6).

Until step 4 is met the gate reads UNKNOWN, and **UNKNOWN is not a PASS** (PRD §8.2). No
new vocabulary is introduced by this decision.

---

## 6. The 471.250 ms played no part (AC-6)

The undersampled pool's p95 in run-a was **471 250 µs**, inside the 500 ms threshold
(`run-a/query-latency/grpc-go/report.json`, `.repo.query_latency.pools[]`:
`n: 975`, `minimum: 1000`, `sufficient: false`). **It is not a reason for anything in this
record, and it is not evidence about the gate.**

- A percentile over 975 of a required 1000 executions is not the measurement FR-8 asked
  for. The published artifact says so itself, in `unknown_is_not_pass`.
- The 25 missing executions are not a random 2.5 % of the pool. They are exactly the
  executions on the highest-item-count symbols — systematically the most expensive
  (diagnosis §11).
- The same report records `max_us: 499645` for that pool: the largest **retained**
  execution is 0.355 ms under the threshold, while the executions excluded by
  construction are the ones with more items than any retained one. That is a reason for
  caution, not for confidence — and it is offered here only to close the door on the
  optimistic reading, not to predict a FAIL.

> **CORRECTION 2026-07-30 (SW-130 correction round 1, review finding 1) — added;
> nothing in §6 above is rewritten or withdrawn.** This section discusses **only
> run-a's** figure. The harness recorded the pooled p95 for **both** runs, and
> they fall on **opposite sides of the gate**:
>
> | | pooled n | pooled p95 | vs the 500 ms gate |
> |---|---|---|---|
> | run-a | 975 of 1000 | **471 250 µs** (471.250 ms) | below |
> | run-b | 975 of 1000 | **601 732 µs** (601.732 ms) | **above, by 20.3 %** |
>
> Source: `run-{a,b}/query-latency/grpc-go/report.json` →
> `.repo.query_latency.pools[]` (`agent_context_p95`, `p95_us`), each
> independently recomputed from
> `run-{a,b}/query-latency/grpc-go/raw/query-latency.json` (pool = `agent_brief`
> 250 + `change_risk` 246 + `explain_symbol` 234 + `related_files` 245 = 975
> `samples_us`, nearest rank `ceil(0.95 × 975) = 927`). The run-to-run spread is
> **+27.7 %**.
>
> **This omission originates in SW-130**, whose published `p0-baseline.md`,
> `p0-baseline.json` and evidence-index WP4 row all quoted run-a alone; this
> record inherited it. SW-130's own review (2026-07-29) found it independently of
> SW-135's review, and both are addressed by SW-130 correction round 1.
>
> **The omission worked against this record's own argument.** §6's thesis is that
> the 471.250 ms plays no part and supports no inference about the gate. Run-b is
> the single strongest piece of evidence for exactly that: on the same
> undersampled pool, measured 27 minutes later, the value lands a fifth *outside*
> the threshold. It closes the optimistic reading harder than the `max_us` bullet
> above does, and it does so without predicting a FAIL either — two readings that
> disagree about the side of a threshold support no inference in either direction.
> Nothing in this decision depended on the omitted figure, so no conclusion
> changes; Outcome B stands on §3's defect (D1), not on any p95.

**A correction to an earlier record.** `backlog.md`, 2026-07-28, says of this gate:
*“this gate is plausibly a PASS waiting on a bug fix rather than a latency problem.”*
That phrasing is retired by this decision. It was already qualified there (*“must not be
asserted until the pool is whole”*), but the qualifier does not rescue it: an
undersampled percentile whose missing members are drawn from the slow tail supports no
inference about the whole, in either direction.

---

## 7. The cost of this decision, stated before it is paid

Outcome B is the expensive answer and the record should say what it costs rather than let
SW-136 discover it. Under the STALE rule (`2026-07-p0-candidate-freeze-v070.md` §9, PRD
FR-1), when the candidate moves, every row of `docs/rc/evidence-index.yaml` that was
stated or measured against `5815db5` is marked `STALE` — re-marked, never re-pointed:

| Row | Status today | What happens at the move |
|---|---|---|
| **WP2** — Candidate & reproducible technical baseline | **PASS**, `sha: 5815db5…` | → **STALE**. This is P0's only PASS row, and the move costs it. It was measured, honestly, against a candidate that is being superseded. |
| **WP4** — §12.2 gate results | **FAIL**, `sha: 5815db5…` | → **STALE**. A published FAIL is still evidence, and it is still lost as a statement about the successor. |
| **M1** — reproducible accuracy/performance raw baseline | **UNKNOWN**, `sha: 5815db5…` | → **STALE** for its performance half; its accuracy half was never measured on any candidate. |
| **WP0**, **M0** | UNKNOWN / STALE, prose citing `5815db5` | citation updated to name this record and the successor freeze record; **not** re-pointed, **not** moved toward green. |

Plus: the two published runs (`docs/eval/runs/2026-07-28-ubuntu-latest/`) become evidence
about a superseded candidate. They are **not** deleted, **not** re-labelled and **not**
re-run in place — Delta PRD §6.1 requires the first honest baseline to be preserved, and
a red baseline remains useful evidence.

**None of that marking happens in this record**, deliberately. §9 rule 2 requires a move
to be recorded *in a successor freeze record before it takes effect*, and rule 3 requires
dependent rows to be marked *in the same change*. The candidate has not moved yet.
Marking WP2 STALE today would be false: v0.7.0 is still the candidate. The obligation is
carried to SW-136 and is on `backlog.md` so it cannot be dropped silently
([§9](#9-deferred-work-ac-7)).

**This is the second candidate move under manual STALE discipline.** As the freeze record
notes, that strengthens rather than weakens the case for the deferred P0-E automatic
staleness detector.

---

## 8. What this decision does **not** decide

- **It does not perform the correction.** That is SW-136, and its scope is bounded by
  Delta PRD §6.2 and §8: regression test before code correction, minimal diff, no Stable
  surface expansion, no threshold changes, no repository removal, product-tree changes
  listed explicitly. On the evidence here the correction should touch only the NFR-7
  measurement-code set (`cmd/eval`, `engine/scenario`) and leave the product tree
  byte-identical to v0.7.0 — the same relationship v0.7.0 already has to v0.6.7
  (`2026-07-p0-candidate-freeze-v070.md` §11). That is a requirement on SW-136, not a
  fact: nothing has been built.
- **It does not re-measure anything**, and it moves no threshold.
- **It does not touch `freshness_p95`** (FAIL at ~3.2× budget) **or the ~15.5 s
  progress-stall tail.** Those are WP-R4 items on `backlog.md`, explicitly out of scope
  for this candidate decision (spec, *Boundaries*). If SW-136's correction cycle is the
  right place to batch them, that is SW-136's call to make and to justify — this record
  neither authorises nor forbids it.
- **It does not re-tier or re-scope the 12 frozen Stable operations.** `partial` under
  truncation remains designed, documented, GA-frozen behaviour.

---

## 9. Deferred work (AC-7)

Added to `projects/graphi/backlog.md` on 2026-07-29 with provenance, per the
no-silent-drops rule:

1. Execute the STALE re-marking of WP2 / WP4 / M1 and the WP0 / M0 citation updates in
   SW-136, in the same change as the successor freeze record.
2. Verify the diagnosis's falsifiable prediction (§10.1) — that exactly 16 / 5 / 4 symbols
   of the published 250-symbol sample exceed 10 items for the three operations — which
   needs a corpus clone and was never checked.
3. Determine F4's contribution (§10.2) — how many of the 25 rejections survive at the
   shipped cap of 20. Unknown, and it must not be guessed at when the corrected baseline
   is interpreted.
4. Decide whether the shipped default cap of 20 is the right product choice for large
   repositories (§7, scope note) — a PRD question, not a harness question, and not what
   caused this shortfall.

A fifth item is recorded there as a plan inconsistency rather than deferred work: the
backlog's 2026-07-29 *Measured Corrections (SW-142)* entry says the partial-defect fixes
live in SW-142, while the spec and Delta PRD put them in SW-136. The spec governs; the
backlog entry is stale in that one respect.

---

## 10. Verification

Every fact in this record was re-derived on 2026-07-29 on
`p0-delta-registration-and-partial-diagnosis` at `eaa9525`, not transcribed from the
diagnosis:

```sh
# the rejection rule and its asymmetry inside one pool  → §2.1
sed -n '425,460p' cmd/eval/querylatency.go
#   → allowed=["found"] for explain_symbol/change_risk; ["found","empty"] for
#     related_files; ["found","partial"] for agent_brief

# the harness cap vs the shipped default                → §2.3
sed -n '288,305p' engine/scenario/fixture.go   # → intArg(args,"max_items",10) ×3; agent_brief 0
grep -n DefaultMaxItems engine/agenttools/shape/shape.go   # → const DefaultMaxItems = 20

# FR-8's floor                                          → §2.2, §5
sed -n '35,45p' internal/evalreport/querylatency.go        # → QueryExecutionMinimum = 1000

# candidate_match: why a corrected harness cannot measure 5815db5 → §4
sed -n '150,165p' cmd/eval/coldseries.go

# the shipped binary does not contain engine/scenario   → §4
for d in cmd/*/; do echo "$(go list -deps ./$d | grep -c engine/scenario) $d"; done
#   → 1 for cmd/eval only; 0 for cmd/graphi and all sixteen other binaries

# the published tallies and the pool                    → §2.2, §6
jq '.repo.stable_checks' docs/eval/runs/2026-07-28-ubuntu-latest/run-a/query-latency/grpc-go/report.json
#   → explain_symbol found 234 / partial 16; change_risk 246 / 4;
#     related_files found 155 / empty 90 / partial 5; agent_brief found 250
jq '.repo.query_latency.pools' …/report.json
#   → agent_context_p95: executions 975, minimum 1000, sufficient false,
#     p50_us 162, p95_us 471250, max_us 499645
jq '…agent_context_p95…' docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.json
#   → verdict UNKNOWN, agreed true, both runs 975/1000, unknown_is_not_pass present

# the evidence rows this decision puts at risk          → §7
grep -n 'id:\|status:\|sha:' docs/rc/evidence-index.yaml
#   → WP2 PASS 5815db5…, WP4 FAIL 5815db5…, M1 UNKNOWN 5815db5…; WP0/M0 cite it in prose

# the index still renders and still checks              → story test note
go run ./cmd/evidence -check      # → PASS (source) + PASS (freshness)
go run ./cmd/evidence -generate   # → regenerated docs/rc/evidence-index.md (17 gates), no diff
```

`16 + 5 + 4 + 0 = 25`; `1000 − 25 = 975`. The arithmetic is the published pool size
exactly, and `partial` is the only rejected outcome.

---

## 11. References

- [`docs/eval/p0/partial-outcome-diagnosis.md`](../eval/p0/partial-outcome-diagnosis.md) — SW-134; findings F1–F5, §8 correction-touch table, §10 residuals, §11 the 471.250 ms
- [`docs/plan/2026-07-graphi-p0-completion-delta-prd.md`](../plan/2026-07-graphi-p0-completion-delta-prd.md) — §6.1 preserve the first honest baseline, §6.2 the move bar, Story SW-135 outcome conditions, Story SW-136 correction scope
- [`docs/decisions/2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md) — the candidate this decision supersedes-in-principle; §9 change control and the STALE rule, §10 the precedent for what a move marks, §11 product-tree byte-identity
- [`docs/eval/runs/2026-07-28-ubuntu-latest/`](../eval/runs/2026-07-28-ubuntu-latest/) — the published baseline: `p0-baseline.{md,json}`, `run-{a,b}/query-latency/<repo>/report.json`
- [`docs/eval/reference-scenario.json`](../eval/reference-scenario.json) — gate `agent_context_p95`: pool membership, 500 ms threshold
- `docs/rc/evidence-index.yaml` — the rows [§7](#7-the-cost-of-this-decision-stated-before-it-is-paid) names
