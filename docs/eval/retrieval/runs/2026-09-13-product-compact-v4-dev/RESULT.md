# Sealed-snapshot compact task_context/2 — candidate-bound development result

Status: **development evidence, not a release result**.

Frozen candidate: `e95543d5dafc6f8bf390891c96f501b1e15b2c4d`.

This run exercises the exact production path twice from independent indexes:

`MCP stdio -> task_context/2 -> semantic retrieval -> one bounded source snapshot -> compact structuredContent -> exact JSON-RPC bytes`.

The candidate closes the final independent-audit findings without changing the
ranking rules, frozen 1,200-token budget, methodology, targets, or dev split:
an explicit empty snapshot cannot authorize direct reads; the shared snapshot
contains Markdown for optional hydration but searches only Go; cancellation is
checked during search and AST work; and partial read bytes are charged before
an I/O error is classified.

## Result

- 44/44 final MCP responses, digests and tokenizer counts were identical across
  two independently built indexes with distinct freshness generations.
- Equal-recall grade-3 overlap was 40/40. At least one complete grade-3 span was
  present in 26/40; all grade-3 spans overlapped in 30/40 and were fully
  contained in 14/40.
- All 40 answerable responses passed the recomputed whole-response cl100k
  ceiling of 1,200. Mean / median / maximum were 910.075 / 914.5 / 1,079.
- Against the preserved GrepRead/2 development baseline, 32/40 responses were
  cheaper and 8/40 were more expensive. Paired median saving was 106.5 tokens,
  or 9.5930%.
- The product dev check independently reported `reached=40/40`,
  `within_1200=40/40`, with a 1,085-token maximum across all checked rows.
- The historical retrieval target check remains green for architecture flow
  (0.4624671523 >= 0.4578575263), exact identifier (1.0), natural-language
  behavior and bundle coverage. Its only red result is the immutable, spent old
  holdout; this development run does not replace that release evidence.

The sealed holdout was not opened.

## Reproduce

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v4-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=e95543d5dafc6f8bf390891c96f501b1e15b2c4d \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The exact `captures.json` SHA-256 is
`a76428b26438b6336b7ed9beed1018771ffa8f3bf189fadab2ff7b7c7c9d7dd0`.

The development product check is:

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v
```

A fresh, preregistered, independently operated sealed holdout is still required
before the repository may report `RELEASE: YES`.
