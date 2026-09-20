# Recursive tree-walk projection development result

Status: **development improvement; not a release result**.

This run uses only the 40 answerable development queries from dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6`.
The spent holdout split was not opened or used for tuning. The serialized MCP
ceiling remains 1,200 `cl100k_base` tokens and the CGo-free build contract is
unchanged.

## Diagnosis

The remaining failure is not explained by changing the embedding model alone.
For development query `cb-22` (how the documentation generator walks the
command tree), the correct recursive `GenMarkdownTreeCustom` declaration was
already present in the projection candidate set and its complete 110-field
unit fit the source budget. The selector nevertheless completed higher-priority
wrappers first and emitted only one-line fragments from the recursive walker.

The defect is a budget-allocation error: a recursive traversal is an atomic
answer unit for a tree-walk question, but the selector treated it as an
ordinary related declaration. The change recognizes a bounded source shape —
a function or method containing a range loop and a recursive self-call — and
reserves up to two complete units of at most 160 source fields before generic
depth allocation. Two are allowed because repositories commonly implement the
same walk for multiple output formats. No repository name, query ID, answer
span, judgement or held-out value is consulted.

The actor-visible identity changes from `task_context/2-compact/5` to
`task_context/2-compact/6`.

## Before / after

The before row is the committed compact-v8 development result at
`1430daf27e795c8c3a7dcc230d858935044bc33f`. The after row is produced by the
same production projector test after the recursive-walk reservation.

| Measure (40 answerable dev queries) | Before | After |
|---|---:|---:|
| Required grade-3 overlap reached | 40 | 40 |
| Every grade-3 span overlapped | 29 | 29 |
| At least one complete grade-3 span | 30 | 31 |
| Every grade-3 span complete | 16 | 17 |
| Architecture-flow reached | 5/5 | 5/5 |
| Architecture-flow with a complete span | 2/5 | 3/5 |
| Architecture-flow with every span complete | 1/5 | 2/5 |
| Config/docs reached | 19/19 | 19/19 |
| Responses at or below 1,200 real tokens | 40 | 40 |
| Median response tokens | 1,027.5 | 1,030.5 |
| Maximum response tokens | 1,186 | 1,186 |
| Individually cheaper than equal-recall GrepRead/2 | 24/40 | 24/40 |
| Paired median token saving | 46.0 | 46.0 |
| Paired median percent saving | 4.0760% | 4.0760% |

The improvement is narrow but clean: one additional architecture question now
contains the complete recursive implementation, with no lost development
recall, no budget violation and no loss on the registered paired-median cost
measure.

## Negative probes

The following one-variable probes were rejected and are not in the candidate:

| Probe | Observed development result | Decision |
|---|---|---|
| Generic two-hop callee hydration | reached 37/40; any complete 28/40; all complete 13/40 | Reject: high-priority callees displaced better evidence. |
| Hydrate every ranked Markdown section for NL questions | reached 35/40; any complete 27/40; all complete 15/40 | Reject: documentation displaced answer-bearing code. |
| Reduce ordinary NL output from ten regions to six | reached 37/40; architecture reached 4/5; config/docs reached 17/19 | Reject: extra depth did not compensate for lost breadth. |
| Hydrate small GrepRead declarations for all how-to questions | reached 40/40; any complete 29/40; all complete 15/40 | Reject: fallback declarations displaced semantic candidates. |
| Raise the internal source frontier to 400 fields | 40/40 within 1,200, but median 1,145 tokens, only 11/40 individually cheaper, paired median saving -53 tokens | Reject: it abandons the token-savings claim for only one more complete query. |

These probes show that the next improvement needs grouped/atomic selection,
not globally more candidates, more Markdown, fewer sources or a wider internal
budget.

## Release status

The latest independent holdout remains **RELEASE: NO**, 42/64 with a
pre-registered passing count of 56. It evaluated the preceding compact/5
candidate, not this development-only compact/6 change. No third holdout was
spent on this single-query development gain. A release YES still requires a
materially stronger, pre-registered candidate and a new independent holdout;
the two spent holdouts must not be rerun or inspected for tuning.

## Reproduce

```sh
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v

CGO_ENABLED=0 go test ./engine/agenttools/taskctx/compact/v9 \
  -run '^TestTreeWalkSelectionPreservesRecursiveImplementation$' -count=1

CGO_ENABLED=0 go test ./...
```

The first command reports the per-stratum and overall rows above. The second
pins the structural regression. The full repository suite passes CGo-free.
