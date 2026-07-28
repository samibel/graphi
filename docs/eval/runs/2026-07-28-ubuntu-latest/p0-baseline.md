# P0 performance baseline — the first measured numbers for a graphi candidate

**Candidate:** v0.7.0 at `5815db5b053c2bb1bf3119cdb9939c1dea03cc45`
**Runner class:** `ubuntu-latest` (reference) · **Harness:** `p0-perf/1` · **Scorer:** `p0-aggregate/1`
**Reference scenario:** grpc-go v1.60.1 at `dbbcf5995...`
**Runs:** [30377165970](https://github.com/samibel/graphi/actions/runs/30377165970) (run-a) and [30379259481](https://github.com/samibel/graphi/actions/runs/30379259481) (run-b), both dispatched from `main` at `afa1b68`
**Machine-readable:** [`p0-baseline.json`](p0-baseline.json) · **Raw data:** [`run-a/`](run-a/), [`run-b/`](run-b/)

> **Scope limitation (PRD FR-8).** Every verdict below holds for **the reference
> scenario only** — grpc-go v1.60.1 indexed on the `ubuntu-latest` runner class,
> measured by harness `p0-perf/1`. These numbers are **not a universal
> guarantee**. They say nothing about other repositories, other languages, other
> runner classes, other build flavors, other workloads, or a user's machine.
> uuid, lo, cobra and gin are measured and published in full beside grpc-go, and
> every §12.2 gate over them reads UNKNOWN with that reason named.

---

## The ten gates

| # | Gate (PRD §12.2) | Threshold | run-a | run-b | Verdict |
|---|---|---|---|---|---|
| 1 | Cold index p50 | ≤ 90 s | 19.830 s | 16.939 s | **PASS** |
| 2 | Cold index p95 | ≤ 120 s | 20.368 s | 18.735 s | **PASS** |
| 3 | Peak RSS | ≤ 2 GB | 0.670 GB | 0.664 GB | **PASS** |
| 4 | OOM on an 8 GB host | 0 kills | no kill | no kill | **PASS** |
| 5 | DB size | ≤ 300 MB | 32.688 MB | 32.688 MB | **PASS** |
| 6 | Progress stall p95 | ≤ 2 s | 0.006 s | 0.006 s | **PASS** ⚠ |
| 7 | Warm search p95 | ≤ 100 ms | 3.591 ms | 4.521 ms | **PASS** |
| 8 | Caller/callee/impact p95 | ≤ 200 ms | 0.999 ms | 1.657 ms | **PASS** |
| 9 | Agent context p95 | ≤ 500 ms | — | — | **UNKNOWN** |
| 10 | Freshness p95 | ≤ 2 s | 6.315 s | 6.486 s | **FAIL** |

**8 PASS · 1 FAIL · 1 UNKNOWN · 0 omitted.** Both runs agree on all ten
verdicts. UNKNOWN is not a PASS (PRD §8.2).

---

## The FAIL: freshness is 3.2× over its gate

```
freshness_p95   6.315 s (run-a) / 6.486 s (run-b)   >   2.000 s
                100 of 100 changes applied and converged, both runs
```

This is published as a FAIL (AC-8). **No threshold was moved, no scenario was
eased and no repository was dropped from the run** (PRD §17).

Three things make it a solid result rather than a bad-luck reading:

- **The sample is complete.** 100 of 100 requested incremental changes applied
  *and converged* in both runs. This is not a percentile over a truncated
  sample — the harness had everything it asked for.
- **It is the most reproducible number in the report.** 6.315 s and 6.486 s are
  2.7 % apart, measured on two different CPU families. Every gate that passes
  varies more than the one that fails. The failure is a property of the
  artifact, not of the runner.
- **Profiles were captured automatically.** SW-129 fired on the missed gate and
  wrote CPU, heap, allocation and block profiles, committed at
  [`run-a/freshness/grpc-go/profiles/incremental/`](run-a/freshness/grpc-go/profiles/incremental/)
  and the same path under `run-b/`. They come from a diagnostic *re-execution*
  immediately after the gate was read — same machine, same binary — not from the
  measured run, which is never profiled.

Fixing it is **WP4 work and explicitly out of scope for this story**. It is
recorded in `backlog.md`.

---

## The UNKNOWN: 25 executions short, and it is a defect, not noise

```
agent_context_p95   975 of 1000 required executions pooled, BOTH runs
```

The pool is `agent_brief` / `change_risk` / `explain_symbol` / `related_files`,
and FR-8's floor is 1000 timed executions. It came up **exactly 25 short in both
runs** — an identical shortfall on two different machines is not sampling noise.

The cause is named: `change_risk`, `explain_symbol` and `related_files` return
outcome **`"partial"`** on this corpus, and a partial result is not counted as a
timed execution. These are the same three operations that appear in the
`run_failures` list of every one of the 20 cold runs. The defect is
deterministic and costs 25 executions per 1000.

**The underlying pool's p95 was 471.250 ms in run-a, which is below the 500 ms
threshold — and it is deliberately not published as a PASS.** A percentile over
an undersampled pool is not the measurement the PRD asked for, and quoting it as
though it were is exactly the substitution P0 exists to remove. The gate stays
UNKNOWN until the pool is whole.

---

## The PASS that deserves an asterisk: a 15.5-second stall behind a passing p95

```
progress_stall_p95   0.006 s  ≤  2 s        PASS
longest stall        15.529 s (run-a) / 15.358 s (run-b)
```

The gate is met as PRD §12.2 specifies it, and the property a reader assumes it
protects is not.

The p95 runs over 1088 intervals between 1089 progress events. 1050 of those
intervals are in the `parse` phase, where the median gap is ~0.3 ms. The
distribution is so dominated by short gaps that a single 15.5-second silence
cannot move the 95th percentile — the worst observed silence is roughly **2600×
the threshold** while the gate reads 0.006 s.

A user watching the index would experience that 15.5 s as a hang. The tail is
reproducible too: 15.529 s and 15.358 s, 1.1 % apart. Recorded in `backlog.md`;
**not fixed here, and not hidden**.

---

## Provenance — why these gates are readable at all

A §12.2 gate is only read when `candidate_match` is true: `git rev-parse HEAD`
equals the SHA the evidence index cites, **and** the worktree is clean.
Otherwise every gate is forced to UNKNOWN before a threshold is compared. That
refusal is correct and was not weakened. Both runs satisfy it:

```json
"candidate_sha":   "5815db5b053c2bb1bf3119cdb9939c1dea03cc45",
"measured_sha":    "5815db5b053c2bb1bf3119cdb9939c1dea03cc45",
"candidate_match": true,
"worktree_dirty":  false
```

Each measurement job checks the candidate out, **builds the harness from that
checkout and runs the built binary** — never `go run` from a working tree
(AC-1) — takes the candidate *citation* from the dispatched ref before the tree
moves (a commit cannot contain its own hash), and writes every output outside
the checkout so nothing turns it dirty.

**Every number here recomputes from its raw samples.** Each job reproduced its
own output in CI at the moment it was produced, and all **40** leaf run
directories (2 runs × 4 families × 5 repositories) were re-aggregated from the
committed raw data at publish time. All 40 exited `0`: every published metric
reproduced, zero discrepancies.

```sh
go run ./cmd/eval -aggregate docs/eval/runs/2026-07-28-ubuntu-latest/run-a/cold-index/grpc-go
```

---

## The second run is a clean runner, not a second person (FR-9 substitute)

PRD §16 wants two consecutive complete runs and FR-9 wants independent
reproduction. **The project is solo, so the second run is a second freshly
provisioned GitHub-hosted runner rather than a second person.** FR-9 explicitly
permits "another person *or* a clean runner", and spec decision 3 requires the
substitution to be named wherever the gate is reported — it is named here, in
`p0-baseline.json`, and in the WP2/WP4/M1 rows of `docs/rc/evidence-index.yaml`.

**It is weaker evidence than the original and is never reported as the
original.** What it does rule out: machine-local state. run-b landed on
different silicon — AMD EPYC 9V74 against run-a's Intel Xeon Platinum 8573C —
and reproduced all ten verdicts. What it does **not** rule out: a shared defect
in the harness, the workflow, or the operator's method, since one operator
dispatched both runs from the same repository state. That is precisely what a
second person would independently probe, and it remains unprobed.

### Deviations between the runs, explained rather than smoothed

No deviation was averaged, discarded or reconciled by hand. Both runs are
published whole and separately.

| Gate | run-a | run-b | Δ | Why |
|---|---|---|---|---|
| cold_index_p50 | 19.830 s | 16.939 s | −14.6 % | different host CPU (Intel vs AMD); `ubuntu-latest` does not pin hardware. Both ~4.5× inside the threshold. |
| cold_index_p95 | 20.368 s | 18.735 s | −8.0 % | same cause |
| warm_search_p95 | 3.591 ms | 4.521 ms | +25.9 % | same cause, opposite direction — the AMD host indexed faster but searched slower. Both >20× inside the threshold. |
| caller_callee_impact_p95 | 0.999 ms | 1.657 ms | +65.9 % | largest relative spread, least consequential: both are 120–200× inside a 200 ms gate, where sub-millisecond percentiles are scheduler noise. |
| progress_stall_p95 | 0.006443 s | 0.005611 s | −12.9 % | both round to 0.006 s; the tails are 1.1 % apart |
| freshness_p95 | 6.315 s | 6.486 s | +2.7 % | the failing gate is the *most* stable number here |
| peak_rss | 0.670 GB | 0.664 GB | −0.9 % | essentially identical |
| db_size | 32.688 MB | 32.688 MB | 0.0 % | identical to the byte — deterministic |

---

## What a complete run costs (AC-9)

Read from **the harness's own accounting**, not the CI job clock: each
measurement step stamps a timestamp immediately before invoking the built binary
and immediately after it returns, emitted per job as
`run-<a|b>/cost/<family>-<repo>.json`. Checkout, toolchain setup, the candidate
checkout and the compile all sit outside that window. 20 of 20 measurement jobs
emitted a cost file in each run.

| Run | Runner cost (Σ 20 jobs) | Critical path (longest job) | CI job clock, for contrast |
|---|---|---|---|
| run-a | **1070 s** (17 m 50 s) | **455 s** (7 m 35 s) — freshness/grpc-go | 511 s (8 m 31 s) |
| run-b | **1094 s** (18 m 14 s) | **457 s** (7 m 37 s) — freshness/grpc-go | 554 s (9 m 14 s) |
| both | **2164 s** (36 m 04 s) | — | 1065 s (17 m 45 s) |

By family, run-a / run-b: cold-index 342 / 322 s · freshness 518 / 519 s ·
query-latency 172 / 215 s · progress-stalls 38 / 38 s.

The two costs answer different questions. **2164 harness-seconds** is what PRD
§16's "two consecutive complete runs" consumes in **runner capacity**;
**~7 m 37 s** is what an **operator waits**, because the twenty jobs are
independent and run in parallel. The critical path is the same job in both runs
— freshness over grpc-go — so the slowest thing to measure is also the only
thing holding a failing gate.

The last column is the CI job clock, shown **only so the two are not confused**.
It is the elapsed time of the whole 20-job dispatch, not a sum, and it includes
setup and compilation. Neither column may be substituted for the other.

---

## What this baseline does **not** establish

1. **Nothing about accuracy.** No gold corpus, no scorer, no precision/recall —
   that is P0-B, deferred.
2. **Nothing about full-vs-incremental parity.** A PRD §12.3 reliability gate,
   deferred to P0-D. The incremental series measures freshness, not parity.
3. **Nothing about the stress target.** kubernetes is tier 4, manual-only, bound
   by the §17 4 GB stop rule rather than a §12.2 gate. Its stop-rule status
   remains UNKNOWN.
4. **Nothing about the released binaries' build flavor.** The measured *source*
   is the candidate's byte for byte; the harness build carries none of the
   release's 22 build tags or `-trimpath`.
5. **No budget was re-baselined.** `docs/eval/hero-budgets.json` still holds the
   retired harness's historical ceilings. Replacing them is a reviewed decision
   about thresholds, and this story deliberately moves none.

## Recorded, not fixed

All four are in `backlog.md`:

- **`freshness_p95` FAIL** — 3.2× over gate, reproducible, profiles committed (WP4).
- **A p95-only stall gate hides its own tail** — 0.006 s passes while 15.5 s of
  silence goes unremarked.
- **`coldRunArgv` drops `-candidate`** (`cmd/eval/coldseries.go:497`) — child
  cold-runs do not inherit the candidate citation, so the secondary gate
  families inside a child's report compare against the *superseded* candidate
  and read UNKNOWN naming `fb3bf03`. **No published gate is affected** — the
  cold-series gates are computed in the parent, and the query/stall gates are
  published from their own dedicated jobs — but the child reports carry
  misleading UNKNOWN reasons.
- **The `partial` defect costs 25 of 1000** agent-tool executions and is what
  leaves gate 9 UNKNOWN.
