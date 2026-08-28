# AX-06 — canary latency threshold (fixed **before** measuring)

**Story:** SW-226 (AX-06) · **Spec:** extension-platform-kernel · **Canary:** `dead_code`
**Amended:** SW-242 (2026-08-28) — §1, §2 and §3 replaced; see "Amendment record" below.
**Extended:** SW-244 (2026-08-28) — §6 added (the dual-run cost of the shipped default). §1–§3 untouched.

This page exists so the acceptance bar for SW-226 AC-5 is a **prediction**, not a
description. It is committed on its own, ahead of the implementation and ahead of any
number, so that `git log --follow` shows the threshold predates the measurement it judges.
A threshold written after the run is not a threshold; it is a transcript.

---

## Amendment record (SW-242)

The original version of this page carried a standing rule: *"Nothing in §1–§3 may be edited
once a measurement exists — if the bar turns out to be wrong, that is a finding to record,
not a bar to move."* That rule is being honoured, not waived, and it is worth being exact
about how, because a page whose whole point is that thresholds are not retrofitted cannot
quietly retrofit its own.

**The finding is recorded first.** The AX-06 bar was wrong, and it was wrong in a way the
page itself half predicted. §2 was a comparison between two sample blocks taken in
*different time windows*, read at a *tail* statistic, against a threshold calibrated on an
*idle laptop*. On a shared CI runner it failed on essentially every pull request — five
occurrences in the backlog (`:1043`), each failing all three of the anti-flake rounds §1
provided, each green on a plain re-run of the identical commit, and in each case on a change
that provably touched nothing under `surfaces/client` or the canary path. The fifth landed
in `release-gate`, so it taxed the release path and not only the conformance job. The
original §2 was measuring the machine and reporting it as the seam — which is the exact
failure mode its own second paragraph named and then failed to prevent.

**Then the bar moves, deliberately, with an authority.** SW-242 AC-1/AC-4 direct that the
gate be recalibrated against a same-run reference and that the superseded §2 threshold be
*replaced* rather than accumulated beside. It is replaced below. The old rule is not kept as
a live alternative; keeping two rules would make reading a result a choice between them,
which is the thing §2 has always refused to allow. Its numbers survive in §4 as the
historical record of what was measured under it, clearly labelled.

**One correction, while the page is open.** The original §1 described the fixture as "the same
fixture `surfaces/agentintel_golden_test.go` uses". It never was: `canaryLatencyFixture` builds its
own 120-symbol graph with a sparse call chain, in the test file itself. §1 now names what actually
runs. This is a correction to a description, not a change to what was measured — the numbers in §4
were always taken against the fixture §1 now describes.

**A second finding, from review of the amendment itself.** The first cut of this
recalibration gated the **median only**, and §3 listed "p95, and every other tail statistic"
among the things deliberately not gated. Review demonstrated the price of that: because the
median does not move until more than half the distribution does, a regression confined to a
minority of calls became invisible *at any magnitude*. A ~20 ms per-incident cost applied to
one call in three scored **+97 µs** of median overhead and passed clean. The old p95 gate
would have caught it. Retiring the tail statistic was therefore not a neutral trade of noise
for robustness — it was a ~10× widening of the gate's blind spot, from about 5 % incidence to
about 50 %, over failure shapes (a slow path on cache miss, a lock contended only sometimes,
an allocation that occasionally trips GC) that are ordinary rather than exotic. Documenting
that hole was considered and rejected as insufficient: SW-242 AC-3 requires the gate to fail
on a genuine regression, and a 20 ms hit on a third of calls is a genuine regression. §2 now
gates the median **and** the tail, by the same arithmetic; §3 states in numbers what each can
and cannot see. `TestAX06_LatencyGateFailsOnMinorityIncidenceRegression` is the test that
holds it, and §5.5 the measurement that calibrates it.

**A third finding, from review of the amendment's second cut.** Gating the tail created a
second, weaker way to reach a PASS — the *median-only* verdict, returned when the median is
clean and the tail's own control is too wide to judge. The round arbitration ("any PASS
round wins") did not distinguish it from a full pass, and review demonstrated the two
consequences. A later round's median-only PASS could turn an earlier round's genuine,
tail-caught FAIL of the *same* regression into a green run, because contention arriving
mid-run widens the p95 control and the tail then measures nothing. And the round loop, which
exits on the first PASS, exited on the first *median-only* PASS — so on a runner contended
enough for the tail to be unjudgeable (4 runs in 6 under the load §5.6 measured) the gate
routinely ended after round 1 and never used the calmer rounds the anti-flake provision
exists to buy. Absence of a measurement was reading as evidence of good latency again, this
time across rounds rather than within one. §2's round-collapse rule and §3's disclosure are
corrected below, and §5.8 records the measurement that chose between the two candidate
corrections.

**A fourth finding, from CI on the story's own pull request.** PR #169 failed on GitHub's
runners in `test-gate`, `go test -race` and `release-gate`, all on
`TestAX06_LatencyGateFailsOnMinorityIncidenceRegression/one_call_in_eight`. The three rounds
were `FAIL`, `FAIL`, `UNKNOWN` — and the run reported **UNKNOWN**. Two stacked ordering bugs,
both the same mistake in different places:

*Within* round 3, the p95 was `FAIL` at **74.65 ms against a 1.773 ms budget** — 42× over —
while the p50 was `UNKNOWN` because its own A/A control had collapsed to 442.871 µs. The rule
"median UNKNOWN ⇒ run UNKNOWN" fired first and discarded the tail's answer. *Across* rounds,
that one UNKNOWN round then erased two FAIL rounds that had each caught the same injected
regression, at **79.56 ms** and **92.47 ms** over budget. Because `cmd/testgate` does not
summarise skips, an UNKNOWN renders green in the rollup: a run in which the tail definitively
caught a ~75–90 ms regression reported no failure at all, which is strictly worse than the
gate this page replaces.

The correction is not a new principle. It is the principle already written into the
evaluation order above — *an overhead past both the clamp and the run's own signal-to-noise
requirement is signal at whatever resolution that run achieved* — applied in the two places it
had not been: the composition of a round, and the arbitration of a run. §2 carries both, with
the boundary that keeps them safe, and §5.9 the measurement that fixed where that boundary
sits. `TestAX06_LatencyGateFailsThePR169CISequence` pins the sequence above, with the job's
own numbers, as a regression test.

**What did not move.** The seam itself is untouched — SW-242 changes how the executor path
is *measured*, never what it does. The kill-switch default stays `legacy`. And the direction
of the bar did not soften: the fixed part of the threshold is the same 10 % / 250 µs it
always was, and the new terms can only ever be checked *in addition* to it.

---

## 1. What is measured

One `dead_code` invocation, end to end through `surfaces/client`, against a hermetic
120-symbol fixture graph built by `canaryLatencyFixture` in
`surfaces/client/canary_latency_test.go`, on a warm store. **Four** paths are timed:

| Arm | Kill-switch position | What runs |
|---|---|---|
| **legacy-a** | `legacy` | `Client.DeadCode` — today's dispatch, the baseline |
| **legacy-b** | `legacy` | the same code again — the **same-run reference** (see below) |
| **executor** | `active` | `Executor.Execute("dead_code")` — catalog lookup, JSON argument round trip, typed decode, adapter call |
| **shadow** | `shadow` | both of the above, plus the byte/error comparison |

Sample size **N = 200** timed iterations per arm after **20** warm-up iterations per arm,
single goroutine.

**The arms are interleaved, and the interleave rotates.** The arms are not sampled in
blocks. For iteration *i* and rotation slot *j*, the arm sampled is `arms[(i+j) mod 4]`, and
N is rounded up to a multiple of the arm count, so **every arm occupies every slot in the
rotation exactly N/4 times**. Two consequences follow from the schedule alone, with no
assumption about the machine:

- no arm is systematically sampled later in the run than another, so a drift in machine load
  across the run cannot be charged to one arm;
- no arm is systematically sampled immediately after the same neighbour, so the cache and GC
  after-effects of an expensive neighbour (`shadow` runs both paths) are shared evenly.

This is the correctness argument for the method, and it is deliberately a property of the
code rather than of a lucky reproduction — `TestAX06_LatencyRotationIsBalanced` **runs the
production sampler** with instrumented arms and asserts the balance of the order it actually
executed, so a future edit to the loop cannot silently lose it. (It reads back what the
sampler did rather than re-deriving the rotation formula alongside it; a re-derivation would
agree with an edit that broke the real loop.)

**The same-run reference.** `legacy-a` and `legacy-b` execute byte-identical code. The
difference between their medians is therefore, by construction, **pure measurement noise**:
it is this run's demonstrated inability to tell two identical paths apart. Every run
consequently carries its own null control, taken on the same machine, in the same rotation,
under the same load, at the same moment. The bar in §2 is expressed against it.

**Two statistics are gated: the median and the tail.** p95 is a tail statistic and
contention is a tail phenomenon — preemption and co-tenant interference add a heavy right
tail while barely moving the centre. A *systemic* seam regression is the opposite shape: the
seam runs on **every** call, so it is a location shift that moves the whole distribution,
median included. Judging at the median therefore reads that regression where its cost lives
and reads the runner's noise where it is weakest, and it is the median that carries the
gate through a contended runner.

The median alone is not enough, and §3 gives the numbers. It is blind by construction to a
regression that hits only a **minority** of calls, however severe, because the median does
not move until more than half the distribution does. So **p95 is gated too**, against a
same-run A/A control at p95 built by the identical arithmetic — which is to say the original
AX-06 tail bar survives this amendment, with a reference this run actually measured in place
of an absolute delta calibrated on one laptop. p95 reads the top 5 % of calls, so it moves by
close to the full per-incident cost as soon as a regression's incidence clears roughly 5 %.
The two statistics answer different questions and either can turn the gate red.

**Anti-flake provision** (from the original §1, retained): the gate repeats the whole
measurement up to **3 rounds** and passes if **any** round meets §2. The recorded numbers
are from the first round regardless of which round passed, so the record is not
cherry-picked.

## 2. The gate (AC-5, recalibrated by SW-242)

The rule below is applied **twice**: once at the median (`p = 0.50`) and once at the tail
(`p = 0.95`). Let `base` and `ref` be the two legacy arms at that percentile, `exec` the
executor arm at the same percentile, and

```
baseline  = (base + ref) / 2
overhead  = exec − baseline
refDelta  = |base − ref|                            the same-run reference measurement
fixedBar  = max( 10 % of baseline , 250 µs )        the machine-independent bar
noiseTerm = 3 × refDelta                            the signal-to-noise requirement
ceiling   = 4 × fixedBar                            the hard clamp
budget    = min( max( fixedBar , noiseTerm ) , ceiling )
```

> A statistic is **UNKNOWN** when an arm produced no samples, or when `noiseTerm > ceiling`
> *and* the overhead is not itself past both. It **fails** when
> `overhead > budget` **and** `overhead > noiseTerm`. Otherwise it **passes**
> (`overhead ≤ budget`).

The evaluation order matters and is checked in that order in the code: the FAIL test is
applied **before** the degraded test. In the ordinary regime this changes nothing —
`budget ≥ noiseTerm` there, so `overhead > budget` already implies `overhead > noiseTerm`,
and the rule reads exactly as it did. It only bites when the control is wider than the
ceiling, and only ever in the direction of failing: an overhead past both the clamp *and*
three times the run's own demonstrated resolution is signal at whatever resolution that run
achieved, and a degraded runner is not a licence to launder it into a pass. Without this
ordering, enough contention would make any regression invisible, which is AC-2's prohibition
read in the opposite direction.

### A FAIL is *marginal* or *decisive*

The order above establishes that an overhead past both the clamp and the run’s
signal-to-noise requirement is signal at any resolution. That property is given a name,
because everything below turns on it:

```
resolution = the WIDEST same-run A/A control demonstrated at that percentile
             (the round’s own when only the round is in evidence; the worst any
              round of the run produced when the whole run is)
decisive   = the statistic FAILed  AND  overhead > 3 × max( ceiling , resolution )
```

Everything else that FAILs is **marginal**: over its budget, but not past every scale the
measurement demonstrated. A decisive FAIL is allowed to outrank the *absence* of a
measurement, within a round and across rounds. A marginal FAIL is not.

Three details, each of which was measured rather than assumed (§5.9):

- **The multiple is the gate’s existing `3×` signal-to-noise factor.** No new constant is
  introduced; it is applied to the largest scale in evidence instead of the smallest.
- **The scale is the widest control, not the failing round’s own.** `refDelta` is `|a − b|`
  from a *single* pair of A/A arms — a folded-normal draw that is sometimes near zero — and
  3× a lucky-narrow control is not a bar at all. Using the round’s own control here reds
  **2.365 %** of clean contended runs against the previous rule’s 0.100 %: a 24× regression,
  measured, on the very runner this page exists to stop crying wolf on.
- **The two percentiles are kept separate.** A round that could not resolve the tail says
  nothing about the run’s resolution at the median, and vice versa. That separation is what
  makes PR #169 come out right: its round 3 lost the *median* control (443 µs), which raises
  the bar for median FAILs and leaves the bar for the tail FAILs — measured against narrow
  tail controls in all three rounds — exactly where it was.

**The two statistics compose asymmetrically:**

| median | tail | run |
|---|---|---|
| FAIL | anything | **FAIL** (median reason) |
| UNKNOWN | FAIL, **decisive** | **FAIL** — a definitive tail measurement is not suppressed by an unjudgeable median |
| UNKNOWN | anything else | **UNKNOWN** — nothing was resolved at the centre, and nothing definitive at the tail |
| PASS | FAIL | **FAIL** — a regression the median cannot see, which is what the tail is for |
| PASS | UNKNOWN | **PASS on the median alone**, and the log line says so. This is a verdict for the ROUND; it is not enough to pass the RUN — see the round-collapse rule below |
| PASS | PASS | **full PASS** — the only round shape that can pass the run |

Two asymmetries live in that table and they are separate.

The first is between the statistics: the tail is the noisier measurement and its noise is
exactly what made the old gate cry wolf, so a tail whose *own* A/A control is too wide to
judge degrades to a median-only verdict instead of dragging a clean run to UNKNOWN. A
contended runner still gets a usable answer from the statistic that survives contention. The
median gets no such courtesy: if a run cannot tell two identical paths apart at the centre of
the distribution, it has measured nothing at all.

The second is between *measurement* and *absence of measurement*. Row 2 is PR #169’s round 3.
An unjudgeable median is an absence of evidence; a tail overhead past 3× every scale the round
demonstrated is evidence. Absence does not erase presence — that is AC-2 read in the direction
that protects the gate rather than the tree. Row 3 is the boundary: when the tail’s FAIL is
only *marginal*, the round is still UNKNOWN, because a marginal FAIL on a runner that could
not resolve its own median is exactly what noise looks like.

Each term earns its place:

- **`fixedBar` is the original bar, unchanged.** The relative 10 % term is the real bar on a
  repository-sized graph, where `dead_code`'s whole-graph node+edge pass dominates and the
  seam is rounding error. The absolute 250 µs term is a floor: it bounds what the *seam
  itself* can add — one memoised catalog lookup, one `json.Marshal`/`json.Unmarshal` of a
  single-integer argument struct, one map lookup, one interface call — with room to spare.
  If the seam ever needs more than that, the design is wrong and the number should fail
  rather than be raised. Nothing below can make the budget *smaller* than this.

- **`noiseTerm` is the same-run reference term, and it is why the gate stopped crying
  wolf.** A difference smaller than a multiple of what the apparatus demonstrably cannot
  resolve is not a measurement of the seam; it is a measurement of the machine. Requiring
  the overhead to clear 3× the run's own A/A control is the standard signal-over-noise
  demand, made against a control this run actually took rather than against a constant
  copied from a laptop. On a quiet machine `refDelta` is a couple of microseconds and this
  term is inert — the budget is the 250 µs floor and the gate is exactly as tight as it
  was. On a loaded machine the term widens the budget precisely as far as that machine has
  proven it needs, and not one microsecond further.

- **`ceiling` is what stops adaptation from becoming abdication.** The budget is clamped, so
  `budget ≤ 4 × fixedBar` is an invariant of the code and not a property of the inputs. Both
  gated statistics are produced by **one** function, so this is a single property proved once
  and holding for each of them rather than a rule re-implemented per statistic and at risk of
  being lost in one; `TestAX06_LatencyDecisionRule` asserts it — along with
  `budget ≥ fixedBar ≥ 250 µs`, and "a statistic past the ceiling never reads PASS" — for the
  median *and* the tail on every case in the table. A runner cannot talk the gate into
  tolerating an arbitrary regression by being noisy: past the clamp the available answers are
  FAIL and UNKNOWN, never PASS.

- **UNKNOWN is a real verdict, not a quiet pass.** When the null control is wider than the
  ceiling, two byte-identical legacy paths could not be told apart well enough for *any*
  comparison to mean anything, and the honest report is that this run does not know. It is
  emitted through `t.Skip` with the full numbers and the reason, which `go test -json` — and
  therefore `cmd/testgate` — always records, so an UNKNOWN run is visible rather than hidden
  behind a green package line. It is deliberately **not** a pass: absence of a usable
  measurement must never read as evidence of good latency. A flattering *number* on a
  degraded runner is still UNKNOWN — `TestAX06_LatencyDecisionRule` pins that case
  specifically, because it is the one where a lazy implementation would launder noise into
  a green tick.

- **Rounds collapse in this order:**

  1. a **full PASS** round wins — both statistics judged, both clean. This is §1's anti-flake
     provision, restricted to the rounds that earn it;
  2. failing that, a round that recorded a **decisive FAIL** — judged against the resolution
     the *whole run* demonstrated — makes the run **FAIL**. A round that could not be judged
     does not withdraw a measurement that was taken;
  3. failing that, an **UNKNOWN** round beats a (marginal) FAIL round — if the machine was at
     any point too degraded to distinguish two identical paths *at the centre of the
     distribution*, it resolved nothing, and the honest answer to "did the seam regress" is
     that this run does not know, not that it did;
  4. failing that, a **median-only** round makes the run UNKNOWN too — half the gate ran, so
     the run cannot claim the conclusion the other half would have supplied;
  5. failing that, every round FAILed, and the run **FAILs**.

  Step 2 is ranked below step 1 on purpose. A full pass is a *positive* measurement that
  contradicts the FAIL; an UNKNOWN or median-only round is the *absence* of one. Only the
  first is entitled to overturn a FAIL, and the asymmetry cuts both ways.

  And the round loop stops **only on a full PASS**. A median-only round has not measured the
  tail, so ending the loop on one spends the remaining rounds — whose only purpose is to give
  a regression another chance to be measured on a calmer interval — to buy nothing.

  The consequence that matters: **a median-only round can never turn a FAIL into a PASS, and
  it can no longer turn a *decisive* FAIL into an UNKNOWN either.** Where the FAIL held is
  marginal, the run is UNKNOWN and that FAIL is quoted verbatim in the verdict line,
  explicitly *not withdrawn and not disbelieved* — it simply could not be re-measured on a
  round that could resolve the statistic, and one unrepeatable marginal measurement is not the
  standard this gate fails on. Where it is decisive, the run is red and the verdict line says
  how many rounds could not be judged and that they did not withdraw it.

  **Why UNKNOWN and not FAIL for a *marginal* FAIL.** The alternative — letting *any*
  recorded FAIL outrank UNKNOWN and median-only rounds — was implemented and measured before
  being rejected. On a noise model calibrated against this gate's own measured observables
  (§5.8) it raises the run-level *false*-fail rate on a clean tree from **0.05 % to 9.75 %**
  (§5.9, re-measured; round 3 measured 10.4 %, and review independently reproduced 9.75 %):
  roughly one PR in ten turned red for being scheduled on a busy runner. That is the disease
  SW-242 exists to cure, reintroduced through the tail statistic that caused it the first
  time. Restricting the override to *decisive* FAILs buys the coverage without the redness:
  **0.05 %**, bit-for-bit the same as the rule it replaces (§5.9).

  And what the correction costs is bounded by a theorem rather than an estimate:

  > The round-4 composition FAILs a superset of the rounds the round-3 composition FAILed —
  > it adds "median UNKNOWN + decisive tail FAIL" and changes nothing else — and PASSes
  > exactly the same rounds. Both arbitrations scan for a full pass first, so a run
  > containing one reports PASS under both, and the extra FAIL branch is reachable only when
  > no round was a full pass. So the corrected arbitration differs from the superseded one
  > **only by turning some UNKNOWN verdicts into FAIL**. It cannot fail anything the
  > superseded rule passed.

  `TestAX06_LatencyArbitrationIsMonotone` checks that exhaustively over all 2 954 sequences
  of one to three rounds across every reachable round shape — marginal and decisive FAILs
  counted as distinct shapes, since that distinction is the whole boundary — asserts that
  every disagreement is UNKNOWN → FAIL *and* carries a decisive FAIL, and fails if the two
  rules ever agree everywhere (which would mean the correction was not wired in).

- **An all-median-only run is UNKNOWN, not PASS.** If the tail was never judgeable in any
  round, the run holds no evidence either way about a regression confined to a minority of
  calls, and saying PASS would state a conclusion it never reached. It is not a FAIL either:
  nothing failed. So it is reported as what it is, through the same `t.Skip` path as any
  other UNKNOWN, with the median's numbers, the tail's control width, and the sentence
  *"Reported as UNKNOWN rather than PASS because half the gate did not run"*. A job that
  reports this routinely is a finding about the runner.

**The gate must still be able to fail, and that is tested, not asserted — in both shapes.**
`TestAX06_LatencyGateFailsOnInjectedSeamRegression` injects a real *systemic* slowdown at the
real seam — the executor seam's own code, run extra times inside the timed window, on every
call — and requires the shipped decision path to return FAIL, specifically FAIL and not
UNKNOWN. §5.3 records its output. `TestAX06_LatencyGateFailsOnMinorityIncidenceRegression`
does the same for the *minority-incidence* shape, charging the same real cost to only one
call in eight and one call in sixteen, and requires FAIL from the same shipped path. §5.5
records its output and the incidence sweep that calibrates it, and §5.6 what the tail check
costs under contention. Between them the two tests close the coverage of both failure shapes
the gate claims to cover.

Removing this gate, raising the budget until nothing can fail, or reducing it to a warning
were all considered and rejected (SW-242 AC-5); the recalibration exists so the gate can be
*believed*, which is worth nothing if it cannot go red.

## 3. What is deliberately NOT gated

**Shadow mode.** It runs both paths by construction, so ~2× legacy is its *correct*
behaviour, not a regression. Gating it would either be vacuous or would pressure someone to
weaken the comparison it exists to perform. It is measured and recorded for information, and
its cost is the reason the shipped default is documented and revisitable rather than
assumed.

**Cold-start and first-call costs.** `opcatalog.Shadow()` decodes and freezes the embedded
catalog exactly once per process (`sync.OnceValues`). That one-off is a process-startup cost
already paid by SW-225's descriptor projection, and attributing it to the canary would
double-count it.

**Anything measured on a different machine or a different fixture.** The numbers below are a
same-process, same-run A/B on one machine (§5.7). They are evidence that the *seam* is cheap; they
are not a published performance figure, and they do not belong in `docs/eval/`, whose
harness and provenance rules (`docs/eval/hero-protocol.md`) govern published numbers.

### What the gated statistics can and cannot detect (SW-242, AC-4)

This is the section to read before trusting a green run. Every gate has a blind spot; this
one's is measurable, and the numbers below are from the sweep in §5.5 on an idle
darwin/arm64 M2 Max, where a `dead_code` call costs ~385 µs at p50 and ~775 µs at p95.

| Regression shape | Statistic that sees it | Detected from |
|---|---|---|
| **Systemic** — every call slower (the shape of all five backlog `:1043` incidents, and of any real seam cost) | median | ~250 µs of added per-call cost, the unchanged AX-06 floor. Measured: a uniform +239 µs passes, +296 µs fails |
| **Minority incidence** — a large cost on a fraction of calls (slow path on cache miss, an occasionally contended lock, an allocation that sometimes trips GC) | tail (p95) | **incidence above ~5 %**, provided the per-incident cost clears the tail budget. Measured: 5.6 % (1 in 18) incidence fails, 5.0 % (1 in 20) passes — and 5.0 % passes *even at 20 ms per incident*. The review counterexample (20 ms on 1 call in 3) now fails at +24.57 ms |

**What still escapes, stated plainly:**

- **A regression on fewer than ~1 call in 20 is not detected, at any magnitude.** p95 reads
  the top 5 % of calls; below that incidence the incident calls sit entirely above the
  statistic and never move it. §5.5 shows 20 ms per incident at 5.0 % incidence passing
  clean. This is the residual blind spot, and it is narrow rather than absent: it is where
  the *original* AX-06 gate's blind spot was, before the median-only cut of SW-242 widened
  it tenfold and this amendment closed it back.
- **A minority-incidence regression smaller than the per-incident tail budget is not
  detected** even above 5 % incidence. On an idle machine that budget is the 250 µs floor;
  measured, one extra seam pass (~420 µs) is caught at 25 % incidence but not at 12.5 %,
  because at low incidence the injected calls compete with the fixture's own ~385 µs natural
  spread between p50 and p95 rather than clearing it outright.
- **Under sustained contention the tail can stop being judgeable at all**, and the run is
  then reported as **UNKNOWN**, not as a pass — with the tail's UNKNOWN and its numbers
  printed in the verdict line as `tail not judgeable this run (median-only verdict)`, never
  silently. Measured under 24 spinning processes on 12 cores (~2.2× slowdown), the tail was
  unjudgeable in **4 runs out of 6** (§5.6), the p95 A/A control widening from 6–28 µs idle
  to 297 µs–1.12 ms. **For those runs, minority-incidence coverage is lost** — roughly two
  runs in three on a runner that bad — and the run says so rather than reporting a verdict it
  did not reach. Three things bound the damage:

  - the **median's** coverage is not lost at all: it held at +19…+35 µs of overhead against
    an unmoved 250 µs budget through the same load, and the injected minority regression
    still turned the gate red in **6 of 6** runs under it;
  - the gate now uses **all three rounds** in that state instead of stopping at the first
    one, so the tail gets every chance the anti-flake provision was meant to give it;
  - and the result of losing the tail is an honest UNKNOWN rather than a green tick. On the
    calibrated model of that same runner, a 2 ms-per-incident regression on one call in eight
    went from being reported PASS in **85.5 %** of runs under the superseded rule to **7.8 %**
    under the corrected one, with the FAIL rate unchanged at 14.5 % and the remainder reported
    as UNKNOWN (§5.8). The regression is not caught more often; it is *laundered* far less
    often, which is the property AC-2 asks for.

  **What a lost statistic does NOT do, since PR #169:** it does not erase what the *other*
  statistic measured, and it does not erase what *another round* measured. A **decisive**
  FAIL — past 3× every scale the run demonstrated at that percentile — stands through an
  unjudgeable median in the same round and through unjudgeable rounds elsewhere in the run
  (§2). On the model calibrated to PR #169's own runner, a 20 ms-per-incident regression on
  one call in eight went from **37.6 %** reported FAIL under the superseded rule to
  **42.6 %**, with the false-FAIL rate on a clean tree unchanged (§5.9). The coverage lost to
  contention is the coverage that was never *measured*; measurements that were taken are kept.

  A CI job that reports a median-only verdict routinely is a finding about the runner, not a
  reason to widen the ceiling.
- **Everything above is per-run resolution, not a guarantee about production.** These are
  same-process A/B numbers on one fixture, and §5.7 bounds what they claim.

**Other tail statistics — p99, max, and the shape of the distribution above p95.** Recorded
for information, never gated. Past p95 the sample count (N = 200) leaves too few
observations for a same-run A/A control to mean anything, so a gate there would be measuring
its own resolution. Reporting and gating are different jobs.

## 4. Measurement under the SUPERSEDED method (SW-226, 2026-08-27)

Retained as the historical record of what the original §2 measured. **These numbers were
taken with block sampling at p95 and are not comparable with §5.** Measured on darwin/arm64,
Apple M2 Max, `CGO_ENABLED=0`, machine otherwise idle.

### 4.1 Per-call latency, first round of four consecutive runs

| Path | p50 | p95 |
|---|---|---|
| legacy | 435.7 µs | 816.5 µs |
| executor (`active`) | 385.1 µs | 778.7 µs |
| shadow (both, not gated) | 774.4 µs | 1231.8 µs |

`p95(executor) − p95(legacy)` = **−37.9 µs**. Budget = max(10 % = 81.7 µs, 250 µs) = **250 µs**.

### 4.2 Four consecutive runs, the honest spread

| Run | legacy p95 | executor p95 | difference | budget | verdict |
|---|---|---|---|---|---|
| 1 | 816.5 µs | 778.7 µs | −37.9 µs | 250 µs | pass |
| 2 | 843.5 µs | 762.5 µs | −80.9 µs | 250 µs | pass |
| 3 | 746.2 µs | 785.3 µs | +39.1 µs | 250 µs | pass |
| 4 | 778.5 µs | 811.0 µs | +32.5 µs | 250 µs | pass |

The difference changes **sign** between runs — at this fixture size the executor seam's cost
was smaller than the run-to-run noise of the operation it wraps, so the honest conclusion
was "indistinguishable from legacy", not "faster". SW-242 read that spread as the warning it
was: a method whose noise exceeds its signal on an idle laptop has no margin left for a
shared runner. §5 shows the same seam, measured properly, is not indistinguishable at all.

### 4.3 What the seam costs, from the allocation counters

`go test -bench BenchmarkCanaryDispatch -benchtime 300x`:

| Position | ns/op | B/op | allocs/op |
|---|---|---|---|
| legacy | 498 116 | 456 085 | 1 905 |
| `active` | 454 804 | 460 569 | 1 975 |
| `shadow` | 938 202 | 916 401 | 3 879 |

Allocations were the stable signal wall-clock was too noisy to give: the executor seam adds
**+70 allocations and +4.5 KB per call** — the catalog lookup's spec copy, the argument
struct's `json.Marshal`/`json.Unmarshal` round trip, and the request value — against an
operation that already allocates ~1 900 times and ~456 KB doing its whole-graph pass. That
is **+3.7 % allocations**, and it remains the number a future regression should be compared
against. §5.1 shows the recalibrated wall-clock figure now agrees with it.

## 5. Measurement under the RECALIBRATED method (SW-242, 2026-08-28)

Measured on darwin/arm64, Apple M2 Max, `CGO_ENABLED=0`, Go test binary. Instrument:
`surfaces/client/canary_latency_test.go`.

```
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_ExecutorSeamLatencyWithinThreshold -v -count=5
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_LatencyGateFailsOnInjectedSeamRegression -v
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_LatencyGateFailsOnMinorityIncidenceRegression -v
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_LatencyGateReportsUnknownOnDegradedReference -v
```

### 5.1 Idle machine, five consecutive runs

Both gated statistics, budget 250 µs for each (the noise term never became binding):

| Run | **p50** legacy-a / legacy-b | refDelta | executor | overhead | **p95** legacy baseline | refDelta | executor | overhead | verdict |
|---|---|---|---|---|---|---|---|---|---|
| 1 | 386.5 / 383.2 µs | 3.37 µs | 395.5 µs | **+10.69 µs** | 758.1 µs | 27.96 µs | 802.5 µs | **+44.40 µs** | PASS (round 1) |
| 2 | 382.2 / 382.0 µs | 0.25 µs | 390.8 µs | **+8.75 µs** | 778.2 µs | 16.75 µs | 785.4 µs | **+7.25 µs** | PASS (round 1) |
| 3 | 386.6 / 386.4 µs | 0.25 µs | 397.3 µs | **+10.79 µs** | 784.0 µs | 15.04 µs | 795.6 µs | **+11.61 µs** | PASS (round 1) |
| 4 | 384.0 / 383.6 µs | 0.46 µs | 390.0 µs | **+6.23 µs** | 765.5 µs | 12.71 µs | 784.3 µs | **+18.77 µs** | PASS (round 1) |
| 5 | 387.0 / 384.9 µs | 2.08 µs | 399.5 µs | **+13.63 µs** | 768.5 µs | 6.38 µs | 811.2 µs | **+42.69 µs** | PASS (round 1) |

Three findings, and the first is the one that matters most.

**The seam's cost is now measurable.** Under the old method the difference changed sign
between runs (§4.2) and the honest answer was "indistinguishable". Under the new one it is
**+6.5 to +12.3 µs, positive in every run** — a real, stable ~2 % of the operation, and in
the same direction and the same order of magnitude as the +3.7 % allocation figure in §4.3
that wall-clock previously could not see. Recalibrating for noise did not blur the picture;
it brought it into focus.

**The apparatus's resolution is ~0.25–3.4 µs at the median**, i.e. well under 1 % of the
operation. That is `refDelta`, and it is what makes the 250 µs floor an honest bar on this
machine rather than a coincidence: there are roughly two orders of magnitude between what
the instrument can resolve and what the gate is asked to detect.

**The tail is a usable measurement on this machine too, which is why it is gated.** The A/A
control at p95 is 6.4–28.0 µs — an order of magnitude coarser than the median's, exactly as
expected, but still an order of magnitude inside the 250 µs budget. That margin is what
makes the tail check able to *fail* rather than a decoration: the executor's own tail
overhead sits at −25…+44 µs across runs, and §5.5 shows real regressions clearing 250 µs at
incidences down to 5.6 %. A tail control this narrow was not assumed; it was measured before
the check was gated, because a tail check that could never fail would have been worse than
no tail check at all.

### 5.2 Under synthetic contention — the failure mode, reproduced

The old gate's failure mode *was* reproduced locally after all: 24 spinning processes on 12
cores, which slows the operation ~2.2×. Both gates were built from their own commits and run
against the same load, alternately.

**Old gate (block sampling, p95), three runs, round 1 as logged:**

| Run | legacy p95 | executor p95 | p95 difference | budget | round 1 |
|---|---|---|---|---|---|
| 1 | 2.096 ms | 2.506 ms | **+410.7 µs** | 250 µs | over budget |
| 2 | 2.549 ms | 2.810 ms | **+261.1 µs** | 254.9 µs | over budget |
| 3 | 2.753 ms | 2.340 ms | **−412.7 µs** | 275.3 µs | under |

An **824 µs peak-to-peak swing on a 250 µs budget**, with the two extremes equal in
magnitude and opposite in sign — the clearest possible statement that the quantity being
measured was the machine. Two of three runs blew their round-1 budget and survived only
because a later anti-flake round happened to land low; on a runner where the contention
lasts the whole job, no round lands low, which is precisely the five recorded CI failures.

**New gate (rotating interleave), the MEDIAN statistic, three runs under the same load:**

| Run | legacy-a p50 | legacy-b p50 | refDelta | executor p50 | overhead | budget | verdict |
|---|---|---|---|---|---|---|---|
| 1 | 833.1 µs | 835.5 µs | 2.33 µs | 870.3 µs | +36.0 µs | 250 µs | PASS (round 1) |
| 2 | 840.4 µs | 835.6 µs | 4.83 µs | 862.4 µs | +24.4 µs | 250 µs | PASS (round 1) |
| 3 | 835.8 µs | 835.3 µs | 0.50 µs | 859.9 µs | +24.3 µs | 250 µs | PASS (round 1) |

The operation slowed 2.2× and the *reported seam overhead* moved from ~8 µs to ~28 µs —
which is real (a slower machine really does make a fixed seam cost more wall-clock) and is
still 9× inside the budget. `refDelta` stayed at single-digit microseconds throughout, so
the noise term never even became the binding one. The 2.2× slowdown that swung the old gate
by 824 µs moves the new gate by ~20 µs.

Note what this says about the UNKNOWN branch **at the median**: it is a genuine last resort,
not the expected answer. Even at 2.2× slowdown the median control stayed three orders of
magnitude inside the ceiling. A median UNKNOWN is reserved for a machine far worse than this
one. The **tail** behaves differently under the same load and §5.6 records it: that is the
whole reason the composition in §2 is asymmetric.

### 5.3 The gate still fails — injected regression (AC-3)

`canaryLatencyExtraSeamPasses(n)` runs the executor seam's **own code** *n* extra times
inside the executor arm's timed window: `NewExecutor`, `NewRequest` with its JSON argument
round trip, typed decode, adapter call. It is what "the seam became *n*+1 times as
expensive" actually costs, on the same clock, on the same arm, under the same rotation — not
a sleep standing in for one. The demonstration drives the **shipped** decision path
(`runCanaryLatencyGate` → `evaluateCanaryLatency`, all three rounds), so it proves the gate
and not a parallel copy of it.

| Injection | rounds | p50 overhead (r1 / r2 / r3) | p95 overhead (r1 / r2 / r3) | budget | verdict |
|---|---|---|---|---|---|
| seam cost **doubled** (1 extra pass) | 3 of 3 FAIL | **+407.5 / +403.8 / +490.4 µs** | +484.3 / +523.4 / +534.7 µs | 250 µs each | **FAIL** |
| seam cost **quadrupled** (3 extra passes) | 3 of 3 FAIL | **+1.571 / +1.582 / +1.588 ms** | +1.397 / +1.393 / +1.455 ms | 250 µs each | **FAIL** |

A systemic regression moves **both** statistics, as the design predicts — the median is the
one that would catch it alone, and does. Both cases fail in **all three** rounds, so the
result is not a single unlucky round. The
verdict is required by the test to be FAIL specifically and not UNKNOWN: a gate that answers
"I could not tell" to a doubled seam is worth what a gate that answers PASS is worth, and
that assertion is what keeps the calibration from drifting there.

### 5.4 The degraded path reports UNKNOWN (AC-2)

Sustained CI contention cannot be summoned on demand, but its *signature* can: the run's own
A/A control stops reading zero, because two byte-identical legacy paths no longer measure
the same. `canaryLatencyExtraLegacyPasses(3)` loads one legacy arm to produce that signature
deterministically, and the gate is then run unmodified.

| Round | legacy-a p50 | legacy-b p50 | refDelta | noiseTerm | ceiling | executor p50 | overhead | verdict |
|---|---|---|---|---|---|---|---|---|
| 1 | 387.0 µs | 1.948 ms | 1.561 ms | 4.684 ms | 1 ms | 394.7 µs | **−773.0 µs** | UNKNOWN |
| 2 | 388.1 µs | 1.924 ms | 1.536 ms | 4.608 ms | 1 ms | 394.6 µs | −761.4 µs | UNKNOWN |
| 3 | 381.4 µs | 1.925 ms | 1.544 ms | 4.631 ms | 1 ms | 387.9 µs | −765.3 µs | UNKNOWN |

The overhead is **negative** — this is the flattering case, the one where a lazy gate would
report a comfortable green. It reports UNKNOWN instead, in all three rounds, with the reason
attached: *"the same-run A/A control differs by 1.561334ms, so 3x noise = 4.684002ms exceeds
the 1ms ceiling (4x the 250µs bar). Two byte-identical legacy paths could not be told apart
at this resolution, so nothing can be concluded about the executor seam from this run."*
That is AC-2's requirement discharged in the direction that is easy to get wrong.

### 5.5 The gate fails on a MINORITY-INCIDENCE regression (AC-3, second shape)

This is the section that answers the review finding recorded in the amendment above. The
injection is the same real seam work as §5.3 — `canaryLatencyExtraSeamPassesEvery(n, k)` runs
the executor seam's own code *n* extra times, but only on every *k*-th timed call, so the
cost lands on a minority of calls and leaves the rest untouched. That is the shape a
median-only gate cannot see.

`TestAX06_LatencyGateFailsOnMinorityIncidenceRegression`, 20 extra passes (~8 ms) per
incident, budget 250 µs for each statistic:

| Incidence | rounds | **p50** overhead (r1 / r2 / r3) | **p95** overhead (r1 / r2 / r3) | verdict |
|---|---|---|---|---|
| 1 call in 8 (12.5 %) | 3 of 3 FAIL | PASS: +11.2 / +9.8 / +17.3 µs | **FAIL: +8.971 / +8.954 / +9.069 ms** | **FAIL** |
| 1 call in 16 (6.25 %) | 3 of 3 FAIL | PASS: +9.6 / +17.0 / +7.1 µs | **FAIL: +8.658 / +8.812 / +8.841 ms** | **FAIL** |

Read the two middle columns together: the median reports **single-digit microseconds of
overhead and passes**, on a run where one call in eight is nine milliseconds slower. That is
not a defect in the median; it is what a median *is*. The tail sees the same run at +9 ms and
turns the gate red in every round. This is the coverage the median-only cut had lost and this
amendment restores.

**The incidence sweep — where the boundary actually sits.** Idle machine, gate run unmodified
at each point, one line per configuration:

| Per-incident cost | Incidence | p50 overhead | p95 overhead | verdict |
|---|---|---|---|---|
| ~20 ms (50 passes) | 33.3 % (1 in 3) | +80.9 µs (PASS) | **+24.570 ms** | **FAIL** |
| ~420 µs (1 pass) | 33.3 % (1 in 3) | +31.2 µs (PASS) | **+438.1 µs** | **FAIL** |
| ~420 µs (1 pass) | 25.0 % (1 in 4) | +32.7 µs (PASS) | **+421.8 µs** | **FAIL** |
| ~420 µs (1 pass) | 12.5 % (1 in 8) | +11.7 µs (PASS) | +208.8 µs (PASS) | PASS |
| ~2 ms (5 passes) | 12.5 % (1 in 8) | +14.7 µs (PASS) | **+2.094 ms** | **FAIL** |
| ~2 ms (5 passes) | 6.25 % (1 in 16) | +10.0 µs (PASS) | **+1.933 ms** | **FAIL** |
| ~2 ms (5 passes) | **5.6 % (1 in 18)** | +11.8 µs (PASS) | **+1.675 ms** | **FAIL** ← smallest incidence still caught |
| ~2 ms (5 passes) | 5.0 % (1 in 20) | +8.5 µs (PASS) | +69.9 µs (PASS) | PASS |
| ~2 ms (5 passes) | 3.1 % (1 in 32) | +8.8 µs (PASS) | +67.5 µs (PASS) | PASS |
| ~20 ms (50 passes) | 6.25 % (1 in 16) | +13.8 µs (PASS) | **+23.140 ms** | **FAIL** |
| ~20 ms (50 passes) | 5.0 % (1 in 20) | +10.4 µs (PASS) | +178.9 µs (PASS) | PASS |
| ~20 ms (50 passes) | 3.1 % (1 in 32) | +11.4 µs (PASS) | +65.2 µs (PASS) | PASS |

Three things this measures, all of them recorded in §3 as limits rather than claims:

1. **The smallest incidence still caught is 5.6 % (1 in 18).** The boundary is the statistic,
   not the magnitude: at 5.0 % incidence even a 20 ms per-incident cost passes, because the
   incident calls then sit entirely above the 95th percentile and never move it. This is
   sharp and predictable rather than a fuzzy sensitivity curve, which is what makes it
   documentable.
2. **The median is blind throughout.** Its overhead never leaves the +8…+81 µs band across
   the whole sweep, including at 33 % incidence and 20 ms per incident — the review's exact
   counterexample, which scored +97 µs under the median-only gate and now fails on the tail
   at **+24.57 ms**.
3. **Magnitude still has to clear the budget.** One extra seam pass (~420 µs) is caught at
   25 % but not at 12.5 %, because at low incidence the injected calls compete with the
   fixture's own ~385 µs natural spread between p50 and p95 instead of clearing it.

### 5.6 The tail statistic under contention — what it costs, honestly

The tail is the noisier statistic, so the question SW-242 has to answer is whether gating it
reintroduces the flake this story exists to remove. Measured under the same synthetic load as
§5.2 (24 spinning processes on 12 cores), gate run unmodified, six consecutive runs:

| Run | p50 overhead | p50 budget | p95 A/A control | p95 noise vs ceiling | p95 verdict | run verdict |
|---|---|---|---|---|---|---|
| 1 | +24.5 µs | 250 µs | 380.6 µs | 1.142 ms > 1.000 ms | UNKNOWN | **PASS** (median only) |
| 2 | +28.7 µs | 250 µs | 619.8 µs | 1.859 ms > 1.175 ms | UNKNOWN | **PASS** (median only) |
| 3 | +19.3 µs | 250 µs | 297.2 µs | 892 µs ≤ 1.222 ms | PASS (−365 µs) | **PASS** |
| 4 | +29.3 µs | 250 µs | 379.2 µs | 1.138 ms ≤ 1.165 ms | PASS (−344 µs) | **PASS** |
| 5 | +21.5 µs | 250 µs | 1.121 ms | 3.363 ms > 1.243 ms | UNKNOWN | **PASS** (median only) |
| 6 | +34.9 µs | 250 µs | 400.1 µs | 1.200 ms > 1.173 ms | UNKNOWN | **PASS** (median only) |

**Zero spurious failures in six runs — the tail never turned the gate red on a clean seam.**
The p95 A/A control widened from 6–28 µs (idle, §5.1) to 297 µs–1.12 ms, and in **4 of the 6
runs** that carried the noise term past the ceiling, so the tail reported UNKNOWN and the run
was decided on the median alone — printed in the verdict line as `tail not judgeable this
run (median-only verdict)`, never silently. The median was untouched by the same load:
+19…+35 µs against an unmoved 250 µs budget.

That is the trade recorded in §3, with its price attached: **on a contended runner the tail
check degrades to nothing about 2 runs in 3, and minority-incidence coverage is lost for
those runs.** It is not lost quietly, and it does not cost the run its verdict.

**The tail can still fail under that load**, which is what stops the degradation from being a
loophole. Same load, `TestAX06_LatencyGateFailsOnMinorityIncidenceRegression` run six times:
**6 of 6 turned the gate red.** With the ~8 ms per-incident injection, the executor's tail
overhead reached 2.8–6.3 ms against budgets that clamped at 250 µs–1.4 ms, so it cleared both
the clamp and three times the run's own resolution — the FAIL-before-degraded ordering in §2
doing exactly the job it was added for. Without that ordering, an earlier build of this
amendment let a **4.97 ms** overhead pass on a run whose control had widened past the
ceiling; that is the specific hole the ordering closes.

### 5.7 Scope of these numbers

Same-process, same-run A/B on one machine and one fixture, per §3. They are evidence that
the seam is cheap and that the instrument measuring it is trustworthy. They are not a
published performance figure, they are not comparable across machines, and they do not enter
`docs/eval/`.

### 5.8 The round-collapse rule, measured (SW-242 round 3)

The correction in §2 had two candidate shapes, and the choice between them is a measurement,
not a preference. Both stop a median-only round from ending the loop and from producing a
PASS; they differ only in what happens when one round recorded a FAIL and another could not
judge the tail:

- **(A) FAIL outranks median-only** — the tail-caught regression survives to red.
- **(B) median-only ranks with UNKNOWN** — the run is UNKNOWN and the FAIL is quoted, held
  rather than withdrawn. This is what shipped.

`TestAX06_LatencyArbitrationOnPureNoise` drives both against the superseded rule over 2 000
three-round trials per condition, on a heavy-right-tailed noise model whose observables are
pinned against this page's own measurements by `TestAX06_LatencyNoiseModelIsCalibrated`
(idle: p50 386 µs, p50 A/A control ≤ 4.4 µs, p95 A/A control 7.5 µs median / 30 µs p90 —
compare §5.1's 385 µs, 0.25–3.4 µs, 6–28 µs; contended: p95 A/A control 363 µs median /
902 µs p90 with the median budget still on its 250 µs floor — compare §5.6's 297 µs–1.12 ms).

The shipped and superseded columns below are the figures that test logs on every run. The
**rejected rule (A)** column was measured the same way, with a temporary probe carrying that
rule instead — it is not committed, because the repository does not keep an implementation of
a rule it rejected; the probe differed from the committed test only in the arbitration
function it called and in using 6 000 trials per condition.

**No regression injected — every FAIL here is false:**

| condition | superseded rule | shipped rule (B) | rejected rule (A) |
|---|---|---|---|
| idle | PASS 100 %, FAIL 0 % | PASS 100 %, FAIL 0 % | FAIL 0 % |
| lightly loaded | PASS 100 %, FAIL 0 % | PASS 99.95 %, UNKNOWN 0.05 %, FAIL 0 % | FAIL 0.03 % |
| **contended as measured** | PASS 99.95 %, **FAIL 0.05 %** | PASS 75.75 %, UNKNOWN 24.20 %, **FAIL 0.05 %** | **FAIL 10.4 %** |
| worse than measured | PASS 99.95 %, FAIL 0.05 % | PASS 59.95 %, UNKNOWN 40.00 %, FAIL 0.05 % | FAIL 13.7 % |

**2 ms per incident on one call in eight — every PASS here is a missed regression:**

| condition | superseded rule | shipped rule (B) |
|---|---|---|
| idle | PASS 0 %, FAIL 100 % | PASS 0 %, FAIL 100 % |
| lightly loaded | PASS 2.75 %, FAIL 97.25 % | **PASS 0 %**, FAIL 97.25 %, UNKNOWN 2.75 % |
| contended as measured | **PASS 85.5 %**, FAIL 14.5 % | **PASS 7.8 %**, FAIL 14.5 %, UNKNOWN 77.7 % |
| worse than measured | PASS 99.55 %, FAIL 0.45 % | PASS 55.5 %, FAIL 0.45 %, UNKNOWN 44.05 % |

Read together: the shipped rule cuts the rate at which a real minority-incidence regression
is reported as a pass by an order of magnitude on the runner that motivated this story, and
it does so at **exactly zero** false-FAIL cost — the FAIL columns are identical to the
superseded rule's, which is the theorem in §2 rather than a fortunate sample. The rejected
rule buys more red (84.4 % FAIL at 2 ms/1-in-8 on the contended condition, against 14.5 %)
for a 200× worse false-FAIL rate, and a gate that reds one clean PR in ten is the failure
this story exists to remove. A corroboration variant — a FAIL standing only when two rounds
recorded it — was also measured: 51.5 % FAIL for a 1.95 % false-FAIL rate, still 39× the
superseded rule's, and also rejected.

The incidence boundary is unaffected by the change, re-measured through the shipped path
after it: 1 in 18 (5.6 %) FAILs with a tail overhead of 8.56 ms against a 250 µs budget, 1 in
20 (5.0 %) passes with 94.6 µs — the same boundary §5.5 recorded, because the round-collapse
rule does not touch the per-statistic arithmetic that sets it.

### 5.9 The decisiveness boundary, measured (SW-242 round 4)

`TestAX06_LatencyArbitrationOnPureNoise`, 2 000 three-round trials per condition, every arm
of every round drawn from one distribution unless a regression is stated — so **every FAIL on
a no-regression row is a false FAIL by construction**. Three rules on the *same* trials:
`shipped` is the round-3 rule PR #169 failed under, `round 4` is the rule this page now
describes, and `any FAIL` is the rejected variant in which any recorded FAIL outranks an
unjudgeable round.

The noise levels are the ones §5.8 calibrated against real measurements, plus one new level
calibrated to **PR #169's own log**: a 3.24268 ms p50 baseline (round 3 printed a 324.268 µs
fixed bar, which is 10 % of it) and a p50 A/A control wide enough to reach the 442.871 µs that
collapsed that round's median. `TestAX06_LatencyNoiseModelIsCalibrated` pins both.

**False FAIL, clean tree (lower is better):**

| condition | shipped | **round 4** | rejected "any FAIL" |
|---|---|---|---|
| idle | 0.00 % | **0.00 %** | 0.00 % |
| lightly loaded | 0.00 % | **0.00 %** | 0.05 % |
| **contended, as §5.6 measured** | 0.05 % | **0.05 %** | **9.75 %** |
| worse than measured | 0.05 % | **0.05 %** | 14.00 % |
| PR #169's runner | 0.00 % | **0.00 %** | 13.70 % |

The round-4 rate is **unchanged from the rule it replaces on every condition**, against 195×
for the rejected variant on the contended one. That is the whole point of the marginal /
decisive split: "any FAIL always stands" was measured and rejected for good reason in round 3,
and this correction does not reintroduce it.

The *literal* form of the round-2 rule — past the clamp and past 3× **this statistic's own**
control — was implemented first and measured at **2.365 %** false FAIL on the contended
condition (20 000 trials), a 24× regression. `refDelta` is `|a − b|` from a single pair of A/A
arms and is sometimes near zero; 3× a lucky-narrow control is not a bar. Taking the *widest*
control the run demonstrated at that percentile is what brings it back to 0.05 %.

**What it buys, same trials, regression injected on one call in eight:**

| condition | injected | shipped FAIL | **round 4 FAIL** | shipped UNKNOWN | **round 4 UNKNOWN** |
|---|---|---|---|---|---|
| contended, as measured | 2 ms | 14.50 % | 14.50 % | 77.70 % | 77.70 % |
| worse than measured | 20 ms | 97.35 % | **98.80 %** | 2.65 % | **1.20 %** |
| **PR #169's runner** | 20 ms | 37.60 % | **42.55 %** | 62.25 % | **57.30 %** |

The gain is concentrated exactly where the defect was: a large regression on a runner whose
median control collapses. It is small on the conditions where the median survives, which is
correct — there was nothing to fix there, and a rule that moved those numbers would be moving
them for the wrong reason.

**The sequence itself.** `TestAX06_LatencyGateFailsThePR169CISequence` reconstructs PR #169's
three rounds from the numbers the job printed, asserts they reproduce every printed
observable, and asserts the before and the after:

| slate | superseded rule | round 4 |
|---|---|---|
| round 3 alone (p50 UNKNOWN, p95 FAIL at 42× budget) | UNKNOWN | **FAIL** |
| round 1 + two rounds that resolved nothing | UNKNOWN | **FAIL** |
| the full CI sequence (FAIL, FAIL, UNKNOWN) | **UNKNOWN** (green in the rollup) | **FAIL** |
| the same with a *marginal* tail FAIL instead | UNKNOWN | **UNKNOWN** (the boundary holds) |

Every p95 overhead in that job — 79.556457 ms, 92.47372 ms, 74.651076 ms — clears 3× its own
ceiling by 8×, 11× and 11× even under the most conservative reconstruction of the ceiling
available from the log.

---

## 6. The dual-run cost of the shipped default (SW-244, 2026-08-28)

SW-244 moves the compiled-in kill-switch default from `legacy` to `shadow`, which makes the
**shadow arm the default path** for the ten migrated operations. §1–§3 are **not amended**:
the bar is untouched, the gate still judges the executor arm, and §3's exemption of shadow's
*total* still stands for the reason it always gave — shadow runs both paths by construction,
so ~2× legacy is its correct behaviour and gating the total would be either vacuous or a
standing invitation to weaken the comparison the position exists to perform.

What this section adds is the measurement AC-2 of that story requires and the question §3
left unasked, which is the one that actually decides whether the default may move:

> How much of shadow's cost is **not** explained by "both paths ran"?

### 6.1 The residue, and the bar it is held to

At each gated percentile, with the same four arms, the same rotation and the same run:

```
baseline    = (legacy-a + legacy-b) / 2      the pooled legacy centre
accounted   = baseline + executor            what running both paths costs
unaccounted = shadow − accounted             the residue: comparison + recorder hand-off
```

`unaccounted` is judged by **`canaryLatencyBudget`** — literally the function §2's rule
calls, extracted by SW-244 so there is exactly one copy. Same 10 %/250 µs fixed term, same
3× same-run noise term, same 4× ceiling, same three-valued verdict, same evaluation order.
`TestSW244_ShadowAccountingSharesTheGateBudget` asserts the two agree across a sweep of
baselines and controls, so softening the accounting would require softening the AX-06 gate
itself. That is deliberate: SW-244 introduces the cost being judged, and a story does not get
to pick the budget that judges its own cost.

**Where this is lenient, stated rather than hidden.** At the median the arithmetic is sound —
the median of a sum of two costs is close to the sum of their medians. At the tail it is
**generous**: `p95(legacy) + p95(executor)` overestimates `p95(legacy + executor)`, because
the two arms' slow calls need not coincide inside the same shadow call. So `accounted` at p95
is too large, the residue is understated, and a healthy run reads **negative** there. The
consequence is asymmetric and is the reason the median carries this check: a p95 FAIL is
strong evidence, a p95 PASS is weak.

### 6.2 Measured — `TestSW244_ShadowDefaultCostIsAccounted`, round 1

Apple M2 Max, darwin/arm64, N = 200 per arm after 20 warm-up, rotation balanced, one round
(the first round passed, so no second was taken).

| arm | p50 | p95 |
|---|---|---|
| legacy-a | 383.125 µs | 761.500 µs |
| legacy-b | 382.834 µs | 774.542 µs |
| **legacy baseline** (pooled) | **382.979 µs** | **768.021 µs** |
| executor | 391.083 µs | 769.959 µs |
| **shadow** (the new default path) | **783.542 µs** | **1.265625 ms** |

| statistic | shadow vs legacy | accounted | **unaccounted** | budget | verdict |
|---|---|---|---|---|---|
| p50 | +400.563 µs (**2.05×**) | 774.062 µs | **+9.480 µs** | 250 µs (fixed 250 µs, noise 3×291 ns = 873 ns, ceiling 1 ms) | **PASS** |
| p95 | +497.604 µs (**1.65×**) | 1.537980 ms | **−272.355 µs** | 250 µs (fixed 250 µs, noise 3×13.042 µs = 39.126 µs, ceiling 1 ms) | **PASS** |

**The whole of the dual-run cost is the second path.** The residue at the median — the
byte comparison, the error-class comparison and the recorder hand-off together — is
**9.5 µs, about 2.5 % of one legacy call, against a 250 µs budget it uses 3.8 % of.** At the
tail the residue is negative, which is the shape §6.1 predicts for a run with nothing wrong
with it.

The AX-06 gate's own verdict is unchanged by this story, because the gate sets each arm's
position explicitly and never reads the default. A separate run of
`TestAX06_ExecutorSeamLatencyWithinThreshold` on the same fixture and machine, taken the same
day: `p50 PASS overhead=6.085 µs budget=250 µs (legacy baseline 387.124 µs, executor
393.209 µs)`, `p95 PASS overhead=16.896 µs budget=250 µs (legacy baseline 795.313 µs,
executor 812.209 µs)` — one round, no retry. Its shadow arm read p50 788.333 µs / p95
1.280334 ms, reproducing §6.2's numbers on an independent run.

### 6.3 CPU and allocation — `BenchmarkCanaryDispatch`

`go test ./surfaces/client/ -run '^$' -bench BenchmarkCanaryDispatch -benchtime 300x -count=3`,
same machine. Medians of the three runs:

| position | ns/op | B/op | allocs/op |
|---|---|---|---|
| legacy | 446 048 | 456 036 | 1 905 |
| active (executor only) | 463 359 | 463 398 | 1 989 |
| **shadow (new default)** | **916 012** | **919 375** | **3 894** |
| shadow ÷ legacy | **2.05×** | **2.02×** | **2.04×** |
| shadow − (legacy + active) | **+6.6 µs** | **−59 B** | **0** |

The allocation residue is **exactly zero**: shadow allocates 3 894 objects and the two arms
it runs allocate 1 905 + 1 989 = 3 894 between them. That is the expected result and it is
worth saying why it is expected rather than lucky — the comparison is `bytes.Equal` plus an
error-class check, and the recorder is handed **rendered strings only on a mismatch**
(`canary.go`, "Why observations and not only mismatches"), so an agreeing call allocates
nothing for the record beyond the counters it increments.

### 6.4 The stop condition, and what would have triggered it

AC-3 of SW-244 is a hard stop: if the measured cost had exceeded what this bar permits, the
story stops and reports rather than widening the budget. **It was not triggered** — 9.5 µs
against 250 µs at the median, negative at the tail.

That the check *can* trigger is not asserted in prose.
`TestSW244_ShadowAccountingCatchesUnexplainedCost` runs two extra executor passes inside the
shadow arm's timed window — real seam code, same clock, same rotation — and requires the
shipped decision path to return **FAIL**, not UNKNOWN. Measured: `p50 FAIL shadow=1.961375 ms
(4.98× legacy) accounted=796.709 µs unaccounted=1.164666 ms budget=250 µs`.
`TestSW244_ShadowAccountingDecisionRule` pins the boundaries — a clean dual run passes, a
negative tail residue passes, +200 µs inside the floor passes, +600 µs fails, a degraded
control is UNKNOWN and not a pass, and a gross residue fails even on a degraded control.

### 6.5 Scope

§5.7 applies unchanged: same-process, same-run A/B on one machine and one fixture. These are
evidence that the dual run costs one extra legacy-plus-executor call and essentially nothing
else. They are not a published performance figure, they are not comparable across machines,
and they do not enter `docs/eval/`.

## 7. The shipped default off the caller's critical path (SW-245, 2026-08-28)

§6 ended with a number the product had to live with: `shadow` is the shipped default, and it
cost **2.05× legacy at p50**, of which essentially all was the second path running on the
caller's thread (residue +9.5 µs, zero allocations). SW-245 moves the second path off that
thread. The caller returns on the legacy result the moment the legacy method answers, and the
comparison — the same comparison, on the same canonical bytes, with the same error-class
check — runs afterwards on a bounded worker queue.

§1–§3 are again **not amended**. `canaryLatencyBudget`, the 250 µs floor, the 10 % relative
term, the 3× same-run noise term and the `budget ≤ 4×fixedBar` clamp are untouched, and the
AX-06 gate still judges the **executor** arm exactly as it did. What SW-245 adds is a bar on a
statistic §3 deliberately did not gate: shadow's **total**, as a ratio to legacy.

### 7.1 Why shadow's total becomes gateable now

§3 exempted shadow's total because shadow ran both paths *by construction*, so ~2× was its
correct behaviour and holding it to a number would have been either vacuous or a standing
invitation to weaken the comparison. That reasoning rested on a fact SW-245 changes: the
caller no longer runs the second path. There is now a number the caller's cost can be held to
without touching what is compared, and `canaryShadowRatioBar` is it — **1.15× legacy at p50**,
the story's AC-1.

It is a **separate** bar, not a widened one. `evaluateCanaryShadowRatio` is a new function
judging a new statistic; it does not call, extend, or relax `canaryLatencyBudget`. It does
borrow SW-242's resolution discipline: a run whose own A/A control differs by as much as the
bar being tested cannot judge that bar, and answers **UNKNOWN**, never PASS.

### 7.2 Measured — `TestSW245_ShadowIsOffTheCallersCriticalPath`, round 1

Apple M2 Max, darwin/arm64, N = 200 per arm after 20 warm-up, rotation balanced, one round
(the first round passed, so no second was taken). Same fixture, same sampler, same rotation as
§5 and §6 — the story requires the number under SW-242's recalibrated method, and the way to
guarantee that is to call SW-242's own sampler.

| statistic | legacy baseline (pooled) | shadow | ratio | same-run A/A control | verdict |
|---|---|---|---|---|---|
| p50 | 429.271 µs | **417.834 µs** | **0.973×** | 10.874 µs (1.025×) | **PASS** |
| p95 | 924.374 µs | **848.500 µs** | **0.918×** | 43.833 µs (1.047×) | **PASS** |

Against §6.2's recorded before-state, under the same method on the same machine:

| statistic | before (SW-244 §6.2) | after (SW-245) |
|---|---|---|
| p50 | 783.542 µs, **2.05×** legacy | 417.834 µs, **0.973×** legacy |
| p95 | 1.265625 ms, **1.65×** legacy | 848.500 µs, **0.918×** legacy |

Read the "after" ratios honestly: shadow measuring *below* legacy is not evidence that
dispatching in shadow is faster than dispatching in legacy. It is what "indistinguishable"
looks like on this instrument — both ratios sit inside the run's own A/A control band
(1.025× at p50, 1.047× at p95), which is the resolution the run achieved. The defensible
claim is that the caller's cost in `shadow` is **no longer separable from legacy at this
resolution**, not that it is negative.

**What this instrument cannot separate — read the two baselines carefully.** The pooled
legacy baseline moved from **382.979 µs** in §6.2 to **429.271 µs** here, +12 %. It would be
convenient to write that off as machine state, and the run gives no basis to. The sampler
does **not** drain between samples: after each `shadow` sample the worker runs a full
executor pass (plus a `NewExecutor`) *concurrently with the next arms' timed calls*, so every
arm in the after-run — including both legacy control arms — is measured on a machine carrying
that load. The same-run A/A control cannot detect it either, because both control arms carry
it equally. So the honest statement is that the +12 % shift is **unattributed**: this
instrument cannot say how much of it is the worker and how much is the machine, and neither
reading is claimed here.

The instrument is deliberately left that way rather than drained between arms. The concurrent
worker load is not an artefact of the test — it is what the shipped `shadow` default actually
does to a host — so keeping it in makes the after-run's baseline a *loaded-machine* baseline
and the 0.973× a caller-perceived ratio measured under the load shadow really imposes. That
is the conservative direction for AC-1: draining would remove real cost from the comparison
and flatter the result. It also does not threaten the verdict, which survives the bias in
either direction — the ≤1.15× bar is met at 0.973×, and §7.3's injected synchronous dual run
still reads 2.7× on the same instrument. What must not be read off this table is a
before/after comparison of the *baselines* themselves; the comparison that carries the claim
is shadow against legacy **within** each run.

The AX-06 gate itself, measured in the same session and unchanged by this story:
`p50 PASS overhead=4.646 µs budget=250 µs (legacy baseline 426.229 µs, executor 430.875 µs)`,
`p95 PASS overhead=−20.625 µs budget=250 µs` — one round, no retry. SW-244's accounting check
also still passes and now reads a large negative residue at p50 (`shadow=407.333 µs (0.96×
legacy) accounted=844.479 µs unaccounted=−437.146 µs`), which is the arithmetically expected
consequence of subtracting an executor pass the caller no longer makes. That check has not
become useless — `TestSW244_ShadowAccountingCatchesUnexplainedCost` still requires it to go
red on cost injected into the shadow window — but its p50 residue is no longer the interesting
number, and §7.3's ratio is.

### 7.3 The bar can go red — `TestSW245_ShadowRatioCatchesASynchronousDualRun`

A bar that cannot fail is not evidence. The injected regression here is the sharpest one
available, because it is exactly the behaviour this story removed:
`canaryLatencyExtraSeamPasses(1)` runs one whole extra executor pass **inside the shadow arm's
timed window**, which is — to within the enqueue — what `shadow` cost before SW-245.

Measured: `p50 FAIL shadow=1.140166 ms legacy=421.083 µs ratio=2.708× bar=1.15× (same-run A/A
control 4 µs = 1.009×)`, round 1, no retry needed.

The AX-06 gate's own injected-regression proof (AC-5 of the story) is unchanged and still red
on both shapes: `seam_cost_doubled` → `p50 FAIL overhead=539.021 µs budget=250 µs`,
`seam_cost_quadrupled` → `p50 FAIL overhead=1.566437 ms budget=250 µs`, three rounds each.

`TestSW245_ShadowRatioDecisionRule` pins the boundaries without owning a quiet machine: under
the bar passes, exactly at the bar passes, a synchronous dual run fails, faster-than-legacy
passes, a control as wide as the bar reads UNKNOWN, an empty arm reads UNKNOWN and never PASS.

### 7.4 What the move did NOT make cheaper — `BenchmarkCanaryDispatch` (AC-6)

The second path still runs. It still costs the machine a whole executor pass in CPU and
allocations, and the only thing that changed is **who waits for it**. Stating that plainly is
AC-6 of the story, because a cost that stops showing up as caller latency is exactly the kind
that gets quietly dropped from a record.

`go test ./surfaces/client/ -run '^$' -bench BenchmarkCanaryDispatch -benchtime 500x -count=5`,
Apple M2 Max (12 logical CPUs), medians of five runs. Note what each column now measures:
**ns/op is the caller's cost only** — the timer no longer covers the second path — while
**B/op and allocs/op are process-wide** deltas and therefore still include everything the
worker allocated. The benchmark drains before reading its counters (timer stopped), so no
backlog escapes the accounting, and it reports `skipped/op` so an arm that dropped
comparisons cannot silently look cheap.

| position | ns/op | B/op | allocs/op | skipped/op |
|---|---|---|---|---|
| legacy | 467 005 | 455 952 | 1 905 | 0 |
| active (executor only) | 476 388 | 463 272 | 1 989 | 0 |
| **shadow (shipped default)** | **588 868** | **921 514** | **3 867** | 0 |
| shadow ÷ legacy | **1.26×** | **2.02×** | **2.03×** | — |
| shadow − (legacy + active) | −54.5 µs | +2 290 B | **−27** | — |

Three things this table says, and one it does not:

* **The allocation cost is undiminished.** 2.03× legacy, against SW-244's 2.04×. Moving work
  to another goroutine does not stop it allocating. The deferral's own overhead — the job
  struct, the queue slot, and the copy of the legacy result the worker compares against — is
  **−27 allocations** relative to `legacy + active`, i.e. zero within this instrument's
  run-to-run spread (±20 allocs across the five runs). The copy is one allocation per
  dispatch and is real; it is simply two orders of magnitude below the ~1 935 the executor
  pass itself makes.
* **Under a saturating single caller the caller still feels part of it.** 1.26× ns/op is the
  cost of a tight back-to-back loop on one goroutine while the worker allocates at the same
  rate on another: allocator and GC back-pressure, not the dual run. This is a *different
  measurement from AC-1's* — AC-1 names SW-242's rotating method, where the machine has
  headroom and shadow is one arm of four — and both are reported because both are true. The
  honest summary is that the deferral removes the *serialisation*, not the *work*.
* **With no headroom at all it is worse, and the disclosure fires.** Same benchmark at
  `GOMAXPROCS=1`, `-benchtime 300x -count=3`, medians: legacy 628 775 ns/op, active
  635 046 ns/op, shadow **1 186 493 ns/op — 1.89× legacy**, and one of the three runs reported
  `0.01667 skipped/op` (5 comparisons of 300 dropped). That is the design behaving as
  specified rather than a defect: a single-CPU process cannot both serve and compare, so the
  bounded queue fills, the drops are counted, and `graphi doctor -divergence` prints them as a
  coverage gap. **An operator on a one-CPU box should expect partial coverage and see it
  stated.**

What the table does **not** say is that shadow is free. It says the caller stopped waiting.

### 7.5 Coverage is part of the measurement (AC-4)

Because the queue is bounded, a latency number taken over an arm that dropped comparisons
would be a number for less work than the position actually does. Every SW-245 instrument
therefore reports skips: the benchmark as a `skipped/op` metric, the AC-1 test as a
`SW-245-SHADOW-RATIO-COVERAGE` log line. Both read zero in the measurements above except the
`GOMAXPROCS=1` run noted there. A ratio recorded beside a non-zero skip count is not
comparable to one recorded beside a zero.

Three causes can raise that count, and only one of them is about load:
`queue-full` (the bound doing its job), `drain-abandoned` (a shutdown that ran out of budget)
and `caller-cancelled` — a caller that hung up or timed out *while the legacy method was
still running*, whose legacy outcome is `context canceled` rather than a result and therefore
has nothing comparable in it. The last one never appears in a latency run (nothing cancels
these callers) but it is counted the same way everywhere else, because a comparison that did
not happen is a coverage gap however it failed to happen. `docs/executor-seam-rollback.md` §5
is the operator-facing version.

### 7.6 Scope

§5.7 applies unchanged: same-process, same-run A/B on one machine and one fixture. These are
evidence that the caller no longer pays for the second path under the method AC-1 names, and
that the second path still costs the machine what it always did. They are not a published
performance figure, they are not comparable across machines, and they do not enter
`docs/eval/`.
