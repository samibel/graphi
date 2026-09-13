# Compact wire v7 — development frontier

Status: **development diagnostic, not a release result**.

This run evaluates `task_context/compact-dev/7` on the frozen 40-query
answerable development population only. It does not open or tune against the
holdout split. The registered source budget remains 250 whitespace fields and
the complete serialized MCP response must fit the unchanged 1,200-token
cl100k ceiling.

## Diagnosis and change

The v6 blind run passed 25/40. Its failures were not one ranking defect: the
correct file or symbol was often present, but the compact response retained a
comment, signature, or nearby branch instead of the semantic unit needed to
answer. The exact-identifier regression was the clearest instance of the same
problem: fusion could outrank the declaration whose complete name was given.

V7 therefore changes source allocation, without changing the measurement:

1. exact identifiers receive a hard whole-name floor, then a bounded
   repository-derived dependency closure;
2. AST declarations are completed without spilling into the following
   declaration, and related/caller/callee evidence is eligible for hydration;
3. flow questions reserve bounded secondary windows at control-flow landmarks
   and allocate fewer, deeper causal regions for traversal, initialization,
   lifecycle, required-flag, help/version and completion-protocol questions;
4. fixed GrepRead windows can contribute the complete small Go declarations
   they overlap, which preserves callback signatures and bodies;
5. exact and flow-specific duplicate fragments no longer spend budget that a
   containing declaration already supplies;
6. the final serialized response is reselected against the pinned real
   tokenizer until it is at most 1,200 tokens. Test counters retain their
   existing behavior.

All added source is selected from query text, ranked artifacts and repository
syntax. No judgement or answer span is consulted during bundle construction.

## Result at the registered 250 source budget

| Measure | compact-dev/6 | compact-dev/7 |
|---|---:|---:|
| Queries with any grade-3 overlap | 40/40 | **40/40** |
| Queries with a complete grade-3 span | 28/40 | 25/40 |
| Byte-identical rebuilds | 40/40 | **40/40** |
| Mean serialized cl100k tokens | 927.85 | **902.25** |
| Median serialized cl100k tokens | 932 | **899** |
| Maximum serialized cl100k tokens | 1,107 | 1,125 |
| Responses within 1,200 tokens | 40/40 | **40/40** |
| Paired median saving vs GrepRead/2 | +106 | **+127.5** (11.77%) |
| Candidate cheaper than GrepRead/2 | 31/40 | **32/40** |

The complete-span proxy decreases by three, while inspected causal units and
wire cost improve. This is intentionally reported as a negative proxy result:
v6 already proved that qrel containment is not a sufficiency score. Only a new
pre-registered blind run can decide whether v7 reaches k=36/40.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
GRAPHI_COMPACT_WIRE_DEV_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-compact-wire-v7-dev/frontier.json" \
GRAPHI_COMPACT_WIRE_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json" \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

Artifact SHA-256:
`9d19fe82aff8b6bebff45eab613d331f219ee7cab1119daea1b70580b08ba1a8`.
