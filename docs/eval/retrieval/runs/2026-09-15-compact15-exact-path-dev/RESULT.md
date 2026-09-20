# Coherent exact-path outlines (compact/15) — development result

Status: **positive development result; not a release result.** No holdout
question, bundle, answer, response, or grade was opened or used. The frozen
1,200-token response ceiling, source budget, evaluation rules, targets, and
methodology are unchanged.

## Diagnosis and change

Exact-path retrieval already had the right file and declarations, but the
projector serialized adjacent declarations as separate source spans. A blank
separator could therefore be the only uncovered line, and the strict
whole-span check correctly rejected the fragmented answer. When an outline
declaration deduplicated with earlier semantic evidence, it also inherited
retrieval order; late functions could then displace the beginning of the file.
Platform files had a third form of the same problem: the outline started at
`package` and omitted the immediately preceding Go build constraints.

`task_context/2-compact/15` makes exact-path outlines coherent without adding
search or widening a budget:

1. Retain blank separators immediately before declarations.
2. Retain adjacent `//go:build` and legacy `// +build` constraints with the
   package declaration, but not a licence header.
3. Select exact-path declarations in source order after deduplication.
4. Merge only already-selected, exactly touching exact-path spans. No gap is
   synthesized and no additional source word is charged.

Three focused tests pin separator coverage, build-constraint coverage, and
source-order selection after deduplication.

## Before / after

| Reviewed development split, 64 questions | compact/14 | **compact/15** |
|---|---:|---:|
| One response: overlapped | 56 | **56** |
| One response: complete | 43 | **49** |
| Exact-path complete | 5/11 | **11/11** |
| Lost in compact selection | 16 | **10** |
| Two-call complete under frozen contract v2 | 49 | **55** |
| Median / maximum response tokens | 959 / 1,187 | **957 / 1,148** |

The diagnostic `cited` count falls from 46 to 39 (exact-path 10/11 to
3/11). This counter applies the retrieval rule to a compact source and asks
whether the source's *start line* lies inside the reviewed span. A coherent
outline often begins before the reviewed declaration, so it can contain that
span completely while failing this start-line proxy. The counter and matching
rule were not changed; byte verification, overlap, and whole-span completeness
all use the emitted source boundaries directly.

On the original committed 40-question development split, whose three
exact-path judgements require entire files larger than the response ceiling,
the change is neutral as expected: 35/40 reached, 24/40 with at least one
complete grade-3 span, 40/40 within 1,200 tokens, 26/40 cheaper than
equal-recall GrepRead/2, and paired median saving +74 tokens (6.64%).

The retrieval ranking gates remain green and are unaffected by projection:
architecture-flow nDCG@10 0.46246715228468249 (required
0.4578575262772977), natural-language nDCG@10 0.70290472235070689, and
exact-identifier Top-1 1.0. The overall release check remains **NO** because
the registered qrel-blind smoke result remains below 56/64. This development
result does not authorize another holdout or a release claim.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02
export GRAPHI_STATIC_MODEL_DIR=/abs/path/to/potion-code-16M-v2@e9d2a44c
export GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b

GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
  go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v

go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v

go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

go test ./...
```

The target check exits 1 only for the already-recorded qrel-blind release
result; it prints PASS for architecture flow, natural-language behaviour,
exact-identifier non-regression, and bundle coverage.
