# Development result: coherent compact task-context selector

Status: **development diagnostic, not release evidence**. This run uses only
the 40 grade-3-answerable questions in the extracted Cobra development slice.
No holdout query was loaded or executed. The production MCP route, frozen
1,200-token budget, methodology, thresholds, and scoring rules are unchanged.

## Change

`task_context/compact-dev/2` replaces breadth-first, line-at-a-time allocation
with query-aware ranking of whole regions and a maximum of eight coherent
sources for natural-language questions (four for exact identifier/path
queries). It discounts `_test.go` name/comment matches unless the query asks
for tests, uses inverse candidate frequency for query concepts, reserves more
depth for higher ranks, and redistributes unused quota to the strongest
regions. Exact source bytes and line citations are unchanged.

## Result

At the same 140 whitespace-field source budget:

| Measure | compact-dev/1 | compact-dev/2 |
| --- | ---: | ---: |
| grade-3 overlap | 40/40 | 40/40 |
| complete grade-3 span | 0/40 | 8/40 |
| median source fragments | 15 | 8 |
| mean cl100k response tokens | 823.9 | 720.3 |
| median cl100k response tokens | 855.0 | 720.5 |
| paired median saving vs GrepRead/2 | +18.3% | +30.7% |
| candidate cheaper than GrepRead/2 | 32/40 | 32/40 |

The representation became both deeper and cheaper because removing seven
median source objects saves repeated path/span/JSON metadata. This mechanical
frontier does not establish answer sufficiency. The next required measurement
is a fresh, preregistered two-reader blind development run.

The complete 13-point frontier is in `frontier.json`, SHA-256
`bde8f6b2f18013590ed0a69efd556096e30c995399af9933e8599b1ae4d3e928`.
Every row was independently rebuilt byte-identically for all 40 queries.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
export CGO_ENABLED=0
export GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact"
export GRAPHI_COMPACT_WIRE_DEV_COBRA=/absolute/path/to/cobra
export GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json"
export GRAPHI_COMPACT_WIRE_DEV_OUT=/tmp/compact-wire-v2-frontier.json

go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v
```

Regression and wire tests:

```sh
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDev' -count=1
```
