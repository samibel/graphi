# AX-06 — canary latency threshold (fixed **before** measuring)

**Story:** SW-226 (AX-06) · **Spec:** extension-platform-kernel · **Canary:** `dead_code`

This page exists so the acceptance bar for SW-226 AC-5 is a **prediction**, not a
description. It is committed on its own, ahead of the implementation and ahead of any
number, so that `git log --follow` shows the threshold predates the measurement it judges.
A threshold written after the run is not a threshold; it is a transcript.

The measured numbers are appended to §4 in a later commit on the same branch. Nothing in
§1–§3 may be edited once a measurement exists — if the bar turns out to be wrong, that is a
finding to record, not a bar to move.

---

## 1. What is measured

One `dead_code` invocation, end to end through `surfaces/client`, against the shared
`surfaces` fixture graph (the same fixture `surfaces/agentintel_golden_test.go` uses), on a
warm store. Three paths are timed:

| Path | Kill-switch position | What runs |
|---|---|---|
| **legacy** | `legacy` | `Client.DeadCode` — today's dispatch, the baseline |
| **executor** | `active` | `Executor.Execute("dead_code")` — catalog lookup, JSON argument round trip, typed decode, adapter call |
| **shadow** | `shadow` | both of the above, plus the byte/error comparison |

Sample size **N = 200** timed iterations per path after **20** warm-up iterations, single
goroutine, reported as **p50** and **p95** of the per-call wall clock.

**Anti-flake provision** (added before any measurement existed, in the same pre-measurement
commit series): the gate repeats the whole A/B up to **3 rounds** and passes if **any** round
meets §2. A seam that is genuinely slower fails all three; a single round lost to a
scheduler hiccup or a GC pause on a shared CI runner does not turn a correctness gate into a
coin flip. The recorded numbers in §4 are from the first round regardless of which round
passed, so the record is not cherry-picked.

## 2. The gate (AC-5)

> The executor path passes when
>
> **p95(executor) − p95(legacy) ≤ max( 10 % of p95(legacy), 250 µs )**

Both terms are deliberate:

- The **relative** 10 % term is the real bar and the one that matters on a repository-sized
  graph, where `dead_code`'s whole-graph node+edge pass dominates and the executor seam is
  rounding error.
- The **absolute** 250 µs term is a floor, not a loophole. The fixture graph answers in well
  under a millisecond, and at that scale a 10 % band is narrower than scheduler and GC
  noise, so a purely relative gate on this fixture would measure the machine rather than the
  change. 250 µs bounds what the *seam itself* can add — one memoised catalog lookup, one
  `json.Marshal`/`json.Unmarshal` of a single-integer argument struct, one map lookup, one
  interface call — with room to spare. If the seam ever needs more than that, the design is
  wrong and the number should fail rather than be raised.

The gate is the **max** of the two, i.e. whichever is more permissive on the day. That is
stated up front so that reading the result cannot become a choice between two rules.

## 3. What is deliberately NOT gated

**Shadow mode.** It runs both paths by construction, so ~2× legacy is its *correct*
behaviour, not a regression. Gating it would either be vacuous or would pressure someone to
weaken the comparison it exists to perform. It is measured and recorded in §4 for
information, and its cost is the reason the shipped default is documented and revisitable
rather than assumed.

**Cold-start and first-call costs.** `opcatalog.Shadow()` decodes and freezes the embedded
catalog exactly once per process (`sync.OnceValues`). That one-off is a process-startup cost
already paid by SW-225's descriptor projection, and attributing it to the canary would
double-count it.

**Anything measured on a different machine or a different fixture.** The numbers in §4 are a
same-process, same-run A/B on one machine. They are evidence that the *seam* is cheap; they
are not a published performance figure, and they do not belong in `docs/eval/`, whose
harness and provenance rules (`docs/eval/hero-protocol.md`) govern published numbers.

## 4. Measurement

Measured 2026-08-27 on darwin/arm64, Apple M2 Max, `CGO_ENABLED=0`, Go test binary, machine
otherwise idle. Instrument: `surfaces/client/canary_latency_test.go`.

```
CGO_ENABLED=0 go test ./surfaces/client/ -run TestAX06_ExecutorSeamLatencyWithinThreshold -v
CGO_ENABLED=0 go test ./surfaces/client/ -bench BenchmarkCanaryDispatch -benchtime 300x
```

### 4.1 Per-call latency, first round of four consecutive runs

| Path | p50 | p95 |
|---|---|---|
| legacy | 435.7 µs | 816.5 µs |
| executor (`active`) | 385.1 µs | 778.7 µs |
| shadow (both, not gated) | 774.4 µs | 1231.8 µs |

`p95(executor) − p95(legacy)` = **−37.9 µs**. Budget = max(10 % = 81.7 µs, 250 µs) = **250 µs**.
**PASS**, on the first round, with the whole 250 µs unused.

### 4.2 Four consecutive runs, the honest spread

| Run | legacy p95 | executor p95 | difference | budget | verdict |
|---|---|---|---|---|---|
| 1 | 816.5 µs | 778.7 µs | −37.9 µs | 250 µs | pass |
| 2 | 843.5 µs | 762.5 µs | −80.9 µs | 250 µs | pass |
| 3 | 746.2 µs | 785.3 µs | +39.1 µs | 250 µs | pass |
| 4 | 778.5 µs | 811.0 µs | +32.5 µs | 250 µs | pass |

The difference changes **sign** between runs. That is the finding, and it is worth stating
plainly rather than reporting the favourable run: at this fixture size the executor seam's
cost is **smaller than the run-to-run noise of the operation it wraps**, so the honest
conclusion is "indistinguishable from legacy", not "faster". The ±80 µs spread is also the
retrospective justification for §2's 250 µs floor — a purely relative 10 % gate (≈ 78 µs
here) would have failed run 2's *negative* twin as readily as a real regression, which is
exactly the machine-measuring failure §2 predicted.

### 4.3 What the seam actually costs, from the allocation counters

`go test -bench BenchmarkCanaryDispatch -benchtime 300x`:

| Position | ns/op | B/op | allocs/op |
|---|---|---|---|
| legacy | 498 116 | 456 085 | 1 905 |
| `active` | 454 804 | 460 569 | 1 975 |
| `shadow` | 938 202 | 916 401 | 3 879 |

Allocations are the stable signal wall-clock is too noisy to give: the executor seam adds
**+70 allocations and +4.5 KB per call** — the catalog lookup's spec copy, the argument
struct's `json.Marshal`/`json.Unmarshal` round trip, and the request value — against an
operation that already allocates ~1 900 times and ~456 KB doing its whole-graph pass. That
is **+3.7 % allocations on a +0 % latency budget line**, and it is the number a future
regression should be compared against.

`shadow` is 1.88× legacy in time and 2.01× in allocations, which is what running both paths
costs and is exactly why it is a switch position rather than the only behaviour.

### 4.4 Scope of this number

Same-process, same-run A/B on one machine and one fixture, per §3. It is evidence that the
seam is cheap. It is not a published performance figure, it is not comparable across
machines, and it does not enter `docs/eval/`.
