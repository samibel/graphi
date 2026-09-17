# Bare-term projection (compact/12) — development result, end of slice A

Status: **development result under the gate policy approved on 2026-09-14;
not a release result.** No holdout was opened, rerun, or consulted. This
run supersedes `2026-09-14-compact11-dev` as the current candidate and
closes slice A by its pre-stated stop rule.

The capture is bound to candidate
`ed2f8370d0dc558e2fb37de4e49cf6ebb6e1ba14`, the pinned Cobra checkout
`a0a6ae020bb3899ff0276067863e50523f897370`, and development dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`. Capture
SHA-256 `33fa0953ccbb4a281ba31fb9313a726fb588fe788e84ef3f93e19d190aae6f3d`;
two independent index builds, distinct freshness generations, 44/44
identical MCP bytes, digests and cl100k counts. The reviewed holdout-shaped
split (SHA-256 `33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`)
is measured through the production MCP path by `TestDraftDevForecast`.

## What changed since compact/11

A single-word query that names no ranked candidate exactly ("directive",
"template", "Aliases") is a bare term, not a lookup. It took the
exact-lookup path, which crowns whichever candidate scores best on that
word; it now takes the retrieval-ordered path. Raising the lead's depth
share from 48 % to 55 % was measured (A2) with no effect and reverted.
Wire identity: `task_context/2-compact/12`.

## Before / after

| Reviewed split, 64 questions, production path | compact/9 (start) | compact/11 | **compact/12** |
|---|---:|---:|---:|
| Span overlapped | 45 | 49 | **51** |
| Span complete in one source | 28 | 37 | **39** |
| Mean span share delivered | 56.6 % | 68.2 % | **71.3 %** |
| ambiguous overlapped / complete | 7 / 6 | 6 / 6 | **8 / 8** |
| Median / max cl100k tokens | 1,036 / 1,190 | 957 / 1,187 | 957 / 1,187 |

| Committed split, 40 questions, bound capture | compact/9 | **compact/12** | Policy |
|---|---:|---:|---|
| Responses ≤ 1,200 tokens | 40 | 40 | held |
| Cheaper than equal-recall GrepRead/2 | 24 | **27** | held (cost) |
| Paired median token saving | 76.0 (6.69 %) | **81.5 (7.27 %)** | held (cost) |
| Required grade-3 overlap reached | 40 | 35 | observed (see compact/10) |
| At least one complete grade-3 span | 33 | 24 | observed (see compact/10) |

Ranking gates unchanged (the projection does not touch retrieval).

## Stop rule and what follows

Slice A was allowed two experiments and a stop rule: pre-register a
holdout only if the reviewed split reached 54 of 64 overlapped. It reached
**51**. Under the rubric's reading the overlap rate, 79.7 %, gives
`P(≥ 56 of 64)` ≈ 0.08 — better than the 0.001 the slice started from,
and not a bar to spend a holdout on. No further selector tuning follows.

The remaining 13 non-overlapped questions, by what the projector was
given: 5 with no covering evidence in the bundle (three are non-Go files
retrieval does not index), 3 below the 15-candidate cap, 5 whose covering
declaration is present but whose emitted window misses the lines. A
single 1,200-token response is at its useful limit on this shape; the
next step is the second-response contract (option C in
`docs/eval/retrieval/contract-v2-options.md`), for which this candidate
is the frozen first response.

## Reproduce

As for `2026-09-14-compact11-dev`, with
`GRAPHI_RECOVERY_CANDIDATE_SHA=ed2f8370d0dc558e2fb37de4e49cf6ebb6e1ba14` and
this directory's `bundles.json`.
