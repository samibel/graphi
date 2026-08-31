# Retrieval evaluation harness (SW-258)

> **Package:** `internal/eval/retrieval` · **Entry point:** `cmd/retrieval-eval` ·
> **PR gate:** `go test ./internal/eval/retrieval` (hermetic, over `testdata/fixture-repo`) ·
> **Targets:** `docs/eval/retrieval-targets.json` · **Budgets:** `docs/eval/retrieval-budgets.json`
> (both immutable until SW-266).

`internal/eval` is the static token-parity harness over prebuilt context strings; it never runs a
retrieval pipeline and cannot produce Recall/MRR/NDCG. This harness is its sibling: it runs real
queries against a pinned repository through the **real engine seams**, scores every ranking against
**graded source spans**, and writes a versioned JSON report whose every published number is
recomputable from exported raw samples. The same discipline as `cmd/eval -aggregate`: a published
number that does not follow from its samples is an error, not a rounding note.

## What is measured

Seven baselines, executed by name and in this order:

| Baseline | Seam | Notes |
|---|---|---|
| `lexical` | `engine/search.Service.Search` | the store's FTS5 bm25 ranking over qualified names (SQLite, the shipped backend) |
| `hybrid_v1` | `engine/agenttools/hybridsearch.Search` (`search_hybrid/1`) | lexical retrieval + identifier/path/degree signals, no vectors; the weights hash is stamped in `method` |
| `semantic_name_only` | `engine/search.Service.SemanticSearch` | over the v1 name-only documents; on the default build (no embedder) it is reported `unavailable` with the engine's typed reason — never zeros |
| `chunk_only` | `engine/retrieval.Retrieve` (`ModeLexicalOnly`) | the SW-263 lexical-only pipeline; no semantic candidates consulted |
| `fusion` | `engine/retrieval.Retrieve` (`ModeFusionNoGraph`) | the SW-263 fused pipeline; integer RRF over lexical + semantic, no graph rerank |
| `fusion+graph` | `engine/retrieval.Retrieve` (`ModeAuto`) | the SW-263 fused pipeline with the bounded graph rerank on top |
| `oracle_upper_bound` | the judged spans themselves, grade ≥ 1 ranked by grade | the ceiling the scorer can reach; proves the metric code, not a retriever |

Per baseline, per query: the top-10 hits, then **Top-1, Recall@5, Recall@10, MRR@10, NDCG@10,
first-relevant-rank, and Recall under 600 / 1200 / 2000 context tokens**. Per baseline: index time,
query p50/p95, peak RSS and vector-sidecar size, each with a status (`measured`, `UNKNOWN` with a
reason, or `not_applicable` with a reason). A measure that could not be taken renders `UNKNOWN`,
never zero.

## Matching rule (defined once, tested once)

`SpanMatches` in `internal/eval/retrieval/metrics.go`, stamped into every report as `matching_rule`:

> A hit matches a judged span when `hit.path == span.path` (exact, repo-relative POSIX path) and
> `span.start_line <= hit.line <= span.end_line`. `hit.line` is the node's declaration line as the
> engine reports it (the oracle uses the span's `start_line`). Each judged span is credited once per
> ranking for recall and DCG, so ten hits inside one function do not inflate either.

Derived definitions:

- **Relevant** = judgement grade ≥ `relevant_min_grade` (default 2; a dataset may override).
- **Top-1** = 1 when the first hit is relevant. **Recall@k** = relevant spans covered by the top-k /
  relevant spans. **MRR@10** = 1/rank of the first relevant hit within the top 10, else 0.
  **NDCG@10** uses gain 2^grade − 1 and log2(rank + 1); the ideal ordering is *every* judged span
  by grade (marginal grade-1 spans included), and a hit credits the highest-grade uncredited span it
  matches. **first_relevant_rank** is over the whole ranking and `null` when no hit is relevant.
- **Recall under B tokens** = recall over the longest prefix of the ranking whose cumulative token
  cost is ≤ B. A hit is charged the whitespace tokens (`tokenizer_id: whitespace-fields-v1`,
  `internal/eval`'s `strings.Fields` counter) of its **read window**: `hit_context_window_lines`
  (40) lines from the hit's line, clipped at end of file. The rule is fixed and
  judgement-independent, so a baseline cannot be charged less for being right.
- **no_hit** queries have no relevant span and are not scored on recall; they report
  `negative_hit_at_5` — whether any top-5 hit matched one of the query's grade-0 negative-example
  spans — aggregated as `negative_hit_rate@5` (lower is better).

Aggregates are plain means over scored queries, per baseline: `overall`, per `stratum`, per
`split`. A group with nothing scored reads `status: UNKNOWN` and carries no numbers.

## Dataset schema (`schema_version: 1`)

```json
{
  "schema_version": 1, "id": "cobra-v1", "repo": "cobra", "repo_sha": "<pinned sha>",
  "language": "go", "evidence_class": "agent-annotated, human-reviewed",
  "queries": [{
    "id": "cb-01", "stratum": "exact_identifier", "language": "en", "split": "dev",
    "query": "ExecuteC",
    "judgements": [{
      "path": "command.go", "start_line": 1051, "end_line": 1137,
      "anchor": "func (c *Command) ExecuteC(", "grade": 3,
      "reason": "The declaration of ExecuteC itself.",
      "annotator": "claude-delegate (SW-258 build)", "reviewer": "orchestrator"
    }]
  }]
}
```

- Strata: `exact_identifier`, `exact_path`, `nl_behaviour`, `architecture_flow`, `config_docs`,
  `ambiguous`, `no_hit`. Splits: `dev` | `holdout` — **no ranking weight may be tuned on holdout**;
  the targets file is derived from `dev` only.
- Grades: 3 exact answer span · 2 directly relevant · 1 marginal · 0 irrelevant / negative example.
  Every judgement carries a one-sentence `reason`, the `annotator` and the `reviewer`.
- `anchor` is a substring that must occur inside `[start_line, end_line]` at the pinned commit. It is
  what makes the coverage test catch a *moved* span, not only a truncated file.
- Validation (`Dataset.Validate`) fails closed on any rule; a `no_hit` query may carry no relevant
  span, every other query must carry at least one.

Datasets live under `internal/eval/retrieval/testdata/datasets/`:

| File | Repo | Queries | Runs on PR |
|---|---|---|---|
| `fixture-v1.json` | `testdata/fixture-repo` (in-tree, buildable Go module) | 7 dev + 3 holdout, every stratum | always |
| `cobra-v1.json` | `cobra` @ `a0a6ae02…` (v1.8.0, `corpus/manifest.json`) | 30 dev (≥3 per stratum) + 10 holdout | shape always; span coverage only when a clone at the pin is present, else `SKIP` |
| `grpc-go-perf-v1.json` | `grpc-go` @ `dbbcf599…` (v1.60.1, `corpus/manifest.json`) | 5 dev + 3 holdout, **performance-only** (exact_identifier / exact_path / no_hit) | shape always; span coverage only when a clone at the pin is present, else `SKIP` |

`grpc-go-perf-v1.json` exists to measure the **large** size class for the budgets file (AC-8). It
is not a quality dataset: eight cheap-to-judge queries, no target is derived from it, and
`TestDatasets_GrpcGoDatasetShape` keeps it that way.

Judged span paths are validated to fit the artifact bound (`trust.MaxPathLength`, 240 bytes) and
never to carry the truncation marker, so a bounded hit path (below) can only match the spans its
canonical value matches.

## Span coverage is a test (AC-9)

`TestDatasets_FixtureSpansResolve` always runs. `TestDatasets_CobraSpansResolveAtPinnedSHA` and
`TestDatasets_GrpcGoSpansResolveAtPinnedSHA` look for a read-only clone at `$GRAPHI_CORPUS_COBRA` /
`$GRAPHI_CORPUS_GRPC_GO` or `$HOME/.cache/graphi/corpus/<repo>`, verify `git rev-parse HEAD` equals
the pin, and then check every judgement: regular file, line range inside the file, anchor inside
the range. A stale judgement fails `go test ./internal/eval/retrieval`. The PR path never clones.

## Running

```bash
# Hermetic PR-time run (the fixture repo, all four baselines, determinism, aggregate round-trip)
go test ./internal/eval/retrieval

# Dispatch: one pinned repo, one dataset, report + raw samples
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo cobra \
  -dataset internal/eval/retrieval/testdata/datasets/cobra-v1.json \
  -out retrieval-cobra.json -export-raw docs/eval/retrieval/runs/<date>-<runner-class>-cobra \
  -runner-class local            # -checkout <dir> overrides $HOME/.cache/graphi/corpus/cobra

# Reproduce every published number from the raw samples (exit 0 reproduced / 1 discrepancy /
# 2 unreadable / 3 incomplete)
go run ./cmd/retrieval-eval -aggregate docs/eval/retrieval/runs/<date>-<runner-class>-cobra

# Large size class for the budgets file: the performance-only grpc-go dataset (same dispatch)
go run ./cmd/retrieval-eval -manifest corpus/manifest.json -repo grpc-go \
  -dataset internal/eval/retrieval/testdata/datasets/grpc-go-perf-v1.json \
  -out retrieval-grpc-go.json -export-raw <dir>

# Regenerate the frozen files (only from reports checked in beside them; immutable until SW-266)
go run ./cmd/retrieval-eval -derive -targets-report <cobra-report.json> \
  -budget-small <fixture-report.json> -budget-medium <cobra-report.json> -budget-large <grpc-go-report.json> \
  -targets-out docs/eval/retrieval-targets.json -budgets-out docs/eval/retrieval-budgets.json
```

`-repo fixture` selects the in-tree fixture without a manifest entry. A URL-pinned entry is never
cloned: the checkout must exist locally at the pinned sha or the run fails closed.

## Report layout (`format_version: 2`, `harness_version: retrieval-eval/1`)

> **Why 2.** Version 2 added `dataset.query_ids` to the report citation, and `-aggregate` compares
> that citation against the dataset rebuilt from its own bytes — the check that catches a query
> removed coherently from dataset, report and raw. A version-1 report carries no such field, so it
> is **refused** rather than read under a gate written for the stronger shape
> (`TestAggregate_DetectsDrift/a report from the previous format version is refused…`).
> `harness_version` did not move: what a hit is, how it is charged and which seams run are unchanged.

- `reproducible` — a pure function of candidate SHA, repository at its SHA, dataset bytes and
  harness/scorer version: header (`candidate_sha`, `runner_class`, `repo` with node/edge/file
  counts, `dataset` with `sha256` and sorted `query_ids`, `tokenizer_id`, `top_k`, `token_budgets`,
  `hit_context_window_lines`, `relevant_min_grade`, `matching_rule`) and the per-baseline results
  (`status`, `reason`, `method`, per-query `hits` + `metrics`, `overall`, `strata`, `splits`).
  **Byte-identical across two runs over the same inputs** (`TestRun_ReproducibleSectionIsByteIdenticalAcrossRuns`).
- `performance` — per baseline `index_ms`, `query_p50_us`, `query_p95_us`, `latency_samples`,
  `peak_rss_mb`, `vector_sidecar_bytes`; the block is `PerformanceFromRaw` over
  `raw/latency-<baseline>.json` and is recomputed exactly — status, value, unit and reason — so an
  `UNKNOWN` or `not_applicable` figure is checked against the raw record, not against itself.
- `environment` — `generated_at`, `os`, `arch`, `go_version`, `cpu_count`; checked for presence only.

Hit fields under repository control (`path`, `node_id`, `qualified_name`) are bounded at
`trust.MaxPathLength` (240 bytes) with a visible `…[truncated]` marker before they enter the report
or the raw files (`context/standards.md`); scoring runs over the canonical value.

Run directory (`-export-raw`): `run.json` (index with per-file sha256), `report.json`,
`dataset.json` (the exact judged bytes), `raw/hits-<baseline>.json` (every ranking, nothing
derived), `raw/latency-<baseline>.json` (every timed execution + the single-sample measures
`index_ms` / `peak_rss_mb` / `vector_sidecar_bytes` with their status and reason). An unavailable
baseline's raw records say `collected: false` and carry the typed `reason` — the only thing that can
justify `unavailable` in the report.

Every raw file is read **twice-identified**: `run.json` says which series and baseline a file is,
and the file says the same about itself (`format_version`, `harness_version`, `series`, `baseline`,
`collected`, `samples`). `ReadRunDir` requires the two to agree. The per-file sha256 proves the
bytes were not edited after the run; it does *not* prove the index and the payload mean the same
thing by them — swapping two raw files and re-stamping the index leaves both digests valid, and
only this check refuses it.

`-aggregate` is closed-world first: the report's `dataset` citation (`id`, `sha256`,
`evidence_class`, counts, sorted `query_ids`) must equal the same citation rebuilt from
`dataset.json` — whose sha256 is recomputed from its bytes, never read from `run.json` — and the
baseline universe on every side (report results, performance blocks, raw hits series, raw latency
series listed in `run.json`) must equal the harness constant of four baselines. A query removed
coherently from the dataset copy, the report and every raw series is therefore caught by the
citation the report still carries; a tamperer who also rewrites that citation has produced a
different report, and the `derived_from.sha256` in the targets/budgets files no longer matches it —
that provenance layer, not the aggregate, binds a checked-in artifact to its report.

It then checks, per baseline, **exact query-id set equality** between the report, the dataset
copy and each raw series (an omitted or extra query on any side is a discrepancy, whether or not
the aggregates were re-averaged); recomputes every per-query metric through the same `Evaluate`,
every aggregate through the same `Aggregate` over the *raw* hit set in dataset order, every
performance block through the same `PerformanceFromRaw`; compares the report's hit lists to the raw
hit lists; and checks the complete shape of an `unavailable` baseline against **both** raw records
(status only when both say `collected: false`; report reason == hits reason == latency reason;
zero samples and an empty query set in both records; zero queries, `UNKNOWN` aggregates and
`UNKNOWN` measures in the report). Every comparison is `reflect.DeepEqual` — no tolerances.

## Targets and budgets

`docs/eval/retrieval-targets.json` (AC-7): per stratum, the best single-baseline value of every
metric over the dev split, the oracle ceiling, a `fusion_target` on `nl_behaviour` and
`architecture_flow` (best `ndcg@10` + 0.10, capped at the ceiling) and the Top-1 `no_regression`
floor on `exact_identifier`. `docs/eval/retrieval-budgets.json` (AC-8): per fixture size class —
small = the in-tree fixture, medium = cobra, large = grpc-go (performance-only dataset) — index
time, worst indexed-baseline query p95 and peak RSS with the measurement each came from and
budget = measured × 2.0; a class with no measurement reads `UNKNOWN`. Both carry `date`,
`derived_from` (report path + sha256) and `immutable_until: "SW-266"`. The reports they were derived
from are checked in under `docs/eval/retrieval/runs/`.

## SW-263 AC-9 evaluation runs

The `chunk_only`, `fusion` and `fusion+graph` baselines in this harness
are the SW-263 retrieval ablations (`engine/retrieval` in
`ModeLexicalOnly`, `ModeFusionNoGraph`, and `ModeAuto` respectively).
The AC-9 gate in `internal/eval/retrieval/targets_test.go`
(`TestReport_MeetsAC9GateAgainstTargetsFile`) compares the most recent
checked-in cobra run's fusion ndcg@10 against the targets file's
`fusion_target.must_reach` on `nl_behaviour` and `architecture_flow`,
and the fusion top1 against the `no_regression.floor` on
`exact_identifier`.

**Embedder caveat (read this before citing fusion numbers):** the fusion
ablations require a configured embedder; the SW-258 targets were derived
without one (so `semantic_name_only` was `unavailable` there). The AC-9
runs that exercise fusion therefore use `ollama:nomic-embed-text` —
**not** the static code embedder the spec eventually wants
(`static:potion-code-16M-v2`, SW-262, deliberately not built yet). The
fusion numbers are model-dependent and will move when the static embedder
lands. Do not present AC-9 numbers as the spec's final numbers.

The 2026-08-31 cobra run (`docs/eval/retrieval/runs/2026-08-31-local/`)
is the SW-263 AC-9 evaluation: the `README.md` in that directory records
the actual numbers and the embedder id, and the verdict against the
targets (gate PASS / MISS). Per the story, if the target is missed the
story does not go to review and the miss is reported with the actual
per-stratum numbers.

## What this harness does not do

- It does not wire an opt-in embedder: `semantic_name_only` runs through the real
  `SemanticSearch` seam with the default (empty, frozen) registry, so on every build to date it is
  `unavailable`. SW-262 supplies the embedder; the baseline will then measure name-only documents
  without a harness change.
- It does not tune anything. The holdout split exists so that later stories cannot.
- It does not compute the `grep+read` token baseline (SW-266).
