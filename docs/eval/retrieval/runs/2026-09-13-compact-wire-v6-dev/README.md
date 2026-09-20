# Compact wire v6 — development frontier

Status: **development diagnostic, not a release result**.

This run measures `task_context/compact-dev/6` only on the frozen 40-query
answerable development population. It does not read or tune against the
holdout split. The candidate keeps the registered 250 whitespace-field source
budget and checks the complete serialized response with the pinned cl100k
tokenizer.

## Diagnosis and change

The preregistered v5 blind run scored 28/40 even though every response touched
a grade-3 span. The common failures contained the correct name or a nearby
line but omitted the operation that made the answer defensible. V6 therefore:

1. hydrates the enclosing production Go declaration for a query-only GrepRead
   hit when its body exceeds GrepRead's fixed 40-line window;
2. keeps that depth in the bounded fallback channel so semantic breadth is not
   displaced;
3. reserves a second window at control-flow landmarks such as
   `getCompletions`, `cmd.execute`, `ParseFlags`, and `preRun`;
4. follows one small lifecycle callee, allowing `OnInitialize` -> `execute` ->
   `preRun` -> `initializers` to fit in the same response;
5. retains stronger item metadata when two sources deduplicate to the same
   span;
6. recognizes exact API names inside natural-language questions and preserves
   CLI flag intent carried by spellings such as `--help`.

All additions are query-derived and judgement-blind. Repository-derived bytes
remain bound into the response provenance digest.

## Result at the registered 250 budget

| Measure | compact-dev/5 | compact-dev/6 |
|---|---:|---:|
| Queries with any grade-3 overlap | 40/40 | **40/40** |
| Queries with a complete grade-3 span | 24/40 | **28/40** |
| Byte-identical rebuilds | 40/40 | **40/40** |
| Mean serialized cl100k tokens | 928.0 | **927.85** |
| Median serialized cl100k tokens | 923.5 | **932** |
| Maximum serialized cl100k tokens | 1,107 | **1,107** |
| Responses within 1,200 tokens | 40/40 | **40/40** |
| Paired median saving vs GrepRead/2 | +108 | **+106** (9.48%) |
| Candidate cheaper than GrepRead/2 | 30/40 | **31/40** |

The stricter complete-span proxy improves by four queries without weakening
overlap, reproducibility, or the 1,200-token ceiling. It is still not a release
result: only a fresh preregistered blind sufficiency run can establish whether
at least 36/40 bundles are answerable.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-compact-wire-v6-dev/frontier.json" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

Artifact SHA-256:
`25939e341dd6e21c332f2346aed756e608c92486de042ebe9877cfd49a81c469`.
