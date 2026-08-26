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

_(Appended after §1–§3 were committed. See the story's verification record for the raw run.)_

<!-- MEASUREMENT-APPENDED-BELOW -->
