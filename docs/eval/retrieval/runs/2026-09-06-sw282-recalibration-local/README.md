# SW-282 recalibration run — the report `docs/eval/retrieval-targets.json` is derived from

**Development-only. Comparators only. This report sets the targets; it does not gate them.**

| | |
|---|---|
| Story / AC | SW-282 AC-4 |
| Repository | `cobra` @ `a0a6ae020bb3899ff0276067863e50523f897370` (`corpus/manifest.json` pin) |
| Dataset | `cobra-v2-dev` — the development-only slice of the frozen release dataset `internal/eval/retrieval/testdata/datasets/cobra-v2.json` (sha256 `7de5ce6e…`), produced by `retrieval.SelectDevSplit`. 44 development queries: 40 answerable, 1 excluded (`cb-31`), 3 `no_hit`. Slice sha256 `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`, checked in here as `dataset.json`. |
| Embedder | `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (SW-262 production static embedder) |
| Candidate | `e824197cf4610e3824587e0cb76dcb7a17d9410f+dirty` |
| Runner class | `local` |

## Why the baseline set is only four

`lexical`, `hybrid_v1`, `semantic_name_only` are the comparators; `oracle_upper_bound` supplies the
ceilings. `chunk_only`, `fusion` and `semantic_first` are the **candidate pipeline** and are absent
by design: a bar set to "the candidate plus 0.10" restates what the candidate already does. Since
SW-282 that exclusion is a **refusal inside `retrieval.DeriveTargets`**, not a convention about how
the command was invoked — `-derive` over a report carrying any of the three exits non-zero.

`DeriveTargets` also **refuses** a report carrying any `split == holdout` row rather than filtering
it out, which is why this run is over a development-only slice: silently dropping holdout rows
cannot be told apart from never having executed them, and the holdout may not be touched.

## Measured aggregates (whole dev slice, 41 of 44 scored — `no_hit` is not scored on recall)

| baseline | top1 | recall@5 | recall@10 | mrr@10 | ndcg@10 |
|---|---|---|---|---|---|
| `lexical` | 0.146 | 0.141 | 0.174 | 0.166 | 0.163 |
| `hybrid_v1` | 0.415 | 0.313 | 0.391 | 0.514 | 0.382 |
| `semantic_name_only` | 0.439 | 0.330 | 0.438 | 0.518 | 0.419 |
| `oracle_upper_bound` | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |

`semantic_name_only` is no longer `unavailable`. That is the whole reason SW-266 asked for this
recalibration: the SW-258 bars were set before any embedder existed, and on the conceptual strata
the best single baseline is now the semantic one.

## What it changed in the targets file

| stratum | rule | old bar (SW-258, `cobra-v1`, no embedder) | new bar (`cobra-v2-dev`, static embedder) |
|---|---|---|---|
| `nl_behaviour` | best single baseline + 0.10, capped at the ceiling | 0.4058272100782391 (`hybrid_v1` 0.30582721…) | **0.544970253069991** (`semantic_name_only` 0.44497025…) |
| `architecture_flow` | same | 0.32862302249679975 (`hybrid_v1` 0.22862302…) | **0.4578575262772977** (`semantic_name_only` 0.35785752…) |
| `exact_identifier` | Top-1 no-regression floor = best single baseline's Top-1 | 0.75 (`lexical`) | **1** (`semantic_name_only`) |

The shipped pipeline's verdict against those bars is **not** taken from this report — see
`../2026-09-06-sw282-gate-local/` and `docs/eval/retrieval/targets-gate-expectations.json`.
Calibrating and gating on the same observations is not a gate, and
`retrieval.CheckTargets` refuses a report whose digest equals `derived_from.sha256`.

## Reproduce

```bash
# 1. the development-only slice
SW282_DEV_SLICE_OUT=/tmp/cobra-v2-dev.json CGO_ENABLED=0 \
  go test ./internal/eval/retrieval -run '^TestSW282_WriteDevSlice$' -count=1 -v

# 2. the report
GRAPHI_STATIC_MODEL_DIR=<pinned potion-code-16M-v2 model dir> CGO_ENABLED=0 \
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo cobra \
  -checkout <cobra checkout at a0a6ae02…> \
  -dataset /tmp/cobra-v2-dev.json \
  -baseline lexical -baseline hybrid_v1 -baseline semantic_name_only -baseline oracle_upper_bound \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out /tmp/sw282-recalibration-report.json \
  -export-raw docs/eval/retrieval/runs/2026-09-06-sw282-recalibration-local

# 3. the targets file
CGO_ENABLED=0 go run ./cmd/retrieval-eval -derive \
  -targets-report docs/eval/retrieval/runs/2026-09-06-sw282-recalibration-local/cobra-v2-dev-report.json \
  -targets-out docs/eval/retrieval-targets.json -date 2026-09-06
```

`-aggregate` is **not** applicable to this directory: its closed-world check requires the harness's
full default baseline set, and this run is deliberately a four-baseline subset. The run that the
targets are gated against (`../2026-09-06-sw282-gate-local/`) carries the full set and does
round-trip, and `TestAC9Evidence_RoundTripsFromRaw` checks it on every PR.
