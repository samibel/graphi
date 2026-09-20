# Coherent flow and depth projection (compact/16) — development result

Status: **release candidate on development data; not RELEASE: YES.** No sealed
holdout question, answer, bundle, response, or grade was opened or used. The
frozen 1,200-token response ceiling, 325-word source budget, evaluation rules,
targets, and methodology are unchanged.

## Diagnosis

The compact/15 candidate pool often already contained the required source, but
the natural-language projector discarded or fragmented it in five measurable
ways:

1. A named declaration that fit in the budget could remain cut because already
   completed lower citations became unreclaimable.
2. A complete hydrated Markdown section lost deduplication to a wider raw
   window that ended inside the section.
3. A direct callee one row below the six-seed cap was dropped even when a
   retained caller named it and both matched the question strongly.
4. Adjacent caller/callee declarations were returned as separate spans with a
   blank separator missing, so strict whole-span verification rejected them.
5. Long regions grew around the first lexical anchor without following a later
   query-bearing line, and could stop immediately before a syntactic closer.

The exact-identifier and exact-path fixes in compact/15 remain unchanged.

## Change

`task_context/2-compact/16` keeps the frozen budgets and:

- lets an explicitly named lead reclaim completed lower citations when the
  named unit fits;
- completes coherent Markdown leads up to 250 source words and prefers a
  hydrated Markdown section over an overlapping raw window;
- promotes one direct callee across the seed cap only when its local query
  score is at least its caller's score;
- preserves zero-word declaration separators and merges only already-selected,
  exactly touching spans;
- lets a file explicitly named by query stem (for example `rest.md`) lead when
  its local match exceeds the retrieval lead by at least one full query term;
- directs growth toward the next query-bearing line, reserves 60% depth only
  for an explicitly named long function or method, and retains up to three
  immediately following pure closing-delimiter lines.

Focused tests pin every rule. None names Cobra, a dataset query ID, an answer
span, or a judgement.

## Before / after

| Reviewed 64-query development forecast | compact/15 | **compact/16** |
|---|---:|---:|
| One response: overlapped | 56 | **58** |
| One response: complete | 49 | **56** |
| Architecture-flow complete | 4/11 | **7/11** |
| Config-docs complete | 6/10 | **9/10** |
| Natural-language complete | 8/11 | **9/11** |
| Exact-identifier complete | 11/11 | **11/11** |
| Exact-path complete | 11/11 | **11/11** |
| Lost in compact selection | 10 | **3** |
| Two-call complete under frozen contract v2 | 55 | **57** |
| Median / maximum response tokens | 957 / 1,148 | **925 / 1,160** |

At the observed complete rate of 56/64, the binomial forecast prints
`P(>=56 of 64) = 0.593`. This is planning evidence, not a holdout result.

The diagnostic `cited` counter falls because it asks whether a compact source's
*start line* lies inside the reviewed span. compact/16 deliberately retains the
blank separator immediately before many Go declarations, so a byte-verifiable
source can completely contain an answer while its start-line proxy is false.
The counter and scoring rule were not changed; overlap and completeness use the
emitted source boundaries and exact repository bytes.

On the original committed 40-query development split, compact/16 also improves
over compact/15: reached 35→36, at least one complete grade-3 span 24→26,
cheaper than equal-recall GrepRead/2 26→27, and paired median saving +74→+88
tokens (7.83%). All 40 responses remain within 1,200 tokens; the maximum is
1,194.

The frozen ranking gates remain green and are unaffected by projection:
architecture-flow nDCG@10 is 0.46246715228468249 (required
0.4578575262772977), natural-language nDCG@10 is 0.70290472235070689, and
exact-identifier Top-1 is 1.0. `bundle_coverage` remains 6/6.

## Release interpretation

The repository's recorded release verdict remains **NO** because every already
registered blind holdout is spent and below its preregistered threshold. This
development result is strong enough to nominate compact/16 for a new,
independently curated and preregistered 64-query holdout with the unchanged
`k=56` decision rule. Only that untouched run can produce `RELEASE: YES`.

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
