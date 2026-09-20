# Development result: compact task-context wire frontier

Status: **development diagnostic, not release evidence**. This run uses only
the 40 grade-3-answerable development questions from the frozen Cobra dev
slice. The sealed holdout was not loaded, read, or executed. The production
MCP route, the 1,200-token maximum, methodology, thresholds, and historical
artifacts are unchanged.

## Result

The original `task_context/2` response is JSON serialized into an MCP text
field, while verbose item/evidence joins and provenance are repeated on the
wire. A development-only representation instead returns a concise text
fallback plus one directly encoded `structuredContent` object. It keeps ranked
source spans (`path`, `start_line`, `end_line`, `text`) and provenance, and
binds omitted ranking detail to the SHA-256 of the complete input bundle.

Source selection is judgement-blind. It ranks the bundle's existing snippets
from query terms and the exact-definition signal, admits one strongest complete
line per source before spending on surrounding context, and never consults a
qrel until after response bytes are final.

| Source budget | Equal-recall reached | Complete grade-3 span | Candidate median cl100k | Paired median saving | Candidate cheaper |
|---:|---:|---:|---:|---:|---:|
| 80 | 39/40 | 0/40 | 641.0 | +36.0% | 32/40 |
| 120 | 39/40 | 0/40 | 809.5 | +22.5% | 32/40 |
| **140** | **40/40** | **0/40** | **855.0** | **+18.3%** | **32/40** |
| 180 | 40/40 | 0/40 | 932.0 | +10.9% | 31/40 |
| 200 | 40/40 | 1/40 | 976.5 | +7.9% | 29/40 |
| 250 | 40/40 | 2/40 | 1071.5 | -1.5% | 18/40 |
| 600 | 40/40 | 14/40 | 1792.0 | -81.1% | 2/40 |
| 1200 | 40/40 | 37/40 | 3252.5 | -216.7% | 0/40 |

The comparator is the explicitly versioned development `GrepRead/2`
prototype in `../2026-09-07-grepread-v2-dev/`: 40/40 equal-recall, mean 990.2
and median 1076 cl100k tokens to its earliest successful response prefix.

The full 13-point machine-readable frontier is in `frontier.json`, SHA-256
`31b66f157a653008af3f9dec8fb2803b352223611ee0ea5d82cc707612e35ade`.
Two independent generations were byte-identical.

## Interpretation

Budget 140 is the first measured row reaching the frozen atomic
`tokens_to_exact_span` target on all 40 development questions while retaining
a positive paired median. It is **not** evidence that an independent reader
can answer all 40 questions. At that row no reviewed grade-3 span is fully
contained; the historical blind evaluation already proved that mechanical
overlap and answer sufficiency can disagree. A blind development dry-run is
therefore required before this representation is considered for the product
surface.

This also rejects two simpler conclusions:

- compact serialization alone is insufficient at the old depth allocation;
- retaining the full 1,200 source tokens cannot support the savings claim
  against a judgement-blind comparator that reaches the same atomic target.

## Reproduce

From the repository root, with a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
export CGO_ENABLED=0
export GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact"
export GRAPHI_COMPACT_WIRE_DEV_COBRA=/absolute/path/to/cobra
export GRAPHI_COMPACT_WIRE_DEV_GREPREAD="$PWD/docs/eval/retrieval/runs/2026-09-07-grepread-v2-dev/grepread-v2.json"
export GRAPHI_COMPACT_WIRE_DEV_OUT=/tmp/compact-wire-frontier.json

go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDevFrontier$' -count=1 -v

cmp docs/eval/retrieval/runs/2026-09-07-compact-wire-dev/frontier.json \
  /tmp/compact-wire-frontier.json
```

Unit validation:

```sh
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestCompactTaskContextDev_' -count=1
```
