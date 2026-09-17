# Compact flow-depth development result

Status: **development diagnostic; not a release result**.

Candidate `1430daf27e795c8c3a7dcc230d858935044bc33f` changes only the
actor-visible compact projection. The retrieval ranking candidate and frozen
1,200-token response ceiling are unchanged. The evaluation uses only the 40
answerable development queries from dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.

## Diagnosis

The ranking gates no longer explain the release failure: architecture-flow
nDCG@10 is `0.46246715228468249` (required `0.4578575262772977`) and exact
identifier Top-1 is `1.0`. The independent fresh holdout still returned
`RELEASE: NO` at 32/64; its public aggregate was strongest on exact identifiers
(11/11) and weakest on architecture flow (3/11), configuration/docs (2/10),
and natural-language behaviour (4/11).

The production projector admitted up to ten regions for an ordinary flow
question (nine semantic regions and one GrepRead fallback). At 250 source
fields this frequently produced many one- or two-line anchors instead of a
small readable call chain. The source candidate was often present, but too
little of it survived projection.

The change caps ordinary flow answers at four regions: three semantic regions
and one query-only fallback, weighted `12:8:5:3`. It also raises the source
field frontier from 250 to 325. This does **not** raise the serialized budget:
the projector still counts the final cl100k wire bytes and deterministically
backs off until they are at most 1,200 tokens. The wire identity changes from
`task_context/2-compact/4` to `task_context/2-compact/5`.

## Before / after

The before row is the committed v7 product capture. The after row is the
existing production compact development test at the candidate above.

| Measure (40 answerable dev queries) | Before | After |
|---|---:|---:|
| Required grade-3 overlap reached | 40 | 40 |
| Every grade-3 span overlapped | 30 | 29 |
| At least one complete grade-3 span | 26 | 30 |
| Every grade-3 span complete | 14 | 16 |
| Responses at or below 1,200 cl100k tokens | 40 | 40 |
| Median response tokens | 914.5 | 1,027.5 |
| Maximum response tokens | 1,079 | 1,186 |
| Individually cheaper than equal-recall GrepRead/2 | 32 | 24 |
| Paired median token saving | 106.5 | 46.0 |
| Paired median percent saving | 9.5930% | 4.0760% |

The result is a real quality/cost trade: four more queries contain a complete
answer span, while one query loses complete multi-span overlap and the median
saving narrows. The candidate remains measurably cheaper in the registered
paired-median sense, but this development evidence does not justify a release
YES. A new independent holdout is required for that claim; the spent 64-query
holdout must not be rerun or tuned against.

The source-budget frontier is recorded in `frontier.json`. Budget 350 gains
only one additional complete-span query while reducing the paired median
saving to 1.6076%; 325 is the selected knee.

## Reproduce

```sh
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v

CGO_ENABLED=0 go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

CGO_ENABLED=0 go test ./...
```

The target checker reports the four development gates as PASS and exits 1 for
the historical qrel-blind `RELEASE: NO`; there is no override or waiver.
