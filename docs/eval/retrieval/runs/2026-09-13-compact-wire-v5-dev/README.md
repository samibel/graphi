# Compact wire v5 — development frontier

Status: **development diagnostic, not a release result**.

This run measures `task_context/compact-dev/5` only on the frozen 40-query
answerable development population. It does not read or tune against the
holdout split. The candidate keeps the 250 whitespace-field source budget and
checks the complete serialized response with the pinned cl100k tokenizer.

## Diagnosis and change

The v4 bundles often contained a correct symbol but only a narrow snippet of
its definition. That is enough for name lookup, but not for questions whose
answer is in a caller or in another part of the declaration. V5 therefore:

1. hydrates the complete Go declaration named by each primary/candidate item;
2. for lifecycle/flow questions only, follows one query-ranked identifier to
   the highest-ranked production declaration that uses it;
3. completes compact declaration units before generic line growth;
4. excludes test files from definition hydration and preferred-unit choice;
5. preserves the stronger item symbol/priority when duplicate anchors merge.

The repository hydration is bound into the response provenance digest. The
frontier builds every response twice from a clean Cobra checkout pinned at
`a0a6ae020bb3899ff0276067863e50523f897370` and requires byte equality.

## Result at the registered 250 budget

| Measure | compact-dev/4 | compact-dev/5 |
|---|---:|---:|
| Queries with any grade-3 overlap | 40/40 | **40/40** |
| Queries with a complete grade-3 span | 8/40 | **24/40** |
| Byte-identical rebuilds | 40/40 | **40/40** |
| Mean serialized cl100k tokens | — | **928.0** |
| Median serialized cl100k tokens | 916 | **923.5** |
| Maximum serialized cl100k tokens | 1,169 | **1,107** |
| Responses within 1,200 tokens | 40/40 | **40/40** |
| Paired median saving vs GrepRead/1 | +132.5 | **+108** (9.77%) |
| Candidate cheaper than GrepRead/1 | 32/40 | **30/40** |

This is a stronger answer-containment result, not yet evidence that an agent
can answer 36/40 questions. The qrel-blind sufficiency run is the next gate.
The full release decision also remains `NO` until the frozen release evaluator
accepts the new blind outcome and every comparator requirement.

## Reproduce

From the repository root, with a clean pinned Cobra checkout:

```sh
GRAPHI_COMPACT_WIRE_DEV_OUT=docs/eval/retrieval/runs/2026-09-13-compact-wire-v5-dev/frontier.json \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

The source-budget row used by the blind diagnostic is
`source_budget_whitespace_tokens = 250` in `frontier.json`.
