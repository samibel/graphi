# P0 performance baseline — 2026-07-28, runner class `ubuntu-latest`

The first measurement of a graphi release candidate against the PRD §12.2
performance gates. Two complete runs, on the frozen candidate **v0.7.0 at
`5815db5b053c2bb1bf3119cdb9939c1dea03cc45`**, over the five pinned Go
repositories of `corpus/manifest.json` v3, with every individual sample kept.

**The numbers are in [`p0-baseline.md`](p0-baseline.md) (human) and
[`p0-baseline.json`](p0-baseline.json) (machine).** This file says what the
directory is and — the part that is easy to skip — what it does **not**
establish.

## What is here

```
run-a/                         workflow run 30377165970  (Intel Xeon Platinum 8573C)
run-b/                         workflow run 30379259481  (AMD EPYC 9V74)
  cold-index/<repo>/             SW-124 — ten cold indexes, one process each
  query-latency/<repo>/          SW-125 — >=1000 timed executions per class
  freshness/<repo>/              SW-126 — 100 incremental changes
  progress-stalls/<repo>/        SW-127 — every interval between progress events
    run.json                       table of contents: versions, environment, digests
    environment.json               the machine, and the provenance of the artifact
    report.json                    the PUBLISHED aggregate — percentiles, gates
    aggregate.json                 the reproduction, recomputed from raw/
    raw/                           samples only, nothing derived
    profiles/                      SW-129 — present only where a gate was MISSED
  cost/<family>-<repo>.json      the harness's own wall-clock for that job (AC-9)
  other-jobs/                    the historical-ceiling matrix and the hero suite
p0-baseline.json / .md         the published report over both runs
```

`<repo>` is `uuid`, `lo`, `cobra`, `gin`, `grpc-go`. Reproduce any directory:

```sh
go run ./cmd/eval -aggregate docs/eval/runs/2026-07-28-ubuntu-latest/run-a/cold-index/grpc-go
```

Both runs were reproduced in CI at the moment they were produced — the
`reproduce the published numbers from the raw samples` step of every job — and
all **40** leaf directories were re-aggregated from this committed raw data at
publish time. Every one exited `0`.

## The headline

8 PASS · 1 FAIL · 1 UNKNOWN, and both runs agree on all ten verdicts.

- **FAIL — `freshness_p95`**: 6.315 s / 6.486 s against a 2 s gate, over 100 of
  100 converged changes. Published as a FAIL; no threshold moved, no scenario
  eased, no repository dropped (PRD §17). Profiles are committed beside it.
- **UNKNOWN — `agent_context_p95`**: 975 of 1000 required executions pooled, in
  *both* runs. The cause is a deterministic `"partial"` outcome from
  `change_risk` / `explain_symbol` / `related_files`, not sampling noise.
- **PASS with an asterisk — `progress_stall_p95`**: 0.006 s against a 2 s gate,
  while the longest observed silence was **15.5 s**. The gate is met as
  specified; the property a reader assumes it protects is not.

## The provenance, and why it needed work

The harness refuses to read a PRD §12.2 gate unless the measured revision **is**
the frozen candidate: `candidate_match` compares `git rev-parse HEAD` with the
SHA the evidence index cites, and a mismatch (or a dirty worktree) forces every
gate to `UNKNOWN` before a threshold is compared. That refusal is correct and was
not weakened. It is also why a naive dispatch measures nothing readable:

| Where the harness runs | `HEAD` | index cites | `candidate_match` |
|---|---|---|---|
| the default branch | `afa1b68`… | `5815db5`… | **false** |
| the candidate's own tree | `5815db5`… | `fb3bf03`… (superseded) | **false** |

A commit cannot contain its own hash, so the freeze record that names a SHA never
exists **at** that SHA. The citation is an external fact about the artifact —
which is exactly what `cmd/eval -candidate` takes. So each job copies the
citation out of the dispatched ref, then checks the candidate out, builds the
harness from that checkout and runs the built binary, writing every output
outside the worktree so nothing turns it dirty. The result, in every
`environment.json` of both runs:

```json
"candidate_sha":   "5815db5b053c2bb1bf3119cdb9939c1dea03cc45",
"measured_sha":    "5815db5b053c2bb1bf3119cdb9939c1dea03cc45",
"candidate_match": true,
"worktree_dirty":  false
```

## The FR-9 substitution, named rather than implied

PRD §16 wants two consecutive complete runs and FR-9 wants independent
reproduction. The project is solo, so the second run is **a clean, freshly
provisioned GitHub-hosted runner rather than a second person**. FR-9 permits
that; it is nevertheless *weaker evidence* than the original, and it is recorded
as a substitute in `p0-baseline.json` and in the WP2/WP4/M1 rows of
`docs/rc/evidence-index.yaml`. It is never reported as the original gate.

It rules out machine-local state — run-b landed on a different CPU family and
reproduced every verdict. It does **not** rule out a shared defect in the
harness, the workflow or the operator's method, because one operator dispatched
both runs from the same repository state.

## Scope limitation (PRD FR-8)

Every §12.2 gate here holds for **the reference scenario** — grpc-go v1.60.1 on
`ubuntu-latest` — and for nothing else. The other four repositories are measured
and published, and every §12.2 gate over them reads `UNKNOWN` with that reason
named, because the measurement contract scopes those gates to the reference
scenario. These numbers are not a universal guarantee for other repositories,
other runner classes, other build flavors or other workloads.

## What these runs do NOT establish

1. **Nothing about accuracy.** No gold corpus, no scorer, no precision/recall.
   That is P0-B and is not in this directory.
2. **Nothing about full-vs-incremental parity.** It is a PRD §12.3 reliability
   gate, deferred to P0-D; the incremental series measures freshness, not parity.
3. **Nothing about the stress target.** kubernetes is tier 4, manual-only, and
   the contract binds it to the §17 4 GB stop rule rather than to a §12.2 gate.
   The stop rule's status for kubernetes remains `UNKNOWN`.
4. **Nothing about the released binaries' build flavor.** The harness links the
   product packages into itself; the published release assets are built with 22
   build tags and `-trimpath`. The measured *source* is the candidate's, byte for
   byte; the *build* is not the release build. Giving the harness a real
   external-binary path is recorded as a follow-up, not done here.
5. **No budget was re-baselined.** `docs/eval/hero-budgets.json` still carries the
   retired harness's historical ceilings. Replacing them is a reviewed decision
   about thresholds, and this story deliberately moves no threshold.

## The other jobs of the same dispatch

`other-jobs/` holds the historical-ceiling matrix (cobra, flask, guava) and the
hero suite from the same workflow runs. They are **not** part of the §12.2
baseline and they do not run from the candidate checkout — they measure the
dispatched ref. That ref's product tree (`engine`, `core`, `surfaces`,
`cmd/graphi`, `extensions`, `go.mod`, `go.sum`) is byte-identical to the
candidate's, so what they exercise is the same source; the provenance is
nevertheless weaker and is not used for any gate.

## Earlier dispatches of the same measurement code, not published here

Three earlier `workflow_dispatch` runs on 2026-07-28 exercised this measurement
code before the two published above. None is published, and none was excluded
for being inconvenient — their reference-scenario verdicts agree with the
published pair in every case, which is stated here so a reader can see the
verdicts did not move:

| Run | Ref | Why not published |
|---|---|---|
| [30371822636](https://github.com/samibel/graphi/actions/runs/30371822636) | `1e60f17` | earlier measurement code: its workflow lost the AC-9 cost accounting on any job that exited non-zero (`bash -e` aborted the step before the cost line), so it cannot answer what a complete run costs. Verdicts agreed: cold p50 16.931 s, p95 17.227 s, RSS 0.664 GB, DB 32.688 MB, OOM PASS, warm search 4.522 ms, caller/callee 1.659 ms, stall p95 PASS, agent context UNKNOWN at 975 pooled, freshness 6.342 s FAIL. |
| [30372254580](https://github.com/samibel/graphi/actions/runs/30372254580) | `8f8347c` | superseded by the published pair. `8f8347c` is the pre-merge tip of PR #81 and its tree is byte-identical to the merge commit `afa1b68` (`ab129688…`), so it measured the same code — but the published pair is cited by SHA, and citing two different SHAs for one baseline would invite exactly the provenance confusion this story exists to remove. |
| [30372969771](https://github.com/samibel/graphi/actions/runs/30372969771) | `8f8347c` | same |
