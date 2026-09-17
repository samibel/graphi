# Query-directed projection (compact/17) — development result

Status: **development candidate; not `RELEASE: YES`.** Candidate implementation
`0bcc0e6ea54114221b47850748c2c87619257c61` was developed only against committed
development data. No sealed holdout question, answer, bundle, response, or grade
was opened or used. The 1,200-token response ceiling, 325-word source budget,
targets, scoring rules, and release decision rule are unchanged.

## Diagnosis

The remaining failures were not one retrieval problem. Development traces
separated four projection defects:

1. GrepRead stemmed identifier-shaped words such as `getCompletions`, so exact
   query evidence for a named long declaration disappeared before projection.
2. When query-only discovery did find a named long declaration, it remained in
   the fallback band and spent its depth at the declaration header. A requested
   decision branch hundreds of lines later was outside the emitted bytes.
3. A bare separated identifier such as `post-run` found the `PostRun` field, but
   the enclosing `Command` type has a different symbol name. Retrieval order
   therefore stayed above the declaration line that the exact lexical channel
   had proved.
4. GrepRead hydration skipped declarations shorter than its fixed read window.
   The selector consequently compared arbitrary windows instead of complete
   small functions. Separately, a long Markdown heading section was treated as
   one unit even when the answer was a short paragraph plus its fenced example.

The 40-item transport cap was not widened. Increasing the internal candidate
pool from 15 to 50 was tested and produced no additional complete answer. The
frozen budget was also not widened.

## Change

`task_context/2-compact/17` keeps the existing retrieval ranking and budgets but:

- preserves tokens with internal CamelCase or underscores while still stemming
  ordinary capitalized prose (`Flags` remains `flag`);
- hydrates bounded GrepRead hits to Go declarations for every natural-language
  query, including small declarations;
- promotes a query-named discovered declaration to the same lead class as a
  query-named indexed declaration;
- locates a dense non-name query cluster inside a long named fallback function,
  centers a bounded initial window there, and grows evenly around that cluster;
- promotes an exact field declaration for bare/separated identifier lookup even
  when the enclosing type has another name;
- prefers complete small fallback declarations by distinct-query-term density;
- treats a Markdown paragraph and its immediately attached fenced code example
  as an atomic unit when the enclosing heading section is too large; and
- matches a base verb ending in silent `e` to its inflection for projection
  (`combine` / `combining`) without changing exact-identifier mode.

Focused tests cover each new boundary. None reads a judgement, answer span, or
dataset query ID.

## Before / after

The primary tuning diagnostic is the independently reviewed, holdout-shaped
64-query development dataset.

| Reviewed 64-query development forecast | compact/16 | **compact/17** |
|---|---:|---:|
| One response: overlapped | 58/64 | **61/64** |
| One response: complete | 56/64 | **60/64** |
| Architecture-flow complete | 7/11 | **8/11** |
| Config-docs complete | 9/10 | **10/10** |
| Natural-language complete | 9/11 | **10/11** |
| Ambiguous complete | 9/10 | **10/10** |
| Exact-identifier complete | 11/11 | **11/11** |
| Exact-path complete | 11/11 | **11/11** |
| Absent / below cap / lost in selection | 2 / 3 / 3 | **0 / 2 / 2** |
| Two-call complete | 57/64 | **60/64** |
| Median / maximum response tokens | 925 / 1,160 | **963 / 1,172** |

At the observed complete rate of 60/64, the diagnostic prints
`P(>=56 of 64) = 0.982`. This is planning evidence, not a holdout result.

On the original committed 40-query development split, quality does not regress:
`reached=36/40`, `any_complete=26/40`, and all 40 responses remain within 1,200
tokens. The cost comparison is slightly weaker than compact/16: 26/40 rather
than 27/40 responses are cheaper than equal-recall GrepRead/2, and paired median
saving is 74 rather than 88 tokens. This negative result is retained rather than
hidden. The independently rebuilt MCP capture reports 36/40 with any overlap,
26/40 with any complete grade-3 span, a 994.5 median and 1,170 maximum real-token
count.

## Frozen gates and byte reproducibility

The existing ranking report remains green on every non-release target:

- architecture-flow nDCG@10: `0.46246715228468249` (required
  `0.4578575262772977`);
- natural-language nDCG@10: `0.70290472235070689`;
- exact-identifier Top-1: `1.0`; and
- bundle coverage: `6/6` at 1,200 tokens.

The candidate-bound recovery test built two independent indexes from the pinned
768-document Cobra checkout. Their freshness generations differed, while all
**44/44 final MCP response bytes, payload digests, and tokenizer counts were
identical**. The temporary capture SHA-256 was
`3fa3cc630e113fe62969995163cbc5f9fa313e7ea100947746b384d2fa66455e`.

## Release interpretation

The release verdict remains **NO**. The latest valid preregistered holdout scored
48/64 against `k=56` and is now spent. compact/17 has not seen a fresh holdout.
Its 60/64 development result is strong enough to nominate the frozen candidate
for a new independently curated, preregistered holdout with the unchanged rule.
Only that untouched run can produce `RELEASE: YES`.

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

The target check exits 1 only because its committed, spent qrel-blind result is
`RELEASE: NO`; it prints PASS for the four non-release targets above.

For the independent-index check, run from a clean detached worktree at the
candidate SHA and choose an output directory inside that worktree:

```sh
mkdir -p docs/eval/retrieval/runs/2026-09-15-compact17-projection-dev
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-15-compact17-projection-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/abs/path/to/cobra-at-a0a6ae02 \
GRAPHI_RECOVERY_CANDIDATE_SHA=0bcc0e6ea54114221b47850748c2c87619257c61 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```
