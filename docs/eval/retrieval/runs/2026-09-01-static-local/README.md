# AC-9 against the production static embedder — `cobra` @ `a0a6ae02…` (2026-09-01, static local)

> **Status: AC-9 still MISSES on both conceptual strata.** This is the AC-9 re-measurement
> the spec asked for once the SW-262 production static embedder was available. The headline
> ablations are unchanged in shape from the previous ollama run; the `semantic_name_only`
> channel moves sharply upward, which makes the **blending loss** on `architecture_flow`
> starkly visible: the static semantic channel by itself clears the gate on `architecture_flow`
> (0.4123 ≥ 0.3286), but the `fusion` RRF blend turns it into 0.2440. The model was no
> longer the constraint; the RRF / rerank weights are.

## What changed

The harness now knows about the SW-262 `static:` scheme. `cmd/retrieval-eval/main.go` adds
one side-effect import (`_ "github.com/samibel/graphi/engine/embed/static"`) so the
constructor table `internal/eval/retrieval/runner.go::buildSearchService` consults at
`-embedder static:potion-code-16M-v2@…` time is the same one the production `cmd/internal/
runtime/builder.go` uses. Without that import, the production embedder is unreachable from
the harness even when its artifact is installed and verified (the orchestrator's pre-flight
confirmed `~/.cache/graphi/models/potion-code-16M-v2@e9d2a44…/model.safetensors` is 32 MB
and `go test ./engine/embed/static/` passes).

No ranking code changed. The artifact SHA, model, tokenizer, config are exactly what
`engine/embed/static/pins.go` pins (`PinnedRevision = e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`).

## What was run

Seven baselines against the pinned `cobra` checkout
(`a0a6ae020bb3899ff0276067863e50523f897370`) over the graded `cobra-v1` dataset
(40 queries — 30 dev, 10 holdout) on a local macOS arm64 runner.

| Baseline | Seam / mode |
|---|---|
| `lexical` | `engine/search.Service.Search` (sqlite fts5 bm25) |
| `hybrid_v1` | `engine/agenttools/hybridsearch.Search` (`search_hybrid/1`) |
| `semantic_name_only` | `engine/search.Service.SemanticSearch` (static embedder) |
| `oracle_upper_bound` | judged spans ranked by grade |
| `chunk_only` | **SW-263** `engine/retrieval` in `ModeLexicalOnly` |
| `fusion` | **SW-263** `engine/retrieval` in `ModeFusionNoGraph` |
| `fusion+graph` | **SW-263** `engine/retrieval` in `ModeAuto` |

Embedder: `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` (dim 256,
938 vectors persisted to the GenerationStore; full fingerprint printed into the run
header).

## AC-9 gate (ndcg@10, dev split)

| Stratum | best single baseline | semantic_name_only (ollama → static) | fusion (ollama → static) | fusion+graph (ollama → static) | must_reach | verdict |
|---|---|---|---|---|---|---|
| `nl_behaviour` | `hybrid_v1` = 0.3058 | 0.2164 → **0.3848** (+0.168) | 0.3302 → **0.3785** (+0.048) | 0.3302 → **0.3785** (+0.048) | 0.4058 | **MISS** (Δ −0.0273) |
| `architecture_flow` | `hybrid_v1` = 0.2286 | 0.3501 → **0.4123** (+0.062) | 0.2914 → **0.2440** (−0.047) | 0.2914 → **0.2991** (+0.008) | 0.3286 | **MISS** (Δ −0.0846) |

`exact_identifier` Top-1 (no-regression floor `lexical` = 0.75):

| Baseline | ollama top1 | static top1 | verdict |
|---|---|---|---|
| `fusion` | 1.00 | **0.75** | meets floor (no regression) |
| `fusion+graph` | 0.75 | **0.75** | meets floor (no regression) |

The AC-9 gate as written in `internal/eval/retrieval/targets_test.go` still points at the
**previous conformance run** (`docs/eval/retrieval/runs/2026-08-31-conformance-local/`) and
its reviewed CandidateSHA. The orchestrator re-points `ac9ReportPath` / `ac9CandidateSHA`
after this run lands. Until then, no AC-9 verdict flips — this run is the evidence that
goes into the next gate decision, not the gate itself.

## The prediction that was on the record, and what happened

> `nl_behaviour` should improve, because the semantic channel is currently *worse* than the
> lexical one and a code-specialised model is exactly what should change that.
> `architecture_flow` was predicted NOT to be solved, because the semantic channel there
> already reaches 0.3256 while fusion turns it into 0.2206 — the loss is in the blending,
> not the model.

**Both halves held.** The data:

- `nl_behaviour` `semantic_name_only` went from a below-lexical 0.2164 to an above-lexical
  0.3848; `fusion` followed it up by 0.048 (0.3302 → 0.3785). The model was the binding
  constraint on this stratum and is now materially closer to clearing it. It still misses
  by 0.0273.
- `architecture_flow` `semantic_name_only` improved 0.3501 → 0.4123 — clearing the gate on
  its own (0.4123 ≥ 0.3286). The same query set, fed through `fusion`, regresses
  (0.2914 → 0.2440). The model is **not** the constraint; the RRF blend is. The graph
  rerank partially rescues it (`fusion+graph` 0.2991), but not enough.

The `fusion` loss on `architecture_flow` is not a single bad query — every dev query in
the stratum loses NDCG going from `semantic_name_only` to `fusion`:

| Query | semantic_name_only ndcg@10 | fusion ndcg@10 | Δ |
|---|---|---|---|
| `cb-19` | 0.5704 | 0.4146 | −0.1558 |
| `cb-20` | 0.3040 | 0.1297 | −0.1743 |
| `cb-21` | 0.3308 | 0.1065 | −0.2243 |
| `cb-22` | 0.0773 | 0.0993 | +0.0220 |
| `cb-24` | 0.7788 | 0.4702 | −0.3087 |

That is uniform enough that it is a property of the RRF weights, not a per-query noise
artefact.

## What this run does NOT do

- It does NOT re-point `internal/eval/retrieval/targets_test.go::ac9ReportPath` /
  `ac9CandidateSHA`. The orchestrator closes that loop after committing.
- It does NOT touch `docs/eval/retrieval-targets.json` (immutable until SW-266).
- It does NOT touch `engine/retrieval/` ranking code (a measurement run whose code moved
  mid-flight measures nothing).
- It does NOT consult `holdout`. The per-query table above is the dev split; `oracle`
  numbers include holdout in the report but no AC-9 verdict uses them.

## Files

- `cobra-v1-report.json` — the published artifact (deterministic over inputs + harness
  version).
- `dataset.json` — the exact graded dataset bytes (sha256
  `be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82`, identical to the
  other cobra runs in this directory tree).
- `run.json` — index with per-file sha256 and the environment block.
- `raw/hits-<baseline>.json` — every ranking per baseline.
- `raw/latency-<baseline>.json` — every timed execution + the single-sample measures.

## Reproducing

```bash
# The run:
go run ./cmd/retrieval-eval \
  -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out docs/eval/retrieval/runs/2026-09-01-static-local/cobra-v1-report.json \
  -export-raw docs/eval/retrieval/runs/2026-09-01-static-local \
  -embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  -runner-class local -date 2026-09-01

# Aggregate check (705 metrics reproduced, 0 discrepant):
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-09-01-static-local
```

The aggregate round-trip reproduces all 705 published metrics from the raw samples; the
new `aggregate.json` lives next to the report.
