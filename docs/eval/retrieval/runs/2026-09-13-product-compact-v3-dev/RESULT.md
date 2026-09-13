# Final-audit compact task_context/2 — candidate-bound development result

Status: **development evidence, not a release result**.

Frozen candidate: `a3089de24d574d3675e9b09b55e85451688f0228`.

This run exercises the exact production path twice from independent indexes:

`MCP stdio -> task_context/2 -> semantic retrieval -> one bounded source snapshot -> compact structuredContent -> exact JSON-RPC bytes`.

The capture verifies a clean candidate worktree matching the frozen SHA and a
clean Cobra checkout at `a0a6ae020bb3899ff0276067863e50523f897370`.
Only this preregistered run directory is excluded from the candidate-tree
comparison. The sealed holdout was not opened.

## Result

- 44/44 final MCP responses, digests and tokenizer counts were identical across
  two independently built indexes with distinct freshness generations.
- Equal-recall grade-3 overlap was 40/40. At least one complete grade-3 span was
  present in 26/40; all grade-3 spans overlapped in 30/40 and were fully
  contained in 14/40.
- All 40 answerable responses passed the validator's recomputed whole-response
  cl100k ceiling of 1,200. Mean / median / maximum were 911.12 / 915 / 1,086.
- Against the preserved GrepRead/2 development baseline, 32/40 responses were
  cheaper and 8/40 were more expensive. Paired median saving was 110.5 tokens,
  or 9.8761%.
- The independent preregistered blind development sufficiency evidence remains
  37/40 and is not conflated with this source-overlap/cost diagnostic.

The candidate additionally has executable negative tests for the audit
boundaries: exact wire over-budget and malformed model identity; source file,
file-count and total-byte caps; invalid UTF-8 byte accounting; cancellation;
single-snapshot reuse by follow-up reads and reference hydration; root-escaping
symlinks; negative token budgets; and graceful non-ready retrieval fallback.

## Reproduce

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v3-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=a3089de24d574d3675e9b09b55e85451688f0228 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The exact `captures.json` SHA-256 is
`dd86486eee35622d981b8a0b00a5bc2194f9320d01af085a83adb52cdfcd9933`.

The development product check is:

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v
```

It reports `reached=40/40`, `within_1200=40/40`, and no misses. The historical
ranking check still passes architecture flow (0.4624671523), exact identifier
(1.0), natural-language behavior and bundle coverage. Its command exits 1 only
because the spent old holdout remains correctly immutable at `RELEASE: NO`.
A fresh independently operated holdout is still required for `RELEASE: YES`.
