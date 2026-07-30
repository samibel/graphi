# P0 performance baseline — the first measured numbers for a graphi candidate

> ⚠️ **The instrument that produced these numbers has since been corrected
> (SW-136).** They remain valid statements about v0.7.0 at `5815db5` and nothing
> here is deleted or re-pointed — but they are **not** evidence about what graphi
> measures next, and gate 9's `UNKNOWN` below is now **final** for this candidate
> rather than pending. Read
> [`STALENESS-NOTICE.md`](STALENESS-NOTICE.md) before quoting any figure on this
> page.

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

> **CORRECTION 2026-07-29 (SW-130 review, finding 1) — added, nothing above is
> rewritten.** The paragraph immediately above quotes **only run-a**. The harness
> recorded the pooled p95 for **both** runs, and they do not agree about which
> side of the gate this value falls on:
>
> | | pooled n | pooled p95 | vs the 500 ms gate |
> |---|---|---|---|
> | run-a | 975 of 1000 | **471.250 ms** | below |
> | run-b | 975 of 1000 | **601.732 ms** | **above, by 20.3 %** |
>
> Run-b is **+27.7 % above run-a** — see the deviations table below, where this
> gate was also missing. Source for both figures:
> `run-{a,b}/query-latency/grpc-go/report.json` →
> `.repo.query_latency.pools[]` (`agent_context_p95`, `p95_us` 471250 / 601732),
> each independently recomputed from
> `run-{a,b}/query-latency/grpc-go/raw/query-latency.json` by pooling
> `agent_brief` (250) + `change_risk` (246) + `explain_symbol` (234) +
> `related_files` (245) = 975 `samples_us` and taking the nearest rank
> `ceil(0.95 × 975) = 927`.
>
> **Why this correction exists.** As originally written, the sentence supports
> only one reading — that the UNKNOWN is a conservatively withheld *PASS*. The
> full data does not support that reading in either direction: on the same
> undersampled pool, one run is inside the gate and the other is a fifth outside
> it. **The UNKNOWN may equally be a withheld FAIL.** This is strictly stronger
> support for having no verdict than the original sentence was.
>
> Nothing was hidden from the repository — run-b's 601.732 ms has been in the
> committed `report.json` since publication. What was missing was its appearance
> in the prose, in `p0-baseline.json` and in the evidence index. It is added to
> all three as of this date. The verdict does not change: **UNKNOWN**, and
> UNKNOWN is not a PASS (PRD §8.2).

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

> **CORRECTION 2026-07-29 (SW-130 review, finding 2) — the "(AC-1)" citation
> above claims more than was delivered.** Added, not rewritten: the *fact*
> asserted above is true and unchanged — each job did check out `5815db5`, did
> run `go build -o "${RUNNER_TEMP}/eval" ./cmd/eval`, and did execute that built
> binary rather than `go run` from a working tree. What is wrong is citing it as
> satisfying AC-1.
>
> **AC-1 as written requires "the release binary of the frozen candidate".** What
> ran is the **eval harness** with the product packages linked into it. No
> `graphi` binary was executed, and the harness still has no external-binary path
> — that is limitation #4 below and it is carried as an open follow-up. SW-131
> made `candidate_match` true; it fixed **provenance**, not the missing binary
> path.
>
> **The precise reading under which AC-1 is met**, and no stronger: *the run
> executed a compiled binary, built from a clean checkout of the frozen
> candidate's own tree, verified byte-identical to it by `candidate_match` — not
> `go run` from a working tree, and not the published release binary.* Under the
> literal reading of AC-1 the criterion is **partially met**, with the
> external-binary path as the open remainder. Nothing measured here changes; what
> changes is that the record stops claiming the stronger version.

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
| agent_context_p95 (pooled, undersampled) | 471.250 ms | 601.732 ms | **+27.7 %** | **ADDED 2026-07-29 (SW-130 review, finding 1).** The one deviation the original table omitted, and **the only one in the baseline that crosses its own threshold**: run-a is below the 500 ms gate, run-b is 20.3 % above it. Not a verdict either way — the pool is 975 of 1000 in both runs, so gate 9 is UNKNOWN regardless of where the number lands. |

> **CORRECTION 2026-07-29 (SW-130 review, finding 1) — the row above is an
> addition; the eight rows before it are unchanged.** AC-2 requires deviations
> between the two runs to be documented and explained rather than smoothed over,
> and this one was neither documented nor explained. Two points of precision, so
> the correction is not overstated in the other direction:
>
> - **It is not the largest relative deviation in the baseline.**
>   `caller_callee_impact_p95` at **+65.9 %** is larger, and `warm_search_p95` at
>   **+25.9 %** is close behind. The published row calling
>   `caller_callee_impact_p95` "the largest relative spread" remains correct and
>   is not amended.
> - **It is the most consequential deviation in the baseline**, because it is the
>   only one where the two runs disagree about the *verdict-relevant* question —
>   which side of the threshold the value falls on. Every other gate's spread
>   moves a number that is 4×–200× inside its gate.

---

## CORRECTION 2026-07-29 (SW-130 review, finding 4) — one job's CPU was stamped on twenty

**Added; no figure above is withdrawn and no verdict changes.** Each run carries a
single `cpu_model` in `p0-baseline.json`, and the two deviation explanations above
("Intel vs AMD", "the AMD host indexed faster but searched slower") read it as if
one machine ran the whole run. It did not. **Each run is 20 independent CI jobs on
20 freshly provisioned VMs**, and the stamped value is only the `cold-index/grpc-go`
job's CPU. Read from all 40 `run-{a,b}/*/*/environment.json`:

| | CPU models observed across the run's 20 jobs |
|---|---|
| run-a | AMD EPYC 7763 (×13) · AMD EPYC 9V74 (×5) · Intel Xeon Platinum 8573C (×2) — **3 models** |
| run-b | AMD EPYC 7763 (×11) · AMD EPYC 9V74 (×5) · Intel Xeon Platinum 8573C (×3) · Intel Xeon Platinum 8370C (×1) — **4 models** |

All 40 jobs report 4 vCPUs and kernel `6.17.0-1020-azure`; only the CPU model and
the reported RAM (within ~5 MB) vary.

Per published gate, the machine that actually produced it:

| Gate(s) | Job | run-a CPU | run-b CPU |
|---|---|---|---|
| cold_index_p50 / p95, peak_rss, db_size, oom_8gb_host | `cold-index/grpc-go` | Intel Xeon Platinum 8573C | AMD EPYC 9V74 |
| warm_search_p95, caller_callee_impact_p95, agent_context_p95 | `query-latency/grpc-go` | AMD EPYC 9V74 | AMD EPYC 7763 |
| freshness_p95 | `freshness/grpc-go` | AMD EPYC 7763 | AMD EPYC 9V74 |
| progress_stall_p95 | `progress-stalls/grpc-go` | AMD EPYC 7763 | **AMD EPYC 7763 — the same model in both runs** |

What this changes in the explanations above, stated plainly:

- **"the AMD host indexed faster but searched slower" conflates two machines.**
  Indexing and searching ran on separate VMs. In run-a the search job was AMD EPYC
  9V74 — not the Intel host the row implies — and in run-b it was AMD EPYC 7763. The
  warm-search and caller/callee spreads are *both-AMD, different-model* deltas, not
  an Intel-versus-AMD delta.
- **"Intel vs AMD" is accurate for the cold-index gates only** (rows 1–5). It does
  not hold uniformly: `progress_stall_p95` was measured on the *same* CPU model in
  both runs, so its −12.9 % spread has no silicon explanation at all.
- **"two different CPU families" in the FAIL section** is true of `freshness_p95`
  in the AMD sense — EPYC 7763 (run-a) and EPYC 9V74 (run-b) are different EPYC
  generations — but it is not the Intel/AMD contrast the per-run stamp suggests.

**This strengthens the FR-9 substitute argument rather than weakening it.** The
honest claim is not "run-b landed on different silicon" but: *run-b was 20
independently provisioned machines spanning four CPU models, and it reproduced all
ten verdicts.* That rules out machine-local state considerably more thoroughly than
a single-CPU claim does.

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
