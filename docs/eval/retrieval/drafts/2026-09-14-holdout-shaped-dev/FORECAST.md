# Forecast of candidate `40133db5` on the unreviewed draft

Status: **indicative only.** The dataset is an unreviewed draft written by
the candidate's author (`REVIEW.md`); the numbers below can be moved by the
review in either direction and are evidence of nothing. They are recorded
because they are the first measurement on a holdout-shaped split and they
say where the loss is, which the reviewed split will refine rather than
overturn.

Instrument: `TestDraftDevForecast` (`internal/eval/retrieval/draft_forecast_dev_test.go`),
production compact MCP path, static embedder
`potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`, one index build,
dataset SHA-256 `d7af540a0515c26a8720cac824c59c86d707f98cf256d2d60249d0bb5c789d4f`,
pinned checkout `a0a6ae020bb3899ff0276067863e50523f897370`.

## Aggregate

| Measure (64 draft questions, one grade-3 span each) | Count | Rate |
|---|---:|---:|
| Span overlapped by at least one emitted source | 50/64 | 78.1 % |
| Span cited (a source starts inside it) | 41/64 | 64.1 % |
| Span delivered complete in one source | 30/64 | 46.9 % |
| Mean share of span lines delivered | — | 63.9 % |
| Median / max response tokens | 1,019 / 1,200 | — |

The last sealed holdout scored 42/64 = 65.6 % under its rubric, between this
draft's overlap and complete rates. Read either as a forecast of the rubric:

| If the true per-question rate were | P(≥ 56 of 64) |
|---|---:|
| 46.9 % (complete) | ≈ 0 |
| 78.1 % (overlapped) | 0.042 |

## Per stratum

| Stratum | Overlapped | Cited | Complete |
|---|---:|---:|---:|
| exact_identifier | 11/11 | 10/11 | **11/11** |
| ambiguous | 8/10 | 8/10 | 8/10 |
| exact_path | 11/11 | 11/11 | 2/11 |
| nl_behaviour | 6/11 | 5/11 | 4/11 |
| config_docs | 6/10 | 0/10 | 3/10 |
| architecture_flow | 8/11 | 7/11 | 2/11 |

## Where the 34 misses are

Classified from the emitted sources, not from ranks:

- **14 questions receive nothing from their span** (share 0.00): five
  `nl_behaviour`, three `architecture_flow`, four `config_docs`, two
  `ambiguous`. These are retrieval or ranking misses; no projection change
  can recover them. Examples: `cd-53` ("stop cobra from sorting commands in
  the help output", target `EnableCommandSorting`) is answered entirely with
  help-template code; `cd-33` (`Commands()` sorting) emits the sorter's
  one-line methods but never the method that calls them.
- **9 `exact_path` questions deliver 86–94 % of their span in pieces**: the
  outline mode emits each declaration as its own region and drops the blank
  line between them (`active_help.go:24-31` + `33-33` for a target `24-33`).
  For a reader this is the answer; for the complete-span measure it is not.
  Whether the sealed holdouts' `exact_path` rubric counts it is unknown to
  the author. The reviewer who knows the rubric decides whether these nine
  are quality losses or a measurement artifact.
- **11 questions reach the span but cut it** (share 0.03–0.67): mostly
  `architecture_flow` and `nl_behaviour` bodies reduced to one- or two-line
  anchors beside unrelated regions (`cd-36`, `cd-42`, `cd-24`). These are the
  depth-versus-breadth losses the earlier frontier sweep showed budget does
  not fix; they are ranking-order losses inside the compact selector.

## What this changes in the plan

Nothing in the ranking of levers from `runs/2026-09-14-holdout-answer-span-ceiling/RESULT.md`;
it puts numbers on them for the holdout's shape. Retrieval recall and
ranking (14 + 11) dominate. The `exact_path` question is a rubric question
for the reviewer, not an engineering one yet. `exact_identifier` is solved
on this shape.

Reproduce:

```sh
export CGO_ENABLED=0
GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v
```
