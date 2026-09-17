# Compact/17 release-candidate ranking evidence

Status: **development evidence, not a release verdict**. This run evaluates
the committed development population only. It does not read or score a
holdout query.

The report is bound to production candidate
`26a36f0958cb16d925c4dd871cda76d0e117b15a`, Cobra
`a0a6ae020bb3899ff0276067863e50523f897370`, the production static embedder,
and the committed `cobra-v2-dev` dataset (44 queries). The frozen targets and
the 1,200-token product budget were not changed.

## Result

The `semantic_first` candidate meets every ranking target:

- architecture-flow nDCG@10: `0.46246715228468249` (required
  `0.4578575262772977`);
- natural-language nDCG@10: `0.70290472235070689` (required
  `0.54497025306999103`); and
- exact-identifier Top-1: `1.0` (required `1.0`).

The report SHA-256 is
`556e93fcbccefb392fd32f620d67614a9cbb95cfb0f87bd54aa445b2ee01b70e`.
The aggregate independently reconstructed all **761/761** published metrics
from `dataset.json` and `raw/`, with 0 discrepancies and 0 unknowns.

Bundle sufficiency remains a separate blind gate. This development run cannot
turn a holdout `RELEASE: NO`, a refused run, or a missing outcome into YES.

## Reproduce

```sh
CGO_ENABLED=0 \
GRAPHI_STATIC_MODEL_DIR=/abs/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
go run ./cmd/retrieval-eval \
  -repo cobra \
  -checkout /abs/path/to/cobra-at-a0a6ae02 \
  -dataset docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local \
  -date 2026-09-15 \
  -out docs/eval/retrieval/runs/2026-09-15-compact17-release-dev/cobra-v2-dev-report.json \
  -export-raw docs/eval/retrieval/runs/2026-09-15-compact17-release-dev

go run ./cmd/retrieval-eval -aggregate \
  docs/eval/retrieval/runs/2026-09-15-compact17-release-dev
```
