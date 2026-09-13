# Frozen-holdout candidate development evidence

Status: **candidate-bound development evidence, not a release result**.

Candidate `fd27bc083df69913746a3f1f10fc13145733edd2` is the exact commit used by
the independently captured fresh holdout. This development-only reproduction
uses the committed 40-query development population and never reads the fresh
holdout dataset.

## Result

- Architecture-flow nDCG@10 is `0.46246715228468249`, above the frozen
  `0.4578575262772977` requirement.
- Exact-identifier Top-1 is `1.0`; NL-behaviour and the existing six-query
  coverage target also pass.
- Two independent Cobra indexes produced byte-identical MCP responses,
  payload digests and real cl100k counts for all `44/44` queries.
- All 40 answerable compact bundles are at or below 1,200 cl100k tokens; their
  range is 758–1,079 tokens. The full 44-query development population ranges
  from 577 to 1,079 tokens.
- Grade-3 overlap remains `40/40`. Against the frozen GrepRead/2 development
  comparator, `32/40` bundles are cheaper and the paired median saving is
  `106.5` tokens (`9.593040233614538%`).

The capture differs from the independently audited V6 capture only in its
candidate binding. Canonical JSON after removing `candidate_binding` has the
same SHA-256 for both files:
`10f6eef3306ba7d959b0762579596b5512adeb5f540e4bcdab9f8714d62ad62a`.

Artifacts:

- `cobra-v2-dev-report.json` SHA-256:
  `5c2c6455249735ad94c5057b695e646251ff3620380270c895a38127bbcee318`
- `captures.json` SHA-256:
  `17328d3f1393d1cc051760356fd6c348eb226104580300f6d373d0bd87e7642f`

`-check-targets` passes all four development targets. Until the fresh blind
decision is complete, its fifth check still reads the immutable historical
`RELEASE: NO` outcome.

## Reproduce

```sh
CGO_ENABLED=0 GRAPHI_STATIC_MODEL_DIR=/path/to/potion-code-16M-v2 \
go run ./cmd/retrieval-eval -repo cobra -checkout /path/to/cobra \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-13 -out /tmp/cobra-v2-dev-report.json

GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=fd27bc083df69913746a3f1f10fc13145733edd2 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v
```
