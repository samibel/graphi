# Documentation-section hydration (compact/11) — development result

Status: **development result under the gate policy approved on 2026-09-14;
not a release result.** No holdout was opened, rerun, or consulted. This
run supersedes `2026-09-14-compact10-dev` as the current candidate.

The capture is bound to candidate
`2be4177b9891a693535e62c3d526fae3721ce622`, the pinned Cobra checkout
`a0a6ae020bb3899ff0276067863e50523f897370`, and development dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`. Capture
SHA-256 `01e64cd37d73bfa8d8d624f873fa7ec70e7ed578ee146bf093e1be431aa37848`;
two independent index builds, distinct freshness generations, 44/44
identical MCP bytes, digests and cl100k counts. The reviewed holdout-shaped
split (SHA-256 `33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`)
is measured through the production MCP path by `TestDraftDevForecast`.

## What changed since compact/10

For a natural-language question the compact stage now hydrates **every**
ranked Markdown heading to its whole section. Previously it did so only
when the question matched one of two hand-written shapes
(`WantsMarkdownFlow && !WantsLifecycleHooks`) or named the file. The
retrieval row for a heading is the heading line; the answer is usually a
paragraph further down the section, which the ±6-line heading window never
held. The selector decides how much of the section to emit, exactly as for
a Go declaration. Wire identity: `task_context/2-compact/11`.
`TestHydrationExpandsEveryRankedMarkdownHeadingForNaturalLanguage` pins it.

## Before / after

Before is compact/10 (`85706895`, `2026-09-14-compact10-dev`); the
reference before this slice, compact/9 (`40133db5`), is shown for the
whole distance.

### Reviewed holdout-shaped split (primary), 64 questions, production path

| Measure | compact/9 | compact/10 | **compact/11** |
|---|---:|---:|---:|
| Span overlapped | 45 | 46 | **49** |
| Span cited | 34 | 41 | 41 |
| Span complete in one source | 28 | 33 | **37** |
| Mean span share delivered | 56.6 % | 62.7 % | **68.2 %** |
| Median / max cl100k tokens | 1,036 / 1,190 | 949 / 1,187 | 957 / 1,187 |
| config_docs overlapped / complete | 5 / 3 | 5 / 2 | **8 / 6** |
| nl_behaviour overlapped / complete | 7 / 3 | 9 / 8 | 9 / 8 |
| architecture_flow overlapped / complete | 7 / 3 | 7 / 4 | 7 / 4 |
| exact_identifier complete | 11 | 11 | 11 |
| exact_path overlapped / complete | 8 / 2 | 8 / 2 | 8 / 2 |
| ambiguous overlapped / complete | 7 / 6 | 6 / 6 | 6 / 6 |

Misses by first losing stage: 5 absent from the 50-row window (three are
non-Go files retrieval does not index), 3 below the 15-candidate cap, 19
retrieved and lost in selection — nine of them `exact_path` pieces that
the sealed rubric accepts (`REVIEW.md`). Under the rubric's reading the
overlap rate, **76.6 %**, is the closer proxy for a holdout; `P(≥ 56 of
64)` at that rate is 0.022 (was 0.001 at compact/9).

### Committed development split, 40 questions, bound captures

| Measure | compact/9 | compact/10 | **compact/11** | Policy |
|---|---:|---:|---:|---|
| Responses ≤ 1,200 cl100k tokens | 40 | 40 | 40 | held |
| Cheaper than equal-recall GrepRead/2 | 24 | 27 | **27** | held (cost) |
| Paired median token saving | 76.0 (6.69 %) | 96.0 (8.41 %) | **81.5 (7.27 %)** | held (cost) |
| Median / max response tokens | 1,019.5 / 1,174 | 993 / 1,167 | 1,006 / 1,165 | held (cost) |
| Contained duplicate sources | 0 | 0 | 0 | held |
| Required grade-3 overlap reached | 40 | 35 | 35 | observed |
| At least one complete grade-3 span | 33 | 24 | 24 | observed |
| Every grade-3 span complete | 18 | 17 | 16 | observed |

The old-split regression is the one recorded for compact/10 and is
unchanged here: the five lost required overlaps are the questions the
compact/9 selector reached only through query-shape literals, and the
completeness loss sits in the multi-span `config_docs` keys that reward
breadth. Ranking gates re-verified: architecture-flow nDCG@10
`0.46246715228468249`, NL-behaviour nDCG@10 `0.70290472235070689`,
exact-identifier Top-1 `1.0`, bundle coverage 6/6; the target checker
exits 1 only on the immutable historical `RELEASE: NO`.

## Rejected on the same instruments

| Probe | Reviewed (overlapped / complete) | Decision |
|---|---|---|
| candidate-pool cap 15 → 20 on top of this change (both splits re-captured) | 48 / 36 | reject: no gain, one loss |

## Release status

**No release YES.** Both sealed holdouts remain `RELEASE: NO` (32/64,
42/64) against `k = 56`. On the reviewed split this candidate forecasts
49/64 overlapped and 37/64 complete. It is the strongest measured
candidate so far and still short of the bar: at a 76.6 % true rate the
chance of 56 of 64 is about one in fifty. The remaining reviewed-split
misses, by what the projector was given: 8 with no covering evidence in
the bundle (candidate pool; three non-Go files), 9 `exact_path` pieces the
rubric accepts, 3 hydration windows that stop short, and about 10 where a
covering declaration is present but its emitted window misses the lines
(`cd-24` at 70 %, `cd-83` at 60 %, the ambiguous single-word queries that
still take the compact/9 path).

The candidate is frozen and clean at `2be4177b`; the commit recording this
evidence adds only this directory and the two pin-rotation inventories.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370
export GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA"

GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-14-compact11-dev/bundles.json" \
GRAPHI_RECOVERY_CANDIDATE_SHA=2be4177b9891a693535e62c3d526fae3721ce622 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=docs/eval/retrieval/runs/2026-09-14-compact11-dev/bundles.json \
go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v

GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v

go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

go test ./...
```
