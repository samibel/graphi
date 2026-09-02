# AC-9 corrective re-run — `cobra` @ `a0a6ae02…` (2026-08-31, local rerun)

> **Status: AC-9 STILL MISSES (both conceptual strata), but for the right reason now.** Per the
> SW-263 / `decision-ac9` reviewer finding, the previous run (2026-08-31-local) did not measure the
> intended RRF implementation; this rerun applies the four corrective fixes the reviewer identified
> and re-runs with byte-identical inputs (same embedder `ollama:nomic-embed-text`, same pinned
> cobra SHA `a0a6ae02…`, same pinned arithmetic constants, same default weights, no tuning).
> The miss is reported honestly per the story's "good outcome" definition. Per-stratum numbers
> **moved in the direction the fixes should move them**, which is the differential evidence the
> reviewer asked for; the headline gate remains MISSED, so SW-263 stays blocked out of review.

## What changed in the implementation (defects 1-4 from `decision-ac9`)

The reviewer identified four defects in the AC-9 implementation that the previous run did not
evaluate cleanly. Each was fixed in the same source tree this rerun measures:

1. **RRF intersection-only payout.** `engine/retrieval/rrf.go` previously applied RRF only to
   rows in BOTH the lexical and semantic lists; single-list members scored `rrfScore = 0` and
   were tie-broken by `node_id` in `ModeFusionNoGraph`. The corrected implementation pays each
   contributing source its own RRF term (`lexicalRank > 0 ⇒ RRFScale/(RRFk+lexicalRank)`,
   `semanticRank > 0 ⇒ RRFScale/(RRFk+semanticRank)`), and gates the whole stage with a global
   "semantic list absent?" check (so the AC-7 byte-parity path — no embedder configured, lexical-
   only — still scores 0 across the row set). The fix makes a semantic-only hit reachable in
   the fused result with a positive RRF contribution, which is the AC-2 contract.

2. **Union dedupe on `node_id` only.** AC-2 says dedupe on `document_id` then `node_id`. In v1
   schemas `document_id` is a deterministic function of `node_id`, so the prior `node_id` key is
   functionally equivalent; the code's documentation now states this relationship explicitly and
   the merge carries the semantic `document_id` on the row (the v2 multi-node case where two
   rows share a `document_id` but differ in `node_id` correctly stays as two rows). A defensive
   consistency check is in place for the impossible-but-defensive case where the same `node_id`
   arrives with two different `document_id` values.

3. **`fusion+graph` measured with no graph.** `engine/retrieval.NewGraphReader` adapts
   `graphstore.BoundedGraphLookup.IncomingBounded` into the `GraphReader` the rerank consults
   for the bounded degree signal. Both the eval runner (`internal/eval/retrieval/runner.go`)
   and the production composition (`cmd/internal/runtime/builder.go`) now wire a non-nil graph
   reader over the indexed store. The `fusion+graph` ablation now sees a degree signal on
   every semantic-only row (which never went through `HybridSearchBridge`), so the ablation
   measures what its name claims.

4. **Eligibility filter asymmetry.** The retrieval module now applies the AC-2 eligibility
   rule (no source path ⇒ drop from the top `Limit`) **before** the rerank-and-diversify
   truncation, in `engine/retrieval/diversify.go`, so a backfillable eligible row can take
   an ineligible row's slot. The runner's `retrievalToRaws` keeps the post-retrieval skip
   as defense-in-depth. `semantic_name_only` still applies the filter post-search-service
   truncation (its search service does not expose an over-fetch API), documented in
   `runner.go` and below — for cobra this is a no-op because every indexed node has a
   source path.

## What was NOT done (per the orientation contract)

- `docs/eval/retrieval-targets.json` was NOT edited. `git diff -- docs/eval/retrieval-targets.json`
  is empty in this branch. The targets file remains immutable until SW-266.
- `go.mod` / `go.sum` were NOT touched. No new dependencies.
- No weight tuning. `RRFk`, `RRFScale`, `CandidateK`, `MaxPerFile`, `defaultRerankWeights` are
  unchanged from the previous run. The story explicitly forbids tuning until the implementation
  is correct; the implementation is now correct and still misses, so tuning remains off the
  table.
- No holdout numbers were consulted. Holdout queries are NOT in this report's per-query
  breakdown — the differential is dev only.
- The corpus at `$HOME/.cache/graphi/corpus/cobra` was NOT cloned or modified; it remains at
  the pinned SHA `a0a6ae020bb3899ff0276067863e50523f897370`.
- No git operations: the orchestrator handles committing. `git status --short` will show the
  new directory untracked when this report lands.

## What was run

Seven baselines against the pinned `cobra` checkout
(`a0a6ae020bb3899ff0276067863e50523f897370`, v1.8.0) over the graded `cobra-v1` dataset
(40 queries — 30 dev, 10 holdout) on a local macOS arm64 runner.

| Baseline | Seam / mode |
|---|---|
| `lexical` | `engine/search.Service.Search` (sqlite fts5 bm25) |
| `hybrid_v1` | `engine/agenttools/hybridsearch.Search` (`search_hybrid/1`) |
| `semantic_name_only` | `engine/search.Service.SemanticSearch` (Ollama path) |
| `oracle_upper_bound` | judged spans ranked by grade |
| `chunk_only` | **SW-263** `engine/retrieval` in `ModeLexicalOnly` |
| `fusion` | **SW-263** `engine/retrieval` in `ModeFusionNoGraph` (RRF with graph reader wired, semantically-active gate) |
| `fusion+graph` | **SW-263** `engine/retrieval` in `ModeAuto` (RRF + bounded graph rerank + diversify, with the production graph reader) |

Embedder: `ollama:nomic-embed-text` (dim 768, 938 vectors persisted to the GenerationStore).
Same model and corpus as the previous run; the differential isolates the implementation
correction.

## AC-9 gate (ndcg@10, dev split)

Per-stratum dev-split numbers (30 dev queries over the `cobra-v1` dataset; aggregates are mean
over the scored queries in the stratum):

| Stratum | best single baseline | fusion (prev → new) | fusion+graph (prev → new) | must_reach | verdict |
|---|---|---|---|---|---|
| `nl_behaviour` | `hybrid_v1` = 0.3058 | 0.2753 → **0.3302** (+0.0549) | 0.3218 → **0.3302** (+0.0084) | 0.4058 | **MISS** (need ≥ +0.10) |
| `architecture_flow` | `hybrid_v1` = 0.2286 | 0.2872 → **0.2914** (+0.0042) | 0.2872 → **0.2914** (+0.0042) | 0.3286 | **MISS** (need ≥ +0.10) |

(Numbers computed by `aggregate.go::AggregateAll` over the per-stratum dev queries, the same
shape the gate test consumes.)

`fusion` numbers in **bold** are the headline metric the AC-9 gate tests against. `fusion+graph`
is reported for visibility — it is NOT pinned by the targets file, so a delta short on it is
informational.

`exact_identifier` Top-1 (no-regression floor `lexical` = 0.75):

| Baseline | prev → new top1 | verdict |
|---|---|---|
| `fusion` | 0.75 → **1.00** | no regression (and a substantial improvement) |
| `fusion+graph` | 0.75 → **1.00** | no regression (and a substantial improvement) |

Both fusion ablations now hit top1 1.0 on `exact_identifier` (the four dev queries in that
stratum all find the relevant declaration as rank 1). The Top-1 floor is comfortably met.

## What the per-query differential says

The `differential.json` (produced by `go run ./cmd/differential -repo <cobra> -dataset
internal/eval/retrieval/testdata/datasets/cobra-v1.json -new <this-dir> -prev
docs/eval/retrieval/runs/2026-08-31-local`) captures for every dev query:

- the lexical rank, the semantic rank, each individual RRF contribution, the graph rerank
  score, the classification penalty, the post-RRF final score, and the qualified name of the
  ranked row;
- the previous run's node-id list (so a reader can see when a query's top-K changed);
- per-query NDCG@10 and Top-1 on the new run, plus the reconstructed metrics on the previous
  run's hit list.

The differential separates the genuinely-bad-semantic-rank queries from the zero-score / tie
artefacts the previous run conflated. Highlights:

- **`cb-12` (`exact_identifier`, `Find` query):** top-1 unchanged, but the new run surfaces
  `cobra.Command.Find` at lex=1, sem=6, lex_rrf=16393 + sem_rrf=15151 = rrf=31544 (was a
  lexical-only hit previously, semantic term now contributes). NDCG@10 holds at 0.7698.
- **`cb-16` (`nl_behaviour`, version-flag query):** the new run's top row is a **semantic-only**
  hit — `cobra.TestLongVersionFlagOnlyInHelpWhenShortPredefined` with lex_rank=0, sem_rank=1,
  sem_rrf=16393 — exactly the kind of row the previous implementation scored 0 for and the
  correct RRF behaviour surfaces. The previous run's top was `002266f377a5e486` (a content
  file); the new run prefers the semantic match. NDCG@10 on this query: 0.08 → 0.24.
- **`cb-11` (`nl_behaviour`, "required flags" query):** top-K IDENTICAL between the two runs.
  The fix did not regress this query; the previous miss on it was a real retrieval gap that
  semantic contribution alone does not fix.

`fusion+graph` and `fusion` differ on the dev split by at most 0.01 ndcg@10 per stratum on
this corpus. The graph reader wiring (defect 3) is the production-correctness change; the
headline metric does not yet reflect it because most dev queries already had eligible
lexical-degree contributions via the delegating bridge and the semantic-only rows that
benefit from the new graph reader are a minority of the dev split.

## Eligibility filter choice

I chose **option B** ("score empty-path outputs as deliberately nonrelevant") in the form
of an *eligibility rule enforced before ranking/truncation* — the retrieval module's
`diversify.go` drops ineligible rows before they enter the top-K, so a backfillable eligible
row from beyond the cap can take an ineligible row's slot. A row is ineligible iff its
source path is empty after both lexical and semantic sides have contributed.

For the runner's other baselines:

- `lexical` and `hybrid_v1` never produce empty-path rows for an indexed corpus; a defensive
  filter is a no-op.
- `semantic_name_only` applies the filter post-search-service truncation because the search
  service does not expose an over-fetch+filter API. For cobra this is a no-op (every
  indexed node has a path); for a corpus that surfaced package / external nodes with empty
  paths, this would still favour the filtered rankings. The asymmetry is documented in
  `runner.go`'s `retrievalToRaws` comment and remains for the present rerun. Extending it
  to a pre-truncation backfill is a small change but is out of scope for the corrective
  pass.

## Why the gate still misses

The implementation is now correct; the gate still misses because the semantic signal from
`ollama:nomic-embed-text` on cobra's conceptual queries is below the +0.10 ceiling the
SW-258 baseline scan pinned. The static code-specialised embedder the spec eventually
wants is SW-262 and was deliberately not built. The reviewer's judgement holds: a miss on
a correct implementation is the evidence-backed next move (plan A in the reviewer's
recommendation), not a target-file edit or a weight-tuning exercise.

Concretely:

- `nl_behaviour` improved from 0.2753 → 0.3302 (delta +0.055), but `must_reach` is 0.4058.
  The remaining 0.07 gap is on queries like `cb-11` (`required flags`), `cb-14` (shell
  completions), `cb-15` (version flag) where neither lexical nor semantic rank surfaces the
  judged declaration in the top-10.
- `architecture_flow` improved from 0.2872 → 0.2914 (delta +0.004); the corrected RRF mostly
  shuffles the existing top-K without lifting new rows into relevance. `must_reach` is 0.3286.

Per the story:

> "Your fusion numbers therefore come from `ollama:nomic-embed-text`, not the
> `static:potion-code-16M-v2` embedder the spec eventually wants (that is SW-262,
> deliberately not built yet). Stamp the embedder id into the report and state plainly…
> that these numbers are model-dependent and will move when the static embedder lands."

The embedder id is `ollama:nomic-embed-text`, stamped into the per-row `method` and into
this README.

## Files

- `cobra-v1-report.json` — the published artifact (deterministic over inputs + harness
  version; byte-identical to `report.json`).
- `report.json` — same bytes as `cobra-v1-report.json`.
- `dataset.json` — the exact graded dataset bytes.
- `run.json` — index with per-file sha256 and the environment block.
- `aggregate.json` — the aggregator's reproduction of every published metric.
- `differential.json` — per-query RRF breakdown + per-stratum delta vs the previous run
  (the artefact the SW-263 reviewer asked for).
- `raw/hits-<baseline>.json` — every ranking per baseline.
- `raw/latency-<baseline>.json` — every timed execution + the single-sample measures
  (index_ms, peak_rss_mb, vector_sidecar_bytes).
- `README.md` — this file.

The `aggregator` reproduces all 705 published metrics from these files. The `differential`
artefact is generated by `cmd/differential` and is byte-stable for a given embedder and
generation store.

## Reproducing

```bash
# This rerun:
go run ./cmd/retrieval-eval \
  -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out docs/eval/retrieval/runs/2026-08-31-rerun-local/cobra-v1-report.json \
  -export-raw docs/eval/retrieval/runs/2026-08-31-rerun-local \
  -embedder ollama -runner-class local -date 2026-08-31

# Aggregate check (must pass — 705 metrics reproduced, 0 discrepant):
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/2026-08-31-rerun-local

# Differential artefact (per-query RRF breakdown vs the previous run):
go run ./cmd/differential \
  -repo $HOME/.cache/graphi/corpus/cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -new docs/eval/retrieval/runs/2026-08-31-rerun-local \
  -prev docs/eval/retrieval/runs/2026-08-31-local

# Gate the numbers against the targets file (this run still misses; that is the honest
# outcome):
go test ./internal/eval/retrieval -run TestReport_MeetsAC9GateAgainstTargetsFile
# → AC-9 MISS on nl_behaviour: fusion ndcg@10 = 0.330186 < must_reach 0.405827
# → AC-9 MISS on architecture_flow: fusion ndcg@10 = 0.291381 < must_reach 0.328623
```
