# Public-version-bound compact task_context/2 — development result

Status: **candidate-bound development evidence, not a release result**.

Frozen product candidate: `4f9a39f809859e9f15e162c30da28900fc946bf4`.

The run uses the committed development slice only and exercises the exact MCP
stdio product path twice from independent indexes. The fresh sealed holdout was
not created, opened or executed.

## Result

- 44/44 final MCP responses, payload digests and real-tokenizer counts were
  identical across the two independent index builds.
- All 88 captured actor-visible payloads identify themselves as
  `task_context/2-compact/4`; V4's stale public `/3` identity is fixed.
- Equal-recall grade-3 overlap was 40/40. At least one complete grade-3 span was
  present in 26/40; all grade-3 spans overlapped in 30/40 and were fully
  contained in 14/40.
- All 40 answerable responses passed the exact whole-response cl100k ceiling of
  1,200. Mean / median / maximum were 910.075 / 914.5 / 1,079.
- Against the preserved GrepRead/2 development baseline, 32/40 were cheaper
  and 8/40 more expensive. Paired median saving was 106.5 tokens, or 9.5930%.
- The separate product check reported `reached=40/40`, `within_1200=40/40`
  and a maximum of 1,085 across its complete checked population.
- The frozen retrieval report remains above the architecture-flow target
  (0.4624671523 >= 0.4578575263), holds exact-identifier Top-1 at 1.0, and
  passes natural-language behavior and bundle coverage.

The candidate also closes the release-audit boundaries: public/internal version
identity cannot drift, one explicit bounded source snapshot supplies discovery
and optional hydration, absent snapshot sources do not trigger unbounded reads,
partial/error reads and post-read file growth fail closed, cancellation reaches
scan/search/AST/sort work, negative token budgets cannot reactivate reads, and
grader packets name the run-local rubric frozen by the precondition record.

## Reproduce

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v5-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=4f9a39f809859e9f15e162c30da28900fc946bf4 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The exact `captures.json` SHA-256 is
`f24cbd8b0ac8e4fb9d45d423fe2c2d9f4dada35197aa13a46379dbc137f28a64`.

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v
```

This evidence authorizes the independent fresh-holdout preparation, not
`RELEASE: YES`. The latter still requires a newly curated sealed dataset and the
write-once preregistered run reserved in the adjacent V5 holdout directory.
