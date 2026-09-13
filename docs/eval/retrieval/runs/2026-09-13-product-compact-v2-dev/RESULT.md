# Candidate-bound compact task_context/2 — development result

Status: **development evidence, not a release result**.

Frozen candidate: `4864b1895119209eec0d60c122df2a58069d5316`.

The capture records and verifies that both the candidate worktree and the pinned
Cobra checkout were clean, that the observed candidate matched the frozen SHA,
and that only this predeclared run directory was excluded from the comparison.
It uses the committed development slice only; it does not open the sealed
holdout.

## Result

- 44/44 final MCP responses, SHA-256 digests and tokenizer counts were identical
  across two independently built indexes with distinct freshness generations.
- Equal-recall grade-3 overlap was 40/40. At least one complete grade-3 span was
  present in 26/40 responses; all grade-3 spans overlapped in 30/40 and were
  fully contained in 14/40.
- The capture validator recomputed the complete serialized response with the
  pinned cl100k tokenizer. All 40 answerable responses were at most 1,200
  tokens; mean / median / maximum were 911.30 / 917.5 / 1,082.
- Against the preserved GrepRead/2 development transcript, 32/40 responses were
  cheaper and 8/40 were more expensive. Paired median saving was 110.5 tokens,
  or 9.9472%.
- The unchanged ranking report still passes architecture flow at 0.4624671523,
  exact identifier at 1.0, natural-language behavior, and bundle coverage.
- The preregistered blind development sufficiency result remains 37/40. It is
  separate evidence and is not upgraded by this structural capture.

The audit fixes in this candidate move the shared real tokenizer into `core`,
make the release capture fail closed above the exact wire budget, bound and
cancel source discovery, prevent repository escape through source symlinks, and
preserve the documented lexical fallback when retrieval is not ready. The
compact source-selection behavior on the pinned Dev checkout remains 40/40 for
the overlap target.

## Reproduce

Use a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370` and a clean checkout of the frozen
candidate:

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v2-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=4864b1895119209eec0d60c122df2a58069d5316 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The exact `captures.json` SHA-256 is
`f18a21a4443d1a039427c168a27243110fe495e20d7b4edfc501acacfd9f6896`.

The development product target is reproduced with:

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestProductCompactTaskContextDev$' -count=1 -v
```

This reports `reached=40/40`, `within_1200=40/40`, and no misses. A fresh,
independently operated holdout is still required before `RELEASE: YES`.
