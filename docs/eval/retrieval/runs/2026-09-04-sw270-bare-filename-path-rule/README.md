# SW-270 — bare filename in the exact-path rule, before/after on the SW-258 dev split

Two complete `cmd/retrieval-eval` runs over the pinned Cobra checkout, identical in every input
except the candidate commit: `before/` at `9bf9326bb56e4f27ae330235d3c4687e2c0c445e` (main, the rule requires a
`/`) and `after/` at `71cba5efc3a033012ba47d56ddadaeca6417ca2a` (the SW-270 change: a bare filename with a known
source extension also matches). Every baseline in the harness default set ran both times; every
stratum is reported below, not only the one the change targets (AC-4).

**Resolution:** the dev split has 30 queries, 27 answerable (`no_hit` is excluded from aggregates).
`exact_path` has **n=3; one query is 1/3 (33.3%) of that stratum.** Decimal places are retained for
reproduction, not as a claim of statistical precision.

## Population (AC-5: the holdout split was not read)

The judged bytes are `dataset.json` in each run directory — a derived dataset `cobra-v1-dev`
(sha256 `1436a29b6f1b2432f7c79266fbfcb80000105eaeefcc827489657dc553cec9d5`) containing every and only `split == "dev"` query of the
frozen source `internal/eval/retrieval/testdata/datasets/cobra-v1.json` (sha256
`be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82`). Judgements, grades and the
source's relevance policy (`relevant_min_grade` 2) are inherited unchanged; only the
`id` and `notes` fields differ and the 10 holdout queries are absent. Both reports cite
`holdout_queries: 0` and their `splits.holdout` block reads `UNKNOWN`. No holdout query was
executed, scored, or inspected for this story. The derivation was a mechanical `jq` filter (see
Recompute); no ranking weight, threshold or target was tuned.

## Result — ndcg@10 and top-1, every baseline × every stratum, dev split

| Baseline | Stratum | scored | ndcg@10 before | ndcg@10 after | Δ ndcg@10 | top-1 before | top-1 after |
|---|---|---:|---:|---:|---:|---:|---:|
| lexical | exact_identifier | 4/4 | 0.6985 | 0.6985 | +0.0000 | 0.7500 | 0.7500 |
| lexical | exact_path | 3/3 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| lexical | nl_behaviour | 6/6 | 0.0000 | 0.0000 | +0.0000 | 0.0000 | 0.0000 |
| lexical | architecture_flow | 5/5 | 0.0000 | 0.0000 | +0.0000 | 0.0000 | 0.0000 |
| lexical | config_docs | 5/5 | 0.0000 | 0.0000 | +0.0000 | 0.0000 | 0.0000 |
| lexical | ambiguous | 4/4 | 0.2216 | 0.2216 | +0.0000 | 0.0000 | 0.0000 |
| lexical | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| hybrid_v1 | exact_identifier | 4/4 | 0.7003 | 0.7003 | +0.0000 | 0.7500 | 0.7500 |
| hybrid_v1 | exact_path | 3/3 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| hybrid_v1 | nl_behaviour | 6/6 | 0.3058 | 0.3058 | +0.0000 | 0.5000 | 0.5000 |
| hybrid_v1 | architecture_flow | 5/5 | 0.2286 | 0.2286 | +0.0000 | 0.0000 | 0.0000 |
| hybrid_v1 | config_docs | 5/5 | 0.4112 | 0.4112 | +0.0000 | 0.6000 | 0.6000 |
| hybrid_v1 | ambiguous | 4/4 | 0.4493 | 0.4493 | +0.0000 | 0.2500 | 0.2500 |
| hybrid_v1 | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| semantic_name_only | exact_identifier | 4/4 | 0.9624 | 0.9624 | +0.0000 | 1.0000 | 1.0000 |
| semantic_name_only | exact_path | 3/3 | 0.3290 | 0.3290 | +0.0000 | 0.0000 | 0.0000 |
| semantic_name_only | nl_behaviour | 6/6 | 0.4450 | 0.4450 | +0.0000 | 0.5000 | 0.5000 |
| semantic_name_only | architecture_flow | 5/5 | 0.3579 | 0.3579 | +0.0000 | 0.2000 | 0.2000 |
| semantic_name_only | config_docs | 5/5 | 0.7047 | 0.7047 | +0.0000 | 1.0000 | 1.0000 |
| semantic_name_only | ambiguous | 4/4 | 0.3518 | 0.3518 | +0.0000 | 0.5000 | 0.5000 |
| semantic_name_only | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| oracle_upper_bound | exact_identifier | 4/4 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | exact_path | 3/3 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | nl_behaviour | 6/6 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | architecture_flow | 5/5 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | config_docs | 5/5 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | ambiguous | 4/4 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| oracle_upper_bound | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| chunk_only | exact_identifier | 4/4 | 0.7003 | 0.7003 | +0.0000 | 0.7500 | 0.7500 |
| chunk_only | exact_path | 3/3 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| chunk_only | nl_behaviour | 6/6 | 0.3058 | 0.3058 | +0.0000 | 0.5000 | 0.5000 |
| chunk_only | architecture_flow | 5/5 | 0.2286 | 0.2286 | +0.0000 | 0.0000 | 0.0000 |
| chunk_only | config_docs | 5/5 | 0.4112 | 0.4112 | +0.0000 | 0.6000 | 0.6000 |
| chunk_only | ambiguous | 4/4 | 0.4493 | 0.4493 | +0.0000 | 0.2500 | 0.2500 |
| chunk_only | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| fusion | exact_identifier | 4/4 | 0.7760 | 0.7760 | +0.0000 | 0.7500 | 0.7500 |
| fusion | exact_path | 3/3 | 1.0000 | 1.0000 | +0.0000 | 1.0000 | 1.0000 |
| fusion | nl_behaviour | 6/6 | 0.5076 | 0.5076 | +0.0000 | 0.6667 | 0.6667 |
| fusion | architecture_flow | 5/5 | 0.3187 | 0.3187 | +0.0000 | 0.4000 | 0.4000 |
| fusion | config_docs | 5/5 | 0.6397 | 0.6397 | +0.0000 | 1.0000 | 1.0000 |
| fusion | ambiguous | 4/4 | 0.4388 | 0.4388 | +0.0000 | 0.2500 | 0.2500 |
| fusion | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |
| semantic_first | exact_identifier | 4/4 | 0.7575 | 0.7575 | +0.0000 | 0.7500 | 0.7500 |
| semantic_first | exact_path | 3/3 | 0.6667 | 1.0000 | **+0.3333** | 0.6667 | 1.0000 |
| semantic_first | nl_behaviour | 6/6 | 0.6368 | 0.6368 | +0.0000 | 0.6667 | 0.6667 |
| semantic_first | architecture_flow | 5/5 | 0.3278 | 0.3278 | +0.0000 | 0.2000 | 0.2000 |
| semantic_first | config_docs | 5/5 | 0.8563 | 0.8563 | +0.0000 | 1.0000 | 1.0000 |
| semantic_first | ambiguous | 4/4 | 0.3976 | 0.3976 | +0.0000 | 0.5000 | 0.5000 |
| semantic_first | no_hit | 0/3 | n/a | n/a | n/a | n/a | n/a |

`no_hit` rows carry no metrics by construction (0 scored) and are listed so the stratum set is
visibly complete.

**Reading.** The only cell that moved is `semantic_first` — the shipped `ModeAuto` strategy, the
one path that consults `isExactPath` in production — on `exact_path`: **0.6667 → 1.0000**, top-1
0.6667 → 1.0000, driven by exactly one query (cb-09, see below). Every other stratum of
`semantic_first` is numerically identical before and after, and so is every stratum of every
other baseline (`lexical`, `hybrid_v1`, `chunk_only`, `semantic_name_only`, `oracle_upper_bound`,
and the evaluator-only `fusion`). `semantic_first` overall on dev: ndcg@10
0.6060 → 0.6430, top-1 0.6296 → 0.6667 — the whole delta is the
`exact_path` stratum.

AC-3 verdict: `exact_path` ndcg@10 did not regress (it recovered the perfect score SW-263's
restoration had left at 0.6667). The change is kept.

## Per query — `semantic_first`, dev `exact_path`

| Query | Text | ndcg@10 before | ndcg@10 after | first relevant rank before | after | top-1 hit before | top-1 hit after |
|---|---|---:|---:|---:|---:|---|---|
| cb-07 | `flag_groups.go` | 1.0000 | 1.0000 | 1 | 1 | `flag_groups.go:121` | `flag_groups.go:1` |
| cb-08 | `doc/man_docs.go` | 1.0000 | 1.0000 | 1 | 1 | `doc/man_docs.go:1` | `doc/man_docs.go:1` |
| cb-09 | `shell_completions.go` | 0.0000 | 1.0000 | miss | 1 | `site/content/user_guide.md:740` | `shell_completions.go:1` |

Blast radius, checked by diffing every ranking in both reports (`raw/hits-*.json` are the scorer's
input): across all seven baselines and all 30 queries, exactly two hit lists changed — cb-07 and
cb-09 under `semantic_first`, the two dev queries that are bare filenames. cb-07 (`flag_groups.go`)
was already scored 1.0000 before, because the semantic prefix happened to lead with rows from that
file; after the change its rows come from the lexical path override instead, still rank 1. cb-08
(`doc/man_docs.go`, a slash path) matched the rule both times and is byte-identical. No query in
any other stratum has the bare-filename shape, and none changed.

## What was measured

- Shipped path under test: `engine/retrieval` `readyDispatch` — `isExactPath(query)` selects the
  lexical path override; otherwise the semantic-first prefix + lexical backfill. Method string in
  both reports: `engine/retrieval (semantic_first, ModeAuto, weights 92a8589c)` (weights hash unchanged: the change is in the rule, not the ranking).
- Rule change: `exactPathPattern` in `engine/retrieval/rules.go` gains a second shape, a bare
  filename whose last segment is one of the documented `exactPathExtensions` (lowercase, mirroring
  the `engine/classify` parser catalog). Dotted and bare identifiers stay rejected; the slash-path
  shape is unchanged. This is deliberately narrower than the broad path change SW-263 tried and
  reverted.
- Embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (the production
  static model from the default cache); `GRAPHI_EMBEDDER` was unset for both runs so only the
  explicit `-embedder` selector applied. The generation id and vector rows (768) are identical in
  both `execution.log`s.
- Cobra `a0a6ae020bb3899ff0276067863e50523f897370`, 938 nodes / 4036 edges / 58 files;
  tokenizer `whitespace-fields-v1`; `top_k` 10; repeats 3; runner class `static-local`;
  `go1.26.6` darwin/arm64.

## Reproducibility record

- `before/run.json` and `after/run.json` content-address the report, the dataset copy and all
  fourteen raw hit/latency series of their run.
- `before-aggregate-output.log` / `after-aggregate-output.log`: `-aggregate` reproduced
  **565/565** metrics in each directory, 0 discrepant, 0 unknown (`aggregate.json` beside each).
- `before-execution.log` / `after-execution.log`: the harness's verbatim stderr for each run.
- `SHA256SUMS` covers every file in this directory except itself.

Report sha256: before `5ee0b3035d4dc12239e95396d2395a995b898390f937a1c5d0398ce2fd99f050`,
after `763345ddf4241d6119c01a9a0ab7d1ed49fb8268ecbdf220a549f4f703d44020`.

## Limits

- n=3 on the stratum that moved; one query is a third of it. This run shows the rule now fires on
  the shape it was meant to protect and costs nothing measurable elsewhere on dev; it is not a
  claim about the holdout, which SW-266 owns.
- `docs/eval/retrieval-targets.json` was not touched (AC-7) and no number in it is affected.
- Latency and RSS columns in the reports are single-machine figures on a shared laptop; nothing
  here is a performance claim.

## Redaction note

Absolute paths under the operator's home directory and the session scratch directory were
replaced in the two execution logs before commit (`$HOME`, `$SW270_TMP`), following the SW-264 /
SW-272 precedent. Only the environment prefix changed; no measured value, count, score or hash was
altered. `SHA256SUMS` covers the redacted files.

## Recompute

~~~bash
# 1. Reproduce every published number from the raw samples (no network, no model needed)
CGO_ENABLED=0 go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/before
CGO_ENABLED=0 go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/after
(cd docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule && shasum -a 256 -c SHA256SUMS)

# 2. Re-run either side (requires the pinned checkout and the verified static model)
SW270_COBRA_ROOT="${SW270_COBRA_ROOT:?set to a read-only cobra clone at a0a6ae020bb3899ff0276067863e50523f897370}"
SW270_MODEL_DIR="${SW270_MODEL_DIR:?set to the verified static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b artifact directory}"
test "$(git -C "$SW270_COBRA_ROOT" rev-parse HEAD)" = a0a6ae020bb3899ff0276067863e50523f897370
test -d "$SW270_MODEL_DIR"
SW270_TMP="$(mktemp -d)"
# the dev-only population, derived mechanically from the frozen source dataset
SRC=internal/eval/retrieval/testdata/datasets/cobra-v1.json
jq --arg sha "$(shasum -a 256 $SRC | cut -d' ' -f1)" \
  '.id = "cobra-v1-dev" | .notes = ("SW-270 dev-only measurement population: every and only split=dev query from source dataset cobra-v1 at sha256 " + $sha + "; the holdout split is excluded so it is never read (AC-5). Judgements, grades and relevant_min_grade are inherited unchanged from the source.") | .queries |= map(select(.split == "dev"))' \
  "$SRC" > "$SW270_TMP/cobra-v1-dev.json"
test "$(shasum -a 256 "$SW270_TMP/cobra-v1-dev.json" | cut -d' ' -f1)" = 1436a29b6f1b2432f7c79266fbfcb80000105eaeefcc827489657dc553cec9d5
# check out 9bf9326bb56e4f27ae330235d3c4687e2c0c445e for "before" or 71cba5efc3a033012ba47d56ddadaeca6417ca2a for "after", then:
env -u GRAPHI_EMBEDDER GRAPHI_STATIC_MODEL_DIR="$SW270_MODEL_DIR" CGO_ENABLED=0 \
go run ./cmd/retrieval-eval \
  -manifest corpus/manifest.json -repo cobra -checkout "$SW270_COBRA_ROOT" \
  -dataset "$SW270_TMP/cobra-v1-dev.json" \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -out "$SW270_TMP/report.json" -export-raw "$SW270_TMP/run" \
  -runner-class static-local -repeats 3 -date 2026-09-04
CGO_ENABLED=0 go run ./cmd/retrieval-eval -aggregate "$SW270_TMP/run"
~~~
