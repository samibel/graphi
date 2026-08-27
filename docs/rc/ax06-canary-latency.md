# AX-06 — canary latency threshold (fixed **before** measuring)

**Story:** SW-226 (AX-06) · **Spec:** extension-platform-kernel · **Canary:** `dead_code`
**Amended:** SW-242 (2026-08-28) — §1 and §2 replaced; see "Amendment record" below.

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
code rather than of a lucky reproduction — `TestAX06_LatencyRotationIsBalanced` asserts the
balance directly so a future edit to the loop cannot silently lose it.

**The same-run reference.** `legacy-a` and `legacy-b` execute byte-identical code. The
difference between their medians is therefore, by construction, **pure measurement noise**:
it is this run's demonstrated inability to tell two identical paths apart. Every run
consequently carries its own null control, taken on the same machine, in the same rotation,
under the same load, at the same moment. The bar in §2 is expressed against it.

**The statistic is the median.** p95 is a tail statistic and contention is a tail
phenomenon — preemption and co-tenant interference add a heavy right tail while barely
moving the centre. A genuine seam regression is the opposite shape: the seam runs on *every*
call, so it is a location shift that moves the whole distribution, median included. Judging
at the median therefore reads the seam where its cost lives and reads the runner's noise
where it is weakest. p95 is still measured and still reported for every arm, because it is
the number that describes what a caller feels; it is simply not the number the gate reads.

**Anti-flake provision** (from the original §1, retained): the gate repeats the whole
measurement up to **3 rounds** and passes if **any** round meets §2. The recorded numbers
are from the first round regardless of which round passed, so the record is not
cherry-picked.

## 2. The gate (AC-5, recalibrated by SW-242)

Let `base` and `ref` be the medians of the two legacy arms, `exec` the median of the
executor arm, and

```
baseline  = (base + ref) / 2
overhead  = exec − baseline
refDelta  = |base − ref|                            the same-run reference measurement
fixedBar  = max( 10 % of baseline , 250 µs )        the machine-independent bar
noiseTerm = 3 × refDelta                            the signal-to-noise requirement
ceiling   = 4 × fixedBar                            the hard clamp
budget    = min( max( fixedBar , noiseTerm ) , ceiling )
```

> The run is **UNKNOWN** when `noiseTerm > ceiling`, or when an arm produced no samples.
> Otherwise the executor path **passes** when `overhead ≤ budget`, and **fails** otherwise.

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
  `budget ≤ 4 × fixedBar` is an invariant of the code and not a property of the inputs
  (`TestAX06_LatencyDecisionRule` asserts it on every case). A runner cannot talk the gate
  into tolerating an arbitrary regression by being noisy: past the clamp the available
  answers are FAIL and UNKNOWN, never PASS.

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

- **Rounds collapse in this order:** any PASS round wins (the anti-flake provision). Failing
  that, an UNKNOWN round beats a FAIL round — if the machine was at any point too degraded
  to distinguish two identical paths, the honest answer to "did the seam regress" is that
  this run does not know, not that it did.

**The gate must still be able to fail, and that is tested, not asserted.**
`TestAX06_LatencyGateFailsOnInjectedSeamRegression` injects a real slowdown at the real
seam — the executor seam's own code, run extra times inside the timed window — and requires
the shipped decision path to return FAIL, specifically FAIL and not UNKNOWN. §5.3 records
its output. Removing this gate, raising the budget until nothing can fail, or reducing it to
a warning were all considered and rejected (SW-242 AC-5); the recalibration exists so the
gate can be *believed*, which is worth nothing if it cannot go red.

## 3. What is deliberately NOT gated

**Shadow mode.** It runs both paths by construction, so ~2× legacy is its *correct*
behaviour, not a regression. Gating it would either be vacuous or would pressure someone to
weaken the comparison it exists to perform. It is measured and recorded for information, and
its cost is the reason the shipped default is documented and revisitable rather than
assumed.

**p95, and every other tail statistic.** Recorded for all four arms, never gated, for the
reason in §1: the tail is where the runner lives and the median is where the seam lives.
Reporting it and gating it are different jobs.

**Cold-start and first-call costs.** `opcatalog.Shadow()` decodes and freezes the embedded
catalog exactly once per process (`sync.OnceValues`). That one-off is a process-startup cost
already paid by SW-225's descriptor projection, and attributing it to the canary would
double-count it.

**Anything measured on a different machine or a different fixture.** The numbers below are a
same-process, same-run A/B on one machine. They are evidence that the *seam* is cheap; they
are not a published performance figure, and they do not belong in `docs/eval/`, whose
harness and provenance rules (`docs/eval/hero-protocol.md`) govern published numbers.

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
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_LatencyGateReportsUnknownOnDegradedReference -v
```

### 5.1 Idle machine, five consecutive runs

| Run | legacy-a p50 | legacy-b p50 | refDelta | executor p50 | overhead | budget | verdict |
|---|---|---|---|---|---|---|---|
| 1 | 386.7 µs | 384.6 µs | 2.13 µs | 395.3 µs | +9.60 µs | 250 µs | PASS (round 1) |
| 2 | 395.3 µs | 393.7 µs | 1.54 µs | 401.0 µs | +6.48 µs | 250 µs | PASS (round 1) |
| 3 | 382.0 µs | 381.4 µs | 0.63 µs | 390.3 µs | +8.52 µs | 250 µs | PASS (round 1) |
| 4 | 386.5 µs | 383.5 µs | 3.00 µs | 397.3 µs | +12.25 µs | 250 µs | PASS (round 1) |
| 5 | 386.8 µs | 383.3 µs | 3.42 µs | 391.6 µs | +6.58 µs | 250 µs | PASS (round 1) |

Two findings, and the first is the one that matters most.

**The seam's cost is now measurable.** Under the old method the difference changed sign
between runs (§4.2) and the honest answer was "indistinguishable". Under the new one it is
**+6.5 to +12.3 µs, positive in every run** — a real, stable ~2 % of the operation, and in
the same direction and the same order of magnitude as the +3.7 % allocation figure in §4.3
that wall-clock previously could not see. Recalibrating for noise did not blur the picture;
it brought it into focus.

**The apparatus's resolution is ~0.6–3.4 µs**, i.e. under 1 % of the operation. That is
`refDelta`, and it is what makes the 250 µs floor an honest bar on this machine rather than
a coincidence: there are roughly two orders of magnitude between what the instrument can
resolve and what the gate is asked to detect.

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

**New gate (rotating interleave, median), three runs under the same load:**

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

Note what this says about the UNKNOWN branch: it is a genuine last resort, not the expected
answer. Even at 2.2× slowdown the control stayed three orders of magnitude inside the
ceiling. UNKNOWN is reserved for a machine far worse than this one.

### 5.3 The gate still fails — injected regression (AC-3)

`canaryLatencyExtraSeamPasses(n)` runs the executor seam's **own code** *n* extra times
inside the executor arm's timed window: `NewExecutor`, `NewRequest` with its JSON argument
round trip, typed decode, adapter call. It is what "the seam became *n*+1 times as
expensive" actually costs, on the same clock, on the same arm, under the same rotation — not
a sleep standing in for one. The demonstration drives the **shipped** decision path
(`runCanaryLatencyGate` → `evaluateCanaryLatency`, all three rounds), so it proves the gate
and not a parallel copy of it.

| Injection | rounds | executor p50 | baseline | overhead | budget | verdict |
|---|---|---|---|---|---|---|
| seam cost **doubled** (1 extra pass) | 3 of 3 FAIL | 807.5 µs | 385.8 µs | **+421.8 µs** | 250 µs | **FAIL** |
| seam cost **quadrupled** (3 extra passes) | 3 of 3 FAIL | 1.916 ms | 386.1 µs | **+1.530 ms** | 250 µs | **FAIL** |

Both cases fail in **all three** rounds, so the result is not a single unlucky round. The
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

### 5.5 Scope of these numbers

Same-process, same-run A/B on one machine and one fixture, per §3. They are evidence that
the seam is cheap and that the instrument measuring it is trustworthy. They are not a
published performance figure, they are not comparable across machines, and they do not enter
`docs/eval/`.
