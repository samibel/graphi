# AC-9 evaluation run — `cobra` @ `a0a6ae02…` (2026-08-31, local)

> **Status: AC-9 MISSED (both conceptual strata).** Per the SW-263 story,
> "if the target is missed, the story does not go to review; weights may
> be tuned on `dev` only, never on `holdout`." The numbers below are
> recorded exactly as measured on this run; the targets file
> `docs/eval/retrieval-targets.json` was NOT edited and is byte-identical
> to its checked-in commit. The miss is reported honestly per the story's
> "good outcome" definition.

## What was run

Seven baselines against the pinned `cobra` checkout
(`a0a6ae020bb3899ff0276067863e50523f897370`, v1.8.0) over the graded
`cobra-v1` dataset (40 queries — 30 dev, 10 holdout) on a local macOS
arm64 runner. Determinism: byte-identical `result.json` across two
sequential runs of the harness on the same inputs.

| Baseline | Seam / mode |
|---|---|
| `lexical` | `engine/search.Service.Search` (sqlite fts5 bm25) |
| `hybrid_v1` | `engine/agenttools/hybridsearch.Search` (`search_hybrid/1`) |
| `semantic_name_only` | `engine/search.Service.SemanticSearch` (now active: the Ollama path was wired for this run) |
| `oracle_upper_bound` | judged spans ranked by grade |
| `chunk_only` | **new (SW-263)** `engine/retrieval` in `ModeLexicalOnly` |
| `fusion` | **new (SW-263)** `engine/retrieval` in `ModeFusionNoGraph` |
| `fusion+graph` | **new (SW-263)** `engine/retrieval` in `ModeAuto` (RRF + bounded graph rerank + diversify) |

Embedder: `ollama:nomic-embed-text` (dim 768, 938 vectors persisted to
the GenerationStore). Per the SW-263 story, this is the model's text
embedding — the static `potion-code-16M-v2` embedder the spec eventually
wants is SW-262 and was deliberately not built. **All fusion / fusion+graph
numbers in this report are model-dependent and will move when the static
embedder lands.** They are not the spec's final numbers.

## AC-9 gate (ndcg@10, dev split)

Per-stratum dev-split numbers (30 dev queries over the `cobra-v1`
dataset; aggregates are mean over the scored queries in the stratum):

| Stratum | best single baseline | fusion | fusion+graph | must_reach | delta vs best | verdict |
|---|---|---|---|---|---|---|
| `nl_behaviour` | `hybrid_v1` = 0.3058 | **0.2753** | 0.3218 | 0.4058 | fusion -0.0305, fusion+graph +0.0160 | **MISS** (need ≥ +0.10) |
| `architecture_flow` | `hybrid_v1` = 0.2286 | **0.2872** | 0.2872 | 0.3286 | fusion +0.0586, fusion+graph +0.0586 | **MISS** (need ≥ +0.10) |

`fusion` numbers in **bold** are the headline metric the AC-9 gate tests
against. `fusion+graph` is reported for visibility — it is NOT pinned by
the targets file, so a delta short on it is informational.

`fusion` is `engine/retrieval` in `ModeFusionNoGraph`: union + integer RRF
fusion, NO bounded graph rerank. `fusion+graph` is the same in `ModeAuto`
(RRF + bounded rerank + diversify). On cobra the rerank lifts
`nl_behaviour` by +0.046 but is within noise on `architecture_flow`.

`exact_identifier` Top-1 (no-regression floor `lexical` = 0.75):

| Baseline | top1 | verdict |
|---|---|---|
| `fusion` | 1.0000 | no regression |
| `fusion+graph` | 0.7500 | no regression (at the floor) |

The Top-1 floor is met on both fusion ablations.

## Why the target was missed

`ollama:nomic-embed-text` is a general-purpose text embedder; on cobra's
conceptual queries (`"how does the command find its subcommands"`,
`"walk me through how cobra parses flags"`) the cosine signal it returns
is noisier than the lexical+identifier+degree ranking `hybrid_v1`
already provides. On `nl_behaviour` the semantic contribution actively
hurts fusion: 0.2753 vs `hybrid_v1`'s 0.3058 (-0.0305). On
`architecture_flow` the lift is real (+0.0586) but well below the
+0.10 ceiling the SW-258 baseline scan pinned. The static
code-specialised embedder the spec eventually wants (SW-262) is the
binding reason these numbers are below target.

Per the story:

> "Your fusion numbers therefore come from `ollama:nomic-embed-text`,
> not the `static:potion-code-16M-v2` embedder the spec eventually
> wants (that is SW-262, deliberately not built yet). Stamp the
> embedder id into the report and state plainly… that these numbers
> are model-dependent and will move when the static embedder lands.
> Do not present them as the spec's final numbers."

The model choice is the binding reason these numbers are below the
target. SW-262 will revisit when the static embedder is in place. No
weight tuning, stratum drop, or targets-file edit was performed to
reconcile the miss; the report is the honest outcome of this run.

## What was NOT done

- `docs/eval/retrieval-targets.json` was not edited. `git diff -- docs/eval/retrieval-targets.json` is empty in this branch.
- No holdout numbers were consulted while tuning weights. No weight
  tuning was performed at all; the run used the retrieval module's
  default rerank weights (`engine/retrieval.WeightsHash()` stamped in the
  per-row `method`).
- No stratum was dropped or relabelled. `nl_behaviour` and
  `architecture_flow` are the conceptual strata the SW-258 derivation
  set the targets on; both are reported as MISSED exactly as the targets
  file says.

## Reproducing

```bash
# This run:
go run ./cmd/retrieval-eval \
  -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out docs/eval/retrieval/runs/2026-08-31-local/cobra-v1-report.json \
  -export-raw docs/eval/retrieval/runs/2026-08-31-local \
  -embedder ollama -runner-class local -date 2026-08-31

# Verify the report:
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-08-31-local
# → 705 metric(s) checked, 705 reproduced, 0 discrepant

# Gate the numbers against the targets file:
go test ./internal/eval/retrieval -run TestReport_MeetsAC9GateAgainstTargetsFile
# → AC-9 MISS on nl_behaviour: fusion ndcg@10 = 0.275295 < must_reach 0.405827
# → AC-9 MISS on architecture_flow: fusion ndcg@10 = 0.287172 < must_reach 0.328623
```

## Files

- `cobra-v1-report.json` — the published artifact (deterministic over
  inputs + harness version; byte-identical to `report.json`)
- `report.json` — same bytes as `cobra-v1-report.json`
- `dataset.json` — the exact graded dataset bytes
- `run.json` — index with per-file sha256 and the environment block
- `raw/hits-<baseline>.json` — every ranking per baseline (the scorer's
  input, nothing derived)
- `raw/latency-<baseline>.json` — every timed execution + the
  single-sample measures (index_ms, peak_rss_mb, vector_sidecar_bytes)
- `README.md` — this file

The `aggregator` reproduces all 705 published metrics from these files.