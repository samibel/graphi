# SW-282 gate run — the report `docs/eval/retrieval-targets.json` is ENFORCED against

**Development-only. Full default baseline set. This report is graded; it sets nothing.**

| | |
|---|---|
| Story / AC | SW-282 AC-7, AC-8 |
| Repository | `cobra` @ `a0a6ae020bb3899ff0276067863e50523f897370` |
| Dataset | `cobra-v2-dev` (sha256 `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`) — the same development-only slice the recalibration used, so the bar and the measurement describe the same population |
| Embedder | `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` |
| Candidate | `e824197cf4610e3824587e0cb76dcb7a17d9410f+dirty` (`retrieval.GateCandidateSHA`) |
| Report | `cobra-v2-dev-report.json`, sha256 `39ac0140a93f106dc990b3545bcd6d459ae3f0d003220881e6a443db5d0f7edf` |

This is a **separate execution** from `../2026-09-06-sw282-recalibration-local/`. SW-266's
constraint — "a threshold set and passed on the same observations is not a gate" — is satisfied
structurally: the derivation report carries no candidate-pipeline baseline at all, and
`retrieval.CheckTargets` **refuses** a report whose digest equals the targets file's
`derived_from.sha256`.

No holdout query was measured, here or anywhere in SW-282. The holdout was opened by SW-280 and is
spent for prospective validation.

## Measured aggregates (whole dev slice, 41 of 44 scored)

| baseline | top1 | recall@5 | recall@10 | mrr@10 | ndcg@10 |
|---|---|---|---|---|---|
| `lexical` | 0.146 | 0.141 | 0.174 | 0.166 | 0.163 |
| `hybrid_v1` | 0.415 | 0.313 | 0.391 | 0.514 | 0.382 |
| `semantic_name_only` | 0.439 | 0.330 | 0.438 | 0.518 | 0.419 |
| `oracle_upper_bound` | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| `chunk_only` | 0.415 | 0.313 | 0.391 | 0.514 | 0.382 |
| `fusion` | 0.512 | 0.406 | 0.467 | 0.640 | 0.479 |
| `semantic_first` (shipped) | 0.512 | 0.444 | 0.552 | 0.622 | 0.510 |

## Verdict against the recalibrated targets

`go run ./cmd/retrieval-eval -check-targets docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/cobra-v2-dev-report.json`
— exit **1**, three of five targets missed. The verdict is recorded verbatim in
`docs/eval/retrieval/targets-gate-expectations.json` and re-derived on every PR by
`TestTargets_GateVerdictMatchesCheckedInExpectations`, which fails on drift **in either direction**.

| target | required | observed | verdict |
|---|---|---|---|
| `nl_behaviour` fusion_target | `semantic_first` ndcg@10 ≥ 0.54497025306999103 | 0.63677401077084517 | **PASS** |
| `architecture_flow` fusion_target | ≥ 0.4578575262772977 | 0.32777888533499866 | **MISS** by 0.13007864094229904 |
| `exact_identifier` no_regression | `semantic_first` top1 ≥ 1 | 0.75 | **MISS** by exactly one query of four |
| `bundle_coverage` | 6 of 6 covered, 0 misses, budget 1200 | 6 of 6 | **PASS** |
| `qrel_blind_smoke` | outcome present and `RELEASE: YES` | `RELEASE: NO` (31 of 64 against k=56) | **MISS** |

**The misses are recorded, not fixed and not excepted.** SW-282 sets targets and may not change
retrieval behaviour; the fix is the recovery story. The release-line `retrieval-targets` gate is
therefore RED until that story lands, which is the gate working: a bar the product misses should
block a release, and the alternative — carrying forward a bar set before any embedder existed — is
the thing SW-266 asked this story to stop doing.

Two notes on reading the misses honestly:

- `architecture_flow` has **five** development queries. Its aggregate is a mean over five per-query
  values, so a shortfall smaller than 1/5 is not evidence of a difference. This shortfall is 0.130 —
  about two thirds of one query's influence, on a bar that is now 0.458 rather than 0.329. It is a
  real miss, not a rounding artefact. (SW-263's recorded −0.00084 MISS against the OLD bar stands as
  history and is not retroactively satisfied by this one; the `0.00085` numeric exception that
  carried it has been deleted, not restated.)
- `exact_identifier` has **four** development queries, so its resolution is 1/4 = 0.25. The floor
  moved from `lexical`'s 0.75 to `semantic_name_only`'s 1.0 because the floor is defined as the best
  single baseline's Top-1 and the semantic comparator is no longer unavailable. `semantic_first`
  scores 0.75 — exactly one query below the best comparator.

## Reproduce

```bash
GRAPHI_STATIC_MODEL_DIR=<pinned potion-code-16M-v2 model dir> CGO_ENABLED=0 \
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo cobra \
  -checkout <cobra checkout at a0a6ae02…> \
  -dataset /tmp/cobra-v2-dev.json \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-06 \
  -out /tmp/sw282-gate-report.json \
  -export-raw docs/eval/retrieval/runs/2026-09-06-sw282-gate-local

# every published number recomputed from raw/
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-09-06-sw282-gate-local
```
